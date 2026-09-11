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
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

func TestResolver_ExplicitOverride(t *testing.T) {
	cfg := appconfig.NetNSConfig{
		Enabled:      true,
		DefaultNetNS: "default-ns",
		Items: []appconfig.NetNSItemConfig{
			{Name: "item-ns", Tenants: []string{"tenant-1"}},
		},
	}
	r := NewResolver(cfg)

	target := &appconfig.Target{
		Tenant:   "tenant-1",
		NetNS:    "custom-ns",
		Endpoint: "tcp://10.0.0.1:5555",
	}

	assert.Equal(t, "custom-ns", r.Resolve(target))
}

func TestResolver_TenantMatch(t *testing.T) {
	cfg := appconfig.NetNSConfig{
		Enabled: true,
		Items: []appconfig.NetNSItemConfig{
			{
				Name:    "qrouter-tenant-a",
				Tenants: []string{"tenant-alpha", "uuid-1234"},
			},
			{
				Name:    "qrouter-tenant-b",
				Tenants: []string{"tenant-beta", "uuid-5678"},
			},
		},
	}
	r := NewResolver(cfg)

	// Match by target.Tenant
	assert.Equal(t, "qrouter-tenant-a", r.Resolve(&appconfig.Target{
		Tenant: "tenant-alpha",
	}))
	assert.Equal(t, "qrouter-tenant-a", r.Resolve(&appconfig.Target{
		Tenant: "UUID-1234", // case-insensitive
	}))
	assert.Equal(t, "qrouter-tenant-b", r.Resolve(&appconfig.Target{
		Tenant: "uuid-5678",
	}))

	// Match by Labels["project_id"]
	assert.Equal(t, "qrouter-tenant-a", r.Resolve(&appconfig.Target{
		Labels: map[string]string{"project_id": "uuid-1234"},
	}))

	// Match by Labels["tenant"]
	assert.Equal(t, "qrouter-tenant-b", r.Resolve(&appconfig.Target{
		Labels: map[string]string{"tenant": "tenant-beta"},
	}))
}

func TestResolver_Template(t *testing.T) {
	cfg := appconfig.NetNSConfig{
		Enabled:  true,
		Template: "qrouter-{{.Tenant}}",
		Items: []appconfig.NetNSItemConfig{
			{Name: "special-ns", Tenants: []string{"vip-tenant"}},
		},
	}
	r := NewResolver(cfg)

	// VIP tenant matches item
	assert.Equal(t, "special-ns", r.Resolve(&appconfig.Target{
		Tenant: "vip-tenant",
	}))

	// Other tenant uses template
	assert.Equal(t, "qrouter-tenant-99", r.Resolve(&appconfig.Target{
		Tenant: "tenant-99",
	}))
}

func TestResolver_SubnetMatch(t *testing.T) {
	cfg := appconfig.NetNSConfig{
		Enabled: true,
		Items: []appconfig.NetNSItemConfig{
			{
				Name:    "ns-v4",
				Subnets: []string{"10.10.1.0/24"},
			},
			{
				Name:    "ns-v6",
				Subnets: []string{"fd00:10:1::/64"},
			},
		},
		DefaultNetNS: "fallback-ns",
	}
	r := NewResolver(cfg)

	// IPv4 inside subnet
	assert.Equal(t, "ns-v4", r.Resolve(&appconfig.Target{
		Endpoint: "tcp://10.10.1.55:5555",
	}))

	// IPv6 inside subnet
	assert.Equal(t, "ns-v6", r.Resolve(&appconfig.Target{
		Endpoint: "tcp://[fd00:10:1::abc]:5555",
	}))

	// Outside subnet -> fallback
	assert.Equal(t, "fallback-ns", r.Resolve(&appconfig.Target{
		Endpoint: "tcp://192.168.1.1:5555",
	}))
}

func TestResolver_AutoDetectRoute(t *testing.T) {
	cfg := appconfig.NetNSConfig{
		Enabled:         true,
		AutoDetectRoute: true,
		Items: []appconfig.NetNSItemConfig{
			{Name: "router-1"},
			{Name: "router-2"},
		},
		DefaultNetNS: "fallback-ns",
	}
	r := NewResolver(cfg)

	r.SetRouteChecker(func(ctx context.Context, netns string, ip net.IP) bool {
		if netns == "router-2" && ip.String() == "172.16.0.10" {
			return true
		}
		return false
	})

	assert.Equal(t, "router-2", r.Resolve(&appconfig.Target{
		Endpoint: "tcp://172.16.0.10:5555",
	}))

	// Verify route cache
	assert.Equal(t, "router-2", r.Resolve(&appconfig.Target{
		Endpoint: "tcp://172.16.0.10:5555",
	}))

	// Unroutable IP falls back
	assert.Equal(t, "fallback-ns", r.Resolve(&appconfig.Target{
		Endpoint: "tcp://172.16.0.20:5555",
	}))
}

