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
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

var (
	_ = flag.Bool("web-systemd-socket", false, "test flag")
	_ = flag.String("r", "", "test flag")
)

func TestNewInstanceDefaultsAndSanitization(t *testing.T) {
	target := appconfig.Target{
		ID:       "vm:01/test@zone",
		Name:     "my-vm",
		Endpoint: "192.168.1.10",
	}

	cfg := appconfig.ExporterConfig{
		SocketDir: "/run/test-dcgm",
	}

	inst := NewInstance(target, "", cfg, nil)

	expectedSock := "/run/test-dcgm/vm_01_test_zone.sock"
	if inst.SocketPath != expectedSock {
		t.Fatalf("expected socket path %s, got %s", expectedSock, inst.SocketPath)
	}

	expectedEndpoint := "192.168.1.10:5555"
	if inst.Target.Endpoint != expectedEndpoint {
		t.Fatalf("expected endpoint %s, got %s", expectedEndpoint, inst.Target.Endpoint)
	}
}

// TestHelperProcessSocketActivation runs inside the child process when spawned during TestInstanceSocketActivationEndToEnd.
func TestHelperProcessSocketActivation(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Verify LISTEN_FDS=1
	if os.Getenv("LISTEN_FDS") != "1" {
		fmt.Fprintf(os.Stderr, "expected LISTEN_FDS=1, got %s\n", os.Getenv("LISTEN_FDS"))
		os.Exit(2)
	}

	// In socket activation, FD 3 is the inherited listener
	file := os.NewFile(3, "systemd-socket")
	listener, err := net.FileListener(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create listener from fd 3: %v\n", err)
		os.Exit(3)
	}
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("DCGM_FI_DEV_GPU_TEMP{gpu=\"0\"} 50\n"))
	})

	server := &http.Server{Handler: mux}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		os.Exit(4)
	}
	os.Exit(0)
}

func TestInstanceSocketActivationEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "child-activation.sock")

	target := appconfig.Target{
		ID:       "child-activation",
		Name:     "child-activation",
		Endpoint: "tcp://127.0.0.1:5555",
	}

	cfg := appconfig.ExporterConfig{
		BinaryPath:      os.Args[0], // execute this test binary
		SocketDir:       tmpDir,
		ExtraArgs:       []string{"-test.run=TestHelperProcessSocketActivation"},
		ShutdownTimeout: 2 * time.Second,
	}

	// Set env var for child helper
	os.Setenv("GO_WANT_HELPER_PROCESS", "1")
	defer os.Unsetenv("GO_WANT_HELPER_PROCESS")

	inst := NewInstance(target, sockPath, cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inst.Start(ctx)
	defer inst.Stop()

	// Wait for child socket to appear and serve
	var scrapeErr error
	var metrics []byte
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		metrics, scrapeErr = inst.Scrape(ctx)
		if scrapeErr == nil {
			break
		}
	}

	if scrapeErr != nil {
		t.Fatalf("failed to scrape child process over activated UNIX socket: %v", scrapeErr)
	}

	if string(metrics) != "DCGM_FI_DEV_GPU_TEMP{gpu=\"0\"} 50\n" {
		t.Fatalf("unexpected metrics from activated child: %s", string(metrics))
	}

	// Verify health check succeeds
	if err := inst.CheckHealth(ctx); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if inst.State() != StateRunning {
		t.Fatalf("expected state running, got %s", inst.State())
	}

	// Verify Stop terminates process and unlinks socket
	inst.Stop()
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket file %s to be unlinked after Stop", sockPath)
	}
}

func TestInstance_CommandBuildingNetNS(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		netns       string
		expectedBin string
		prefixArgs  []string
	}{
		{
			name:        "empty netns uses direct sh",
			netns:       "",
			expectedBin: "sh",
			prefixArgs:  []string{"-c", "export LISTEN_PID=$$; exec \"$@\"", "_", "dcgm-exporter"},
		},
		{
			name:        "named netns uses ip netns exec",
			netns:       "qrouter-1234",
			expectedBin: "ip",
			prefixArgs:  []string{"netns", "exec", "qrouter-1234", "sh", "-c", "export LISTEN_PID=$$; exec \"$@\"", "_", "dcgm-exporter"},
		},
		{
			name:        "path netns uses nsenter",
			netns:       "/run/netns/tenant-x",
			expectedBin: "nsenter",
			prefixArgs:  []string{"--net=/run/netns/tenant-x", "-F", "--", "sh", "-c", "export LISTEN_PID=$$; exec \"$@\"", "_", "dcgm-exporter"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := appconfig.Target{
				ID:       "test-vm",
				Name:     "test-vm",
				Endpoint: "tcp://10.0.0.1:5555",
				NetNS:    tt.netns,
			}
			cfg := appconfig.ExporterConfig{
				BinaryPath: "dcgm-exporter",
				SocketDir:  tmpDir,
			}

			var capturedBin string
			var capturedArgs []string

			builder := func(ctx context.Context, name string, args ...string) *exec.Cmd {
				capturedBin = name
				capturedArgs = args
				// Return a harmless command that exits immediately
				return exec.CommandContext(ctx, "true")
			}

			sockPath := filepath.Join(tmpDir, "test-vm.sock")
			inst := NewInstance(target, sockPath, cfg, builder)
			_ = inst.execProcess(context.Background())

			if capturedBin != tt.expectedBin {
				t.Errorf("expected binary %s, got %s", tt.expectedBin, capturedBin)
			}
			for i, pfx := range tt.prefixArgs {
				if i >= len(capturedArgs) || capturedArgs[i] != pfx {
					t.Errorf("arg mismatch at %d: expected %s, got %v", i, pfx, capturedArgs)
				}
			}
		})
	}
}
