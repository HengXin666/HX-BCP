package api

import (
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
)

// NetworkAddresses answers "how does another process on this host reach a
// listener?" — the question a container has to ask before it can use a proxy
// service.
//
// Why the control plane answers it: inside a container 127.0.0.1 is the
// container itself, and the configured public endpoint is normally a
// Cloudflare domain whose A record points at an edge, not at this host. The
// candidate is the host IP on a Docker bridge. Firewall isolation and custom
// networks can prevent access; this is address discovery, not a reachability
// guarantee. LAN addresses are offered when no bridge exists.
type NetworkAddresses struct {
	// DockerGateway is the host IPv4 address on its docker bridge, empty when no
	// docker bridge is present.
	DockerGateway string `json:"docker_gateway,omitempty"`
	// LANAddresses are the host non-loopback IPv4 addresses, bridge addresses
	// first. The UI offers them in this order.
	LANAddresses []string `json:"lan_addresses"`
}

// handleSystemNetwork reports the addresses a container or another host needs
// to reach a bound listener. It exposes routing facts only (interface
// addresses), so it carries the same auth posture as /system/resources: an
// administrator session, no 2FA step-up.
func (s *Server) handleSystemNetwork(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, resolveNetworkAddresses())
}

func resolveNetworkAddresses() NetworkAddresses {
	result := NetworkAddresses{LANAddresses: []string{}}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	type candidate struct {
		name string
		ip   string
	}
	bridge := make([]candidate, 0, 2)
	lan := make([]candidate, 0, 4)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if _, err := os.Stat("/sys/class/net/" + iface.Name + "/tun_flags"); err == nil {
			continue
		}
		addresses, addressErr := iface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, address := range addresses {
			ip := ipv4FromAddress(address)
			if ip == "" {
				continue
			}
			if iface.Name == "docker0" || strings.HasPrefix(iface.Name, "br-") {
				bridge = append(bridge, candidate{name: iface.Name, ip: ip})
				continue
			}
			lan = append(lan, candidate{name: iface.Name, ip: ip})
		}
	}
	// Prefer the default bridge deterministically, without claiming that a
	// container on an isolated/custom network can necessarily reach it.
	sort.SliceStable(bridge, func(left, right int) bool {
		if (bridge[left].name == "docker0") != (bridge[right].name == "docker0") {
			return bridge[left].name == "docker0"
		}
		return bridge[left].name < bridge[right].name
	})
	if len(bridge) > 0 {
		result.DockerGateway = bridge[0].ip
	}
	seen := map[string]struct{}{}
	for _, item := range append(bridge, lan...) {
		if _, duplicate := seen[item.ip]; duplicate {
			continue
		}
		seen[item.ip] = struct{}{}
		result.LANAddresses = append(result.LANAddresses, item.ip)
	}
	return result
}

func ipv4FromAddress(address net.Addr) string {
	var ip net.IP
	switch typed := address.(type) {
	case *net.IPNet:
		ip = typed.IP
	case *net.IPAddr:
		ip = typed.IP
	default:
		parsed, _, err := net.ParseCIDR(address.String())
		if err != nil {
			return ""
		}
		ip = parsed
	}
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return ""
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return ""
	}
	return ipv4.String()
}
