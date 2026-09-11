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

package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// escapeLabelValue escapes quotes, backslashes, and newlines for Prometheus label values.
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// formatExtraLabels generates a deterministic comma-separated string of key="val".
func formatExtraLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(escapeLabelValue(labels[k]))
		sb.WriteByte('"')
	}
	return sb.String()
}

// InjectLabels streams through Prometheus exposition text, injecting extra labels into each metric sample.
// Comment lines (# HELP, # TYPE) and whitespace are preserved intact.
func InjectLabels(rawMetrics []byte, extraLabels map[string]string) ([]byte, error) {
	if len(extraLabels) == 0 {
		return rawMetrics, nil
	}

	formattedExtra := formatExtraLabels(extraLabels)
	var out bytes.Buffer
	out.Grow(len(rawMetrics) + len(rawMetrics)/4)

	scanner := bufio.NewScanner(bytes.NewReader(rawMetrics))
	for scanner.Scan() {
		line := scanner.Bytes()
		trimmed := bytes.TrimSpace(line)

		// Pass through empty lines and comments
		if len(trimmed) == 0 || trimmed[0] == '#' {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}

		// Find where metric name ends and labels/value begins
		braceOpen := bytes.IndexByte(trimmed, '{')
		if braceOpen != -1 {
			// Line has existing labels: metric_name{labels} value [timestamp]
			braceClose := bytes.LastIndexByte(trimmed, '}')
			if braceClose == -1 || braceClose < braceOpen {
				// Malformed line, pass as is
				out.Write(line)
				out.WriteByte('\n')
				continue
			}

			metricName := trimmed[:braceOpen]
			existingLabels := trimmed[braceOpen+1 : braceClose]
			rest := trimmed[braceClose+1:]

			out.Write(metricName)
			out.WriteByte('{')
			if len(existingLabels) > 0 {
				out.Write(existingLabels)
				out.WriteByte(',')
			}
			out.WriteString(formattedExtra)
			out.WriteByte('}')
			out.Write(rest)
			out.WriteByte('\n')
		} else {
			// Line has no labels: metric_name value [timestamp]
			spaceIdx := bytes.IndexAny(trimmed, " \t")
			if spaceIdx == -1 {
				// Malformed line
				out.Write(line)
				out.WriteByte('\n')
				continue
			}

			metricName := trimmed[:spaceIdx]
			rest := trimmed[spaceIdx:]

			out.Write(metricName)
			out.WriteByte('{')
			out.WriteString(formattedExtra)
			out.WriteByte('}')
			out.Write(rest)
			out.WriteByte('\n')
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning metrics buffer: %w", err)
	}

	return out.Bytes(), nil
}

