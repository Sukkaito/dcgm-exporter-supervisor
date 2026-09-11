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

package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/discovery/static"
)

func TestDiscoveryManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()

	t1 := appconfig.Target{
		ID:       "vm-1",
		Name:     "vm-1",
		Endpoint: "tcp://192.168.1.10:5555",
		Labels:   map[string]string{"env": "test"},
	}

	p1 := static.NewProvider([]appconfig.Target{t1})
	mgr.RegisterProvider(p1)

	outCh := mgr.Start(ctx)

	select {
	case targets := <-outCh:
		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}
		if targets[0].ID != "vm-1" {
			t.Fatalf("expected vm-1, got %s", targets[0].ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for discovery update")
	}

	curr := mgr.CurrentTargets()
	if len(curr) != 1 || curr[0].ID != "vm-1" {
		t.Fatalf("unexpected CurrentTargets: %v", curr)
	}
}
