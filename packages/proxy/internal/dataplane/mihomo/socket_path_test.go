package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// socket 路径有一个不可让步的要求: 写进 active.yaml 的那条, 必须就是控制面拨号的那条。
// mihomo 只按配置里的 external-controller-unix 建 socket, 控制面只按 Manager.controllerSocket
// 拨 —— 两边算法差一个输入 (HOME 有没有被设置), 就变成"数据面在跑、控制面说它没运行"。
//
// 见 .agents/notes/implemented/architecture/2026-10-14-controller-socket-path-must-match-the-data-plane.md
func newSocketPathManager(t *testing.T, external bool, runtimeDirectory string) *Manager {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo 'Mihomo test'; fi\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(context.Background(), filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	box, _ := secret.New(make([]byte, 32))
	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		compiler, binary, filepath.Join(runtimeDirectory, "active.yaml"),
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithExternalProcess(external),
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

// systemd 托管的数据面必须拿到 runtimeDirectory 下的直接路径, 且与 HOME 无关。
// 2026-10-13 的生产升级就是踩在这里: unit 用 User= 起服务时 systemd 会设 HOME,
// 而写配置的那个进程没有 HOME, 于是同一份配置里外两条路径, 数据面永远握不上手。
func TestSystemdManagedSocketPathIgnoresHome(t *testing.T) {
	runtimeDirectory := filepath.Join(t.TempDir(), "runtime")
	for _, home := range []string{"", "/var/lib/hx-pxy", "/root"} {
		t.Setenv("HOME", home)
		manager := newSocketPathManager(t, true, runtimeDirectory)
		want := filepath.Join(runtimeDirectory, "mihomo.sock")
		if manager.controllerSocket != want {
			t.Errorf("HOME=%q: controllerSocket = %q, want %q", home, manager.controllerSocket, want)
		}
		if strings.Contains(manager.controllerSocket, ".hx-pxy-runtime") {
			t.Errorf("HOME=%q: systemd 托管的数据面拿到了 HOME 短路径 %q; unit 的沙箱只允许它在 runtimeDirectory 下建 socket", home, manager.controllerSocket)
		}
		// 编译器写进 active.yaml 的那条必须就是拨号用的那条。
		if manager.compiler.controllerSocket != manager.controllerSocket {
			t.Errorf("HOME=%q: active.yaml 用 %q, 控制面拨 %q", home, manager.compiler.controllerSocket, manager.controllerSocket)
		}
	}
}

// 自带数据面 (非 systemd 托管) 在路径够短时也用直接路径: 它是 data plane 自己 -d 的目录,
// 两边天然一致, 不依赖任何环境变量。
func TestSelfManagedSocketPathUsesDirectPathWhenItFits(t *testing.T) {
	t.Setenv("HOME", "/var/lib/hx-pxy")
	runtimeDirectory := filepath.Join(t.TempDir(), "runtime")
	manager := newSocketPathManager(t, false, runtimeDirectory)
	want := filepath.Join(runtimeDirectory, "mihomo.sock")
	if manager.controllerSocket != want {
		t.Errorf("controllerSocket = %q, want %q", manager.controllerSocket, want)
	}
	if manager.compiler.controllerSocket != manager.controllerSocket {
		t.Errorf("active.yaml 用 %q, 控制面拨 %q", manager.compiler.controllerSocket, manager.controllerSocket)
	}
}

// 只有直接路径超过内核 sun_path 上限时, 自带数据面才挪到 HOME 下的短目录;
// 挪过去的路径本身也必须在限制之内, 否则等于没修。
func TestSelfManagedSocketPathFallsBackToShortPathWhenTooLong(t *testing.T) {
	// HOME 要短: 短目录策略只保证"比直接路径短", 结果能否落在内核上限内取决于 HOME 本身。
	home, err := os.MkdirTemp("", "hxh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	runtimeDirectory := filepath.Join(t.TempDir(), strings.Repeat("d", 120), "runtime")
	manager := newSocketPathManager(t, false, runtimeDirectory)
	if len(filepath.Join(runtimeDirectory, "mihomo.sock")) <= maxUnixSocketPath {
		t.Fatal("fixture 的直接路径没有超过内核上限, 这个用例测不到回退")
	}
	digest := sha256.Sum256([]byte(runtimeDirectory))
	want := filepath.Join(home, ".hx-pxy-runtime", hex.EncodeToString(digest[:8]), "mihomo.sock")
	if manager.controllerSocket != want {
		t.Errorf("controllerSocket = %q, want %q", manager.controllerSocket, want)
	}
	if len(manager.controllerSocket) > maxUnixSocketPath {
		t.Errorf("回退后的路径 %q 仍然超过 %d 字节", manager.controllerSocket, maxUnixSocketPath)
	}
	if manager.compiler.controllerSocket != manager.controllerSocket {
		t.Errorf("active.yaml 用 %q, 控制面拨 %q", manager.compiler.controllerSocket, manager.controllerSocket)
	}
}

// systemd 托管的数据面即使路径超长也留在 runtimeDirectory: unit 是 ProtectSystem=strict
// + ReadWritePaths=<runtimeDirectory>, 挪到外面 mihomo 根本建不了 socket。让失败发生在
// "路径太长"这个明处, 好过变成一个查不到的"控制面说数据面没运行"。
func TestSystemdManagedSocketPathStaysDirectEvenWhenTooLong(t *testing.T) {
	t.Setenv("HOME", "/var/lib/hx-pxy")
	runtimeDirectory := filepath.Join(t.TempDir(), strings.Repeat("d", 120), "runtime")
	manager := newSocketPathManager(t, true, runtimeDirectory)
	want := filepath.Join(runtimeDirectory, "mihomo.sock")
	if manager.controllerSocket != want {
		t.Errorf("controllerSocket = %q, want %q", manager.controllerSocket, want)
	}
}
