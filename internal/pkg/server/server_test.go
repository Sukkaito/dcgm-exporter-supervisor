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

package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/allocator"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/supervisor"
)

func mockCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "1")
}

func TestServerEndpoints(t *testing.T) {
	// Spin up a mock dcgm-exporter child HTTP server
	mockChild := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("DCGM_FI_DEV_GPU_TEMP{gpu=\"0\"} 45\n"))
		case "/health":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockChild.Close()

	_, portStr, _ := net.SplitHostPort(mockChild.Listener.Addr().String())
	childPort, _ := strconv.Atoi(portStr)

	alloc, _ := allocator.NewPortAllocator(9401, 9410)
	cfg := appconfig.NewDefaultConfig()

	mgr := supervisor.NewManager(cfg.Exporter, alloc, mockCommand)
	defer mgr.Shutdown()

	target := appconfig.Target{
		ID:       "vm-worker-1",
		Name:     "vm-worker-1",
		Endpoint: "tcp://10.0.0.1:5555",
		Labels: map[string]string{
			"env": "prod",
		},
	}

	// Add instance to manager via reconcile
	mgr.Reconcile([]appconfig.Target{target})
	if actualInst, ok := mgr.GetInstance("vm-worker-1"); ok {
		actualInst.Port = childPort // point to mock HTTP server
	}

	srv := NewServer(cfg, mgr)

	t.Run("GET /metrics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		body := rec.Body.String()
		if !strings.Contains(body, `DCGM_FI_DEV_GPU_TEMP{gpu="0",env="prod",target_id="vm-worker-1",vm_name="vm-worker-1"} 45`) {
			t.Fatalf("expected injected labels in metrics output, got:\n%s", body)
		}

		if !strings.Contains(body, "dcgm_supervisor_targets_total 1") {
			t.Fatalf("expected self metric dcgm_supervisor_targets_total, got:\n%s", body)
		}
	})

	t.Run("GET /targets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/targets", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var targets []HTTPSDTarget
		if err := json.Unmarshal(rec.Body.Bytes(), &targets); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}

		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}
		if targets[0].Labels["vm_name"] != "vm-worker-1" {
			t.Fatalf("expected vm_name=vm-worker-1, got %v", targets[0].Labels)
		}
	})

	t.Run("GET /probe", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/probe?target=vm-worker-1", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		body := rec.Body.String()
		if !strings.Contains(body, `DCGM_FI_DEV_GPU_TEMP{gpu="0",env="prod",target_id="vm-worker-1",vm_name="vm-worker-1"} 45`) {
			t.Fatalf("unexpected probe response:\n%s", body)
		}
	})

	t.Run("GET /health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var h HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
			t.Fatalf("failed to decode health JSON: %v", err)
		}
		if h.Status != "healthy" {
			t.Fatalf("expected status healthy, got %s", h.Status)
		}
	})

	t.Run("GET /", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "DCGM Exporter Supervisor") {
			t.Fatalf("unexpected landing page content")
		}
	})
}
