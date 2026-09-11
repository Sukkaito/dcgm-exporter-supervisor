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

package nova

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

func TestNovaProvider(t *testing.T) {
	// Mock Keystone and Nova server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/auth/tokens" {
			w.Header().Set("X-Subject-Token", "mock-auth-token-12345")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
			return
		}

		if r.URL.Path == "/servers/detail" {
			if r.Header.Get("X-Auth-Token") != "mock-auth-token-12345" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			resp := `{
				"servers": [
					{
						"id": "server-uuid-1",
						"name": "ai-compute-01",
						"status": "ACTIVE",
						"tenant_id": "tenant-abc",
						"metadata": { "env": "prod" },
						"addresses": {
							"internal": [
								{ "version": 4, "addr": "10.0.0.15", "OS-EXT-IPS:type": "fixed" },
								{ "version": 6, "addr": "2001:db8::15", "OS-EXT-IPS:type": "fixed" }
							]
						}
					}
				]
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(resp))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	t.Run("IPv4 default selection", func(t *testing.T) {
		cfg := appconfig.NovaConfig{
			Enabled:            true,
			AuthURL:            mockServer.URL,
			NovaEndpoint:       mockServer.URL,
			Username:           "testuser",
			Password:           "testpass",
			ProjectName:        "testproject",
			HypervisorHostname: "host01",
			DefaultPort:        5555,
			IPVersion:          "ipv4",
			PollInterval:       100 * time.Millisecond,
		}

		prov := NewProvider(cfg)
		targets, err := prov.scanServers(context.Background())
		if err != nil {
			t.Fatalf("unexpected error scanning nova servers: %v", err)
		}

		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}

		tgt := targets[0]
		if tgt.Endpoint != "tcp://10.0.0.15:5555" {
			t.Errorf("expected Endpoint tcp://10.0.0.15:5555, got %s", tgt.Endpoint)
		}
	})

	t.Run("IPv6 selection", func(t *testing.T) {
		cfg := appconfig.NovaConfig{
			Enabled:            true,
			AuthURL:            mockServer.URL,
			NovaEndpoint:       mockServer.URL,
			Username:           "testuser",
			Password:           "testpass",
			ProjectName:        "testproject",
			HypervisorHostname: "host01",
			DefaultPort:        5555,
			IPVersion:          "ipv6",
			PollInterval:       100 * time.Millisecond,
		}

		prov := NewProvider(cfg)
		targets, err := prov.scanServers(context.Background())
		if err != nil {
			t.Fatalf("unexpected error scanning nova servers: %v", err)
		}

		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}

		tgt := targets[0]
		if tgt.Endpoint != "tcp://[2001:db8::15]:5555" {
			t.Errorf("expected Endpoint tcp://[2001:db8::15]:5555, got %s", tgt.Endpoint)
		}
	})
}
