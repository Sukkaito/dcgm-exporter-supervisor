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

package static

import (
	"context"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// Provider implements discovery.Provider for statically configured targets.
type Provider struct {
	targets []appconfig.Target
}

// NewProvider creates a new StaticProvider.
func NewProvider(targets []appconfig.Target) *Provider {
	return &Provider{
		targets: targets,
	}
}

func (p *Provider) Name() string {
	return "static"
}

func (p *Provider) Run(ctx context.Context, ch chan<- []appconfig.Target) error {
	// Send initial targets
	select {
	case ch <- p.targets:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Keep alive until shutdown
	<-ctx.Done()
	return ctx.Err()
}

