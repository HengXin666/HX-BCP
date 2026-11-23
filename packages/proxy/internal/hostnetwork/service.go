package hostnetwork

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HengXin666/hx-pxy/internal/terminal"
)

var ErrBusy = errors.New("network operation already running")
var ErrInvalid = errors.New("invalid network operation")

type Executor interface {
	Execute(context.Context, string, time.Duration) (terminal.ExecResult, error)
}

type Interface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	Up        bool     `json:"up"`
	Kind      string   `json:"kind"`
}

type Snapshot struct {
	Interfaces     []Interface `json:"interfaces"`
	IPv4Forwarding bool        `json:"ipv4_forwarding"`
	TUNAvailable   bool        `json:"tun_available"`
}

func Inspect() Snapshot {
	result := Snapshot{Interfaces: []Interface{}}
	_, err := os.Stat("/dev/net/tun")
	result.TUNAvailable = err == nil
	forward, _ := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	result.IPv4Forwarding = strings.TrimSpace(string(forward)) == "1"
	devices, _ := net.Interfaces()
	for _, device := range devices {
		kind := "host"
		if device.Name == "docker0" || strings.HasPrefix(device.Name, "br-") {
			kind = "docker"
		}
		if _, err := os.Stat("/sys/class/net/" + device.Name + "/tun_flags"); err == nil {
			kind = "tun"
		}
		addresses, _ := device.Addrs()
		item := Interface{Name: device.Name, Kind: kind, Up: device.Flags&net.FlagUp != 0, Addresses: []string{}}
		for _, address := range addresses {
			item.Addresses = append(item.Addresses, address.String())
		}
		result.Interfaces = append(result.Interfaces, item)
	}
	return result
}

type Service struct {
	executor Executor
	busy     atomic.Bool
}

func NewService(executor Executor) *Service { return &Service{executor: executor} }

var containerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// Commands are fixed; even container selectors accept only Docker hex IDs.
// See .agents/notes/implemented/feature/2026-10-14-network-diagnostics.md.
func commandFor(action, container string) (string, error) {
	switch action {
	case "diagnose":
		if container == "" {
			return `printf '\n=== Routes ===\n'; ip -brief address; ip route; ip -6 route; ip rule; printf '\n=== Docker networks ===\n'; docker network ls; printf '\n=== Forwarding ===\n'; sysctl net.ipv4.ip_forward`, nil
		}
		if !containerID.MatchString(container) {
			return "", ErrInvalid
		}
		return "docker exec " + container + ` sh -c 'printf "=== DNS ===\n"; cat /etc/resolv.conf; printf "\n=== Routes ===\n"; cat /proc/net/route; printf "\n=== DNS lookup ===\n"; if command -v getent >/dev/null; then getent hosts example.com; elif command -v nslookup >/dev/null; then nslookup example.com; else echo "DNS probe unavailable: install getent or nslookup"; exit 2; fi'`, nil
	case "enable-forwarding":
		if container != "" {
			return "", ErrInvalid
		}
		return "sysctl -w net.ipv4.ip_forward=1", nil
	case "disable-forwarding":
		if container != "" {
			return "", ErrInvalid
		}
		return "sysctl -w net.ipv4.ip_forward=0", nil
	default:
		return "", ErrInvalid
	}
}

func (s *Service) Run(ctx context.Context, action, container string) (terminal.ExecResult, error) {
	command, err := commandFor(action, container)
	if err != nil {
		return terminal.ExecResult{}, fmt.Errorf("%w: action or container ID", ErrInvalid)
	}
	if s.executor == nil {
		return terminal.ExecResult{}, errors.New("privileged helper unavailable")
	}
	if !s.busy.CompareAndSwap(false, true) {
		return terminal.ExecResult{}, ErrBusy
	}
	defer s.busy.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return s.executor.Execute(ctx, command, 20*time.Second)
}
