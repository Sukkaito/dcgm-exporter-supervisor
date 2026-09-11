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
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
)

// InstanceState represents the lifecycle status of a child exporter instance.
type InstanceState string

const (
	StateStarting   InstanceState = "starting"
	StateRunning    InstanceState = "running"
	StateUnhealthy  InstanceState = "unhealthy"
	StateRestarting InstanceState = "restarting"
	StateStopped    InstanceState = "stopped"
)

// CommandBuilder creates an *exec.Cmd. Production uses exec.CommandContext; tests can override.
type CommandBuilder func(ctx context.Context, name string, args ...string) *exec.Cmd

func defaultCommandBuilder(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// Instance manages a single child dcgm-exporter process.
type Instance struct {
	mu             sync.RWMutex
	Target         appconfig.Target
	Port           int
	Config         appconfig.ExporterConfig
	cmdBuilder     CommandBuilder
	httpClient     *http.Client

	state          InstanceState
	pid            int
	startTime      time.Time
	restartCount   int
	lastError      error
	cancelFunc     context.CancelFunc
	stoppedCh      chan struct{}
}

// NewInstance creates a new Instance configuration.
func NewInstance(target appconfig.Target, port int, cfg appconfig.ExporterConfig, builder CommandBuilder) *Instance {
	if builder == nil {
		builder = defaultCommandBuilder
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = appconfig.DefaultShutdownTimeout
	}
	return &Instance{
		Target:     target,
		Port:       port,
		Config:     cfg,
		cmdBuilder: builder,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		state:     StateStarting,
		stoppedCh: make(chan struct{}),
	}
}

// Start launches the supervision loop for the child process.
func (inst *Instance) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	inst.mu.Lock()
	inst.cancelFunc = cancel
	inst.mu.Unlock()

	go inst.runLoop(ctx)
}

func (inst *Instance) runLoop(ctx context.Context) {
	defer close(inst.stoppedCh)

	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			inst.setState(StateStopped)
			return
		default:
		}

		inst.setState(StateStarting)
		err := inst.execProcess(ctx)
		if ctx.Err() != nil {
			inst.setState(StateStopped)
			return
		}

		inst.mu.Lock()
		inst.restartCount++
		inst.lastError = err
		inst.mu.Unlock()

		inst.setState(StateRestarting)
		slog.Warn("Child dcgm-exporter exited, scheduling restart",
			slog.String("target", inst.Target.Name),
			slog.Int("port", inst.Port),
			slog.String("error", fmt.Sprint(err)),
			slog.Duration("backoff", backoff))

		select {
		case <-ctx.Done():
			inst.setState(StateStopped)
			return
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (inst *Instance) execProcess(ctx context.Context) error {
	args := []string{
		"-r", inst.Target.Endpoint,
		"-a", fmt.Sprintf("127.0.0.1:%d", inst.Port),
	}

	if inst.Config.CollectorsFile != "" {
		args = append(args, "-f", inst.Config.CollectorsFile)
	}
	if inst.Config.CollectInterval > 0 {
		args = append(args, "-c", strconv.Itoa(inst.Config.CollectInterval))
	}
	if inst.Config.WebConfigFile != "" {
		args = append(args, "--web-config-file", inst.Config.WebConfigFile)
	}
	args = append(args, inst.Config.ExtraArgs...)
	args = append(args, inst.Target.CustomArgs...)

	cmd := inst.cmdBuilder(ctx, inst.Config.BinaryPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	inst.mu.Lock()
	inst.pid = cmd.Process.Pid
	inst.startTime = time.Now()
	inst.state = StateRunning
	inst.mu.Unlock()

	slog.Info("Child dcgm-exporter started",
		slog.String("target", inst.Target.Name),
		slog.Int("pid", inst.pid),
		slog.Int("port", inst.Port),
		slog.String("endpoint", inst.Target.Endpoint))

	go inst.streamLogs(stdout, "stdout")
	go inst.streamLogs(stderr, "stderr")

	// Wait for process to exit
	err = cmd.Wait()

	inst.mu.Lock()
	inst.pid = 0
	inst.mu.Unlock()

	return err
}

func (inst *Instance) streamLogs(r io.Reader, pipeName string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		text := scanner.Text()
		if pipeName == "stderr" {
			slog.Debug("[dcgm-exporter] "+text,
				slog.String("target", inst.Target.Name),
				slog.Int("port", inst.Port))
		} else {
			slog.Debug("[dcgm-exporter] "+text,
				slog.String("target", inst.Target.Name),
				slog.Int("port", inst.Port))
		}
	}
}

// Stop gracefully terminates the child process.
func (inst *Instance) Stop() {
	inst.mu.Lock()
	cancel := inst.cancelFunc
	pid := inst.pid
	inst.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if pid > 0 {
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)

			// Wait with timeout, then SIGKILL
			done := make(chan struct{})
			go func() {
				<-inst.stoppedCh
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(inst.Config.ShutdownTimeout):
				slog.Warn("Child dcgm-exporter did not terminate cleanly, sending SIGKILL",
					slog.String("target", inst.Target.Name),
					slog.Int("pid", pid))
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
	}

	inst.setState(StateStopped)
}

func (inst *Instance) setState(s InstanceState) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.state = s
}

// State returns current instance state.
func (inst *Instance) State() InstanceState {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.state
}

// CheckHealth probes the instance /health endpoint.
func (inst *Instance) CheckHealth(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", inst.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := inst.httpClient.Do(req)
	if err != nil {
		inst.setState(StateUnhealthy)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		inst.setState(StateUnhealthy)
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	inst.setState(StateRunning)
	return nil
}

// Scrape fetches the /metrics endpoint from this instance.
func (inst *Instance) Scrape(ctx context.Context) ([]byte, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", inst.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := inst.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics scrape returned status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

