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
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultAddress             = ":9400"
	DefaultCollectInterval     = 30000 // ms
	DefaultCollectorsFile      = "/etc/dcgm-exporter/default-counters.csv"
	DefaultWebReadTimeout      = 10 * time.Second
	DefaultWebWriteTimeout     = 30 * time.Second
	DefaultExporterBinary      = "dcgm-exporter"
	DefaultPortRangeStart      = 9401
	DefaultPortRangeEnd        = 9500
	DefaultShutdownTimeout     = 5 * time.Second
	DefaultLibvirtURI          = "qemu:///system"
	DefaultLibvirtPollInterval = 15 * time.Second
	DefaultNovaPollInterval    = 30 * time.Second
	DefaultFilePollInterval    = 15 * time.Second
	DefaultHostEnginePort      = 5555
	DefaultExporterListenHost  = "127.0.0.1"
)

// Target represents a discovered or statically defined target VM / hostengine.
type Target struct {
	ID         string            `yaml:"id" json:"id"`
	Name       string            `yaml:"name" json:"name"`
	Endpoint   string            `yaml:"endpoint" json:"endpoint"` // e.g. tcp://192.168.1.10:5555, vsock://3:5555, unix:///path.sock
	Labels     map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	CustomArgs []string          `yaml:"custom_args,omitempty" json:"custom_args,omitempty"`
}

// StaticConfig defines targets configured statically in the YAML config.
type StaticConfig struct {
	Enabled bool     `yaml:"enabled"`
	Targets []Target `yaml:"targets"`
}

// LibvirtConfig defines settings for dynamic Libvirt/KVM domain discovery.
type LibvirtConfig struct {
	Enabled        bool          `yaml:"enabled"`
	URI            string        `yaml:"uri"`             // e.g. qemu:///system
	PollInterval   time.Duration `yaml:"poll_interval"`   // interval between domain scans
	ConnectionMode string        `yaml:"connection_mode"` // "auto", "vsock", "ip"
	IPVersion      string        `yaml:"ip_version"`      // "ipv4", "ipv6", "auto" (default: "ipv4")
	DefaultPort    int           `yaml:"default_port"`    // default 5555
	FilterGPUOnly  bool          `yaml:"filter_gpu_only"` // only discover VMs with GPU passthrough / vGPU
}

// NovaConfig defines settings for OpenStack Nova API discovery.
type NovaConfig struct {
	Enabled            bool          `yaml:"enabled"`
	AuthURL            string        `yaml:"auth_url"`
	Username           string        `yaml:"username"`
	Password           string        `yaml:"password"`
	ProjectName        string        `yaml:"project_name"`
	UserDomainName     string        `yaml:"user_domain_name"`
	ProjectDomainName  string        `yaml:"project_domain_name"`
	NovaEndpoint       string        `yaml:"nova_endpoint"`
	HypervisorHostname string        `yaml:"hypervisor_hostname"` // discover VMs on this hypervisor
	PollInterval       time.Duration `yaml:"poll_interval"`
	IPVersion          string        `yaml:"ip_version"` // "ipv4", "ipv6", "auto" (default: "ipv4")
	DefaultPort        int           `yaml:"default_port"`
	ConnectionMode     string        `yaml:"connection_mode"` // "ip", "vsock"
	FilterGPUOnly      bool          `yaml:"filter_gpu_only"`
}

// FileConfig defines settings for file-based target discovery (directory watcher).
type FileConfig struct {
	Enabled      bool          `yaml:"enabled"`
	Directory    string        `yaml:"directory"`     // path to directory containing YAML target files
	PollInterval time.Duration `yaml:"poll_interval"` // polling/rescan fallback
}

// DiscoveryConfig aggregates all discovery providers.
type DiscoveryConfig struct {
	Static  StaticConfig  `yaml:"static"`
	Libvirt LibvirtConfig `yaml:"libvirt"`
	Nova    NovaConfig    `yaml:"nova"`
	File    FileConfig    `yaml:"file"`
}

// ExporterConfig configures child dcgm-exporter processes.
type ExporterConfig struct {
	BinaryPath      string        `yaml:"binary_path"`
	ListenHost      string        `yaml:"listen_host"` // defaults to 127.0.0.1; can be set to ::1 or [::1]
	CollectorsFile  string        `yaml:"collectors_file"`
	CollectInterval int           `yaml:"collect_interval"` // ms
	PortRangeStart  int           `yaml:"port_range_start"`
	PortRangeEnd    int           `yaml:"port_range_end"`
	ExtraArgs       []string      `yaml:"extra_args"`
	WebConfigFile   string        `yaml:"web_config_file"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// Config represents the top-level supervisor configuration.
type Config struct {
	ConfigFile      string          `yaml:"-"`
	Address         string          `yaml:"address"`
	CollectInterval int             `yaml:"collect_interval"`
	CollectorsFile  string          `yaml:"collectors_file"`
	LogFormat       string          `yaml:"log_format"`
	Debug           bool            `yaml:"debug"`
	WebConfigFile   string          `yaml:"web_config_file"`
	WebReadTimeout  time.Duration   `yaml:"web_read_timeout"`
	WebWriteTimeout time.Duration   `yaml:"web_write_timeout"`
	Exporter        ExporterConfig  `yaml:"exporter"`
	Discovery       DiscoveryConfig `yaml:"discovery"`
}

// NewDefaultConfig returns a Config initialized with sensible default values.
func NewDefaultConfig() *Config {
	return &Config{
		Address:         DefaultAddress,
		CollectInterval: DefaultCollectInterval,
		CollectorsFile:  DefaultCollectorsFile,
		LogFormat:       "text",
		Debug:           false,
		WebReadTimeout:  DefaultWebReadTimeout,
		WebWriteTimeout: DefaultWebWriteTimeout,
		Exporter: ExporterConfig{
			BinaryPath:      DefaultExporterBinary,
			ListenHost:      DefaultExporterListenHost,
			CollectorsFile:  DefaultCollectorsFile,
			CollectInterval: DefaultCollectInterval,
			PortRangeStart:  DefaultPortRangeStart,
			PortRangeEnd:    DefaultPortRangeEnd,
			ShutdownTimeout: DefaultShutdownTimeout,
		},
		Discovery: DiscoveryConfig{
			Static: StaticConfig{
				Enabled: true,
				Targets: []Target{},
			},
			Libvirt: LibvirtConfig{
				Enabled:        true,
				URI:            DefaultLibvirtURI,
				PollInterval:   DefaultLibvirtPollInterval,
				ConnectionMode: "auto",
				IPVersion:      "ipv4",
				DefaultPort:    DefaultHostEnginePort,
				FilterGPUOnly:  true,
			},
			Nova: NovaConfig{
				Enabled:        false,
				PollInterval:   DefaultNovaPollInterval,
				IPVersion:      "ipv4",
				DefaultPort:    DefaultHostEnginePort,
				ConnectionMode: "ip",
				FilterGPUOnly:  true,
			},
			File: FileConfig{
				Enabled:      false,
				Directory:    "/etc/dcgm-exporter-supervisor/targets.d",
				PollInterval: DefaultFilePollInterval,
			},
		},
	}
}

// LoadConfigFile loads and parses a YAML configuration file into a Config struct.
func LoadConfigFile(filePath string) (*Config, error) {
	cfg := NewDefaultConfig()
	if filePath == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", filePath, err)
	}

	cfg.ConfigFile = filePath
	return cfg, nil
}
