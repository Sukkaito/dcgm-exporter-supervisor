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
	"strings"
	"testing"
)

func TestInjectLabels(t *testing.T) {
	input := `# HELP DCGM_FI_DEV_SM_CLOCK SM clock frequency (in MHz).
# TYPE DCGM_FI_DEV_SM_CLOCK gauge
DCGM_FI_DEV_SM_CLOCK{gpu="0",UUID="GPU-123"} 139
DCGM_FI_DEV_POWER_USAGE 55.4
DCGM_FI_DEV_FAN_SPEED 30 1609459200000

# Comment line
`

	extraLabels := map[string]string{
		"vm_name": "ai-worker-01",
		"zone":    "us-central1-a",
	}

	out, err := InjectLabels([]byte(input), extraLabels)
	if err != nil {
		t.Fatalf("InjectLabels returned error: %v", err)
	}

	result := string(out)

	// Verify comments preserved
	if !strings.Contains(result, "# HELP DCGM_FI_DEV_SM_CLOCK SM clock frequency (in MHz).") {
		t.Errorf("HELP comment lost: %s", result)
	}
	if !strings.Contains(result, "# TYPE DCGM_FI_DEV_SM_CLOCK gauge") {
		t.Errorf("TYPE comment lost: %s", result)
	}
	if !strings.Contains(result, "# Comment line") {
		t.Errorf("Comment line lost: %s", result)
	}

	// Verify sample with existing labels
	expectedSampleWithLabels := `DCGM_FI_DEV_SM_CLOCK{gpu="0",UUID="GPU-123",vm_name="ai-worker-01",zone="us-central1-a"} 139`
	if !strings.Contains(result, expectedSampleWithLabels) {
		t.Errorf("expected %q in output, but got:\n%s", expectedSampleWithLabels, result)
	}

	// Verify sample without previous labels
	expectedSampleWithoutLabels := `DCGM_FI_DEV_POWER_USAGE{vm_name="ai-worker-01",zone="us-central1-a"} 55.4`
	if !strings.Contains(result, expectedSampleWithoutLabels) {
		t.Errorf("expected %q in output, but got:\n%s", expectedSampleWithoutLabels, result)
	}

	// Verify sample with timestamp
	expectedSampleWithTimestamp := `DCGM_FI_DEV_FAN_SPEED{vm_name="ai-worker-01",zone="us-central1-a"} 30 1609459200000`
	if !strings.Contains(result, expectedSampleWithTimestamp) {
		t.Errorf("expected %q in output, but got:\n%s", expectedSampleWithTimestamp, result)
	}
}

func TestInjectLabelsEscaping(t *testing.T) {
	input := `my_metric 42`
	extraLabels := map[string]string{
		"path":  `C:\foo\bar`,
		"quote": `a"b`,
	}

	out, err := InjectLabels([]byte(input), extraLabels)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := string(out)
	expected := `my_metric{path="C:\\foo\\bar",quote="a\"b"} 42`
	if !strings.Contains(result, expected) {
		t.Errorf("escaping failed: got %q, expected %q", result, expected)
	}
}

func TestInjectLabelsEmpty(t *testing.T) {
	input := `my_metric 42`
	out, err := InjectLabels([]byte(input), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != input {
		t.Errorf("expected %q, got %q", input, string(out))
	}
}
