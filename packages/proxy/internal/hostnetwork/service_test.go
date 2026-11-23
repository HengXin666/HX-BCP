package hostnetwork

import (
	"context"
	"errors"
	"github.com/HengXin666/hx-pxy/internal/terminal"
	"strings"
	"testing"
	"time"
)

func TestNetworkCommandsRejectInjectionAndUnknownRepairs(t *testing.T) {
	for _, id := range []string{";reboot", "--privileged", "abc", "$(id)", "abcdefghijkl", strings.Repeat("a", 65)} {
		if _, err := commandFor("diagnose", id); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q", id)
		}
	}
	for _, action := range []string{"flush-iptables", "restart-docker", "reboot", ""} {
		if _, err := commandFor(action, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q", action)
		}
	}
	command, err := commandFor("diagnose", "abcdef123456")
	if err != nil || !strings.Contains(command, "docker exec abcdef123456 sh -c") || strings.Contains(command, "env") {
		t.Fatalf("unsafe command: %s %v", command, err)
	}
	for _, action := range []string{"enable-forwarding", "disable-forwarding"} {
		if _, err := commandFor(action, "abcdef123456"); err == nil {
			t.Fatal("repair accepted container selector")
		}
	}
}

type blockedExecutor struct{ started chan struct{} }

func (e blockedExecutor) Execute(ctx context.Context, _ string, timeout time.Duration) (terminal.ExecResult, error) {
	if timeout > 20*time.Second {
		panic("unbounded execution")
	}
	close(e.started)
	<-ctx.Done()
	return terminal.ExecResult{}, ctx.Err()
}
func TestNetworkOperationSerializedAndCancelable(t *testing.T) {
	e := blockedExecutor{make(chan struct{})}
	service := NewService(e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.Run(ctx, "diagnose", ""); done <- err }()
	<-e.started
	if _, err := service.Run(ctx, "diagnose", ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not cancel")
	}
}
