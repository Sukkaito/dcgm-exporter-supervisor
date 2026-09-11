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

package libvirt

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// Provider discovers targets from running Libvirt/KVM domains.
type Provider struct {
	cfg appconfig.LibvirtConfig
}

// NewProvider creates a new Libvirt discovery provider.
func NewProvider(cfg appconfig.LibvirtConfig) *Provider {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = appconfig.DefaultLibvirtPollInterval
	}
	if cfg.DefaultPort <= 0 {
		cfg.DefaultPort = appconfig.DefaultHostEnginePort
	}
	if cfg.URI == "" {
		cfg.URI = appconfig.DefaultLibvirtURI
	}
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string {
	return "libvirt"
}

func (p *Provider) Run(ctx context.Context, ch chan<- []appconfig.Target) error {
	// Initial scan
	targets := p.scanDomains()
	select {
	case ch <- targets:
	case <-ctx.Done():
		return ctx.Err()
	}

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current := p.scanDomains()
			select {
			case ch <- current:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// scanDomains attempts to discover active VM domains.
func (p *Provider) scanDomains() []appconfig.Target {
	// First try using virsh CLI if available
	if virshPath, err := exec.LookPath("virsh"); err == nil {
		targets, err := p.scanWithVirsh(virshPath)
		if err == nil {
			return targets
		}
		slog.Debug("virsh discovery encountered an issue, falling back to XML inspection",
			slog.String("error", err.Error()))
	}

	// Fallback to inspecting QEMU runtime XMLs in /var/run/libvirt/qemu
	return p.scanFromQEMURuntime()
}

func (p *Provider) scanWithVirsh(virshPath string) ([]appconfig.Target, error) {
	cmd := exec.Command(virshPath, "-c", p.cfg.URI, "list", "--state-running", "--name")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("virsh list failed: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	var targets []appconfig.Target

	for _, line := range lines {
		domainName := strings.TrimSpace(line)
		if domainName == "" {
			continue
		}

		xmlCmd := exec.Command(virshPath, "-c", p.cfg.URI, "dumpxml", domainName)
		xmlData, err := xmlCmd.Output()
		if err != nil {
			slog.Debug("Failed to dump XML for domain",
				slog.String("domain", domainName),
				slog.String("error", err.Error()))
			continue
		}

		dom, err := ParseDomainXML(xmlData)
		if err != nil {
			continue
		}

		if p.cfg.FilterGPUOnly && !dom.HasGPU() {
			continue
		}

		endpoint := p.resolveEndpoint(virshPath, domainName, dom)
		if endpoint == "" {
			continue
		}

		targets = append(targets, appconfig.Target{
			ID:       dom.UUID,
			Name:     dom.Name,
			Endpoint: endpoint,
			Labels: map[string]string{
				"vm_name": dom.Name,
				"vm_uuid": dom.UUID,
			},
		})
	}

	return targets, nil
}

func (p *Provider) resolveEndpoint(virshPath, domainName string, dom *DomainXML) string {
	// 1. Check VSOCK first if configured
	if p.cfg.ConnectionMode == "vsock" || p.cfg.ConnectionMode == "auto" {
		if cid := dom.GetVsockCID(); cid != "" {
			return fmt.Sprintf("vsock://%s:%d", cid, p.cfg.DefaultPort)
		}
	}

	// 2. Query IP address via virsh domifaddr
	if (p.cfg.ConnectionMode == "ip" || p.cfg.ConnectionMode == "auto") && virshPath != "" {
		addrCmd := exec.Command(virshPath, "-c", p.cfg.URI, "domifaddr", domainName, "--source", "agent")
		if out, err := addrCmd.Output(); err == nil {
			if ip := parseFirstIPv4(out); ip != "" {
				return fmt.Sprintf("tcp://%s:%d", ip, p.cfg.DefaultPort)
			}
		}

		// Fallback to lease source
		addrCmdLease := exec.Command(virshPath, "-c", p.cfg.URI, "domifaddr", domainName, "--source", "lease")
		if out, err := addrCmdLease.Output(); err == nil {
			if ip := parseFirstIPv4(out); ip != "" {
				return fmt.Sprintf("tcp://%s:%d", ip, p.cfg.DefaultPort)
			}
		}
	}

	return ""
}

func parseFirstIPv4(out []byte) string {
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		fields := strings.Fields(l)
		for _, f := range fields {
			if strings.Contains(f, "/") {
				ip := strings.Split(f, "/")[0]
				if strings.Count(ip, ".") == 3 {
					return ip
				}
			}
		}
	}
	return ""
}

func (p *Provider) scanFromQEMURuntime() []appconfig.Target {
	runDirs := []string{"/var/run/libvirt/qemu", "/etc/libvirt/qemu"}
	var targets []appconfig.Target

	for _, dir := range runDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".xml") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}

			dom, err := ParseDomainXML(data)
			if err != nil {
				continue
			}

			if p.cfg.FilterGPUOnly && !dom.HasGPU() {
				continue
			}

			if cid := dom.GetVsockCID(); cid != "" {
				targets = append(targets, appconfig.Target{
					ID:       dom.UUID,
					Name:     dom.Name,
					Endpoint: fmt.Sprintf("vsock://%s:%d", cid, p.cfg.DefaultPort),
					Labels: map[string]string{
						"vm_name": dom.Name,
						"vm_uuid": dom.UUID,
					},
				})
			}
		}
	}

	return targets
}
