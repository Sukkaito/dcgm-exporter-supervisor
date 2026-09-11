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

package supervisor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// ScrapeResult encapsulates the outcome of scraping a child exporter instance.
type ScrapeResult struct {
	Target     appconfig.Target
	SocketPath string
	Port       int
	Data       []byte
	Error      error
	Elapsed    time.Duration
}

// Manager supervises the set of child dcgm-exporter instances.
type Manager struct {
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	config     appconfig.ExporterConfig
	instances  map[string]*Instance
	cmdBuilder CommandBuilder
}

// NewManager creates a new supervisor Manager.
func NewManager(cfg appconfig.ExporterConfig, builder CommandBuilder) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	if cfg.SocketDir == "" {
		cfg.SocketDir = appconfig.DefaultSocketDir
	}
	return &Manager{
		ctx:        ctx,
		cancel:     cancel,
		config:     cfg,
		instances:  make(map[string]*Instance),
		cmdBuilder: builder,
	}
}

// Reconcile adjusts the set of running instances to match the desired targets.
func (m *Manager) Reconcile(desired []appconfig.Target) {
	m.mu.Lock()
	defer m.mu.Unlock()

	desiredMap := make(map[string]appconfig.Target, len(desired))
	for _, t := range desired {
		id := t.ID
		if id == "" {
			id = t.Name
		}
		desiredMap[id] = t
	}

	// 1. Terminate instances for removed targets
	for id, inst := range m.instances {
		if _, exists := desiredMap[id]; !exists {
			slog.Info("Removing instance for target", slog.String("target", inst.Target.Name), slog.String("id", id))
			inst.Stop()
			delete(m.instances, id)
		}
	}

	// 2. Add or update instances for desired targets
	for id, t := range desiredMap {
		existing, exists := m.instances[id]
		if !exists {
			inst := NewInstance(t, "", m.config, m.cmdBuilder)
			m.instances[id] = inst
			inst.Start(m.ctx)
			continue
		}

		// If endpoint or critical configuration (like netns) changed, restart instance
		if existing.Target.Endpoint != t.Endpoint || existing.Target.NetNS != t.NetNS {
			slog.Info("Target configuration changed, restarting instance",
				slog.String("target", t.Name),
				slog.String("old_endpoint", existing.Target.Endpoint),
				slog.String("new_endpoint", t.Endpoint),
				slog.String("old_netns", existing.Target.NetNS),
				slog.String("new_netns", t.NetNS))
			existing.Stop()
			existing.Target = t
			existing.Start(m.ctx)
		} else {
			// Update dynamic fields
			existing.Target.Labels = t.Labels
			existing.Target.Tenant = t.Tenant
		}
	}
}

// StartHealthLoop starts the periodic background health checker.
func (m *Manager) StartHealthLoop(interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.mu.RLock()
				instances := make([]*Instance, 0, len(m.instances))
				for _, inst := range m.instances {
					instances = append(instances, inst)
				}
				m.mu.RUnlock()

				for _, inst := range instances {
					checkCtx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
					_ = inst.CheckHealth(checkCtx)
					cancel()
				}
			}
		}
	}()
}

// ListInstances returns a copy of all current instances.
func (m *Manager) ListInstances() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		res = append(res, inst)
	}
	return res
}

// GetInstance finds an instance by target ID or Name.
func (m *Manager) GetInstance(targetNameOrID string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if inst, exists := m.instances[targetNameOrID]; exists {
		return inst, true
	}

	for _, inst := range m.instances {
		if inst.Target.Name == targetNameOrID {
			return inst, true
		}
	}

	return nil, false
}

// ScrapeAll queries all managed instances concurrently.
func (m *Manager) ScrapeAll(ctx context.Context) []ScrapeResult {
	instances := m.ListInstances()
	results := make([]ScrapeResult, len(instances))

	var wg sync.WaitGroup
	for i, inst := range instances {
		wg.Add(1)
		go func(idx int, targetInst *Instance) {
			defer wg.Done()
			start := time.Now()
			data, err := targetInst.Scrape(ctx)
			results[idx] = ScrapeResult{
				Target:     targetInst.Target,
				SocketPath: targetInst.SocketPath,
				Port:       targetInst.Port,
				Data:       data,
				Error:      err,
				Elapsed:    time.Since(start),
			}
		}(i, inst)
	}

	wg.Wait()
	return results
}

// Shutdown stops all managed instances and background loops.
func (m *Manager) Shutdown() {
	m.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	var wg sync.WaitGroup
	for _, inst := range m.instances {
		wg.Add(1)
		go func(targetInst *Instance) {
			defer wg.Done()
			targetInst.Stop()
		}(inst)
	}
	wg.Wait()

	clear(m.instances)
}
