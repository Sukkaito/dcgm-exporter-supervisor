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
	"fmt"
	"net"
	"strconv"
	"strings"
)

// IsIPv6 returns true if the input string represents a valid IPv6 address (with or without zone).
func IsIPv6(ipStr string) bool {
	clean := stripZone(stripBrackets(ipStr))
	ip := net.ParseIP(clean)
	return ip != nil && ip.To4() == nil
}

// IsLinkLocal returns true if the input string represents an IPv6 link-local address (fe80::/10).
func IsLinkLocal(ipStr string) bool {
	clean := stripZone(stripBrackets(ipStr))
	ip := net.ParseIP(clean)
	return ip != nil && ip.To4() == nil && ip.IsLinkLocalUnicast()
}

// AttachScopeIfLinkLocal attaches %iface to link-local IPv6 addresses if not already scoped.
func AttachScopeIfLinkLocal(ipStr, iface string) string {
	if iface == "" || !IsLinkLocal(ipStr) {
		return ipStr
	}
	if strings.Contains(ipStr, "%") {
		return ipStr
	}
	// If bracketed, insert scope before closing bracket
	if strings.HasPrefix(ipStr, "[") && strings.HasSuffix(ipStr, "]") {
		return fmt.Sprintf("[%s%%%s]", ipStr[1:len(ipStr)-1], iface)
	}
	return fmt.Sprintf("%s%%%s", ipStr, iface)
}

func stripBrackets(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s[1 : len(s)-1]
	}
	return s
}

func stripZone(s string) string {
	if idx := strings.IndexByte(s, '%'); idx != -1 {
		return s[:idx]
	}
	return s
}

// FormatHostPort combines host and port into a valid host:port string.
// Automatically adds brackets for IPv6 addresses via net.JoinHostPort.
func FormatHostPort(host string, port int) string {
	cleanHost := stripBrackets(host)
	return net.JoinHostPort(cleanHost, strconv.Itoa(port))
}

// FormatHTTPURL formats a safe HTTP URL using net.JoinHostPort and RFC 6874 zone escaping (%25).
func FormatHTTPURL(scheme, host string, port int, path string) string {
	if scheme == "" {
		scheme = "http"
	}

	cleanHost := stripBrackets(host)
	// RFC 6874: in URIs, '%' in IPv6 scoped addresses must be percent-encoded as '%25'
	if strings.Contains(cleanHost, "%") && !strings.Contains(cleanHost, "%25") {
		cleanHost = strings.Replace(cleanHost, "%", "%25", 1)
	}

	hostPort := net.JoinHostPort(cleanHost, strconv.Itoa(port))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("%s://%s%s", scheme, hostPort, path)
}

// NormalizeEndpoint ensures an endpoint is properly formatted for dcgm-exporter.
// For IPv6, it enforces brackets when combined with a port according to DCGM specs.
func NormalizeEndpoint(endpoint string, defaultPort int) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("empty endpoint")
	}

	// Preserve non-TCP schemes
	if strings.HasPrefix(endpoint, "vsock://") || strings.HasPrefix(endpoint, "unix://") {
		return endpoint, nil
	}

	scheme := ""
	rem := endpoint
	if strings.HasPrefix(endpoint, "tcp://") {
		scheme = "tcp://"
		rem = strings.TrimPrefix(endpoint, "tcp://")
	}

	// Check if already bracketed: [host]:port or [host]
	if strings.HasPrefix(rem, "[") {
		closeBracket := strings.IndexByte(rem, ']')
		if closeBracket == -1 {
			return "", fmt.Errorf("malformed bracketed endpoint: %s", endpoint)
		}
		hostPart := rem[1:closeBracket]
		rest := rem[closeBracket+1:]
		if rest == "" {
			return fmt.Sprintf("%s[%s]:%d", scheme, hostPart, defaultPort), nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", fmt.Errorf("invalid character after bracket in endpoint: %s", endpoint)
		}
		return endpoint, nil
	}

	// Check if standard host:port (like 1.2.3.4:5555 or hostname:5555)
	host, portStr, err := net.SplitHostPort(rem)
	if err == nil {
		// If the host is IPv6, bracket it
		if IsIPv6(host) {
			return fmt.Sprintf("%s[%s]:%s", scheme, host, portStr), nil
		}
		return endpoint, nil
	}

	// Check if unbracketed IPv6 with port, e.g. "2001:db8::1:5555" or "fe80::1%vnet0:5555"
	if lastColon := strings.LastIndexByte(rem, ':'); lastColon != -1 {
		possibleHost := rem[:lastColon]
		possiblePort := rem[lastColon+1:]
		if p, err := strconv.Atoi(possiblePort); err == nil && p > 0 && p <= 65535 && IsIPv6(possibleHost) {
			return fmt.Sprintf("%s[%s]:%d", scheme, possibleHost, p), nil
		}
	}

	// If no port specified:
	// If it is IPv6 without port:
	if IsIPv6(rem) {
		return fmt.Sprintf("%s[%s]:%d", scheme, rem, defaultPort), nil
	}

	// Standard hostname or IPv4 without port
	return fmt.Sprintf("%s%s:%d", scheme, rem, defaultPort), nil
}
