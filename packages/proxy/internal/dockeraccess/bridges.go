package dockeraccess

import (
	"net"
	"os"
	"sort"
	"strings"
)

// Bridge is one host-side Docker bridge address a companion entry may bind
// to. Address is the gateway IP containers on that network use to reach the
// host; Subnet is the network it serves, so the UI can tell compose networks
// apart and a firewall rule can be scoped to exactly that range.
type Bridge struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Subnet    string `json:"subnet"`
}

// DiscoverBridges lists the IPv4 gateway addresses of the host's Docker
// bridges (docker0 first, then br-* compose networks by name). It reads
// interface state only; it does not prove that a given container can reach the
// address, which depends on the container's network and the host firewall.
//
// The error is returned rather than folded into an empty list: an empty list
// means "this host has no Docker bridge", and claiming that after failing to
// read the link table is how a host with ten bridges was told it had none. The
// read goes through NETLINK_ROUTE, so a sandbox that drops AF_NETLINK (see
// deploy/systemd/hx-pxy.service) fails here. See
// .agents/notes/implemented/bug-fix/2026-10-17-control-plane-must-enumerate-interfaces.md.
func DiscoverBridges() ([]Bridge, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	bridges := make([]Bridge, 0, 2)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || !isDockerBridgeName(iface.Name) {
			continue
		}
		if _, err := os.Stat("/sys/class/net/" + iface.Name + "/tun_flags"); err == nil {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			ip := network.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
				continue
			}
			subnet := &net.IPNet{IP: ip.Mask(network.Mask), Mask: network.Mask}
			bridges = append(bridges, Bridge{Interface: iface.Name, Address: ip.String(), Subnet: subnet.String()})
		}
	}
	SortBridges(bridges)
	return bridges, nil
}

// SortBridges orders candidates deterministically: the default docker0
// bridge first, then compose bridges by interface name and address.
func SortBridges(bridges []Bridge) {
	sort.SliceStable(bridges, func(left, right int) bool {
		leftDefault, rightDefault := bridges[left].Interface == "docker0", bridges[right].Interface == "docker0"
		if leftDefault != rightDefault {
			return leftDefault
		}
		if bridges[left].Interface != bridges[right].Interface {
			return bridges[left].Interface < bridges[right].Interface
		}
		return bridges[left].Address < bridges[right].Address
	})
}

func isDockerBridgeName(name string) bool {
	return name == "docker0" || strings.HasPrefix(name, "br-")
}

// DiscoverLAN lists the host's other non-loopback IPv4 addresses (not Docker
// bridges, not TUN devices). It names a dial target for a service that already
// binds a wildcard address on a host without a Docker bridge.
func DiscoverLAN() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]string, 0, 2)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || isDockerBridgeName(iface.Name) {
			continue
		}
		if _, err := os.Stat("/sys/class/net/" + iface.Name + "/tun_flags"); err == nil {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := network.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() {
				result = append(result, ip.String())
			}
		}
	}
	return result
}
