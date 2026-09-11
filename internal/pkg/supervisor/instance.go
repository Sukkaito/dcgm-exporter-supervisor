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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/netutil"
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
	mu         sync.RWMutex
	Target     appconfig.Target
	SocketPath string
	Port       int
	Config     appconfig.ExporterConfig
	cmdBuilder CommandBuilder
	httpClient *http.Client

	state        InstanceState
	pid          int
	startTime    time.Time
	restartCount int
	lastError    error
	cancelFunc   context.CancelFunc
	stoppedCh    chan struct{}
}

// NewInstance creates a new Instance configuration.
// If socketPath is empty, it is generated based on target ID and cfg.SocketDir.
func NewInstance(target appconfig.Target, socketPath string, cfg appconfig.ExporterConfig, builder CommandBuilder) *Instance {
	if builder == nil {
		builder = defaultCommandBuilder
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = appconfig.DefaultShutdownTimeout
	}
	if cfg.SocketDir == "" {
		cfg.SocketDir = appconfig.DefaultSocketDir
	}
	if socketPath == "" {
		targetID := target.ID
		if targetID == "" {
			targetID = target.Name
		}
		socketPath = filepath.Join(cfg.SocketDir, sanitizeSocketName(targetID)+".sock")
	}
	if normalized, err := netutil.NormalizeEndpoint(target.Endpoint, 5555); err == nil {
		target.Endpoint = normalized
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}

	return &Instance{
		Target:     target,
		SocketPath: socketPath,
		Config:     cfg,
		cmdBuilder: builder,
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: transport,
		},
		state:     StateStarting,
		stoppedCh: make(chan struct{}),
	}
}

func sanitizeSocketName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// SetHTTPClient overrides the internal HTTP client (useful for testing).
func (inst *Instance) SetHTTPClient(client *http.Client) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.httpClient = client
}

// Start launches the supervision loop for the child process.
func (inst *Instance) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	stoppedCh := make(chan struct{})

	inst.mu.Lock()
	inst.cancelFunc = cancel
	inst.stoppedCh = stoppedCh
	inst.mu.Unlock()

	go inst.runLoop(ctx, stoppedCh)
}

func (inst *Instance) runLoop(ctx context.Context, stoppedCh chan struct{}) {
	defer close(stoppedCh)

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
			slog.String("socket", inst.SocketPath),
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
	// 1. Ensure parent directory for socket exists
	if dir := filepath.Dir(inst.SocketPath); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	// 2. Remove stale socket if any
	_ = os.Remove(inst.SocketPath)

	// 3. Create UNIX domain listener for socket activation
	listener, err := net.Listen("unix", inst.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket %s: %w", inst.SocketPath, err)
	}
	defer func() {
		_ = os.Remove(inst.SocketPath)
	}()

	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		listener.Close()
		return fmt.Errorf("listener is not a UnixListener")
	}
	// Prevent parent Close() from unlinking the socket file
	unixListener.SetUnlinkOnClose(false)

	listenerFile, err := unixListener.File()
	if err != nil {
		listener.Close()
		return fmt.Errorf("failed to get listener file descriptor: %w", err)
	}
	listener.Close()
	defer listenerFile.Close()

	// 4. Build command arguments for child dcgm-exporter
	args := []string{
		"--web-systemd-socket",
		"-r", inst.Target.Endpoint,
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

	// Execute via sh -c so LISTEN_PID=$$ matches child PID for go-systemd activation
	shCmd := append([]string{"-c", "export LISTEN_PID=$$; exec \"$@\"", "_", inst.Config.BinaryPath}, args...)

	netns := inst.Target.NetNS
	var binName string
	var binArgs []string

	if netns == "" {
		binName = "sh"
		binArgs = shCmd
	} else if strings.HasPrefix(netns, "/") {
		// Namespace file path: nsenter --net=<path> -F -- sh -c ...
		binName = "nsenter"
		binArgs = append([]string{"--net=" + netns, "-F", "--", "sh"}, shCmd...)
	} else {
		// Named namespace: ip netns exec <name> sh -c ...
		binName = "ip"
		binArgs = append([]string{"netns", "exec", netns, "sh"}, shCmd...)
	}

	cmd := inst.cmdBuilder(ctx, binName, binArgs...)
	cmd.ExtraFiles = []*os.File{listenerFile}
	cmd.Env = append(os.Environ(), "LISTEN_FDS=1")

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
		slog.String("socket", inst.SocketPath),
		slog.String("netns", netns),
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
		slog.Debug("[dcgm-exporter] "+text,
			slog.String("target", inst.Target.Name),
			slog.String("socket", inst.SocketPath),
			slog.String("stream", pipeName))
	}
}

// Stop gracefully terminates the child process.
func (inst *Instance) Stop() {
	inst.mu.Lock()
	cancel := inst.cancelFunc
	pid := inst.pid
	stoppedCh := inst.stoppedCh
	inst.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if pid > 0 {
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}

	// Wait with timeout for the supervisor runLoop to exit cleanly
	if stoppedCh != nil {
		select {
		case <-stoppedCh:
		case <-time.After(inst.Config.ShutdownTimeout):
			if pid > 0 {
				proc, err := os.FindProcess(pid)
				if err == nil {
					slog.Warn("Child dcgm-exporter did not terminate cleanly, sending SIGKILL",
						slog.String("target", inst.Target.Name),
						slog.Int("pid", pid))
					_ = proc.Signal(syscall.SIGKILL)
				}
			}
		}
	}

	if inst.SocketPath != "" {
		_ = os.Remove(inst.SocketPath)
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
	url := "http://unix/health"
	if inst.SocketPath == "" && inst.Port > 0 {
		url = netutil.FormatHTTPURL("http", inst.Config.ListenHost, inst.Port, "/health")
	}
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
	url := "http://unix/metrics"
	if inst.SocketPath == "" && inst.Port > 0 {
		url = netutil.FormatHTTPURL("http", inst.Config.ListenHost, inst.Port, "/metrics")
	}
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
