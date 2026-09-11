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

package netutil

import (
	"testing"
)

func TestIsIPv6(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", false},
		{"192.168.1.1", false},
		{"::1", true},
		{"[::1]", true},
		{"2001:db8::1", true},
		{"[2001:db8::1]", true},
		{"fe80::1%eth0", true},
		{"[fe80::1%eth0]", true},
		{"not-an-ip", false},
	}

	for _, c := range cases {
		got := IsIPv6(c.in)
		if got != c.want {
			t.Errorf("IsIPv6(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestIsLinkLocal(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", false},
		{"::1", false},
		{"2001:db8::1", false},
		{"fe80::1", true},
		{"fe80::5054:ff:fe12:3456%vnet0", true},
		{"[fe80::1%eth0]", true},
	}

	for _, c := range cases {
		got := IsLinkLocal(c.in)
		if got != c.want {
			t.Errorf("IsLinkLocal(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestAttachScopeIfLinkLocal(t *testing.T) {
	if got := AttachScopeIfLinkLocal("fe80::1", "vnet0"); got != "fe80::1%vnet0" {
		t.Errorf("AttachScopeIfLinkLocal unexpected: %s", got)
	}
	// Already scoped
	if got := AttachScopeIfLinkLocal("fe80::1%eth0", "vnet0"); got != "fe80::1%eth0" {
		t.Errorf("AttachScopeIfLinkLocal should not overwrite scope: %s", got)
	}
	// Global IPv6 shouldn't attach scope
	if got := AttachScopeIfLinkLocal("2001:db8::1", "vnet0"); got != "2001:db8::1" {
		t.Errorf("AttachScopeIfLinkLocal should not affect global IPv6: %s", got)
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		in          string
		defaultPort int
		want        string
		wantErr     bool
	}{
		// Non-TCP schemes
		{"vsock://3:5555", 5555, "vsock://3:5555", false},
		{"unix:///tmp/sock", 5555, "unix:///tmp/sock", false},

		// Standard IPv4
		{"192.168.1.10:5555", 5555, "192.168.1.10:5555", false},
		{"tcp://192.168.1.10:5555", 5555, "tcp://192.168.1.10:5555", false},
		{"192.168.1.10", 5555, "192.168.1.10:5555", false},

		// IPv6 Bracketed
		{"[::1]:5555", 5555, "[::1]:5555", false},
		{"tcp://[::1]:5555", 5555, "tcp://[::1]:5555", false},
		{"[2001:db8::1]:5555", 5555, "[2001:db8::1]:5555", false},
		{"tcp://[2001:db8::1]", 5555, "tcp://[2001:db8::1]:5555", false},

		// IPv6 Unbracketed (normalization should bracket them)
		{"2001:db8::1", 5555, "[2001:db8::1]:5555", false},
		{"tcp://2001:db8::1", 5555, "tcp://[2001:db8::1]:5555", false},
		{"2001:db8::1:5555", 5555, "[2001:db8::1]:5555", false},
		{"tcp://2001:db8::1:5555", 5555, "tcp://[2001:db8::1]:5555", false},

		// IPv6 Link-Local with scope
		{"fe80::1%vnet0:5555", 5555, "[fe80::1%vnet0]:5555", false},
		{"tcp://fe80::1%vnet0:5555", 5555, "tcp://[fe80::1%vnet0]:5555", false},
		{"tcp://[fe80::1%vnet0]:5555", 5555, "tcp://[fe80::1%vnet0]:5555", false},
	}

	for _, c := range cases {
		got, err := NormalizeEndpoint(c.in, c.defaultPort)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeEndpoint(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeEndpoint(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestFormatHTTPURL(t *testing.T) {
	if got := FormatHTTPURL("http", "127.0.0.1", 9400, "/metrics"); got != "http://127.0.0.1:9400/metrics" {
		t.Errorf("FormatHTTPURL IPv4 unexpected: %s", got)
	}

	if got := FormatHTTPURL("http", "::1", 9400, "/metrics"); got != "http://[::1]:9400/metrics" {
		t.Errorf("FormatHTTPURL IPv6 unexpected: %s", got)
	}

	if got := FormatHTTPURL("http", "[::1]", 9400, "/metrics"); got != "http://[::1]:9400/metrics" {
		t.Errorf("FormatHTTPURL Bracketed IPv6 unexpected: %s", got)
	}

	if got := FormatHTTPURL("http", "fe80::1%vnet0", 9400, "/metrics"); got != "http://[fe80::1%25vnet0]:9400/metrics" {
		t.Errorf("FormatHTTPURL Scoped IPv6 unexpected: %s", got)
	}
}

func TestFormatHostPort(t *testing.T) {
	if got := FormatHostPort("127.0.0.1", 9401); got != "127.0.0.1:9401" {
		t.Errorf("FormatHostPort IPv4 unexpected: %s", got)
	}

	if got := FormatHostPort("::1", 9401); got != "[::1]:9401" {
		t.Errorf("FormatHostPort IPv6 unexpected: %s", got)
	}

	if got := FormatHostPort("[::1]", 9401); got != "[::1]:9401" {
		t.Errorf("FormatHostPort Bracketed IPv6 unexpected: %s", got)
	}
}

