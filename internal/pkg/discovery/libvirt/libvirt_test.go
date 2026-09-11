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
	"testing"
)

func TestParseAndSelectIP(t *testing.T) {
	domifaddrOutput := `
 Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 vnet0      52:54:00:12:34:56    ipv4         192.168.122.42/24
 vnet0      52:54:00:12:34:56    ipv6         fe80::5054:ff:fe12:3456/64
 vnet0      52:54:00:12:34:56    ipv6         2001:db8::10/64
`

	t.Run("select ipv4", func(t *testing.T) {
		got := parseAndSelectIP([]byte(domifaddrOutput), "ipv4")
		if got != "192.168.122.42" {
			t.Errorf("expected 192.168.122.42, got %s", got)
		}
	})

	t.Run("select ipv6 global unicast", func(t *testing.T) {
		got := parseAndSelectIP([]byte(domifaddrOutput), "ipv6")
		if got != "2001:db8::10" {
			t.Errorf("expected 2001:db8::10, got %s", got)
		}
	})

	t.Run("select auto prefers ipv4", func(t *testing.T) {
		got := parseAndSelectIP([]byte(domifaddrOutput), "auto")
		if got != "192.168.122.42" {
			t.Errorf("expected 192.168.122.42, got %s", got)
		}
	})

	t.Run("select link-local with scope when only LLA present", func(t *testing.T) {
		llaOnlyOutput := `
 Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 vnet1      52:54:00:99:88:77    ipv6         fe80::5054:ff:fe99:8877/64
`
		got := parseAndSelectIP([]byte(llaOnlyOutput), "ipv6")
		expected := "fe80::5054:ff:fe99:8877%vnet1"
		if got != expected {
			t.Errorf("expected %s, got %s", expected, got)
		}

		// Also verify auto fallback to LLA when IPv4 is not present
		gotAuto := parseAndSelectIP([]byte(llaOnlyOutput), "auto")
		if gotAuto != expected {
			t.Errorf("expected %s under auto, got %s", expected, gotAuto)
		}
	})

	t.Run("ignores loopback lo interface and picks valid ip", func(t *testing.T) {
		agentOutputWithLo := `
 Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 lo         00:00:00:00:00:00    ipv4         127.0.0.1/8
 -          -                    ipv6         ::1/128
 eth0       52:54:00:aa:bb:cc    ipv4         10.0.10.5/24
 -          -                    ipv6         2001:db8::55/64
`
		gotV4 := parseAndSelectIP([]byte(agentOutputWithLo), "ipv4")
		if gotV4 != "10.0.10.5" {
			t.Errorf("expected 10.0.10.5, got %s", gotV4)
		}

		gotV6 := parseAndSelectIP([]byte(agentOutputWithLo), "ipv6")
		if gotV6 != "2001:db8::55" {
			t.Errorf("expected 2001:db8::55, got %s", gotV6)
		}

		gotAuto := parseAndSelectIP([]byte(agentOutputWithLo), "auto")
		if gotAuto != "10.0.10.5" {
			t.Errorf("expected 10.0.10.5 under auto, got %s", gotAuto)
		}
	})

	t.Run("returns empty when only loopback present", func(t *testing.T) {
		loOnlyOutput := `
 Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 lo         00:00:00:00:00:00    ipv4         127.0.0.1/8
 -          -                    ipv6         ::1/128
`
		got := parseAndSelectIP([]byte(loOnlyOutput), "auto")
		if got != "" {
			t.Errorf("expected empty string when only lo present, got %s", got)
		}
	})
}
