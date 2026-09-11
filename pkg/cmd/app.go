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

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/appconfig"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/discovery"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/discovery/file"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/discovery/libvirt"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/discovery/nova"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/discovery/static"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/server"
	"github.com/Sukkaito/dcgm-exporter-supervisor/internal/pkg/supervisor"
)

const (
	CLIConfigFile         = "config-file"
	CLIAddress            = "address"
	CLICollectInterval    = "collect-interval"
	CLIFieldsFile         = "collectors"
	CLIWebConfigFile      = "web-config-file"
	CLIWebReadTimeout     = "web-read-timeout"
	CLIWebWriteTimeout    = "web-write-timeout"
	CLILogFormat          = "log-format"
	CLIDebugMode          = "debug"
	CLIDcgmExporterBin    = "dcgm-exporter-bin"
	CLISocketDir          = "socket-dir"
	CLIExporterListenHost = "exporter-listen-host"
	CLIPortRangeStart     = "port-range-start"
	CLIPortRangeEnd       = "port-range-end"
	CLIShutdownTimeout    = "shutdown-timeout"
)

// NewApp creates the CLI application for dcgm-exporter-supervisor.
func NewApp(buildVersion string) *cli.App {
	c := cli.NewApp()
	c.Name = "dcgm-exporter-supervisor"
	c.Usage = "Supervises multiple dcgm-exporter instances for virtual machines on a host"
	c.Version = buildVersion

	c.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    CLIConfigFile,
			Usage:   "Path to a YAML configuration file.",
			Value:   "",
			EnvVars: []string{"DCGM_SUPERVISOR_CONFIG_FILE"},
		},
		&cli.StringFlag{
			Name:    CLIAddress,
			Aliases: []string{"a"},
			Value:   appconfig.DefaultAddress,
			Usage:   "Listen address for supervisor HTTP server as <HOST>:<PORT>. For IPv6, use \"[<IPv6_ADDR>]:<PORT>\" (e.g., \"[::]:9400\").",
			EnvVars: []string{"DCGM_SUPERVISOR_LISTEN", "DCGM_EXPORTER_LISTEN"},
		},
		&cli.IntFlag{
			Name:    CLICollectInterval,
			Aliases: []string{"c"},
			Value:   appconfig.DefaultCollectInterval,
			Usage:   "Interval of time at which child instances collect metrics (in ms).",
			EnvVars: []string{"DCGM_SUPERVISOR_INTERVAL", "DCGM_EXPORTER_INTERVAL"},
		},
		&cli.StringFlag{
			Name:    CLIFieldsFile,
			Aliases: []string{"f"},
			Value:   appconfig.DefaultCollectorsFile,
			Usage:   "Path to the file that contains the DCGM fields to collect.",
			EnvVars: []string{"DCGM_SUPERVISOR_COLLECTORS", "DCGM_EXPORTER_COLLECTORS"},
		},
		&cli.StringFlag{
			Name:    CLIWebConfigFile,
			Value:   "",
			Usage:   "Path to exporter-toolkit web configuration file for TLS and authentication.",
			EnvVars: []string{"DCGM_SUPERVISOR_WEB_CONFIG_FILE"},
		},
		&cli.DurationFlag{
			Name:    CLIWebReadTimeout,
			Value:   appconfig.DefaultWebReadTimeout,
			Usage:   "Maximum duration for reading an HTTP request.",
			EnvVars: []string{"DCGM_SUPERVISOR_WEB_READ_TIMEOUT"},
		},
		&cli.DurationFlag{
			Name:    CLIWebWriteTimeout,
			Value:   appconfig.DefaultWebWriteTimeout,
			Usage:   "Maximum duration for generating and writing an HTTP response.",
			EnvVars: []string{"DCGM_SUPERVISOR_WEB_WRITE_TIMEOUT"},
		},
		&cli.StringFlag{
			Name:    CLILogFormat,
			Value:   "text",
			Usage:   "Log format to use: text or json.",
			EnvVars: []string{"DCGM_SUPERVISOR_LOG_FORMAT"},
		},
		&cli.BoolFlag{
			Name:    CLIDebugMode,
			Value:   false,
			Usage:   "Enable debug logging.",
			EnvVars: []string{"DCGM_SUPERVISOR_DEBUG"},
		},
		&cli.StringFlag{
			Name:    CLIDcgmExporterBin,
			Value:   appconfig.DefaultExporterBinary,
			Usage:   "Path or executable name of dcgm-exporter binary.",
			EnvVars: []string{"DCGM_EXPORTER_BINARY"},
		},
		&cli.StringFlag{
			Name:    CLISocketDir,
			Value:   appconfig.DefaultSocketDir,
			Usage:   "Directory for internal UNIX domain sockets connecting to child dcgm-exporter processes.",
			EnvVars: []string{"DCGM_SUPERVISOR_SOCKET_DIR"},
		},
		&cli.StringFlag{
			Name:    CLIExporterListenHost,
			Value:   appconfig.DefaultExporterListenHost,
			Usage:   "Listen host for internal loopback child instances (e.g. \"127.0.0.1\" or \"::1\").",
			EnvVars: []string{"DCGM_SUPERVISOR_EXPORTER_LISTEN_HOST"},
		},
		&cli.IntFlag{
			Name:    CLIPortRangeStart,
			Value:   appconfig.DefaultPortRangeStart,
			Usage:   "Start of port pool range for internal loopback child instances.",
			EnvVars: []string{"DCGM_SUPERVISOR_PORT_RANGE_START"},
		},
		&cli.IntFlag{
			Name:    CLIPortRangeEnd,
			Value:   appconfig.DefaultPortRangeEnd,
			Usage:   "End of port pool range for internal loopback child instances.",
			EnvVars: []string{"DCGM_SUPERVISOR_PORT_RANGE_END"},
		},
		&cli.DurationFlag{
			Name:    CLIShutdownTimeout,
			Value:   appconfig.DefaultShutdownTimeout,
			Usage:   "Graceful termination timeout before SIGKILL is sent to child processes.",
			EnvVars: []string{"DCGM_SUPERVISOR_SHUTDOWN_TIMEOUT"},
		},
	}

	c.Action = runSupervisor
	return c
}

func configureLogger(logFormat string, debug bool) {
	var level slog.Level
	if debug {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
}

func runSupervisor(c *cli.Context) error {
	configFile := c.String(CLIConfigFile)
	cfg, err := appconfig.LoadConfigFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Apply CLI flag overrides if explicitly passed
	if c.IsSet(CLIAddress) {
		cfg.Address = c.String(CLIAddress)
	}
	if c.IsSet(CLICollectInterval) {
		cfg.CollectInterval = c.Int(CLICollectInterval)
		cfg.Exporter.CollectInterval = c.Int(CLICollectInterval)
	}
	if c.IsSet(CLIFieldsFile) {
		cfg.CollectorsFile = c.String(CLIFieldsFile)
		cfg.Exporter.CollectorsFile = c.String(CLIFieldsFile)
	}
	if c.IsSet(CLILogFormat) {
		cfg.LogFormat = c.String(CLILogFormat)
	}
	if c.IsSet(CLIDebugMode) {
		cfg.Debug = c.Bool(CLIDebugMode)
	}
	if c.IsSet(CLIWebConfigFile) {
		cfg.WebConfigFile = c.String(CLIWebConfigFile)
	}
	if c.IsSet(CLIWebReadTimeout) {
		cfg.WebReadTimeout = c.Duration(CLIWebReadTimeout)
	}
	if c.IsSet(CLIWebWriteTimeout) {
		cfg.WebWriteTimeout = c.Duration(CLIWebWriteTimeout)
	}
	if c.IsSet(CLIDcgmExporterBin) {
		cfg.Exporter.BinaryPath = c.String(CLIDcgmExporterBin)
	}
	if c.IsSet(CLISocketDir) {
		cfg.Exporter.SocketDir = c.String(CLISocketDir)
	}
	if cfg.Exporter.SocketDir == "" {
		cfg.Exporter.SocketDir = appconfig.DefaultSocketDir
	}
	if c.IsSet(CLIExporterListenHost) {
		cfg.Exporter.ListenHost = c.String(CLIExporterListenHost)
	}
	if c.IsSet(CLIPortRangeStart) {
		cfg.Exporter.PortRangeStart = c.Int(CLIPortRangeStart)
	}
	if c.IsSet(CLIPortRangeEnd) {
		cfg.Exporter.PortRangeEnd = c.Int(CLIPortRangeEnd)
	}
	if c.IsSet(CLIShutdownTimeout) {
		cfg.Exporter.ShutdownTimeout = c.Duration(CLIShutdownTimeout)
	}

	configureLogger(cfg.LogFormat, cfg.Debug)

	// Ensure socket directory exists; attempt fallback to /tmp if default /run is not writable
	if err := os.MkdirAll(cfg.Exporter.SocketDir, 0755); err != nil {
		slog.Warn("Failed to create socket directory, falling back to temp directory",
			slog.String("attempted", cfg.Exporter.SocketDir),
			slog.String("fallback", appconfig.FallbackSocketDir),
			slog.String("error", err.Error()))
		cfg.Exporter.SocketDir = appconfig.FallbackSocketDir
		if err := os.MkdirAll(cfg.Exporter.SocketDir, 0755); err != nil {
			return fmt.Errorf("failed to create socket directory %s: %w", cfg.Exporter.SocketDir, err)
		}
	}

	slog.Info("Starting dcgm-exporter-supervisor",
		slog.String("version", c.App.Version),
		slog.String("listen_address", cfg.Address),
		slog.String("exporter_bin", cfg.Exporter.BinaryPath),
		slog.String("socket_dir", cfg.Exporter.SocketDir))

	mgr := supervisor.NewManager(cfg.Exporter, nil)
	mgr.StartHealthLoop(15 * time.Second)

	discMgr := discovery.NewManager()

	// Register enabled providers
	if cfg.Discovery.Static.Enabled && len(cfg.Discovery.Static.Targets) > 0 {
		discMgr.RegisterProvider(static.NewProvider(cfg.Discovery.Static.Targets))
	}
	if cfg.Discovery.Libvirt.Enabled {
		discMgr.RegisterProvider(libvirt.NewProvider(cfg.Discovery.Libvirt))
	}
	if cfg.Discovery.Nova.Enabled {
		discMgr.RegisterProvider(nova.NewProvider(cfg.Discovery.Nova))
	}
	if cfg.Discovery.File.Enabled {
		discMgr.RegisterProvider(file.NewProvider(cfg.Discovery.File.Directory, cfg.Discovery.File.PollInterval))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	targetCh := discMgr.Start(ctx)

	// Watch target updates from discovery and reconcile child instances
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case targets, ok := <-targetCh:
				if !ok {
					return
				}
				slog.Info("Target inventory update received", slog.Int("count", len(targets)))
				mgr.Reconcile(targets)
			}
		}
	}()

	srv := server.NewServer(cfg, mgr)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.Run(ctx); err != nil {
			serverErrCh <- err
		}
	}()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case err := <-serverErrCh:
			slog.Error("HTTP server error", slog.String("error", err.Error()))
			mgr.Shutdown()
			return err
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				slog.Info("Received SIGHUP, reloading configuration")
				if configFile != "" {
					newCfg, err := appconfig.LoadConfigFile(configFile)
					if err == nil {
						cfg = newCfg
						if cfg.Discovery.Static.Enabled {
							mgr.Reconcile(cfg.Discovery.Static.Targets)
						}
					} else {
						slog.Error("Failed to reload config on SIGHUP", slog.String("error", err.Error()))
					}
				}
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("Received termination signal, shutting down gracefully", slog.String("signal", sig.String()))
				shutdownCtx, sCancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = srv.Shutdown(shutdownCtx)
				sCancel()
				mgr.Shutdown()
				cancel()
				slog.Info("dcgm-exporter-supervisor stopped successfully")
				return nil
			}
		}
	}
}
