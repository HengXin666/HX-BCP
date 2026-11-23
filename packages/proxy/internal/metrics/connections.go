package metrics

import (
	"net/netip"
	"sort"
	"time"

	"github.com/HengXin666/hx-pxy/internal/hostnetwork"
)

const MaxActiveDetails = 500

type ConnectionDetail struct {
	ID              string `json:"id"`
	SourceIP        string `json:"source_ip"`
	SourcePort      string `json:"source_port"`
	Destination     string `json:"destination"`
	DestinationPort string `json:"destination_port"`
	Network         string `json:"network"`
	Inbound         string `json:"inbound"`
	Origin          string `json:"origin"`
	Upload          int64  `json:"upload"`
	Download        int64  `json:"download"`
}

type ActiveConnections struct {
	Items     []ConnectionDetail `json:"items"`
	Total     int                `json:"total"`
	SampledAt time.Time          `json:"sampled_at"`
}

// No extra Controller polling or connection-history database. The existing
// collector owns this bounded, credential-free snapshot.
// See .agents/notes/implemented/feature/2026-10-14-active-connection-origins.md.
func (s *Service) ActiveConnections() ActiveConnections {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.connections
	value.Items = append([]ConnectionDetail{}, value.Items...)
	return value
}

func classifyOrigin(source string, interfaces []hostnetwork.Interface) string {
	ip, err := netip.ParseAddr(source)
	if err != nil {
		return "unknown"
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return "local"
	}
	for _, device := range interfaces {
		for _, address := range device.Addresses {
			prefix, err := netip.ParsePrefix(address)
			if err == nil && prefix.Addr().Unmap() == ip {
				return "local"
			}
		}
	}
	for _, device := range interfaces {
		if device.Kind != "docker" {
			continue
		}
		for _, address := range device.Addresses {
			prefix, err := netip.ParsePrefix(address)
			if err == nil && prefix.Contains(ip) {
				return "docker"
			}
		}
	}
	return "external"
}

func connectionDetails(snapshot RuntimeSnapshot, now time.Time) ActiveConnections {
	result := ActiveConnections{Items: []ConnectionDetail{}, Total: len(snapshot.Connections), SampledAt: now}
	interfaces := hostnetwork.Inspect().Interfaces
	// Stable selection even if Mihomo returns its connection map in a new order.
	sort.Slice(snapshot.Connections, func(i, j int) bool { return snapshot.Connections[i].ID < snapshot.Connections[j].ID })
	for _, connection := range snapshot.Connections {
		if len(result.Items) >= MaxActiveDetails {
			break
		}
		detail := connection.Detail
		detail.ID, detail.Upload, detail.Download = connection.ID, connection.Upload, connection.Download
		detail.Origin = classifyOrigin(detail.SourceIP, interfaces)
		result.Items = append(result.Items, detail)
	}
	return result
}
