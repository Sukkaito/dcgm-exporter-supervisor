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

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/exporter-toolkit/web"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/netutil"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/proxy"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/supervisor"
)

const (
	contentTypeOptionsHeader  = "X-Content-Type-Options"
	prometheusTextContentType = "text/plain; version=0.0.4; charset=utf-8"
)

// Server coordinates HTTP routing for metrics, service discovery, probing, and health.
type Server struct {
	cfg       *appconfig.Config
	manager   *supervisor.Manager
	router    *mux.Router
	httpSrv   *http.Server
	webConfig *web.FlagConfig
}

// NewServer initializes a new Server.
func NewServer(cfg *appconfig.Config, mgr *supervisor.Manager) *Server {
	router := mux.NewRouter()

	readTimeout := cfg.WebReadTimeout
	if readTimeout <= 0 {
		readTimeout = appconfig.DefaultWebReadTimeout
	}
	writeTimeout := cfg.WebWriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = appconfig.DefaultWebWriteTimeout
	}

	httpSrv := &http.Server{
		Addr:         cfg.Address,
		Handler:      router,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	s := &Server{
		cfg:     cfg,
		manager: mgr,
		router:  router,
		httpSrv: httpSrv,
		webConfig: &web.FlagConfig{
			WebListenAddresses: &[]string{cfg.Address},
			WebConfigFile:      &cfg.WebConfigFile,
		},
	}

	s.setupRoutes()
	return s
}

// Router returns the underlying gorilla/mux router for testing.
func (s *Server) Router() *mux.Router {
	return s.router
}

func (s *Server) setupRoutes() {
	s.router.HandleFunc("/", s.handleRoot).Methods(http.MethodGet)
	s.router.HandleFunc("/metrics", s.handleMetrics).Methods(http.MethodGet)
	s.router.HandleFunc("/targets", s.handleTargets).Methods(http.MethodGet)
	s.router.HandleFunc("/probe", s.handleProbe).Methods(http.MethodGet)
	s.router.HandleFunc("/health", s.handleHealth).Methods(http.MethodGet)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(contentTypeOptionsHeader, "nosniff")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html>
<head><title>DCGM Exporter Supervisor</title></head>
<body>
<h1>DCGM Exporter Supervisor</h1>
<ul>
  <li><a href="/metrics">Metrics (Aggregated)</a></li>
  <li><a href="/targets">Prometheus HTTP Service Discovery (/targets)</a></li>
  <li><a href="/probe?target=example">Probe (/probe?target=&lt;name&gt;)</a></li>
  <li><a href="/health">Health Status</a></li>
</ul>
</body>
</html>`
	_, _ = w.Write([]byte(html))
}

// handleMetrics aggregates and enriches metrics from all supervised child instances.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.WebWriteTimeout-1*time.Second)
	defer cancel()

	results := s.manager.ScrapeAll(ctx)

	var buf bytes.Buffer
	targetsUp := 0
	scrapeErrors := 0

	for _, res := range results {
		if res.Error != nil {
			scrapeErrors++
			continue
		}

		labels := make(map[string]string, len(res.Target.Labels)+2)
		for k, v := range res.Target.Labels {
			labels[k] = v
		}
		if _, ok := labels["vm_name"]; !ok {
			labels["vm_name"] = res.Target.Name
		}
		labels["target_id"] = res.Target.ID

		enriched, err := proxy.InjectLabels(res.Data, labels)
		if err != nil {
			scrapeErrors++
			continue
		}

		targetsUp++
		buf.Write(enriched)
		buf.WriteByte('\n')
	}

	// Append supervisor operational self-metrics
	totalElapsed := time.Since(start).Seconds()
	buf.WriteString("# HELP dcgm_supervisor_targets_total Total number of supervised targets\n")
	buf.WriteString("# TYPE dcgm_supervisor_targets_total gauge\n")
	buf.WriteString(fmt.Sprintf("dcgm_supervisor_targets_total %d\n", len(results)))

	buf.WriteString("# HELP dcgm_supervisor_targets_up Number of successfully scraped targets\n")
	buf.WriteString("# TYPE dcgm_supervisor_targets_up gauge\n")
	buf.WriteString(fmt.Sprintf("dcgm_supervisor_targets_up %d\n", targetsUp))

	buf.WriteString("# HELP dcgm_supervisor_scrape_duration_seconds Time taken to aggregate all target scrapes\n")
	buf.WriteString("# TYPE dcgm_supervisor_scrape_duration_seconds gauge\n")
	buf.WriteString(fmt.Sprintf("dcgm_supervisor_scrape_duration_seconds %.6f\n", totalElapsed))

	buf.WriteString("# HELP dcgm_supervisor_scrape_errors_total Total number of target scrape failures\n")
	buf.WriteString("# TYPE dcgm_supervisor_scrape_errors_total counter\n")
	buf.WriteString(fmt.Sprintf("dcgm_supervisor_scrape_errors_total %d\n", scrapeErrors))

	w.Header().Set("Content-Type", prometheusTextContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// HTTPSDTarget represents the Prometheus HTTP SD JSON structure.
type HTTPSDTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// handleTargets exposes Prometheus HTTP Service Discovery endpoint (/targets).
func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	instances := s.manager.ListInstances()
	sdTargets := make([]HTTPSDTarget, 0, len(instances))

	for _, inst := range instances {
		labels := make(map[string]string, len(inst.Target.Labels)+2)
		for k, v := range inst.Target.Labels {
			labels[k] = v
		}
		labels["vm_name"] = inst.Target.Name
		labels["target_id"] = inst.Target.ID
		labels["endpoint"] = inst.Target.Endpoint

		sdTargets = append(sdTargets, HTTPSDTarget{
			Targets: []string{netutil.FormatHostPort(inst.Config.ListenHost, inst.Port)},
			Labels:  labels,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(sdTargets)
}

// handleProbe proxies a scrape request for a single target specified via query param ?target=.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	targetParam := r.URL.Query().Get("target")
	if targetParam == "" {
		http.Error(w, "missing target query parameter", http.StatusBadRequest)
		return
	}

	inst, found := s.manager.GetInstance(targetParam)
	if !found {
		http.Error(w, fmt.Sprintf("target %q not found", targetParam), http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.WebWriteTimeout-1*time.Second)
	defer cancel()

	data, err := inst.Scrape(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("scrape failed: %v", err), http.StatusBadGateway)
		return
	}

	labels := make(map[string]string, len(inst.Target.Labels)+2)
	for k, v := range inst.Target.Labels {
		labels[k] = v
	}
	labels["vm_name"] = inst.Target.Name
	labels["target_id"] = inst.Target.ID

	enriched, err := proxy.InjectLabels(data, labels)
	if err != nil {
		http.Error(w, fmt.Sprintf("relabel failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", prometheusTextContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(enriched)
}

// HealthResponse represents supervisor status.
type HealthResponse struct {
	Status    string                 `json:"status"`
	Instances map[string]interface{} `json:"instances"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	instances := s.manager.ListInstances()
	instMap := make(map[string]interface{}, len(instances))

	allHealthy := true
	for _, inst := range instances {
		st := inst.State()
		if st == supervisor.StateUnhealthy || st == supervisor.StateRestarting {
			allHealthy = false
		}
		instMap[inst.Target.Name] = map[string]interface{}{
			"id":       inst.Target.ID,
			"port":     inst.Port,
			"state":    st,
			"endpoint": inst.Target.Endpoint,
		}
	}

	status := "healthy"
	code := http.StatusOK
	if !allHealthy {
		status = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(HealthResponse{
		Status:    status,
		Instances: instMap,
	})
}

// Run starts the webserver.
func (s *Server) Run(ctx context.Context) error {
	slog.Info("Starting supervisor HTTP server", slog.String("address", s.cfg.Address))
	return web.ListenAndServe(s.httpSrv, s.webConfig, slog.Default())
}

// Shutdown gracefully shuts down the webserver.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}
