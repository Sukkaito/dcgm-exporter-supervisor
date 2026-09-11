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
	"log/slog"
	"sync"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// Provider is implemented by discovery sources (Libvirt, Nova, Static, File).
type Provider interface {
	Name() string
	Run(ctx context.Context, ch chan<- []appconfig.Target) error
}

// Manager aggregates target updates from multiple discovery providers and sends unified updates.
type Manager struct {
	mu        sync.RWMutex
	providers []Provider
	targets   map[string]map[string]appconfig.Target // providerName -> targetID -> Target
	notifyCh  chan []appconfig.Target
}

// NewManager creates a new Discovery Manager.
func NewManager() *Manager {
	return &Manager{
		targets:  make(map[string]map[string]appconfig.Target),
		notifyCh: make(chan []appconfig.Target, 10),
	}
}

// RegisterProvider adds a discovery provider to the manager.
func (m *Manager) RegisterProvider(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers = append(m.providers, p)
}

// Start launches all registered providers in background goroutines.
func (m *Manager) Start(ctx context.Context) <-chan []appconfig.Target {
	m.mu.RLock()
	providers := make([]Provider, len(m.providers))
	copy(providers, m.providers)
	m.mu.RUnlock()

	for _, p := range providers {
		pCh := make(chan []appconfig.Target, 5)
		go func(prov Provider, inCh chan []appconfig.Target) {
			slog.Info("Starting discovery provider", slog.String("provider", prov.Name()))
			if err := prov.Run(ctx, inCh); err != nil && ctx.Err() == nil {
				slog.Error("Discovery provider exited with error",
					slog.String("provider", prov.Name()),
					slog.String("error", err.Error()))
			}
		}(p, pCh)

		go func(prov Provider, inCh chan []appconfig.Target) {
			for {
				select {
				case <-ctx.Done():
					return
				case targets, ok := <-inCh:
					if !ok {
						return
					}
					m.updateTargets(prov.Name(), targets)
				}
			}
		}(p, pCh)
	}

	return m.notifyCh
}

// updateTargets updates the target set for a specific provider and emits a deduplicated list.
func (m *Manager) updateTargets(providerName string, targets []appconfig.Target) {
	m.mu.Lock()
	defer m.mu.Unlock()

	provMap := make(map[string]appconfig.Target, len(targets))
	for _, t := range targets {
		if t.ID == "" {
			t.ID = t.Name
		}
		provMap[t.ID] = t
	}
	m.targets[providerName] = provMap

	// Build deduplicated targets map across all providers
	unified := make(map[string]appconfig.Target)
	for _, pMap := range m.targets {
		for id, t := range pMap {
			unified[id] = t
		}
	}

	result := make([]appconfig.Target, 0, len(unified))
	for _, t := range unified {
		result = append(result, t)
	}

	select {
	case m.notifyCh <- result:
	default:
		// Drain and replace if channel buffer is full
		select {
		case <-m.notifyCh:
		default:
		}
		m.notifyCh <- result
	}
}

// CurrentTargets returns the current snapshot of discovered targets.
func (m *Manager) CurrentTargets() []appconfig.Target {
	m.mu.RLock()
	defer m.mu.RUnlock()

	unified := make(map[string]appconfig.Target)
	for _, pMap := range m.targets {
		for id, t := range pMap {
			unified[id] = t
		}
	}

	result := make([]appconfig.Target, 0, len(unified))
	for _, t := range unified {
		result = append(result, t)
	}
	return result
}

