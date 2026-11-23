package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddress     string
	DataDirectory     string
	ApplicationConfig string
	DatabasePath      string
	MasterKeyPath     string
	RuntimeConfigPath string
	SnapshotsPath     string
	WebRoot           string
	MihomoBinary      string
	MihomoExternal    bool
	// MihomoEgressInterface selects the physical interface used by the managed
	// data plane. "auto" resolves the Linux main-table default route; "off"
	// leaves routing to the host policy tables.
	MihomoEgressInterface string
	MihomoMaxProcs        int
	MihomoLogMaxBytes     int64
	MihomoLogBackups      int
	// TerminalEnabled gates the v2 in-browser terminal. It is enabled by
	// default and requires administrator login plus TOTP step-up verification.
	TerminalEnabled bool
	// TerminalShell optionally overrides the shell used by the terminal.
	TerminalShell string
	// TerminalPrivilegedSocket selects the local root PTY helper used by the
	// production systemd unit. Empty keeps local development unprivileged.
	TerminalPrivilegedSocket string
	// TerminalHistory persists shell command history between terminal sessions
	// (bash ~/.bash_history, zsh ~/.zsh_history). Enabled by default for the
	// sole administrator; set HX_PXY_TERMINAL_HISTORY=0 to restore the
	// no-history lockdown.
	TerminalHistory bool
}

func Default() Config {
	dataDirectory := envOrDefault("HX_PXY_DATA_DIR", "./data")
	return Config{
		ListenAddress:     envOrDefault("HX_PXY_LISTEN", "127.0.0.1:19090"),
		DataDirectory:     dataDirectory,
		ApplicationConfig: envOrDefault("HX_PXY_CONFIG", "./config.yaml"),
		DatabasePath:      envOrDefault("HX_PXY_DATABASE", filepath.Join(dataDirectory, "hx-pxy.db")),
		MasterKeyPath:     envOrDefault("HX_PXY_MASTER_KEY", filepath.Join(dataDirectory, "master.key")),
		RuntimeConfigPath: envOrDefault("HX_PXY_RUNTIME_CONFIG", filepath.Join(dataDirectory, "runtime", "active.yaml")),
		SnapshotsPath:     envOrDefault("HX_PXY_SNAPSHOTS", filepath.Join(dataDirectory, "snapshots")),
		WebRoot:           envOrDefault("HX_PXY_WEB_ROOT", ""),
		MihomoBinary:      envOrDefault("HX_PXY_MIHOMO", "mihomo"),
		MihomoExternal:    envOrDefault("HX_PXY_MIHOMO_EXTERNAL", "") == "1",
		MihomoEgressInterface: envOrDefault(
			"HX_PXY_MIHOMO_EGRESS_INTERFACE",
			"auto",
		),
		MihomoMaxProcs:           envIntOrDefault("HX_PXY_MIHOMO_MAX_PROCS", min(runtime.NumCPU(), 4)),
		MihomoLogMaxBytes:        int64(envIntOrDefault("HX_PXY_MIHOMO_LOG_MAX_BYTES", 8<<20)),
		MihomoLogBackups:         envIntOrDefault("HX_PXY_MIHOMO_LOG_BACKUPS", 2),
		TerminalEnabled:          envOrDefault("HX_PXY_TERMINAL", "1") != "0",
		TerminalShell:            envOrDefault("HX_PXY_TERMINAL_SHELL", ""),
		TerminalPrivilegedSocket: envOrDefault("HX_PXY_TERMINAL_SOCKET", ""),
		TerminalHistory:          envOrDefault("HX_PXY_TERMINAL_HISTORY", "1") != "0",
	}
}

func (config Config) Validate() error {
	if strings.TrimSpace(config.ListenAddress) == "" {
		return errors.New("listen address is required")
	}
	host, portText, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("v1 bootstrap API must bind to an explicit loopback IP until administrator authentication is implemented")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("listen port must be an integer between 1 and 65535")
	}
	if strings.TrimSpace(config.DataDirectory) == "" {
		return errors.New("data directory is required")
	}
	if strings.TrimSpace(config.ApplicationConfig) == "" {
		return errors.New("application config path is required")
	}
	if strings.TrimSpace(config.DatabasePath) == "" {
		return errors.New("database path is required")
	}
	if strings.TrimSpace(config.MasterKeyPath) == "" {
		return errors.New("master key path is required")
	}
	if strings.TrimSpace(config.MihomoEgressInterface) == "" {
		return errors.New("mihomo egress interface is required; use auto or off")
	}
	if config.MihomoMaxProcs < 1 || config.MihomoMaxProcs > 1024 {
		return errors.New("mihomo max procs must be between 1 and 1024")
	}
	if config.MihomoLogMaxBytes < 1<<20 || config.MihomoLogMaxBytes > 1<<30 {
		return errors.New("mihomo log max bytes must be between 1 MiB and 1 GiB")
	}
	if config.MihomoLogBackups < 0 || config.MihomoLogBackups > 10 {
		return errors.New("mihomo log backups must be between 0 and 10")
	}
	return nil
}

func (config Config) EnsureDirectories() error {
	directories := []string{
		config.DataDirectory,
		filepath.Join(config.DataDirectory, "artifacts"),
		filepath.Dir(config.DatabasePath),
		filepath.Dir(config.MasterKeyPath),
		filepath.Dir(config.RuntimeConfigPath),
		config.SnapshotsPath,
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create directory %q: %w", directory, err)
		}
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
