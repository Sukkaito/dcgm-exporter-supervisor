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

package appconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigFile(t *testing.T) {
	yamlContent := `
address: ":9400"
collect_interval: 15000
log_format: "json"
debug: true

exporter:
  binary_path: "/usr/local/bin/dcgm-exporter"
  collectors_file: "/etc/dcgm-exporter/dcp-metrics.csv"
  port_range_start: 9500
  port_range_end: 9600
  shutdown_timeout: 8s
  extra_args:
    - "--replace-blanks-in-model-name"

discovery:
  static:
    enabled: true
    targets:
      - name: "vm-ai-01"
        endpoint: "vsock://3:5555"
        labels:
          cluster: "training"
      - name: "vm-ai-02"
        endpoint: "tcp://192.168.100.2:5555"

  libvirt:
    enabled: true
    uri: "qemu:///system"
    poll_interval: 20s
    connection_mode: "auto"
    filter_gpu_only: true

  nova:
    enabled: true
    nova_endpoint: "http://nova.example.com:8774/v2.1"
    hypervisor_hostname: "node01"
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	cfg, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile failed: %v", err)
	}

	if cfg.Address != ":9400" {
		t.Errorf("expected address :9400, got %s", cfg.Address)
	}
	if cfg.CollectInterval != 15000 {
		t.Errorf("expected collect_interval 15000, got %d", cfg.CollectInterval)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected log_format json, got %s", cfg.LogFormat)
	}
	if !cfg.Debug {
		t.Errorf("expected debug to be true")
	}

	if cfg.Exporter.BinaryPath != "/usr/local/bin/dcgm-exporter" {
		t.Errorf("expected binary_path /usr/local/bin/dcgm-exporter, got %s", cfg.Exporter.BinaryPath)
	}
	if cfg.Exporter.PortRangeStart != 9500 || cfg.Exporter.PortRangeEnd != 9600 {
		t.Errorf("expected port range 9500-9600, got %d-%d", cfg.Exporter.PortRangeStart, cfg.Exporter.PortRangeEnd)
	}
	if cfg.Exporter.ShutdownTimeout != 8*time.Second {
		t.Errorf("expected shutdown timeout 8s, got %v", cfg.Exporter.ShutdownTimeout)
	}
	if len(cfg.Exporter.ExtraArgs) != 1 || cfg.Exporter.ExtraArgs[0] != "--replace-blanks-in-model-name" {
		t.Errorf("unexpected extra_args: %v", cfg.Exporter.ExtraArgs)
	}

	// Static targets
	if !cfg.Discovery.Static.Enabled || len(cfg.Discovery.Static.Targets) != 2 {
		t.Fatalf("expected 2 static targets, got %d", len(cfg.Discovery.Static.Targets))
	}
	if cfg.Discovery.Static.Targets[0].Name != "vm-ai-01" || cfg.Discovery.Static.Targets[0].Endpoint != "vsock://3:5555" {
		t.Errorf("unexpected first static target: %+v", cfg.Discovery.Static.Targets[0])
	}

	// Libvirt
	if !cfg.Discovery.Libvirt.Enabled || cfg.Discovery.Libvirt.PollInterval != 20*time.Second {
		t.Errorf("unexpected Libvirt config: %+v", cfg.Discovery.Libvirt)
	}

	// Nova
	if !cfg.Discovery.Nova.Enabled || cfg.Discovery.Nova.HypervisorHostname != "node01" {
		t.Errorf("unexpected Nova config: %+v", cfg.Discovery.Nova)
	}
}

