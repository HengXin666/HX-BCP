package residential

import (
	"context"
	"strings"
)

// EntryGateway is one residential provider the unified entry can dial directly,
// with the account and username dialect needed to render a session.
type EntryGateway struct {
	ID              string
	Name            string
	Protocol        string
	Host            string
	Port            int
	Username        string
	Password        string
	Template        string
	RotationMode    string
	DefaultRegion   string
	TTLSeconds      int
	UpstreamGroupID string
}

// Sticky reports whether the gateway can pin an exit to a session id.
func (g EntryGateway) Sticky() bool {
	return g.RotationMode == RotationSessionTemplate && TemplateUsesSession(g.Template)
}

// RenderUsername renders the gateway username for one session.
func (g EntryGateway) RenderUsername(session, country, city string, ttlSeconds int) (string, error) {
	region := country
	if region == "" {
		region = g.DefaultRegion
	}
	if ttlSeconds <= 0 {
		ttlSeconds = g.TTLSeconds
	}
	return Render(g.Template, Variables{
		User:    g.Username,
		Session: session,
		Region:  region,
		Country: region,
		City:    city,
		TTL:     itoaSeconds(ttlSeconds),
	})
}

// EntryGateways lists the enabled gateway-login providers, session-template and
// per-request ones. API-list, cf-worker and WsPxy providers hand out endpoints
// through channels and are reached by the entry through those channels only.
func (s *Service) EntryGateways(ctx context.Context) ([]EntryGateway, error) {
	records, err := s.repository.ListResidentialProviders(ctx)
	if err != nil {
		return nil, err
	}
	gateways := make([]EntryGateway, 0, len(records))
	for _, record := range records {
		if !record.Enabled {
			continue
		}
		if record.RotationMode != RotationSessionTemplate && record.RotationMode != RotationPerRequest {
			continue
		}
		credentials, err := s.openCredentials(record)
		if err != nil {
			return nil, err
		}
		gateways = append(gateways, EntryGateway{
			ID:              record.ID,
			Name:            record.Name,
			Protocol:        strings.ToLower(record.Protocol),
			Host:            record.GatewayHost,
			Port:            record.GatewayPort,
			Username:        credentials.Username,
			Password:        credentials.Password,
			Template:        record.UsernameTemplate,
			RotationMode:    record.RotationMode,
			DefaultRegion:   record.DefaultRegion,
			TTLSeconds:      record.SessionTTLSeconds,
			UpstreamGroupID: record.UpstreamProxyGroupID,
		})
	}
	return gateways, nil
}

func itoaSeconds(value int) string {
	if value <= 0 {
		return ""
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
