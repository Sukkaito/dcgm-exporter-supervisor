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

package netns

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

type subnetEntry struct {
	cidr  *net.IPNet
	netns string
}

type routeChecker func(ctx context.Context, netns string, ip net.IP) bool

// Resolver maps targets to their designated network namespace.
type Resolver struct {
	cfg          appconfig.NetNSConfig
	subnets      []subnetEntry
	candidates   []string
	routeChecker routeChecker

	mu         sync.RWMutex
	routeCache map[string]string // ip.String() -> netns
}

func defaultRouteChecker(ctx context.Context, netnsName string, ip net.IP) bool {
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName, "ip", "route", "get", ip.String())
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "dev ") && !strings.Contains(s, "unreachable")
}

// NewResolver initializes a Resolver using the supplied NetNSConfig.
func NewResolver(cfg appconfig.NetNSConfig) *Resolver {
	r := &Resolver{
		cfg:          cfg,
		routeChecker: defaultRouteChecker,
		routeCache:   make(map[string]string),
	}

	seenCandidates := make(map[string]bool)
	for _, item := range cfg.Items {
		if item.Name == "" {
			continue
		}
		if !seenCandidates[item.Name] {
			seenCandidates[item.Name] = true
			r.candidates = append(r.candidates, item.Name)
		}

		for _, s := range item.Subnets {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			_, ipNet, err := net.ParseCIDR(s)
			if err != nil {
				slog.Warn("Invalid CIDR in netns config",
					slog.String("netns", item.Name),
					slog.String("cidr", s),
					slog.String("error", err.Error()))
				continue
			}
			r.subnets = append(r.subnets, subnetEntry{
				cidr:  ipNet,
				netns: item.Name,
			})
		}
	}

	return r
}

// SetRouteChecker sets a custom route checker (primarily for testing).
func (r *Resolver) SetRouteChecker(rc routeChecker) {
	r.routeChecker = rc
}

// Resolve returns the designated network namespace for the given target.
func (r *Resolver) Resolve(t *appconfig.Target) string {
	if t == nil {
		return ""
	}

	// 1. Explicit override on target
	if t.NetNS != "" {
		return t.NetNS
	}

	// 2. Tenant matching (by target.Tenant or labels)
	tenant := t.Tenant
	if tenant == "" && t.Labels != nil {
		if p, ok := t.Labels["project_id"]; ok && p != "" {
			tenant = p
		} else if tn, ok := t.Labels["tenant"]; ok && tn != "" {
			tenant = tn
		}
	}

	if tenant != "" {
		// Check explicit items list
		for _, item := range r.cfg.Items {
			for _, itemTenant := range item.Tenants {
				if strings.EqualFold(itemTenant, tenant) {
					return item.Name
				}
			}
		}

		// Dynamic template rendering
		if r.cfg.Template != "" {
			ns := r.cfg.Template
			ns = strings.ReplaceAll(ns, "{{.Tenant}}", tenant)
			ns = strings.ReplaceAll(ns, "{{.TenantID}}", tenant)
			if ns != "" {
				return ns
			}
		}
	}

	// 3. Subnet / CIDR matching on endpoint IP
	ip := extractIP(t.Endpoint)
	if ip != nil {
		for _, se := range r.subnets {
			if se.cidr.Contains(ip) {
				return se.netns
			}
		}

		// 4. Auto-detect route if enabled
		if r.cfg.AutoDetectRoute && len(r.candidates) > 0 && r.routeChecker != nil {
			ipStr := ip.String()
			r.mu.RLock()
			cached, found := r.routeCache[ipStr]
			r.mu.RUnlock()
			if found {
				return cached
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			for _, candidate := range r.candidates {
				if r.routeChecker(ctx, candidate, ip) {
					r.mu.Lock()
					r.routeCache[ipStr] = candidate
					r.mu.Unlock()
					return candidate
				}
			}
		}
	}

	// 5. Fallback default
	if r.cfg.DefaultNetNS != "" {
		return r.cfg.DefaultNetNS
	}

	return ""
}

// extractIP parses the host/IP from an endpoint string.
func extractIP(endpoint string) net.IP {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}

	// If scheme is present
	host := endpoint
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err == nil && u.Host != "" {
			host = u.Host
		} else {
			parts := strings.SplitN(endpoint, "://", 2)
			if len(parts) == 2 {
				host = parts[1]
			}
		}
	}

	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}

	// Clean IPv6 brackets and interface scope
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	if idx := strings.Index(h, "%"); idx != -1 {
		h = h[:idx]
	}

	return net.ParseIP(h)
}
