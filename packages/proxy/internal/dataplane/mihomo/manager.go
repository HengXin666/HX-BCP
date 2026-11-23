package mihomo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

var ErrUnavailable = errors.New("mihomo binary is unavailable")

type Status struct {
	Available       bool       `json:"available"`
	Running         bool       `json:"running"`
	State           string     `json:"state"`
	PID             int        `json:"pid,omitempty"`
	Binary          string     `json:"binary,omitempty"`
	Version         string     `json:"version,omitempty"`
	ActiveConfig    string     `json:"active_config,omitempty"`
	LastApplyAt     *time.Time `json:"last_apply_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	ListenerCount   int        `json:"listener_count"`
	ProxyCount      int        `json:"proxy_count"`
	ActiveListeners []Endpoint `json:"active_listeners"`
	EgressInterface string     `json:"egress_interface,omitempty"`
	MaxProcs        int        `json:"max_procs"`
	LogMaxBytes     int64      `json:"log_max_bytes"`
}

type process struct {
	command *exec.Cmd
	done    chan error
}

type Manager struct {
	entrySink        func(EntryCatalog)
	compiler         *Compiler
	binary           string
	runtimeDir       string
	activePath       string
	candidate        string
	previous         string
	logPath          string
	controllerSocket string
	logger           *slog.Logger
	available        bool
	version          string
	mu               sync.Mutex
	process          *process
	status           Status
	lastCompiled     Compiled
	egressInterface  string
	maxProcs         int
	logMaxBytes      int64
	logBackups       int
	externalProcess  bool
	// sup 是数据面看护的运行态; nil 表示未启用。所有者是本 Manager。
	sup *supervisor
	// withheld is the Docker companion entries the running document leaves
	// out; see manager_docker_entries.go.
	withheld dockerEntryState
}

type ManagerOption func(*Manager) error

// maxUnixSocketPath 是内核 `sun_path` 的上限: 108 字节, 含结尾 NUL ⇒ 可用 107。
const maxUnixSocketPath = 107

// controllerSocketPath 返回 mihomo 控制 socket 的路径。
//
// 见 .agents/notes/implemented/architecture/2026-10-14-controller-socket-path-must-match-the-data-plane.md
//
// 这条路径有一个不可让步的要求: **写进 active.yaml 的那条, 必须就是控制面拨号的那条**。
// mihomo 只按配置里的 `external-controller-unix` 建 socket, 控制面只按这个返回值拨,
// 两边算法只要差一个输入 (比如 HOME 有没有被设置), 数据面就"开着但控制面认定它没运行"。
//
// 因此取值分两步, 且第一步与 HOME 无关:
//
//  1. 直接路径 `runtimeDirectory/mihomo.sock` —— 长度够用就用它。它是 data plane 自己
//     `-d` 的目录, 两边天然一致, 也不依赖任何环境变量。
//  2. 只有直接路径**超过内核上限**时才挪到 HOME 下的短目录
//     (`<home>/.hx-pxy-runtime/<runtimeDirectory 的摘要>`)。摘要而不是固定名是必需的:
//     同一个进程里可以有多个 Manager (测试就是如此, 每个用自己的 t.TempDir 作
//     runtimeDirectory), 而 unix socket 路径就是它的身份 —— 共用一条路径会让第二个
//     Manager 直接 `bind: address already in use`, 或者更糟: 覆盖掉第一个正在用的 socket。
//
// systemd 托管的数据面 (externalProcess) **永远**用直接路径, 即使超长也不挪: 它的 unit
// 是 ProtectSystem=strict + ReadWritePaths=<runtimeDirectory>, 在 runtimeDirectory 之外
// mihomo 根本建不了 socket。挪过去只会让失败从"路径太长"变成"起不来", 更难查。
func (m *Manager) controllerSocketPath() string {
	direct := filepath.Join(m.runtimeDir, "mihomo.sock")
	if m.externalProcess || len(direct) <= maxUnixSocketPath {
		return direct
	}
	return shortControllerSocketPath(m.runtimeDir)
}

// shortControllerSocketPath 把过长的 socket 路径挪到 HOME 下的短目录。
//
// HOME 为空时退回原路径: 那时行为与修之前一致, 不引入新的失败模式。该目录无法建立时
// 同样退回原路径 —— 短路径尚未就绪不会让服务起不来, 只是写操作像修之前那样失败
// (可观测, 好过直接崩)。
func shortControllerSocketPath(runtimeDirectory string) string {
	home := os.Getenv("HOME")
	if home == "" {
		return filepath.Join(runtimeDirectory, "mihomo.sock")
	}
	digest := sha256.Sum256([]byte(runtimeDirectory))
	short := filepath.Join(home, ".hx-pxy-runtime", hex.EncodeToString(digest[:8]))
	if err := os.MkdirAll(short, 0o700); err != nil {
		return filepath.Join(runtimeDirectory, "mihomo.sock")
	}
	return filepath.Join(short, "mihomo.sock")
}

func WithEgressInterface(name string) ManagerOption {
	return func(manager *Manager) error {
		manager.compiler.setEgressInterface(name)
		manager.egressInterface = strings.TrimSpace(name)
		return nil
	}
}

func WithProcessMaxProcs(value int) ManagerOption {
	return func(manager *Manager) error {
		if value < 1 {
			return errors.New("mihomo max procs must be positive")
		}
		manager.maxProcs = value
		return nil
	}
}

func WithLogRotation(maxBytes int64, backups int) ManagerOption {
	return func(manager *Manager) error {
		if maxBytes < 1 || backups < 0 {
			return errors.New("invalid Mihomo log rotation settings")
		}
		manager.logMaxBytes = maxBytes
		manager.logBackups = backups
		return nil
	}
}

// WithEntryHub compiles the unified entry hub listener into every document and
// hands each applied catalog to sink.
func WithEntryHub(hub EntryHub, sink func(EntryCatalog)) ManagerOption {
	return func(manager *Manager) error {
		manager.compiler.SetEntryHub(hub)
		manager.entrySink = sink
		return nil
	}
}

func WithExternalProcess(enabled bool) ManagerOption {
	return func(manager *Manager) error {
		manager.externalProcess = enabled
		return nil
	}
}

func NewManager(
	compiler *Compiler,
	binary string,
	activeConfigPath string,
	logger *slog.Logger,
	options ...ManagerOption,
) (*Manager, error) {
	if compiler == nil {
		return nil, errors.New("mihomo compiler is required")
	}
	if strings.TrimSpace(activeConfigPath) == "" {
		return nil, errors.New("mihomo active config path is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	activePath, err := filepath.Abs(activeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("resolve mihomo active config path: %w", err)
	}
	runtimeDirectory := filepath.Dir(activePath)
	manager := &Manager{
		compiler:   compiler,
		runtimeDir: runtimeDirectory,
		activePath: activePath,
		candidate:  activePath + ".candidate",
		previous:   activePath + ".previous",
		logPath:    filepath.Join(runtimeDirectory, "mihomo.log"),

		logger:      logger,
		maxProcs:    min(runtime.NumCPU(), 4),
		logMaxBytes: 8 << 20,
		logBackups:  2,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(manager); err != nil {
			return nil, err
		}
	}
	// socket 路径依赖 externalProcess, 所以必须在 options 之后才算: 先算会让
	// systemd 托管的数据面拿到 HOME 短路径, 而 unit 的沙箱只允许它在 runtimeDirectory
	// 下建 socket —— 那正是 2026-10-13 生产升级后"数据面在跑、控制面认定它没运行"的原因。
	manager.controllerSocket = manager.controllerSocketPath()
	manager.compiler.setControllerSocket(manager.controllerSocket)
	manager.resolveBinary(binary)
	manager.status = Status{
		Available:       manager.available,
		State:           "idle",
		Binary:          manager.binary,
		Version:         manager.version,
		ActiveConfig:    manager.activePath,
		EgressInterface: manager.egressInterface,
		MaxProcs:        manager.maxProcs,
		LogMaxBytes:     manager.logMaxBytes,
	}
	return manager, nil
}

func (m *Manager) Apply(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshProcessLocked()

	compiled, err := m.compiler.Compile(ctx)
	if err != nil {
		m.recordFailureLocked(err)
		return err
	}
	if err := m.applyCompiledLocked(ctx, compiled); err != nil {
		if compiled.Entry.HubAddress == "" {
			return err
		}
		if compiled, err = m.applyWithoutEntryHubLocked(ctx, err); err != nil {
			return err
		}
	}
	m.withheld.set(compiled.WithheldEntries)
	if m.entrySink != nil {
		m.entrySink(compiled.Entry)
	}
	m.logWithheldEntries(compiled.WithheldEntries)
	// Only a configuration Mihomo actually accepted becomes the previous
	// membership the Retain stage keeps. A failed record never undoes the
	// apply; it stays pending in memory until a write succeeds.
	if err := m.compiler.recordMembers(ctx, compiled, time.Now()); err != nil {
		m.logger.Warn("record proxy group members failed", "component", "dataplane", "error", err)
	}
	return nil
}

func (m *Manager) applyCompiledLocked(ctx context.Context, compiled Compiled) error {
	if m.status.Running && bytes.Equal(compiled.YAML, m.lastCompiled.YAML) {
		m.status.ListenerCount = len(compiled.Endpoints)
		m.status.ProxyCount = compiled.ProxyCount
		m.status.ActiveListeners = append([]Endpoint(nil), compiled.Endpoints...)
		m.status.LastError = ""
		return nil
	}
	if !m.externalProcess && len(compiled.Endpoints) == 0 && compiled.ProxyCount == 0 && compiled.TUNDevice == "" {
		if err := m.stopLocked(ctx); err != nil {
			m.recordFailureLocked(err)
			return err
		}
		now := time.Now().UTC()
		m.lastCompiled = compiled
		m.status.State = "idle"
		m.status.Running = false
		m.status.PID = 0
		m.status.ListenerCount = 0
		m.status.ProxyCount = 0
		m.status.ActiveListeners = nil
		m.status.LastApplyAt = &now
		m.status.LastError = ""
		return nil
	}
	if !m.available {
		err := fmt.Errorf("%w: configure --mihomo or install mihomo", ErrUnavailable)
		m.recordFailureLocked(err)
		return err
	}
	if err := m.validateEndpointAvailabilityLocked(compiled.Endpoints); err != nil {
		return m.failLocked(err)
	}
	if err := os.MkdirAll(m.runtimeDir, 0o700); err != nil {
		return m.failLocked(fmt.Errorf("create mihomo runtime directory: %w", err))
	}
	if err := os.Chmod(m.runtimeDir, 0o700); err != nil {
		return m.failLocked(fmt.Errorf("secure mihomo runtime directory: %w", err))
	}
	if err := writeFileAtomic(m.candidate, compiled.YAML, 0o600); err != nil {
		return m.failLocked(fmt.Errorf("write mihomo candidate config: %w", err))
	}
	if err := m.validateLocked(ctx, m.candidate); err != nil {
		_ = os.Remove(m.candidate)
		return m.failLocked(err)
	}

	oldEndpoints := append([]Endpoint(nil), m.lastCompiled.Endpoints...)
	hadOldConfig := fileExists(m.activePath)
	if hadOldConfig {
		if err := copyFileAtomic(m.activePath, m.previous, 0o600); err != nil {
			return m.failLocked(fmt.Errorf("preserve previous mihomo config: %w", err))
		}
	}
	if err := publishCandidate(m.candidate, m.activePath); err != nil {
		return m.failLocked(fmt.Errorf("publish mihomo config: %w", err))
	}
	// Both systemd-managed and locally managed Mihomo expose the same Unix
	// Controller. Once a local process is running, hot reload preserves other
	// residential client sessions while one IN-USER route changes.
	if m.externalProcess || m.process != nil {
		if err := m.reloadExternalLocked(ctx); err != nil {
			rollbackErr := m.rollbackAndRestartLocked(ctx, hadOldConfig, oldEndpoints)
			return m.failLocked(errors.Join(err, rollbackErr))
		}
		if err := m.waitReadyLocked(ctx, compiled.Endpoints, compiled.ProxyCount, compiled.TUNDevice); err != nil {
			rollbackErr := m.rollbackAndRestartLocked(ctx, hadOldConfig, oldEndpoints)
			return m.failLocked(errors.Join(err, rollbackErr))
		}
		now := time.Now().UTC()
		m.lastCompiled = compiled
		m.status.State = "running"
		m.status.Running = true
		if m.externalProcess {
			m.status.PID = 0
		} else {
			m.status.PID = m.process.command.Process.Pid
		}
		m.status.ListenerCount = len(compiled.Endpoints)
		m.status.ProxyCount = compiled.ProxyCount
		m.status.ActiveListeners = append([]Endpoint(nil), compiled.Endpoints...)
		m.status.LastApplyAt = &now
		m.status.LastError = ""
		return nil
	}
	if err := m.stopLocked(ctx); err != nil {
		_ = m.rollbackConfigLocked(hadOldConfig)
		return m.failLocked(err)
	}
	if err := m.startLocked(m.activePath); err != nil {
		rollbackErr := m.rollbackAndRestartLocked(ctx, hadOldConfig, oldEndpoints)
		if rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		return m.failLocked(err)
	}
	if err := m.waitReadyLocked(ctx, compiled.Endpoints, compiled.ProxyCount, compiled.TUNDevice); err != nil {
		_ = m.stopLocked(context.Background())
		rollbackErr := m.rollbackAndRestartLocked(ctx, hadOldConfig, oldEndpoints)
		if rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		return m.failLocked(err)
	}

	now := time.Now().UTC()
	m.lastCompiled = compiled
	m.status.State = "running"
	m.status.Running = true
	m.status.PID = m.process.command.Process.Pid
	m.status.ListenerCount = len(compiled.Endpoints)
	m.status.ProxyCount = compiled.ProxyCount
	m.status.ActiveListeners = append([]Endpoint(nil), compiled.Endpoints...)
	m.status.LastApplyAt = &now
	m.status.LastError = ""
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshProcessLocked()
	result := m.status
	result.ActiveListeners = append([]Endpoint(nil), m.status.ActiveListeners...)
	return result
}

func (m *Manager) Close(ctx context.Context) error {
	m.StopSupervisor()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.externalProcess {
		return nil
	}
	return m.stopLocked(ctx)
}

func (m *Manager) resolveBinary(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "mihomo"
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		m.binary = value
		return
	}
	m.binary = resolved
	m.available = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, resolved, "-v").CombinedOutput()
	if err == nil {
		m.version = strings.TrimSpace(string(output))
	}
}

func (m *Manager) validateLocked(ctx context.Context, path string) error {
	validationContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(
		validationContext,
		m.binary,
		"-t",
		"-d", m.runtimeDir,
		"-f", path,
	)
	command.Env = m.commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		m.logger.Error("Mihomo configuration validation failed", "error", err, "output_bytes", len(output))
		return fmt.Errorf("mihomo config validation failed: %w", err)
	}
	return nil
}

func (m *Manager) startLocked(configPath string) error {
	if err := os.Remove(m.controllerSocket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale Mihomo controller socket: %w", err)
	}
	logFile, err := newRotatingLogWriter(m.logPath, m.logMaxBytes, m.logBackups)
	if err != nil {
		return fmt.Errorf("open mihomo log: %w", err)
	}
	command := exec.Command(m.binary, "-d", m.runtimeDir, "-f", configPath)
	command.Env = m.commandEnvironment()
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start mihomo: %w", err)
	}
	m.logger.Info("Mihomo process started", "pid", command.Process.Pid, "config", configPath)
	item := &process{command: command, done: make(chan error, 1)}
	m.process = item
	// 立刻记 PID, 别等 refreshProcessLocked —— 否则拉起后的日志/状态里 PID 恒为 0,
	// 排障时看不出"换了哪个进程"。
	// 只记 PID, 不动 Running/State: 那两者的判据是"端口真的在听", 由看护的
	// listenersReadyLocked 判定; 这里提前置 true 会让看护误以为已恢复而不再处理。
	m.status.PID = command.Process.Pid
	go func() {
		err := command.Wait()
		_ = logFile.Close()
		item.done <- err
		close(item.done)
	}()
	return nil
}

func (m *Manager) reloadExternalLocked(ctx context.Context) error {
	payload := []byte(fmt.Sprintf(`{"path":%q}`, m.activePath))
	transport := &http.Transport{Proxy: nil, DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(dialContext, "unix", m.controllerSocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://unix/configs?force=true", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Mihomo reload request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("reload systemd-managed Mihomo: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("reload systemd-managed Mihomo returned status %d", response.StatusCode)
	}
	return nil
}

// proxyEnvironmentNames are the variables Go's net/http consults through
// http.ProxyFromEnvironment, plus the ones curl-style tools read. The managed
// data plane must not inherit them.
//
// Why this is a correctness rule and not hygiene: Mihomo is a Go program and
// fetches proxy-providers, rule-sets and geo files with an HTTP transport that
// honours HTTP_PROXY / ALL_PROXY. Inheriting the control plane's environment
// therefore lets a *local* proxy (a desktop client's "system proxy" mode, a
// shell exported in run.sh, a service manager EnvironmentFile) silently rewrite
// the data plane's own egress — the exact class of interference the operator
// asked us to stop. The traffic that must leave through the physical NIC is
// the data plane's; it is not a caller's job to decide that at the environment
// level.
//
// Unsetting is deliberately preferred over forcing no_proxy=*: an unset
// variable cannot be re-enabled by an inherited NO_PROXY entry, and it keeps
// the child environment byte-for-byte independent of whatever the parent shell
// happened to export.
var proxyEnvironmentNames = map[string]struct{}{
	"http_proxy":  {},
	"https_proxy": {},
	"all_proxy":   {},
	"ftp_proxy":   {},
	"no_proxy":    {},
}

func (m *Manager) commandEnvironment() []string {
	prefix := "GOMAXPROCS="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, prefix) {
			continue
		}
		if name, _, found := strings.Cut(value, "="); found {
			// Environment names are case-insensitive on the platforms we run on;
			// both HTTP_PROXY and http_proxy are stripped.
			if _, isProxy := proxyEnvironmentNames[strings.ToLower(strings.TrimSpace(name))]; isProxy {
				continue
			}
		}
		environment = append(environment, value)
	}
	return append(environment, prefix+strconv.Itoa(m.maxProcs))
}

func (m *Manager) stopLocked(ctx context.Context) error {
	m.refreshProcessLocked()
	if m.process == nil {
		m.status.Running = false
		m.status.PID = 0
		return nil
	}
	item := m.process
	if err := item.command.Process.Signal(syscall.SIGTERM); err != nil {
		m.logger.Warn("Mihomo graceful stop signal failed", "pid", item.command.Process.Pid, "error", err)
	}
	select {
	case <-item.done:
	case <-ctx.Done():
		m.logger.Warn("Mihomo stop timed out; sending SIGKILL", "pid", item.command.Process.Pid, "reason", "context")
		_ = item.command.Process.Kill()
		<-item.done
		m.process = nil
		m.status.Running = false
		m.status.PID = 0
		return ctx.Err()
	case <-time.After(5 * time.Second):
		m.logger.Warn("Mihomo stop timed out; sending SIGKILL", "pid", item.command.Process.Pid, "reason", "deadline")
		_ = item.command.Process.Kill()
		<-item.done
	}
	m.process = nil
	m.status.Running = false
	m.status.PID = 0
	return nil
}

func (m *Manager) waitReadyLocked(ctx context.Context, endpoints []Endpoint, proxyCount int, tunDevice ...string) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m.refreshProcessLocked()
		if !m.isRunningLocked() {
			return errors.New("mihomo exited before runtime became ready")
		}
		allReady := true
		if len(tunDevice) > 0 {
			device, err := net.InterfaceByName(managedTUNDevice)
			up := err == nil && device.Flags&net.FlagUp != 0
			allReady = up == (tunDevice[0] != "")
		}
		for _, endpoint := range endpoints {
			address := probeAddress(endpoint.BindAddress, endpoint.Port)
			connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
			if err != nil {
				allReady = false
				break
			}
			_ = connection.Close()
		}
		if allReady && proxyCount > 0 {
			connection, err := net.DialTimeout("unix", m.controllerSocket, 250*time.Millisecond)
			if err != nil {
				allReady = false
			} else {
				_ = connection.Close()
			}
		}
		if allReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("mihomo listeners or managed TUN did not become ready (TUN requires /dev/net/tun and CAP_NET_ADMIN)")
}

// validateEndpointAvailabilityLocked catches a port owned by another process
// before publishing a configuration that Mihomo cannot fully bind. Existing
// endpoints from the currently managed configuration are exempt so a normal
// reload can keep them in place, and ports already owned by OUR managed
// config at any bind address are exempt too: changing a listener from
// 127.0.0.1:32822 to 0.0.0.0:32822 releases the old address during the
// upcoming reload, so probing the new address while the old listener is still
// bound would spuriously fail. The reload itself still enforces the bind and
// rolls back on failure.
func (m *Manager) validateEndpointAvailabilityLocked(endpoints []Endpoint) error {
	reserved := m.currentEndpointKeysLocked()
	managedPorts := m.currentManagedPortsLocked()
	for _, endpoint := range endpoints {
		key := endpointKey(endpoint.BindAddress, endpoint.Port)
		if _, alreadyManaged := reserved[key]; alreadyManaged {
			continue
		}
		if _, ours := managedPorts[endpoint.Port]; ours {
			continue
		}
		address := net.JoinHostPort(strings.TrimSpace(endpoint.BindAddress), strconv.Itoa(endpoint.Port))
		probe, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("Mihomo listener %s is unavailable: %w", address, err)
		}
		_ = probe.Close()
	}
	return nil
}

// currentManagedPortsLocked returns the ports currently expected to be owned
// by our own data plane (from the last compiled config and, for an external
// process, the last active file). Used by validateEndpointAvailabilityLocked
// to allow same-port bind-address transitions.
func (m *Manager) currentManagedPortsLocked() map[int]struct{} {
	ports := make(map[int]struct{})
	add := func(bindAddress string, port int) {
		if port > 0 {
			ports[port] = struct{}{}
		}
	}
	if m.isRunningLocked() {
		for _, endpoint := range m.lastCompiled.Endpoints {
			add(endpoint.BindAddress, endpoint.Port)
		}
	}
	if !m.externalProcess {
		return ports
	}
	contents, err := os.ReadFile(m.activePath)
	if err != nil {
		return ports
	}
	var document struct {
		Listeners []struct {
			BindAddress string `yaml:"listen"`
			Port        int    `yaml:"port"`
		} `yaml:"listeners"`
	}
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return ports
	}
	for _, listener := range document.Listeners {
		add(listener.BindAddress, listener.Port)
	}
	return ports
}

func (m *Manager) currentEndpointKeysLocked() map[string]struct{} {
	keys := make(map[string]struct{})
	if m.isRunningLocked() {
		for _, endpoint := range m.lastCompiled.Endpoints {
			keys[endpointKey(endpoint.BindAddress, endpoint.Port)] = struct{}{}
		}
	}
	if !m.externalProcess {
		return keys
	}

	// The systemd-managed data plane may already be running when the control
	// plane starts, so recover its expected endpoints from the last active file.
	contents, err := os.ReadFile(m.activePath)
	if err != nil {
		return keys
	}
	var document struct {
		Listeners []struct {
			BindAddress string `yaml:"listen"`
			Port        int    `yaml:"port"`
		} `yaml:"listeners"`
	}
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return keys
	}
	for _, listener := range document.Listeners {
		if listener.BindAddress != "" && listener.Port > 0 {
			keys[endpointKey(listener.BindAddress, listener.Port)] = struct{}{}
		}
	}
	return keys
}

func endpointKey(bindAddress string, port int) string {
	return net.JoinHostPort(strings.TrimSpace(bindAddress), strconv.Itoa(port))
}

func (m *Manager) refreshProcessLocked() {
	if m.externalProcess {
		wasRunning := m.status.Running
		m.status.Running = m.externalSocketReadyLocked()
		m.status.PID = 0
		if wasRunning && !m.status.Running {
			m.logger.Error("external Mihomo process became unavailable")
		} else if !wasRunning && m.status.Running {
			m.logger.Info("external Mihomo process became available")
		}
		if m.status.Running && m.status.State == "failed" {
			m.status.State = "running"
		}
		return
	}
	if m.process == nil {
		return
	}
	select {
	case err := <-m.process.done:
		item := m.process
		m.process = nil
		m.status.Running = false
		m.status.PID = 0
		m.status.State = "failed"
		if err != nil {
			m.status.LastError = "mihomo exited: " + err.Error()
			m.logger.Error("Mihomo process exited unexpectedly", "pid", item.command.Process.Pid, "error", err)
		} else {
			m.status.LastError = "mihomo exited"
			m.logger.Warn("Mihomo process exited unexpectedly", "pid", item.command.Process.Pid)
		}
	default:
	}
}

func (m *Manager) isRunningLocked() bool {
	if m.externalProcess {
		return m.externalSocketReadyLocked()
	}
	return m.process != nil
}

func (m *Manager) externalSocketReadyLocked() bool {
	connection, err := net.DialTimeout("unix", m.controllerSocket, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func (m *Manager) rollbackAndRestartLocked(ctx context.Context, hadOldConfig bool, oldEndpoints []Endpoint) error {
	if err := m.rollbackConfigLocked(hadOldConfig); err != nil {
		return err
	}
	if !hadOldConfig {
		return nil
	}
	if m.externalProcess || m.process != nil {
		if err := m.reloadExternalLocked(ctx); err != nil {
			if m.externalProcess {
				return fmt.Errorf("reload previous mihomo config: %w", err)
			}
			if stopErr := m.stopLocked(ctx); stopErr != nil {
				return errors.Join(err, stopErr)
			}
			if startErr := m.startLocked(m.activePath); startErr != nil {
				return errors.Join(err, startErr)
			}
		}
		if err := m.waitReadyLocked(ctx, oldEndpoints, m.lastCompiled.ProxyCount, m.lastCompiled.TUNDevice); err != nil {
			return fmt.Errorf("previous mihomo config did not recover: %w", err)
		}
		m.status.Running = true
		if m.externalProcess {
			m.status.PID = 0
		} else {
			m.status.PID = m.process.command.Process.Pid
		}
		m.status.ListenerCount = len(oldEndpoints)
		m.status.ActiveListeners = append([]Endpoint(nil), oldEndpoints...)
		return nil
	}
	if err := m.startLocked(m.activePath); err != nil {
		return fmt.Errorf("restart previous mihomo config: %w", err)
	}
	if len(oldEndpoints) > 0 || m.lastCompiled.ProxyCount > 0 {
		if err := m.waitReadyLocked(ctx, oldEndpoints, m.lastCompiled.ProxyCount, m.lastCompiled.TUNDevice); err != nil {
			return fmt.Errorf("previous mihomo config did not recover: %w", err)
		}
	}
	m.lastCompiled.Endpoints = append([]Endpoint(nil), oldEndpoints...)
	m.status.Running = true
	m.status.PID = m.process.command.Process.Pid
	m.status.ListenerCount = len(oldEndpoints)
	m.status.ActiveListeners = append([]Endpoint(nil), oldEndpoints...)
	return nil
}

func (m *Manager) rollbackConfigLocked(hadOldConfig bool) error {
	if hadOldConfig {
		if err := copyFileAtomic(m.previous, m.activePath, 0o600); err != nil {
			return fmt.Errorf("restore previous mihomo config: %w", err)
		}
		return nil
	}
	if err := os.Remove(m.activePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove failed mihomo config: %w", err)
	}
	return nil
}

func (m *Manager) failLocked(err error) error {
	m.recordFailureLocked(err)
	return err
}

func (m *Manager) recordFailureLocked(err error) {
	m.status.State = "failed"
	m.status.LastError = err.Error()
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".candidate-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func copyFileAtomic(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	content, err := io.ReadAll(io.LimitReader(input, 32<<20))
	if err != nil {
		return err
	}
	return writeFileAtomic(destination, content, mode)
}

func publishCandidate(candidate, active string) error {
	if err := os.Rename(candidate, active); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(active))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func probeAddress(host string, port int) string {
	ip := net.ParseIP(host)
	if ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}
