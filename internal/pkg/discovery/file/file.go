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

package file

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// Provider watches a directory for YAML target files.
type Provider struct {
	dir          string
	pollInterval time.Duration
}

// NewProvider creates a new file-based discovery provider.
func NewProvider(dir string, pollInterval time.Duration) *Provider {
	if pollInterval <= 0 {
		pollInterval = appconfig.DefaultFilePollInterval
	}
	return &Provider{
		dir:          dir,
		pollInterval: pollInterval,
	}
}

func (p *Provider) Name() string {
	return "file"
}

func (p *Provider) Run(ctx context.Context, ch chan<- []appconfig.Target) error {
	// Initial scan
	targets, err := p.scanDirectory()
	if err != nil {
		slog.Warn("Initial scan of target directory failed",
			slog.String("dir", p.dir),
			slog.String("error", err.Error()))
	} else {
		select {
		case ch <- targets:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := p.scanDirectory()
			if err != nil {
				slog.Warn("Failed scanning target directory",
					slog.String("dir", p.dir),
					slog.String("error", err.Error()))
				continue
			}
			select {
			case ch <- current:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (p *Provider) scanDirectory() ([]appconfig.Target, error) {
	if _, err := os.Stat(p.dir); os.IsNotExist(err) {
		return nil, fmt.Errorf("directory does not exist: %s", p.dir)
	}

	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read dir: %w", err)
	}

	var allTargets []appconfig.Target
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}

		fullPath := filepath.Join(p.dir, entry.Name())
		targets, err := parseTargetFile(fullPath)
		if err != nil {
			slog.Warn("Failed parsing target file",
				slog.String("file", fullPath),
				slog.String("error", err.Error()))
			continue
		}
		allTargets = append(allTargets, targets...)
	}

	return allTargets, nil
}

func parseTargetFile(path string) ([]appconfig.Target, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try parsing as slice of targets
	var targets []appconfig.Target
	if err := yaml.Unmarshal(data, &targets); err == nil && len(targets) > 0 {
		return targets, nil
	}

	// Try parsing as a single target
	var single appconfig.Target
	if err := yaml.Unmarshal(data, &single); err == nil && single.Endpoint != "" {
		if single.ID == "" {
			single.ID = single.Name
		}
		return []appconfig.Target{single}, nil
	}

	return nil, fmt.Errorf("file does not contain valid target or target list")
}
