package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidateAcceptsExplicitLoopbackAddresses(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"127.0.0.1:19090", "[::1]:19090"} {
		config := validTestConfig(t)
		config.ListenAddress = address
		if err := config.Validate(); err != nil {
			t.Errorf("Validate(%q) error = %v", address, err)
		}
	}
}

func TestConfigEnablesTerminalByDefaultAndSupportsEmergencyDisable(t *testing.T) {
	t.Setenv("HX_PXY_TERMINAL", "")
	if !Default().TerminalEnabled {
		t.Fatal("terminal must be enabled by default")
	}
	t.Setenv("HX_PXY_TERMINAL", "0")
	if Default().TerminalEnabled {
		t.Fatal("HX_PXY_TERMINAL=0 must disable the terminal")
	}
}

func TestConfigValidateRejectsPublicAndAmbiguousAddresses(t *testing.T) {
	t.Parallel()

	addresses := []string{
		"0.0.0.0:19090",
		"[::]:19090",
		"192.0.2.10:19090",
		"localhost:19090",
		"127.0.0.1:0",
		"127.0.0.1:65536",
		"127.0.0.1:http",
		"127.0.0.1",
	}
	for _, address := range addresses {
		config := validTestConfig(t)
		config.ListenAddress = address
		if err := config.Validate(); err == nil {
			t.Errorf("Validate(%q) error = nil, want rejection", address)
		}
	}
}

func TestConfigEnsureDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	config := Config{
		ListenAddress:     "127.0.0.1:19090",
		DataDirectory:     filepath.Join(root, "data"),
		ApplicationConfig: filepath.Join(root, "etc", "config.yaml"),
		DatabasePath:      filepath.Join(root, "data", "db", "hx-pxy.db"),
		MasterKeyPath:     filepath.Join(root, "data", "master.key"),
		RuntimeConfigPath: filepath.Join(root, "data", "runtime", "active.yaml"),
		SnapshotsPath:     filepath.Join(root, "data", "snapshots"),
	}
	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories() error = %v", err)
	}

	directories := []string{
		config.DataDirectory,
		filepath.Join(config.DataDirectory, "artifacts"),
		filepath.Dir(config.DatabasePath),
		filepath.Dir(config.RuntimeConfigPath),
		config.SnapshotsPath,
	}
	for _, directory := range directories {
		info, err := os.Stat(directory)
		if err != nil {
			t.Errorf("Stat(%q) error = %v", directory, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", directory)
		}
	}
}

func TestConfigValidateRejectsUnsafeMihomoResourceSettings(t *testing.T) {
	t.Parallel()
	tests := []func(*Config){
		func(config *Config) { config.MihomoEgressInterface = "" },
		func(config *Config) { config.MihomoMaxProcs = 0 },
		func(config *Config) { config.MihomoLogMaxBytes = 1024 },
		func(config *Config) { config.MihomoLogBackups = -1 },
	}
	for index, mutate := range tests {
		config := validTestConfig(t)
		mutate(&config)
		if err := config.Validate(); err == nil {
			t.Errorf("case %d Validate() error = nil, want rejection", index)
		}
	}
}

func validTestConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{
		ListenAddress:         "127.0.0.1:19090",
		DataDirectory:         filepath.Join(root, "data"),
		ApplicationConfig:     filepath.Join(root, "config.yaml"),
		DatabasePath:          filepath.Join(root, "data", "hx-pxy.db"),
		MasterKeyPath:         filepath.Join(root, "data", "master.key"),
		RuntimeConfigPath:     filepath.Join(root, "data", "runtime", "active.yaml"),
		SnapshotsPath:         filepath.Join(root, "data", "snapshots"),
		MihomoEgressInterface: "auto",
		MihomoMaxProcs:        4,
		MihomoLogMaxBytes:     8 << 20,
		MihomoLogBackups:      2,
	}
}
