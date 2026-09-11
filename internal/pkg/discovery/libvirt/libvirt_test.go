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
}

