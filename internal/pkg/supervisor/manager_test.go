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
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/allocator"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// mockCommandBuilder returns a long-running sleep command to simulate a child process.
func mockCommandBuilder(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "60")
}

func TestSupervisorManagerReconcile(t *testing.T) {
	alloc, err := allocator.NewPortAllocator(9401, 9410)
	if err != nil {
		t.Fatalf("failed to create allocator: %v", err)
	}

	cfg := appconfig.ExporterConfig{
		BinaryPath:      "sleep",
		ShutdownTimeout: 1 * time.Second,
	}

	mgr := NewManager(cfg, alloc, mockCommandBuilder)
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
	if !found || inst1.Port != 9401 {
		t.Fatalf("expected vm-1 on port 9401, found: %v, port: %d", found, inst1.Port)
	}

	inst2, found := mgr.GetInstance("vm-2")
	if !found || inst2.Port != 9402 {
		t.Fatalf("expected vm-2 on port 9402, found: %v, port: %d", found, inst2.Port)
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
	// vm-1 released port 9401, so vm-3 should get 9401
	if inst3.Port != 9401 {
		t.Fatalf("expected vm-3 to get reused port 9401, got %d", inst3.Port)
	}
}

func TestInstanceScrapeAndHealth(t *testing.T) {
	// Start a mock HTTP server simulating dcgm-exporter
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		case "/metrics":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("DCGM_FI_DEV_SM_CLOCK{gpu=\"0\"} 139\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	_, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to split host port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	target := appconfig.Target{
		ID:       "test-target",
		Name:     "test-target",
		Endpoint: "tcp://127.0.0.1:5555",
	}

	inst := NewInstance(target, port, appconfig.ExporterConfig{}, mockCommandBuilder)

	ctx := context.Background()

	// Verify health check
	if err := inst.CheckHealth(ctx); err != nil {
		t.Fatalf("CheckHealth failed: %v", err)
	}
	if inst.State() != StateRunning {
		t.Fatalf("expected state running, got: %s", inst.State())
	}

	// Verify scrape
	metrics, err := inst.Scrape(ctx)
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	if string(metrics) != "DCGM_FI_DEV_SM_CLOCK{gpu=\"0\"} 139\n" {
		t.Fatalf("unexpected metrics output: %s", string(metrics))
	}
}
