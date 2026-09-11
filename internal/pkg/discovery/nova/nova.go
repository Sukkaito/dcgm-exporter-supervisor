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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/netutil"
)

// Provider discovers target VMs from the OpenStack Nova compute API.
type Provider struct {
	cfg        appconfig.NovaConfig
	httpClient *http.Client
	token      string
	tokenExp   time.Time
}

// NewProvider creates a new Nova API discovery provider.
func NewProvider(cfg appconfig.NovaConfig) *Provider {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = appconfig.DefaultNovaPollInterval
	}
	if cfg.DefaultPort <= 0 {
		cfg.DefaultPort = appconfig.DefaultHostEnginePort
	}
	if cfg.IPVersion == "" {
		cfg.IPVersion = "ipv4"
	}
	return &Provider{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *Provider) Name() string {
	return "nova"
}

func (p *Provider) Run(ctx context.Context, ch chan<- []appconfig.Target) error {
	targets, err := p.scanServers(ctx)
	if err != nil {
		slog.Warn("Initial Nova discovery scan failed", slog.String("error", err.Error()))
	} else {
		select {
		case ch <- targets:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := p.scanServers(ctx)
			if err != nil {
				slog.Warn("Nova discovery scan failed", slog.String("error", err.Error()))
				continue
			}
			select {
			case ch <- current:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

type novaServerListResponse struct {
	Servers []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
		Addresses map[string][]struct {
			Version int    `json:"version"`
			Addr    string `json:"addr"`
			Type    string `json:"OS-EXT-IPS:type"`
		} `json:"addresses"`
		Metadata map[string]string `json:"metadata"`
		HostID   string            `json:"hostId"`
		TenantID string            `json:"tenant_id"`
	} `json:"servers"`
}

func (p *Provider) scanServers(ctx context.Context) ([]appconfig.Target, error) {
	if p.cfg.NovaEndpoint == "" {
		return nil, fmt.Errorf("nova endpoint is not configured")
	}

	token, err := p.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire keystone token: %w", err)
	}

	url := fmt.Sprintf("%s/servers/detail?status=ACTIVE&all_tenants=1", strings.TrimRight(p.cfg.NovaEndpoint, "/"))
	if p.cfg.HypervisorHostname != "" {
		url += fmt.Sprintf("&host=%s", p.cfg.HypervisorHostname)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nova request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nova API returned status %d", resp.StatusCode)
	}

	var data novaServerListResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode nova response: %w", err)
	}

	var targets []appconfig.Target
	for _, s := range data.Servers {
		ip := extractIP(s.Addresses, p.cfg.IPVersion)
		if ip == "" {
			continue
		}

		endpoint, err := netutil.NormalizeEndpoint("tcp://"+ip, p.cfg.DefaultPort)
		if err != nil {
			continue
		}

		labels := map[string]string{
			"vm_name":    s.Name,
			"vm_id":      s.ID,
			"project_id": s.TenantID,
		}
		for k, v := range s.Metadata {
			labels["nova_"+k] = v
		}

		targets = append(targets, appconfig.Target{
			ID:       s.ID,
			Name:     s.Name,
			Endpoint: endpoint,
			Labels:   labels,
		})
	}

	return targets, nil
}

func extractIP(addresses map[string][]struct {
	Version int    `json:"version"`
	Addr    string `json:"addr"`
	Type    string `json:"OS-EXT-IPS:type"`
}, ipVersion string) string {
	if ipVersion == "" {
		ipVersion = "ipv4"
	}

	findV4 := func() string {
		for _, addrs := range addresses {
			for _, a := range addrs {
				if a.Version == 4 && a.Addr != "" {
					return a.Addr
				}
			}
		}
		return ""
	}

	findV6 := func() string {
		// Prefer global unicast IPv6 first
		for _, addrs := range addresses {
			for _, a := range addrs {
				if (a.Version == 6 || netutil.IsIPv6(a.Addr)) && !netutil.IsLinkLocal(a.Addr) && a.Addr != "" {
					return a.Addr
				}
			}
		}
		// Fallback to link-local if present
		for _, addrs := range addresses {
			for _, a := range addrs {
				if (a.Version == 6 || netutil.IsIPv6(a.Addr)) && a.Addr != "" {
					return a.Addr
				}
			}
		}
		return ""
	}

	switch ipVersion {
	case "ipv6":
		return findV6()
	case "auto":
		if ip := findV4(); ip != "" {
			return ip
		}
		return findV6()
	default: // "ipv4"
		return findV4()
	}
}

func (p *Provider) getToken(ctx context.Context) (string, error) {
	if p.token != "" && time.Now().Before(p.tokenExp) {
		return p.token, nil
	}

	if p.cfg.AuthURL == "" {
		// If auth URL is not supplied, use token-less / direct mock
		return "dummy-token", nil
	}

	userDomain := p.cfg.UserDomainName
	if userDomain == "" {
		userDomain = "Default"
	}
	projectDomain := p.cfg.ProjectDomainName
	if projectDomain == "" {
		projectDomain = "Default"
	}

	authPayload := map[string]interface{}{
		"auth": map[string]interface{}{
			"identity": map[string]interface{}{
				"methods": []string{"password"},
				"password": map[string]interface{}{
					"user": map[string]interface{}{
						"name": p.cfg.Username,
						"domain": map[string]string{
							"name": userDomain,
						},
						"password": p.cfg.Password,
					},
				},
			},
			"scope": map[string]interface{}{
				"project": map[string]interface{}{
					"name": p.cfg.ProjectName,
					"domain": map[string]string{
						"name": projectDomain,
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(authPayload)
	if err != nil {
		return "", err
	}

	tokenURL := fmt.Sprintf("%s/v3/auth/tokens", strings.TrimRight(p.cfg.AuthURL, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keystone auth failed with status %d", resp.StatusCode)
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("missing X-Subject-Token header in keystone response")
	}

	p.token = token
	p.tokenExp = time.Now().Add(2 * time.Hour)
	return token, nil
}
