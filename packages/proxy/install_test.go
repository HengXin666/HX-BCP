package hxpxy

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScriptSyntax(t *testing.T) {
	t.Parallel()
	command := exec.Command("bash", "-n", "install.sh")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bash -n install.sh failed: %v\n%s", err, output)
	}
}

func TestSystemdUnitKeepsManagementAPIPrivate(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("deploy/systemd/hx-pxy.service")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	unit := string(content)
	required := []string{
		"--listen 127.0.0.1:19090",
		"--master-key /var/lib/hx-pxy/master.key",
		"--web-root /usr/local/lib/hx-pxy/current/web",
		"--mihomo-external",
		"Requires=hx-pxy-dataplane.service hx-pxy-terminal.service",
		"After=network-online.target hx-pxy-dataplane.service hx-pxy-terminal.service",
		"--terminal-socket /run/hx-pxy/terminal.sock",
		"Restart=on-failure",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"ReadWritePaths=/var/lib/hx-pxy",
	}
	for _, expected := range required {
		if !strings.Contains(unit, expected) {
			t.Errorf("systemd unit is missing %q", expected)
		}
	}
}

// TestControlPlaneUnitCanEnumerateHostInterfaces guards the 2026-10-17 report
// in which a host with ten Docker bridges was told it had none: Go reads the
// link table over NETLINK_ROUTE, so a unit that restricts address families to
// AF_UNIX/AF_INET/AF_INET6 makes net.Interfaces() fail and every bridge / LAN
// discovery silently return empty.
// See .agents/notes/implemented/bug-fix/2026-10-17-control-plane-must-enumerate-interfaces.md.
func TestControlPlaneUnitCanEnumerateHostInterfaces(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("deploy/systemd/hx-pxy.service")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK") {
		t.Error("control plane unit must allow AF_NETLINK: without it net.Interfaces() fails and no Docker bridge is ever detected")
	}
}

func TestPrivilegedTerminalSystemdUnitIsRootOnlyAndSocketScoped(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("deploy/systemd/hx-pxy-terminal.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(content)
	for _, expected := range []string{
		"User=root",
		"--terminal-helper",
		"--terminal-socket /run/hx-pxy/terminal.sock",
		"--terminal-socket-group hx-pxy",
		"--terminal-helper-user hx-pxy",
		"RuntimeDirectoryMode=0750",
	} {
		if !strings.Contains(unit, expected) {
			t.Errorf("privileged terminal unit is missing %q", expected)
		}
	}
	if strings.Contains(unit, "ListenStream=") {
		t.Fatal("privileged terminal must not expose a network socket")
	}
}

func TestDataPlaneSystemdUnitIsIndependentAndSandboxed(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("deploy/systemd/hx-pxy-dataplane.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(content)
	required := []string{
		"/usr/local/lib/hx-pxy/current/mihomo",
		"/var/lib/hx-pxy/runtime/active.yaml",
		"Restart=on-failure",
		"StartLimitBurst=20",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/lib/hx-pxy/runtime",
	}
	for _, expected := range required {
		if !strings.Contains(unit, expected) {
			t.Errorf("data-plane unit is missing %q", expected)
		}
	}
	if strings.Contains(unit, "User=root") {
		t.Fatal("data-plane unit must not run as root")
	}
}

func TestInstallScriptDocumentsVerifiedReleaseAndOneLineUpgrade(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, expected := range []string{"SHA256SUMS", "verify_bundle", "safe_extract_bundle", "switch_current", "sudo hx-pxy-install upgrade", "hx-pxy-dataplane.service", "hx-pxy-terminal.service"} {
		if !strings.Contains(script, expected) {
			t.Errorf("install script is missing %q", expected)
		}
	}
}

func TestInstallScriptRejectsUnsafeOfflineVersion(t *testing.T) {
	t.Parallel()
	command := exec.Command("bash", "-c", `source ./install.sh; VERSION='../../etc'; validate_version`)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("unsafe version was accepted: %s", output)
	}
}

func TestPackageReleaseBundleContract(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	control := writeExecutable(t, root, "hx-pxyd")
	mihomo := writeExecutable(t, root, "mihomo")
	web := filepath.Join(root, "web")
	if err := os.Mkdir(web, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("release-ui\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outputs := []string{filepath.Join(root, "out-a"), filepath.Join(root, "out-b")}
	var digests [2][sha256.Size]byte
	for index, output := range outputs {
		for run := 0; run < 2; run++ {
			command := exec.Command("bash", "scripts/package-release.sh",
				"--version", "v1.2.3", "--arch", "amd64",
				"--control", control, "--mihomo", mihomo,
				"--web-dir", web, "--output-dir", output,
			)
			if combined, err := command.CombinedOutput(); err != nil {
				t.Fatalf("package release: %v\n%s", err, combined)
			}
		}
		bundle := filepath.Join(output, "hx-pxy_v1.2.3_linux_amd64.tar.gz")
		payload, err := os.ReadFile(bundle)
		if err != nil {
			t.Fatal(err)
		}
		digests[index] = sha256.Sum256(payload)
		assertReleaseArchive(t, bundle)
		checksums, err := os.ReadFile(filepath.Join(output, "SHA256SUMS"))
		if err != nil {
			t.Fatal(err)
		}
		expectedLine := fmt.Sprintf("%x  %s", digests[index], filepath.Base(bundle))
		if strings.TrimSpace(string(checksums)) != expectedLine {
			t.Fatalf("SHA256SUMS = %q, want %q", strings.TrimSpace(string(checksums)), expectedLine)
		}
		version, err := os.ReadFile(filepath.Join(output, "VERSION"))
		if err != nil || string(version) != "v1.2.3\n" {
			t.Fatalf("VERSION = %q, err=%v", version, err)
		}
	}
	if digests[0] != digests[1] {
		t.Fatalf("release archives are not reproducible: %x != %x", digests[0], digests[1])
	}
}

// 发布链路与升级路径都是"只在真实事件里执行"的代码: 前者只在打 tag 时跑, 后者只在
// 已安装的机器上跑。它们不会在 go build / go test 里响, 所以由测试读文件来钉住契约。
// 见 .agents/notes/implemented/architecture/2026-10-13-upgrade-adopts-pre-rename-installation.md
func TestReleaseWorkflowPublishesRenamedBundle(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	for _, expected := range []string{
		"./cmd/hx-pxyd",
		"build/hx-pxyd-${GOARCH}",
		"release/hx-pxy_${{ github.ref_name }}_linux_${{ matrix.arch }}.tar.gz",
		"sha256sum hx-pxy_*.tar.gz",
	} {
		if !strings.Contains(workflow, expected) {
			t.Errorf("release workflow is missing %q", expected)
		}
	}
	if strings.Contains(workflow, "hx-proxygroup") {
		t.Error("release workflow still references the pre-rename bundle or binary name")
	}
}

func TestInstallScriptAdoptsPreRenameInstallation(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, expected := range []string{
		`LEGACY_DATA_DIR="/var/lib/hx-proxygroup"`,
		`LEGACY_CONTROL_SERVICE="hx-proxygroup.service"`,
		"adopt_legacy_installation",
		"hx-proxygroup.db",
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("install script is missing %q", expected)
		}
	}
	// The adoption has to run before the new data directory is created, otherwise
	// its "new layout absent, old layout present" precondition can never hold.
	adoption := strings.Index(script, "    adopt_legacy_installation\n")
	directories := strings.Index(script, "    ensure_account_and_directories\n")
	if adoption < 0 || directories < 0 {
		t.Fatalf("install_or_upgrade is missing the adoption or directory step (adoption=%d, directories=%d)", adoption, directories)
	}
	if adoption > directories {
		t.Fatal("legacy adoption must run before ensure_account_and_directories")
	}
}

// 迁移脚本是"只在别人的机器上跑一次"的代码: 它不参与编译, 也不在 CI 的日常路径上。
// 因此它的行为由测试直接驱动脚本里的函数来钉住, 而不是靠人工演练。
// 见 .agents/notes/implemented/architecture/2026-10-13-upgrade-adopts-pre-rename-installation.md
func TestMigrationScriptMovesPreRenameLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	legacyData := filepath.Join(root, "legacy-data")
	legacyConfig := filepath.Join(root, "legacy-config")
	legacyLog := filepath.Join(root, "legacy-log")
	legacyRoot := filepath.Join(root, "legacy-install")
	newData := filepath.Join(root, "new-data")
	newConfig := filepath.Join(root, "new-config")
	newLog := filepath.Join(root, "new-log")
	newRoot := filepath.Join(root, "new-install")
	for _, directory := range []string{legacyData, legacyConfig, legacyLog, legacyRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A WAL-mode database keeps committed data in -wal until checkpoint, so the
	// sidecar has to travel with the main file.
	for name, content := range map[string]string{
		"hx-proxygroup.db":     "main",
		"hx-proxygroup.db-wal": "wal",
		"hx-proxygroup.db-shm": "shm",
	} {
		if err := os.WriteFile(filepath.Join(legacyData, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacyConfig, "config.yaml"), []byte("mode: rule\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	// Dry run first: it must report the plan without touching anything.
	dryRun := exec.Command("bash", "-c", `source scripts/migrate-from-hx-proxygroup.sh; DRY_RUN=true; migrate_layout`)
	dryRun.Env = append(os.Environ(),
		"HX_LEGACY_DATA_DIR="+legacyData, "HX_DATA_DIR="+newData,
		"HX_LEGACY_CONFIG_DIR="+legacyConfig, "HX_CONFIG_DIR="+newConfig,
		"HX_LEGACY_LOG_DIR="+legacyLog, "HX_LOG_DIR="+newLog,
		"HX_LEGACY_INSTALL_ROOT="+legacyRoot, "HX_INSTALL_ROOT="+newRoot,
	)
	if output, err := dryRun.CombinedOutput(); err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(newData); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s (Stat error = %v)", newData, err)
	}
	if _, err := os.Stat(filepath.Join(legacyData, "hx-proxygroup.db")); err != nil {
		t.Fatalf("dry run moved the database: %v", err)
	}

	// Real run: the trees move and the database is renamed in place.
	real := exec.Command("bash", "-c", `source scripts/migrate-from-hx-proxygroup.sh; migrate_layout`)
	real.Env = dryRun.Env
	if output, err := real.CombinedOutput(); err != nil {
		t.Fatalf("migration failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(legacyData); !os.IsNotExist(err) {
		t.Fatalf("legacy data directory survived the migration (Stat error = %v)", err)
	}
	for name, want := range map[string]string{
		"hx-pxy.db":     "main",
		"hx-pxy.db-wal": "wal",
		"hx-pxy.db-shm": "shm",
	} {
		content, err := os.ReadFile(filepath.Join(newData, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(content) != want {
			t.Errorf("%s = %q, want %q", name, content, want)
		}
	}
	if _, err := os.Stat(filepath.Join(newConfig, "config.yaml")); err != nil {
		t.Errorf("configuration did not move: %v", err)
	}
	if _, err := os.Stat(newLog); err != nil {
		t.Errorf("log directory did not move: %v", err)
	}
	if _, err := os.Stat(newRoot); err != nil {
		t.Errorf("install root did not move: %v", err)
	}

	// A second run must be a no-op rather than a merge into a live layout.
	second := exec.Command("bash", "-c", `source scripts/migrate-from-hx-proxygroup.sh; migrate_layout`)
	second.Env = dryRun.Env
	if output, err := second.CombinedOutput(); err != nil {
		t.Fatalf("re-running the migration failed: %v\n%s", err, output)
	}
}

// 安装器的接管与独立脚本是同一条决策的两条实现。它们必须对"旧布局长什么样"
// 有同一个答案, 否则升级路径与手工迁移会搬不同的东西。
func TestMigrationScriptAndInstallerAgreeOnLegacyLayout(t *testing.T) {
	t.Parallel()
	script, err := os.ReadFile("scripts/migrate-from-hx-proxygroup.sh")
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{
		"hx-proxygroup.service",
		"hx-proxygroup-dataplane.service",
		"hx-proxygroup-terminal.service",
		"hx-proxygroup.db",
		"/var/lib/hx-proxygroup",
		"/etc/hx-proxygroup",
		"/usr/local/lib/hx-proxygroup",
		"/usr/local/sbin/hx-proxygroup-install",
		"hx-proxygroup",
	} {
		if !strings.Contains(string(script), legacy) {
			t.Errorf("migration script does not mention legacy identifier %q", legacy)
		}
		if !strings.Contains(string(installer), legacy) {
			t.Errorf("install.sh does not mention legacy identifier %q", legacy)
		}
	}
}

// 安装器的接管必须对"重跑"是安全的。第一次尝试会在失败之前建出新的数据目录,
// 因此把"停旧单元"挂在"新目录不存在"上, 会让重跑永远不再停旧单元 —— 而两代控制面都绑
// 19090, 于是一次瞬时失败变成永久端口冲突。2026-10-13 的生产机就是这样坏的。
func TestInstallerStopsLegacyUnitsOnEveryRun(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	if !strings.Contains(script, "stop_legacy_units() {") {
		t.Fatal("install.sh 没有独立且幂等的 stop_legacy_units")
	}
	if !strings.Contains(script, "legacy_layout_pending() {") {
		t.Fatal("install.sh 没有把搬迁与停单元分开")
	}
	// 搬迁的门槛是"新数据库还不存在", 不是"新目录不存在"。
	if !strings.Contains(script, `[[ ! -e "${DATA_DIR}/hx-pxy.db" && ! -e "${DATA_DIR}/hx-proxygroup.db" ]] || return 1`) {
		t.Fatal("搬迁没有被正确地挡在新数据库之外")
	}
	adopt := strings.Index(script, "adopt_legacy_installation() {")
	if adopt < 0 {
		t.Fatal("install.sh 没有 adopt_legacy_installation")
	}
	body := script[adopt:]
	stop := strings.Index(body, "    stop_legacy_units\n")
	gate := strings.Index(body, "    legacy_layout_pending || return 0\n")
	if stop < 0 || gate < 0 {
		t.Fatalf("adopt_legacy_installation 必须先停旧单元再判断是否搬迁 (stop=%d, gate=%d)", stop, gate)
	}
	if stop > gate {
		t.Fatal("adopt_legacy_installation 在停旧单元之前就判断了搬迁")
	}
}

// 首次安装失败时, 安装器打印的 "restoring <刚装的那个版本>" 是一句不可能兑现的话:
// 它把同一个失败的启动再跑一遍。回滚必须只在确实存在"另一个版本"时发生。
func TestInstallerOnlyRollsBackToADifferentVersion(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	if !strings.Contains(script, `"${old_target}" != "${INSTALL_ROOT}/versions/${VERSION}"`) {
		t.Fatal("install.sh 在 old_target 就是刚装的版本时仍然回滚")
	}
	if !strings.Contains(script, "diagnose_control_plane_failure") {
		t.Fatal("install.sh 没有在控制面起不来时给出诊断")
	}
	if !strings.Contains(script, "address already in use") {
		t.Fatal("诊断没有点名端口冲突这种失败")
	}
}

func writeExecutable(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertReleaseArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	wanted := map[string]bool{
		"./bin/hx-pxyd":                   false,
		"./bin/mihomo":                    false,
		"./LICENSE":                       false,
		"./web/index.html":                false,
		"./install.sh":                    false,
		"./deploy/systemd/hx-pxy.service": false,
		"./deploy/systemd/hx-pxy-dataplane.service": false,
		"./deploy/systemd/hx-pxy-terminal.service":  false,
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := wanted[header.Name]; ok {
			wanted[header.Name] = true
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("release archive is missing %s", name)
		}
	}
}
