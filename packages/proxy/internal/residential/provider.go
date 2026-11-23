package residential

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

var (
	ErrNotFound       = errors.New("residential resource not found")
	ErrConflict       = errors.New("residential resource conflict")
	ErrInvalid        = errors.New("invalid residential configuration")
	ErrRateLimited    = errors.New("rotation rate limit exceeded")
	ErrSessionExpired = errors.New("residential client session expired")
	// ErrProviderUnreachable reports that a residential provider's upstream
	// endpoint (cf-worker panel, api-list extraction, or gateway) could not be
	// reached. It maps to a 502 at the HTTP boundary instead of an opaque 500,
	// and it never leaves an in-progress channel provisioned.
	ErrProviderUnreachable = errors.New("residential provider upstream unreachable")
	// ErrLeaseHeld reports that a declared node's exclusive lease is held by
	// another consumer. Rotation and route changes on a leased node require the
	// holder's lease_id. It maps to a 409 lease_held at the HTTP boundary.
	ErrLeaseHeld = errors.New("residential node lease held by another consumer")
	// ErrLeaseExpired reports that a claim/heartbeat/release referenced a lease
	// that already lapsed or was never granted. It maps to a 409 lease_expired.
	ErrLeaseExpired = errors.New("residential node lease expired or unknown")
	// ErrAllocVersionChanged reports that a rotation/route request carried a
	// stale expected_alloc_version, so the node was rotated by someone else
	// since the caller last read it. It maps to a 409 alloc_version_changed and
	// is the compare-and-swap guard against double rotation of one IP window.
	ErrAllocVersionChanged = errors.New("residential node allocation version changed")
)

// Provider is the administrator-facing view of a vendor account. Credentials are
// deliberately absent: the API never echoes them back.
type Provider struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Vendor               string `json:"vendor"`
	Protocol             string `json:"protocol"`
	GatewayHost          string `json:"gateway_host"`
	GatewayPort          int    `json:"gateway_port"`
	UpstreamProxyGroupID string `json:"upstream_proxy_group_id,omitempty"`
	// APIURL is the vendor extraction endpoint for api-list rotation. It is
	// write-only at the HTTP API boundary because BestProxy extraction URLs may
	// contain an app_key. The service still uses it internally.
	APIURL           string `json:"-"`
	APIURLConfigured bool   `json:"api_url_configured"`
	// WorkerURL is the Cloudflare Worker panel (BPB-Worker-Panel) subscription
	// link for cf-worker rotation. It is write-only at the HTTP API boundary;
	// the API only reports whether it is configured.
	WorkerURL             string     `json:"-"`
	WorkerURLConfigured   bool       `json:"worker_url_configured"`
	APIProxyURL           string     `json:"-"`
	APIProxyConfigured    bool       `json:"api_proxy_configured"`
	UsernameTemplate      string     `json:"username_template"`
	RotationMode          string     `json:"rotation_mode"`
	SessionTTLSeconds     int        `json:"session_ttl_seconds"`
	MaxConcurrentSessions int        `json:"max_concurrent_sessions"`
	SessionExpiryPolicy   string     `json:"session_expiry_policy"`
	PoolSize              int        `json:"-"` // source compatibility for internal callers
	DefaultRegion         string     `json:"default_region,omitempty"`
	DefaultRegionMode     RegionMode `json:"default_region_mode"`
	DefaultRandomRegions  []string   `json:"default_random_regions,omitempty"`
	CredentialsConfigured bool       `json:"credentials_configured"`
	// GatewayUsername is the account login without any session parameters. It
	// is shown so an operator can confirm which account is in use; the password
	// is never returned.
	GatewayUsername string    `json:"gateway_username,omitempty"`
	SupportsSticky  bool      `json:"supports_sticky"`
	Enabled         bool      `json:"enabled"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateProviderRequest struct {
	Name                  string       `json:"name"`
	Vendor                string       `json:"vendor"`
	Protocol              string       `json:"protocol"`
	GatewayHost           string       `json:"gateway_host"`
	GatewayPort           int          `json:"gateway_port"`
	UpstreamProxyGroupID  string       `json:"upstream_proxy_group_id,omitempty"`
	APIURL                string       `json:"api_url,omitempty"`
	WorkerURL             string       `json:"worker_url,omitempty"`
	APIProxyURL           string       `json:"api_proxy_url,omitempty"`
	Credentials           *Credentials `json:"credentials,omitempty"`
	UsernameTemplate      string       `json:"username_template"`
	RotationMode          string       `json:"rotation_mode"`
	SessionTTLSeconds     int          `json:"session_ttl_seconds,omitempty"`
	MaxConcurrentSessions int          `json:"max_concurrent_sessions,omitempty"`
	PoolSize              int          `json:"pool_size,omitempty"` // pre-v19 compatibility
	SessionExpiryPolicy   string       `json:"session_expiry_policy,omitempty"`
	DefaultRegion         string       `json:"default_region,omitempty"`
	DefaultRegionMode     RegionMode   `json:"default_region_mode,omitempty"`
	DefaultRandomRegions  []string     `json:"default_random_regions,omitempty"`
	Enabled               *bool        `json:"enabled,omitempty"`
}

type UpdateProviderRequest struct {
	Version               int          `json:"version"`
	Name                  string       `json:"name"`
	Vendor                string       `json:"vendor"`
	Protocol              string       `json:"protocol"`
	GatewayHost           string       `json:"gateway_host"`
	GatewayPort           int          `json:"gateway_port"`
	UpstreamProxyGroupID  string       `json:"upstream_proxy_group_id,omitempty"`
	APIURL                string       `json:"api_url,omitempty"`
	WorkerURL             string       `json:"worker_url,omitempty"`
	APIProxyURL           string       `json:"api_proxy_url,omitempty"`
	Credentials           *Credentials `json:"credentials,omitempty"`
	UsernameTemplate      string       `json:"username_template"`
	RotationMode          string       `json:"rotation_mode"`
	SessionTTLSeconds     int          `json:"session_ttl_seconds,omitempty"`
	MaxConcurrentSessions int          `json:"max_concurrent_sessions,omitempty"`
	PoolSize              int          `json:"pool_size,omitempty"` // pre-v19 compatibility
	SessionExpiryPolicy   string       `json:"session_expiry_policy,omitempty"`
	DefaultRegion         string       `json:"default_region,omitempty"`
	DefaultRegionMode     RegionMode   `json:"default_region_mode,omitempty"`
	DefaultRandomRegions  []string     `json:"default_random_regions,omitempty"`
	Enabled               bool         `json:"enabled"`
}

func (s *Service) ListProviders(ctx context.Context) ([]Provider, error) {
	records, err := s.repository.ListResidentialProviders(ctx)
	if err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, len(records))
	for _, record := range records {
		providers = append(providers, s.providerFromRecord(record))
	}
	return providers, nil
}

func (s *Service) GetProvider(ctx context.Context, id string) (Provider, error) {
	record, err := s.repository.GetResidentialProvider(ctx, id)
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	return s.providerFromRecord(record), nil
}

func (s *Service) CreateProvider(ctx context.Context, request CreateProviderRequest) (Provider, error) {
	id, err := newID("residential-provider")
	if err != nil {
		return Provider{}, err
	}
	normalized, err := s.normalizeProvider(id, providerInput{
		Name:                  request.Name,
		Vendor:                request.Vendor,
		Protocol:              request.Protocol,
		GatewayHost:           request.GatewayHost,
		GatewayPort:           request.GatewayPort,
		UpstreamProxyGroupID:  request.UpstreamProxyGroupID,
		APIProxyURL:           request.APIProxyURL,
		APIURL:                request.APIURL,
		WorkerURL:             request.WorkerURL,
		Credentials:           request.Credentials,
		UsernameTemplate:      request.UsernameTemplate,
		RotationMode:          request.RotationMode,
		SessionTTLSeconds:     request.SessionTTLSeconds,
		MaxConcurrentSessions: request.MaxConcurrentSessions,
		PoolSize:              request.PoolSize,
		SessionExpiryPolicy:   request.SessionExpiryPolicy,
		DefaultRegion:         request.DefaultRegion,
		DefaultRegionMode:     request.DefaultRegionMode,
		DefaultRandomRegions:  request.DefaultRandomRegions,
		Enabled:               request.Enabled == nil || *request.Enabled,
	}, nil)
	if err != nil {
		return Provider{}, err
	}
	if err := s.validateUpstreamProxyGroup(ctx, normalized.UpstreamProxyGroupID); err != nil {
		return Provider{}, err
	}
	now := s.now().UTC()
	created, err := s.repository.CreateResidentialProvider(ctx, store.ResidentialProviderRecord{
		ID:                   id,
		Name:                 normalized.Name,
		Vendor:               normalized.Vendor,
		Protocol:             normalized.Protocol,
		GatewayHost:          normalized.GatewayHost,
		GatewayPort:          normalized.GatewayPort,
		UpstreamProxyGroupID: normalized.UpstreamProxyGroupID,
		CredentialsEncrypted: normalized.CredentialsEncrypted,
		UsernameTemplate:     normalized.UsernameTemplate,
		RotationMode:         normalized.RotationMode,
		SessionTTLSeconds:    normalized.SessionTTLSeconds,
		PoolSize:             normalized.MaxConcurrentSessions,
		SessionExpiryPolicy:  normalized.SessionExpiryPolicy,
		DefaultRegion:        normalized.DefaultRegion,
		DefaultRegionMode:    string(normalized.DefaultRegionMode),
		DefaultRandomRegions: marshalRegionList(normalized.DefaultRandomRegions),
		Enabled:              normalized.Enabled,
		Version:              1,
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	return s.providerFromRecord(created), nil
}

func (s *Service) UpdateProvider(ctx context.Context, id string, request UpdateProviderRequest) (Provider, error) {
	if request.Version < 1 {
		return Provider{}, fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	existing, err := s.repository.GetResidentialProvider(ctx, id)
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	normalized, err := s.normalizeProvider(id, providerInput{
		Name:                  request.Name,
		Vendor:                request.Vendor,
		Protocol:              request.Protocol,
		GatewayHost:           request.GatewayHost,
		GatewayPort:           request.GatewayPort,
		UpstreamProxyGroupID:  request.UpstreamProxyGroupID,
		APIProxyURL:           request.APIProxyURL,
		APIURL:                request.APIURL,
		WorkerURL:             request.WorkerURL,
		Credentials:           request.Credentials,
		UsernameTemplate:      request.UsernameTemplate,
		RotationMode:          request.RotationMode,
		SessionTTLSeconds:     request.SessionTTLSeconds,
		MaxConcurrentSessions: request.MaxConcurrentSessions,
		PoolSize:              request.PoolSize,
		SessionExpiryPolicy:   request.SessionExpiryPolicy,
		DefaultRegion:         request.DefaultRegion,
		DefaultRegionMode:     request.DefaultRegionMode,
		DefaultRandomRegions:  request.DefaultRandomRegions,
		Enabled:               request.Enabled,
		ExistingAPIURL:        existing.APIURL,
	}, existing.CredentialsEncrypted)
	if err != nil {
		return Provider{}, err
	}
	if err := s.validateUpstreamProxyGroup(ctx, normalized.UpstreamProxyGroupID); err != nil {
		return Provider{}, err
	}
	existing.Name = normalized.Name
	existing.Vendor = normalized.Vendor
	existing.Protocol = normalized.Protocol
	existing.GatewayHost = normalized.GatewayHost
	existing.GatewayPort = normalized.GatewayPort
	existing.UpstreamProxyGroupID = normalized.UpstreamProxyGroupID
	// API extraction URLs are now held in the encrypted provider secret. This
	// also clears the v14 plaintext compatibility column after an edit.
	existing.APIURL = ""
	existing.CredentialsEncrypted = normalized.CredentialsEncrypted
	existing.UsernameTemplate = normalized.UsernameTemplate
	existing.RotationMode = normalized.RotationMode
	existing.SessionTTLSeconds = normalized.SessionTTLSeconds
	existing.PoolSize = normalized.MaxConcurrentSessions
	existing.SessionExpiryPolicy = normalized.SessionExpiryPolicy
	existing.DefaultRegion = normalized.DefaultRegion
	existing.DefaultRegionMode = string(normalized.DefaultRegionMode)
	existing.DefaultRandomRegions = marshalRegionList(normalized.DefaultRandomRegions)
	existing.Enabled = normalized.Enabled
	existing.UpdatedAt = s.now().UTC()
	updated, err := s.repository.UpdateResidentialProvider(ctx, existing, request.Version)
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	// Existing client sessions keep the credentials and endpoint with which
	// they were allocated. New settings take effect on the next allocation or
	// expiry-driven rotation instead of replacing live sessions in bulk.
	return s.providerFromRecord(updated), nil
}

func (s *Service) DeleteProvider(ctx context.Context, id string, version int) error {
	if version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	if err := s.repository.DeleteResidentialProvider(ctx, id, version); err != nil {
		return mapStoreError(err)
	}
	return nil
}

type providerInput struct {
	Name                  string
	Vendor                string
	Protocol              string
	GatewayHost           string
	GatewayPort           int
	UpstreamProxyGroupID  string
	APIURL                string
	WorkerURL             string
	APIProxyURL           string
	Credentials           *Credentials
	UsernameTemplate      string
	RotationMode          string
	SessionTTLSeconds     int
	MaxConcurrentSessions int
	PoolSize              int
	SessionExpiryPolicy   string
	DefaultRegion         string
	DefaultRegionMode     RegionMode
	DefaultRandomRegions  []string
	Enabled               bool
	ExistingAPIURL        string
}

type normalizedProvider struct {
	Name                  string
	Vendor                string
	Protocol              string
	GatewayHost           string
	GatewayPort           int
	UpstreamProxyGroupID  string
	WorkerURL             string
	CredentialsEncrypted  []byte
	UsernameTemplate      string
	RotationMode          string
	SessionTTLSeconds     int
	MaxConcurrentSessions int
	SessionExpiryPolicy   string
	DefaultRegion         string
	DefaultRegionMode     RegionMode
	DefaultRandomRegions  []string
	Enabled               bool
}

// validateUpstreamProxyGroup keeps the stable database id in the provider
// record while allowing operators to rename a group without breaking the
// residential chain. The compiler resolves the id to the current Mihomo name.
func (s *Service) validateUpstreamProxyGroup(ctx context.Context, groupID string) error {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	group, err := s.repository.GetProxyGroup(ctx, groupID)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: upstream proxy group %q does not exist", ErrInvalid, groupID)
	}
	if err != nil {
		return fmt.Errorf("validate upstream proxy group: %w", err)
	}
	if !group.Enabled {
		return fmt.Errorf("%w: upstream proxy group %q is disabled", ErrInvalid, group.Name)
	}
	return nil
}

func (s *Service) normalizeProvider(
	id string,
	input providerInput,
	existingCredentials []byte,
) (normalizedProvider, error) {
	name := strings.TrimSpace(input.Name)
	if len(name) < 1 || len(name) > 128 {
		return normalizedProvider{}, fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	vendor := strings.ToLower(strings.TrimSpace(input.Vendor))
	if vendor == "" {
		vendor = "custom"
	}
	if len(vendor) > 64 {
		return normalizedProvider{}, fmt.Errorf("%w: vendor must contain at most 64 characters", ErrInvalid)
	}
	rotationMode := strings.ToLower(strings.TrimSpace(input.RotationMode))
	if rotationMode == "" {
		rotationMode = RotationSessionTemplate
	}
	if !containsString(SupportedRotationModes(), rotationMode) {
		return normalizedProvider{}, fmt.Errorf(
			"%w: rotation_mode must be one of %s",
			ErrInvalid,
			strings.Join(SupportedRotationModes(), ", "),
		)
	}
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	if rotationMode == RotationCloudflareWorker {
		if !containsString(SupportedWorkerProtocols(), protocol) {
			return normalizedProvider{}, fmt.Errorf(
				"%w: protocol must be one of %s for %q rotation",
				ErrInvalid,
				strings.Join(SupportedWorkerProtocols(), ", "),
				RotationCloudflareWorker,
			)
		}
	} else if rotationMode == RotationHXCFWsPxy {
		if protocol != "http" {
			return normalizedProvider{}, fmt.Errorf(
				"%w: protocol must be http for %q rotation",
				ErrInvalid,
				RotationHXCFWsPxy,
			)
		}
	} else if !containsString(SupportedProtocols(), protocol) {
		return normalizedProvider{}, fmt.Errorf(
			"%w: protocol must be one of %s",
			ErrInvalid,
			strings.Join(SupportedProtocols(), ", "),
		)
	}
	host := strings.ToLower(strings.TrimSpace(input.GatewayHost))
	template := strings.TrimSpace(input.UsernameTemplate)
	existingSecrets := providerSecrets{}
	if len(existingCredentials) > 0 {
		decoded, err := s.openProviderSecrets(id, existingCredentials)
		if err != nil {
			// A new credential pair or API URL replaces the old secret, so a
			// corrupt legacy value should not prevent recovery through edit.
			if input.Credentials == nil && strings.TrimSpace(input.APIURL) == "" && strings.TrimSpace(input.APIProxyURL) == "" && strings.TrimSpace(input.WorkerURL) == "" {
				return normalizedProvider{}, err
			}
		} else {
			existingSecrets = decoded
		}
	}
	apiProxyURL := strings.TrimSpace(input.APIProxyURL)
	if apiProxyURL == "" {
		apiProxyURL = strings.TrimSpace(existingSecrets.APIProxyURL)
	}
	if apiProxyURL != "" {
		validatedProxyURL, err := validateAPIProxyURL(apiProxyURL)
		if err != nil {
			return normalizedProvider{}, err
		}
		apiProxyURL = validatedProxyURL
	}
	if rotationMode == RotationCloudflareWorker {
		// cf-worker providers fetch VLESS/Trojan share URIs from a Cloudflare
		// Worker panel (BPB-Worker-Panel) subscription link. There is no
		// gateway login or username template; the gateway fields hold a
		// placeholder that never reaches the data plane.
		host = cfWorkerGatewayPlaceholder
		input.GatewayPort = 1
		template = ""
		input.APIURL = ""
		workerURL := strings.TrimSpace(input.WorkerURL)
		if workerURL == "" {
			workerURL = strings.TrimSpace(existingSecrets.WorkerURL)
		}
		workerURL, err := validateWorkerURL(workerURL)
		if err != nil {
			return normalizedProvider{}, err
		}
		input.WorkerURL = workerURL
	} else if rotationMode == RotationAPIList {
		// api-list providers get their endpoints from the extraction API, so
		// there is no gateway login and no username template to validate. The
		// gateway fields hold a placeholder that never reaches the data plane.
		host = apiListGatewayPlaceholder
		input.GatewayPort = 1
		template = ""
		apiURL := strings.TrimSpace(input.APIURL)
		if apiURL == "" {
			apiURL = strings.TrimSpace(existingSecrets.APIURL)
		}
		if apiURL == "" {
			// v14 stored the URL in a plaintext compatibility column. An edit
			// transparently upgrades it into the encrypted provider secret.
			apiURL = strings.TrimSpace(input.ExistingAPIURL)
		}
		apiURL, err := validateAPIURL(apiURL)
		if err != nil {
			return normalizedProvider{}, err
		}
		input.APIURL = apiURL
	} else if rotationMode == RotationHXCFWsPxy {
		host = wsPxyGatewayPlaceholder
		input.GatewayPort = 1
		template = ""
		input.WorkerURL = ""
		apiURL := strings.TrimSpace(input.APIURL)
		if apiURL == "" {
			apiURL = strings.TrimSpace(existingSecrets.APIURL)
		}
		if apiURL == "" {
			apiURL = strings.TrimSpace(input.ExistingAPIURL)
		}
		apiURL, err := validateWsPxyControlURL(apiURL)
		if err != nil {
			return normalizedProvider{}, err
		}
		input.APIURL = apiURL
	} else {
		input.APIURL = ""
		if err := validateGatewayHost(host); err != nil {
			return normalizedProvider{}, err
		}
		if input.GatewayPort < 1 || input.GatewayPort > 65535 {
			return normalizedProvider{}, fmt.Errorf("%w: gateway_port must be between 1 and 65535", ErrInvalid)
		}
		if err := ValidateTemplate(template); err != nil {
			return normalizedProvider{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if rotationMode == RotationSessionTemplate && !TemplateUsesSession(template) {
			return normalizedProvider{}, fmt.Errorf(
				"%w: rotation_mode %q requires the username template to reference {session}",
				ErrInvalid,
				RotationSessionTemplate,
			)
		}
	}
	ttl := input.SessionTTLSeconds
	if ttl == 0 {
		ttl = 600
		if vendor == "bestproxy" || vendor == "rapidproxy" {
			// BestProxy's `life` and RapidProxy's `stime` parameters are
			// measured in minutes, unlike the generic provider field name, and
			// their documented range is short.
			ttl = 60
		}
	}
	if ttl < 0 || ttl > 86400 {
		return normalizedProvider{}, fmt.Errorf("%w: session_ttl_seconds must be between 0 and 86400", ErrInvalid)
	}
	if rotationMode == RotationCloudflareWorker || rotationMode == RotationHXCFWsPxy {
		// A Cloudflare Worker panel and HX-CF-WsPxy only rotate when the
		// consumer explicitly asks. Forcing TTL 0 means the pool never expires
		// on its own: if the user does not refresh, the colo pin stays put.
		ttl = 0
	}
	if vendor == "bestproxy" && strings.Contains(template, "_life-") && (ttl < 1 || ttl > 120) {
		return normalizedProvider{}, fmt.Errorf("%w: BestProxy life must be between 1 and 120 minutes", ErrInvalid)
	}
	if vendor == "rapidproxy" && rotationMode == RotationSessionTemplate && (ttl < 1 || ttl > 180) {
		return normalizedProvider{}, fmt.Errorf("%w: RapidProxy stime must be between 1 and 180 minutes", ErrInvalid)
	}
	maxSessions := input.MaxConcurrentSessions
	if maxSessions == 0 {
		maxSessions = input.PoolSize
	}
	if maxSessions == 0 {
		maxSessions = 64
	}
	if maxSessions < 1 || maxSessions > 1024 {
		return normalizedProvider{}, fmt.Errorf("%w: max_concurrent_sessions must be between 1 and 1024", ErrInvalid)
	}
	if rotationMode == RotationPerRequest {
		maxSessions = 1
	}
	expiryPolicy := strings.ToLower(strings.TrimSpace(input.SessionExpiryPolicy))
	if expiryPolicy == "" {
		expiryPolicy = "rotate"
	}
	if expiryPolicy != "expire" && expiryPolicy != "rotate" {
		return normalizedProvider{}, fmt.Errorf("%w: session_expiry_policy must be expire or rotate", ErrInvalid)
	}
	regionSelection, err := normalizeRegionSelection(
		string(input.DefaultRegionMode),
		input.DefaultRegion,
		input.DefaultRandomRegions,
	)
	if err != nil {
		return normalizedProvider{}, err
	}
	region := regionSelection.Region
	if vendor == "bestproxy" && strings.Contains(template, "{region}") &&
		region == "" && regionSelection.Mode != RegionModeApplicationRandom {
		return normalizedProvider{}, fmt.Errorf("%w: BestProxy providers using {region} require a default_region", ErrInvalid)
	}

	secrets := existingSecrets
	if rotationMode == RotationCloudflareWorker {
		secrets = providerSecrets{WorkerURL: input.WorkerURL, APIProxyURL: apiProxyURL}
	} else if rotationMode == RotationAPIList || rotationMode == RotationHXCFWsPxy {
		secrets = providerSecrets{APIURL: input.APIURL, APIProxyURL: apiProxyURL}
	} else if input.Credentials != nil {
		username := strings.TrimSpace(input.Credentials.Username)
		password := input.Credentials.Password
		if username == "" || password == "" {
			return normalizedProvider{}, fmt.Errorf("%w: gateway username and password must both be set", ErrInvalid)
		}
		if len(username) > 128 || len(password) > 512 {
			return normalizedProvider{}, fmt.Errorf("%w: gateway credentials are too long", ErrInvalid)
		}
		// Reject credentials that cannot survive the username framing before
		// they are encrypted, so the failure surfaces at save time.
		validationRegion := region
		if regionSelection.Mode == RegionModeApplicationRandom {
			validationRegion = regionSelection.RandomRegions[0]
		}
		if _, err := Render(template, Variables{
			User:    username,
			Session: "0123456789abcdef",
			Region:  validationRegion,
			Country: validationRegion,
			TTL:     "600",
		}); err != nil {
			return normalizedProvider{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		secrets = providerSecrets{Username: username, Password: password, APIProxyURL: apiProxyURL}
	} else if rotationMode != RotationAPIList && rotationMode != RotationHXCFWsPxy {
		secrets.APIURL = ""
		secrets.APIProxyURL = apiProxyURL
	}
	if rotationMode != RotationAPIList && rotationMode != RotationCloudflareWorker &&
		rotationMode != RotationHXCFWsPxy &&
		(secrets.Username == "" || secrets.Password == "") {
		return normalizedProvider{}, fmt.Errorf("%w: gateway credentials are required", ErrInvalid)
	}
	encoded, err := json.Marshal(secrets)
	if err != nil {
		return normalizedProvider{}, fmt.Errorf("encode residential provider secrets: %w", err)
	}
	sealed, err := s.cipher.Seal(encoded, providerAssociatedData(id))
	if err != nil {
		return normalizedProvider{}, fmt.Errorf("encrypt residential provider secrets: %w", err)
	}

	return normalizedProvider{
		Name:                  name,
		Vendor:                vendor,
		Protocol:              protocol,
		GatewayHost:           host,
		GatewayPort:           input.GatewayPort,
		UpstreamProxyGroupID:  strings.TrimSpace(input.UpstreamProxyGroupID),
		WorkerURL:             input.WorkerURL,
		CredentialsEncrypted:  sealed,
		UsernameTemplate:      template,
		RotationMode:          rotationMode,
		SessionTTLSeconds:     ttl,
		MaxConcurrentSessions: maxSessions,
		SessionExpiryPolicy:   expiryPolicy,
		DefaultRegion:         region,
		DefaultRegionMode:     regionSelection.Mode,
		DefaultRandomRegions:  append([]string(nil), regionSelection.RandomRegions...),
		Enabled:               input.Enabled,
	}, nil
}

// apiListGatewayPlaceholder fills the gateway columns of api-list providers.
// Their real endpoints come from the extraction API, so the placeholder never
// reaches the data plane; it only satisfies the schema's NOT NULL constraints.
const apiListGatewayPlaceholder = "api-list.invalid"

// cfWorkerGatewayPlaceholder fills the gateway columns of cf-worker providers.
// Their real endpoints are VLESS/Trojan share URIs fetched from the Cloudflare
// Worker panel, so the placeholder never reaches the data plane.
const cfWorkerGatewayPlaceholder = "cf-worker.invalid"

// validateWorkerURL checks a Cloudflare Worker panel (BPB-Worker-Panel)
// subscription link before it is stored. The control plane fetches this URL,
// so it must be HTTPS and resolve to a public host; private and loopback
// targets would otherwise turn the provider save into an SSRF primitive.
// Unlike api_url, a path and query parameters are expected: BPB links carry
// the secure path and the client selection, e.g. /<securePath>/sub/raw?app=xray.
// Any accepted link form is normalized to that canonical subscription URL.
func validateWorkerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: worker_url is required for cf-worker rotation", ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: worker_url must be an https URL without a fragment", ErrInvalid)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%w: worker_url must not embed credentials", ErrInvalid)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsPrivate() {
		return "", fmt.Errorf("%w: worker_url must point at a public address", ErrInvalid)
	}
	if err := validateGatewayHost(parsed.Hostname()); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return normalizeWorkerURL(raw)
}

// normalizeWorkerURL rewrites any accepted BPB-Worker-Panel link form into the
// canonical raw subscription endpoint. Upstream derives its client selection
// exclusively from the ?app= query parameter and only /sub/raw returns the
// base64 VLESS/Trojan payload, so a panel link, a bare secure path, or a sub
// link without the parameter would otherwise fetch the HTML panel, a 404, or
// the configured fallback page. The rewrite is idempotent and preserves any
// unknown path layout (custom domains) while pinning the client parameter.
func normalizeWorkerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("%w: worker_url must be an https URL", ErrInvalid)
	}
	segments := make([]string, 0, 3)
	for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return "", fmt.Errorf(
			"%w: worker_url must include the BPB secure path, e.g. https://<worker>/<securePath>/sub/raw?app=xray",
			ErrInvalid,
		)
	}
	securePath := segments[0]
	switch {
	case len(segments) == 1:
		// Bare secure path: https://host/<securePath> -> subscription link.
		parsed.Path = "/" + securePath + "/sub/raw"
	case segments[1] == "panel" || segments[1] == "login":
		// Panel or login page: rewrite to the raw subscription endpoint.
		parsed.Path = "/" + securePath + "/sub/raw"
	case segments[1] == "sub":
		// 20260821 cfnew（BPB-Worker-Panel fork）兼容：`/{securePath}/sub?app=xray`
		// 直接返回 base64 VLESS/Trojan 订阅（实测 200），改写为 /sub/raw 会 404。
		// 标准 BPB 的 raw 链接（/sub/raw?app=xray）走 default 分支保留原路径，
		// 所以这里不再做 sub -> raw 改写；仅 panel/login 才需要重写到 raw。
		// 保留原路径，下方统一 pin ?app=xray 即可。
	default:
		// Unknown layout (e.g. a custom domain route), raw subscription links
		// (/sub/raw?app=xray) and cfnew sub links (/sub?app=xray): keep the
		// path so a fetch error stays truthful; only the client parameter is
		// pinned. cfnew 20260821 实测 /sub 直接返回节点，不能改写成 /sub/raw。
	}
	parsed.Fragment = ""
	query := parsed.Query()
	query.Set("app", "xray")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// validateAPIURL checks an api-list extraction endpoint before it is stored.
// The control plane fetches this URL, so it must be HTTPS and resolve to a
// public host; private and loopback targets would otherwise turn the provider
// save into an SSRF primitive.
func validateAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: api_url is required for api-list rotation", ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: api_url must be an https URL", ErrInvalid)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%w: api_url must not embed credentials", ErrInvalid)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsPrivate() {
		return "", fmt.Errorf("%w: api_url must point at a public address", ErrInvalid)
	}
	if err := validateGatewayHost(parsed.Hostname()); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return raw, nil
}

// validateAPIProxyURL accepts the proxy protocols supported by the control
// plane. Unlike api_url, loopback/private hosts are allowed because a local
// Clash/Mihomo listener such as 127.0.0.1:7890 is a normal deployment.
func validateAPIProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("%w: api_proxy_url must be an HTTP, HTTPS, or SOCKS5 proxy URL", ErrInvalid)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
	default:
		return "", fmt.Errorf("%w: api_proxy_url must use http, https, or socks5", ErrInvalid)
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return "", fmt.Errorf("%w: api_proxy_url must include a host and port", ErrInvalid)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("%w: api_proxy_url port is invalid", ErrInvalid)
	}
	return raw, nil
}

// validateGatewayHost rejects hosts that would point the data plane at the local
// machine. A residential gateway is by definition remote, and allowing loopback
// or link-local targets here would turn provider configuration into an SSRF and
// listener-loop primitive.
func validateGatewayHost(host string) error {
	if host == "" {
		return fmt.Errorf("%w: gateway_host is required", ErrInvalid)
	}
	if len(host) > 253 || strings.ContainsAny(host, "/:@?# \t\r\n") {
		return fmt.Errorf("%w: gateway_host is not a valid hostname", ErrInvalid)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return fmt.Errorf("%w: gateway_host must be a public address", ErrInvalid)
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("%w: gateway_host must be a public address", ErrInvalid)
	}
	if !strings.Contains(host, ".") {
		return fmt.Errorf("%w: gateway_host must be a fully qualified domain or IP", ErrInvalid)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%w: gateway_host is not a valid hostname", ErrInvalid)
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return fmt.Errorf("%w: gateway_host is not a valid hostname", ErrInvalid)
			}
		}
	}
	return nil
}

// CfWorkerSubscriptions returns the plain subscription URL list of every
// enabled cf-worker provider. This is the "config center" export consumed by
// external clients (e.g. HX-OutlookRegister's cf_bpb preset): they pull these
// URLs and dial the Cloudflare Workers directly — traffic never relays through
// this control plane, only the configuration does. 20260821 user decision:
// pxy is a config center / authorization issuer, not a relay.
func (s *Service) CfWorkerSubscriptions(ctx context.Context) ([]CfSubscription, error) {
	records, err := s.repository.ListResidentialProviders(ctx)
	if err != nil {
		return nil, err
	}
	subscriptions := make([]CfSubscription, 0, len(records))
	for _, record := range records {
		if record.RotationMode != RotationCloudflareWorker || !record.Enabled {
			continue
		}
		provider := s.providerFromRecord(record)
		workerURL := strings.TrimSpace(provider.WorkerURL)
		if workerURL == "" {
			continue
		}
		subscriptions = append(subscriptions, CfSubscription{
			Name: provider.Name,
			URL:  workerURL,
		})
	}
	return subscriptions, nil
}

// CfSubscription is one exported Cloudflare Worker subscription URL.
type CfSubscription struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Service) providerFromRecord(record store.ResidentialProviderRecord) Provider {
	secrets, secretsErr := s.openProviderSecrets(record.ID, record.CredentialsEncrypted)
	defaultRegionMode := RegionMode(record.DefaultRegionMode)
	if defaultRegionMode == "" {
		defaultRegionMode = RegionModeFixed
	}
	apiURL := secrets.APIURL
	if apiURL == "" {
		// Read v14 records created before API URLs were moved into the encrypted
		// secret envelope. Saving the provider upgrades this compatibility value.
		apiURL = record.APIURL
	}
	provider := Provider{
		ID:                    record.ID,
		Name:                  record.Name,
		Vendor:                record.Vendor,
		Protocol:              record.Protocol,
		GatewayHost:           record.GatewayHost,
		GatewayPort:           record.GatewayPort,
		UpstreamProxyGroupID:  record.UpstreamProxyGroupID,
		APIURL:                apiURL,
		APIURLConfigured:      (record.RotationMode == RotationAPIList || record.RotationMode == RotationHXCFWsPxy) && apiURL != "",
		WorkerURL:             secrets.WorkerURL,
		WorkerURLConfigured:   record.RotationMode == RotationCloudflareWorker && secrets.WorkerURL != "",
		APIProxyURL:           secrets.APIProxyURL,
		APIProxyConfigured:    secrets.APIProxyURL != "",
		UsernameTemplate:      record.UsernameTemplate,
		RotationMode:          record.RotationMode,
		SessionTTLSeconds:     record.SessionTTLSeconds,
		MaxConcurrentSessions: record.PoolSize,
		PoolSize:              record.PoolSize,
		SessionExpiryPolicy:   record.SessionExpiryPolicy,
		DefaultRegion:         record.DefaultRegion,
		DefaultRegionMode:     defaultRegionMode,
		DefaultRandomRegions:  parseRegionList(record.DefaultRandomRegions),
		CredentialsConfigured: record.RotationMode != RotationAPIList &&
			record.RotationMode != RotationCloudflareWorker &&
			record.RotationMode != RotationHXCFWsPxy && secretsErr == nil &&
			secrets.Username != "" && secrets.Password != "",
		SupportsSticky: (record.RotationMode == RotationSessionTemplate && TemplateUsesSession(record.UsernameTemplate)) ||
			record.RotationMode == RotationAPIList || record.RotationMode == RotationCloudflareWorker ||
			record.RotationMode == RotationHXCFWsPxy,
		Enabled:   record.Enabled,
		Version:   record.Version,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
	// The account login is safe to display; the password never leaves the box.
	if record.RotationMode != RotationAPIList && record.RotationMode != RotationCloudflareWorker &&
		record.RotationMode != RotationHXCFWsPxy && secretsErr == nil {
		provider.GatewayUsername = secrets.Username
	}
	return provider
}

type providerSecrets struct {
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	APIURL      string `json:"api_url,omitempty"`
	WorkerURL   string `json:"worker_url,omitempty"`
	APIProxyURL string `json:"api_proxy_url,omitempty"`
}

func marshalRegionList(regions []string) string {
	if len(regions) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(regions)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func parseRegionList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var regions []string
	if err := json.Unmarshal([]byte(raw), &regions); err != nil {
		return nil
	}
	return regions
}

func (s *Service) openProviderSecrets(id string, encrypted []byte) (providerSecrets, error) {
	if len(encrypted) == 0 {
		return providerSecrets{}, nil
	}
	plaintext, err := s.cipher.Open(encrypted, providerAssociatedData(id))
	if err != nil {
		return providerSecrets{}, fmt.Errorf("decrypt residential provider secrets: %w", err)
	}
	var secrets providerSecrets
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return providerSecrets{}, fmt.Errorf("decode residential provider secrets: %w", err)
	}
	return secrets, nil
}

func (s *Service) openCredentials(record store.ResidentialProviderRecord) (Credentials, error) {
	secrets, err := s.openProviderSecrets(record.ID, record.CredentialsEncrypted)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Username: secrets.Username, Password: secrets.Password}, nil
}

// providerCredentials returns the gateway credentials for a provider. api-list
// providers authenticate by extraction URL and cf-worker providers carry the
// panel identity inside the fetched share URI, so both always yield an empty
// credential pair.
func (s *Service) providerCredentials(record store.ResidentialProviderRecord) (Credentials, error) {
	if record.RotationMode == RotationAPIList || record.RotationMode == RotationCloudflareWorker ||
		record.RotationMode == RotationHXCFWsPxy {
		return Credentials{}, nil
	}
	return s.openCredentials(record)
}

func providerAssociatedData(id string) []byte {
	return []byte("residential-provider:" + id)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mapStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func newID(prefix string) (string, error) {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(buffer[:]), nil
}

func newToken() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate rotate token: %w", err)
	}
	return hex.EncodeToString(buffer[:]), nil
}
