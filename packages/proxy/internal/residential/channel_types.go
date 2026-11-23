package residential

import (
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

// Channel is the administrator-facing view of one residential entry point.
type Channel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name,omitempty"`
	// Providers is the aggregated node list under this channel
	// (CF-渠道 → CF-Node1..N, 20260823 user decision).
	Providers      []Provider              `json:"providers,omitempty"`
	Mode           string                  `json:"mode"`
	ProxyGroupID   string                  `json:"proxy_group_id"`
	ListenerID     string                  `json:"listener_id"`
	Region         string                  `json:"region,omitempty"`
	RegionMode     RegionMode              `json:"region_mode"`
	RandomRegions  []string                `json:"random_regions,omitempty"`
	Endpoint       ChannelEndpoint         `json:"endpoint"`
	PublicEndpoint listener.PublicEndpoint `json:"public_endpoint"`
	// SubscriptionURL publishes stable client nodes. RotationURL is retained
	// only for clients using the pre-0.2.0 on-demand session API.
	SubscriptionURL string `json:"subscription_url,omitempty"`
	RotationURL     string `json:"rotation_url,omitempty"`
	ControlURL      string `json:"control_url,omitempty"`
	// SessionCount is the number of stable logical nodes in the subscription.
	// Zero preserves the legacy on-demand session model.
	SessionCount int `json:"session_count"`
	// IdleReleaseSeconds returns the provider allocation after inactivity while
	// retaining the logical node and its client credential.
	IdleReleaseSeconds int `json:"idle_release_seconds"`
	// Preallocate reports whether sessions eagerly allocate their residential
	// IP at channel create/update (true) or lazily on first client request
	// (false, default). 20260821 user decision.
	Preallocate bool             `json:"preallocate"`
	Sessions    []ChannelSession `json:"sessions,omitempty"`
	// DirectEndpoint is an optional authenticated TCP entry point which bypasses
	// the reverse proxy.
	DirectEndpoint *ChannelEndpoint `json:"direct_endpoint,omitempty"`
	// ControlPath can spend provider quota and is administrator-only.
	ControlPath             string     `json:"control_path,omitempty"`
	ActiveSessionCount      int        `json:"active_session_count"`
	PoolSize                int        `json:"pool_size,omitempty"` // pre-v19 response compatibility
	ActiveSessionIndex      int        `json:"active_session_index"`
	RotateCount             int        `json:"rotate_count"`
	LastRotatedAt           *time.Time `json:"last_rotated_at,omitempty"`
	LastExitIP              string     `json:"last_exit_ip,omitempty"`
	PoolCreatedAt           *time.Time `json:"pool_created_at,omitempty"`
	PoolRefreshAfterSeconds int        `json:"pool_refresh_after_seconds,omitempty"`
	SessionTTLSeconds       int        `json:"session_ttl_seconds,omitempty"`
	RotatePath              string     `json:"rotate_path,omitempty"`
	CanRotate               bool       `json:"can_rotate"`
	Enabled                 bool       `json:"enabled"`
	Version                 int        `json:"version"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// ChannelSession is the administrator-facing view of one published node. It
// never contains the client credential.
type ChannelSession struct {
	Index         int        `json:"index"`
	SessionID     string     `json:"session_id"`
	NodeName      string     `json:"node_name"`
	RouteMode     string     `json:"route_mode"`
	CountryCode   string     `json:"country_code,omitempty"`
	ExitIP        string     `json:"exit_ip,omitempty"`
	Allocated     bool       `json:"allocated"`
	AllocatedAt   *time.Time `json:"allocated_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RotateCount   int        `json:"rotate_count"`
	LastRotatedAt *time.Time `json:"last_rotated_at,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	// AllocVersion and the lease fields expose the unified-window state to the
	// administrator. lease_id is never rendered; the admin sees who holds the
	// node window and until when.
	AllocVersion   int        `json:"alloc_version"`
	LeaseHolder    string     `json:"lease_holder,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
}

type ChannelEndpoint struct {
	Kind        string             `json:"kind"`
	BindAddress string             `json:"bind_address"`
	Port        int                `json:"port"`
	AuthEnabled bool               `json:"auth_enabled"`
	Transport   listener.Transport `json:"transport"`
	SharePath   string             `json:"share_path,omitempty"`
}

type CreateChannelRequest struct {
	Name       string `json:"name"`
	ProviderID string `json:"provider_id"`
	// ProviderIDs aggregates additional providers under the channel
	// (20260823: CF-渠道 聚合多个 CF-Node 供应商).
	ProviderIDs   []string   `json:"provider_ids,omitempty"`
	Mode          string     `json:"mode"`
	Protocol      string     `json:"protocol,omitempty"`
	Region        string     `json:"region,omitempty"`
	RegionMode    RegionMode `json:"region_mode,omitempty"`
	RandomRegions []string   `json:"random_regions,omitempty"`
	PoolSize      int        `json:"pool_size,omitempty"` // ignored for sticky channels
	SessionCount  int        `json:"session_count,omitempty"`
	IdleRelease   int        `json:"idle_release_seconds,omitempty"`
	// Preallocate eagerly allocates an IP for every declared session at
	// channel create/update. Default false = lazy allocation: credentials
	// exist immediately, the first client request triggers allocation
	// (avoids a startup burst blocking channel creation, 20260821).
	Preallocate    bool                    `json:"preallocate,omitempty"`
	Listener       ChannelListenerRequest  `json:"listener"`
	DirectListener *ChannelListenerRequest `json:"direct_listener,omitempty"`
	PublicEndpoint listener.PublicEndpoint `json:"public_endpoint,omitempty"`
	Enabled        *bool                   `json:"enabled,omitempty"`
}

type ChannelListenerRequest struct {
	Kind        string             `json:"kind"`
	BindAddress string             `json:"bind_address"`
	Port        int                `json:"port"`
	Auth        *listener.Auth     `json:"auth,omitempty"`
	Transport   listener.Transport `json:"transport,omitempty"`
}

type UpdateChannelRequest struct {
	Version        int                      `json:"version"`
	Name           string                   `json:"name"`
	Region         string                   `json:"region,omitempty"`
	RegionMode     RegionMode               `json:"region_mode,omitempty"`
	RandomRegions  []string                 `json:"random_regions,omitempty"`
	SessionCount   *int                     `json:"session_count,omitempty"`
	IdleRelease    *int                     `json:"idle_release_seconds,omitempty"`
	Preallocate    *bool                    `json:"preallocate,omitempty"`
	PublicEndpoint *listener.PublicEndpoint `json:"public_endpoint,omitempty"`
	DirectListener *ChannelListenerRequest  `json:"direct_listener,omitempty"`
	ClearDirect    bool                     `json:"clear_direct_listener,omitempty"`
	Enabled        bool                     `json:"enabled"`
}
