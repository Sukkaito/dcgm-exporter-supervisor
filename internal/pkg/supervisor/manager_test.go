/*
 * Copyright (c) 2026, Sukkaito. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package supervisor

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// mockCommandBuilder returns a long-running sleep command to simulate a child process.
func mockCommandBuilder(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "60")
}

func TestSupervisorManagerReconcile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := appconfig.ExporterConfig{
		BinaryPath:      "sleep",
		SocketDir:       tmpDir,
		ShutdownTimeout: 1 * time.Second,
	}

	mgr := NewManager(cfg, mockCommandBuilder)
	defer mgr.Shutdown()

	t1 := appconfig.Target{
		ID:       "vm-1",
		Name:     "vm-1",
		Endpoint: "tcp://10.0.0.1:5555",
	}
	t2 := appconfig.Target{
		ID:       "vm-2",
		Name:     "vm-2",
		Endpoint: "vsock://3:5555",
	}

	// 1. Initial reconcile with 2 targets
	mgr.Reconcile([]appconfig.Target{t1, t2})

	instances := mgr.ListInstances()
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	inst1, found := mgr.GetInstance("vm-1")
	expectedSock1 := filepath.Join(tmpDir, "vm-1.sock")
	if !found || inst1.SocketPath != expectedSock1 {
		t.Fatalf("expected vm-1 on socket %s, found: %v, path: %s", expectedSock1, found, inst1.SocketPath)
	}

	inst2, found := mgr.GetInstance("vm-2")
	expectedSock2 := filepath.Join(tmpDir, "vm-2.sock")
	if !found || inst2.SocketPath != expectedSock2 {
		t.Fatalf("expected vm-2 on socket %s, found: %v, path: %s", expectedSock2, found, inst2.SocketPath)
	}

	// 2. Remove vm-1, add vm-3
	t3 := appconfig.Target{
		ID:       "vm-3",
		Name:     "vm-3",
		Endpoint: "tcp://10.0.0.3:5555",
	}

	mgr.Reconcile([]appconfig.Target{t2, t3})

	instances = mgr.ListInstances()
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances after reconcile, got %d", len(instances))
	}

	if _, found := mgr.GetInstance("vm-1"); found {
		t.Fatal("expected vm-1 to be removed")
	}

	inst3, found := mgr.GetInstance("vm-3")
	if !found {
		t.Fatal("expected vm-3 to be added")
	}
	expectedSock3 := filepath.Join(tmpDir, "vm-3.sock")
	if inst3.SocketPath != expectedSock3 {
		t.Fatalf("expected vm-3 on socket %s, got %s", expectedSock3, inst3.SocketPath)
	}
}

func TestInstanceScrapeAndHealthUnixSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to create unix listener: %v", err)
	}
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("DCGM_FI_DEV_SM_CLOCK{gpu=\"0\"} 139\n"))
	})

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(listener)
	}()
	defer srv.Close()

	target := appconfig.Target{
		ID:       "test-target",
		Name:     "test-target",
		Endpoint: "tcp://127.0.0.1:5555",
	}

	inst := NewInstance(target, sockPath, appconfig.ExporterConfig{SocketDir: tmpDir}, mockCommandBuilder)

	ctx := context.Background()

	// Verify health check over UNIX domain socket
	if err := inst.CheckHealth(ctx); err != nil {
		t.Fatalf("CheckHealth failed: %v", err)
	}
	if inst.State() != StateRunning {
		t.Fatalf("expected state running, got: %s", inst.State())
	}

	// Verify scrape over UNIX domain socket
	metrics, err := inst.Scrape(ctx)
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	if string(metrics) != "DCGM_FI_DEV_SM_CLOCK{gpu=\"0\"} 139\n" {
		t.Fatalf("unexpected metrics output: %s", string(metrics))
	}

	// Verify Stop cleans up the socket file
	inst.Stop()
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket file %s to be removed, but stat err: %v", sockPath, err)
	}
}
