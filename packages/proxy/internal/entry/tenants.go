package entry

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// ErrInvalidTenant reports a tenant request that cannot be stored.
var ErrInvalidTenant = errors.New("invalid entry tenant")

// ErrAuthFailed reports a wrong tenant name or secret.
var ErrAuthFailed = errors.New("entry authentication failed")

// Cipher seals tenant secrets at rest.
type Cipher interface {
	Seal([]byte, []byte) ([]byte, error)
	Open([]byte, []byte) ([]byte, error)
}

// TenantStore is the persistence the tenant registry needs.
type TenantStore interface {
	CreateEntryTenant(context.Context, store.EntryTenantRecord) (store.EntryTenantRecord, error)
	ListEntryTenants(context.Context) ([]store.EntryTenantRecord, error)
	GetEntryTenant(context.Context, string) (store.EntryTenantRecord, error)
	UpdateEntryTenant(context.Context, store.EntryTenantRecord) (store.EntryTenantRecord, error)
	DeleteEntryTenant(context.Context, string) error
	AddEntryTenantUsage(context.Context, string, int64, int64) error
	ResetEntryTenantUsage(context.Context, string) error
}

// Tenant is the API view of a tenant. Secret is only set when it was just
// created or rotated.
type Tenant struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Pools          []string  `json:"pools"`
	ResiQuotaBytes int64     `json:"resi_quota_bytes"`
	ResiUsedBytes  int64     `json:"resi_used_bytes"`
	TotalBytes     int64     `json:"total_bytes"`
	Enabled        bool      `json:"enabled"`
	Note           string    `json:"note"`
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Secret         string    `json:"secret,omitempty"`
}

// TenantRequest creates or edits a tenant. Nil fields keep their value on edit.
type TenantRequest struct {
	Name           string   `json:"name"`
	Pools          []string `json:"pools"`
	ResiQuotaBytes *int64   `json:"resi_quota_bytes"`
	Enabled        *bool    `json:"enabled"`
	Note           *string  `json:"note"`
	RotateSecret   bool     `json:"rotate_secret"`
	Version        int      `json:"version"`
}

type cachedTenant struct {
	record store.EntryTenantRecord
	secret []byte
}

// Tenants authenticates entry clients against a cached copy of the registry.
type Tenants struct {
	store  TenantStore
	cipher Cipher

	mutex   sync.RWMutex
	byName  map[string]cachedTenant
	pending map[string][2]int64
	// changed runs after a tenant is updated or deleted, with its id.
	changed func(id string, deleted bool)
}

// NewTenants builds a registry and loads it once.
func NewTenants(ctx context.Context, tenantStore TenantStore, cipher Cipher) (*Tenants, error) {
	tenants := &Tenants{store: tenantStore, cipher: cipher, byName: map[string]cachedTenant{}, pending: map[string][2]int64{}}
	return tenants, tenants.Reload(ctx)
}

// Reload rereads every tenant and its secret.
func (t *Tenants) Reload(ctx context.Context) error {
	records, err := t.store.ListEntryTenants(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]cachedTenant, len(records))
	for _, record := range records {
		secret, err := t.cipher.Open(record.SecretSealed, tenantAssociatedData(record.ID))
		if err != nil {
			// A tenant whose secret no longer opens cannot authenticate; the
			// others keep working.
			continue
		}
		next[record.Name] = cachedTenant{record: record, secret: secret}
	}
	t.mutex.Lock()
	t.byName = next
	t.mutex.Unlock()
	return nil
}

// Authenticate checks a tenant secret and the pool the selector asks for.
func (t *Tenants) Authenticate(selector Selector, secret string) (store.EntryTenantRecord, error) {
	t.mutex.RLock()
	cached, ok := t.byName[selector.Tenant]
	pending := t.pending[cached.record.ID]
	t.mutex.RUnlock()
	if !ok || !cached.record.Enabled || subtle.ConstantTimeCompare(cached.secret, []byte(secret)) != 1 {
		return store.EntryTenantRecord{}, ErrAuthFailed
	}
	pool := selector.Pool
	if selector.Group != "" && pool != PoolResidential {
		if pool != "" {
			return store.EntryTenantRecord{}, fmt.Errorf("%w: group only combines with pool-resi", ErrPoolDenied)
		}
		if !groupAllowed(cached.record.Pools, selector.Group) {
			return store.EntryTenantRecord{}, fmt.Errorf("%w: group %s", ErrPoolDenied, selector.Group)
		}
		return cached.record, nil
	}
	if pool == "" {
		pool = PoolSubscription
	}
	if !containsString(cached.record.Pools, pool) {
		return store.EntryTenantRecord{}, fmt.Errorf("%w: %s", ErrPoolDenied, pool)
	}
	quota := cached.record.ResiQuotaBytes
	if pool == PoolResidential && quota > 0 && cached.record.ResiUsedBytes+pending[0] >= quota {
		return store.EntryTenantRecord{}, ErrQuotaExceeded
	}
	return cached.record, nil
}

// SecretFor returns a tenant's current secret, for rendering its subscription.
func (t *Tenants) SecretFor(id string) (store.EntryTenantRecord, string, bool) {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	for _, cached := range t.byName {
		if cached.record.ID == id {
			return cached.record, string(cached.secret), true
		}
	}
	return store.EntryTenantRecord{}, "", false
}

// AddUsage records bytes against a tenant until the next Flush.
func (t *Tenants) AddUsage(tenantID string, residentialBytes, totalBytes int64) {
	t.mutex.Lock()
	current := t.pending[tenantID]
	t.pending[tenantID] = [2]int64{current[0] + residentialBytes, current[1] + totalBytes}
	t.mutex.Unlock()
}

// ResidentialExceeded reports whether a tenant has used up its residential
// quota, counting usage not yet flushed.
func (t *Tenants) ResidentialExceeded(id string) bool {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	for _, cached := range t.byName {
		if cached.record.ID == id {
			quota := cached.record.ResiQuotaBytes
			return quota > 0 && cached.record.ResiUsedBytes+t.pending[id][0] >= quota
		}
	}
	return false
}

// Flush writes pending usage and reloads the registry so quotas stay current.
// Booked usage stays pending until the reload carries it, so quota checks
// never see it twice or not at all.
func (t *Tenants) Flush(ctx context.Context) error {
	t.mutex.Lock()
	pending := make(map[string][2]int64, len(t.pending))
	for id, usage := range t.pending {
		pending[id] = usage
	}
	t.mutex.Unlock()
	var failed error
	booked := make(map[string][2]int64, len(pending))
	for id, usage := range pending {
		if err := t.store.AddEntryTenantUsage(ctx, id, usage[0], usage[1]); err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		booked[id] = usage
	}
	records, err := t.store.ListEntryTenants(ctx)
	t.mutex.Lock()
	for id, usage := range booked {
		left := t.pending[id]
		left = [2]int64{left[0] - usage[0], left[1] - usage[1]}
		if left == ([2]int64{}) {
			delete(t.pending, id)
		} else {
			t.pending[id] = left
		}
	}
	if err == nil {
		byID := make(map[string]store.EntryTenantRecord, len(records))
		for _, record := range records {
			byID[record.ID] = record
		}
		for name, cached := range t.byName {
			if record, ok := byID[cached.record.ID]; ok {
				cached.record.ResiUsedBytes, cached.record.TotalBytes = record.ResiUsedBytes, record.TotalBytes
				t.byName[name] = cached
			}
		}
	}
	t.mutex.Unlock()
	return errors.Join(failed, err)
}

// List returns every tenant without secrets.
func (t *Tenants) List(ctx context.Context) ([]Tenant, error) {
	records, err := t.store.ListEntryTenants(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Tenant, 0, len(records))
	for _, record := range records {
		items = append(items, tenantView(record, ""))
	}
	return items, nil
}

// Create stores a new tenant and returns its secret once.
func (t *Tenants) Create(ctx context.Context, request TenantRequest) (Tenant, error) {
	if !validToken(request.Name, maximumSlotLength) {
		return Tenant{}, fmt.Errorf("%w: name must be 1..%d letters, digits or underscore", ErrInvalidTenant, maximumSlotLength)
	}
	pools, err := normalizePools(request.Pools)
	if err != nil {
		return Tenant{}, err
	}
	id, err := randomToken(12)
	if err != nil {
		return Tenant{}, err
	}
	id = "tenant-" + id
	secret, sealed, err := t.newSecret(id)
	if err != nil {
		return Tenant{}, err
	}
	record := store.EntryTenantRecord{ID: id, Name: request.Name, SecretSealed: sealed, Pools: pools, Enabled: true}
	if request.ResiQuotaBytes != nil {
		record.ResiQuotaBytes = max(*request.ResiQuotaBytes, 0)
	}
	if request.Enabled != nil {
		record.Enabled = *request.Enabled
	}
	if request.Note != nil {
		record.Note = strings.TrimSpace(*request.Note)
	}
	created, err := t.store.CreateEntryTenant(ctx, record)
	if err != nil {
		return Tenant{}, err
	}
	return tenantView(created, secret), t.Reload(ctx)
}

// Update edits a tenant, rotating its secret when asked.
func (t *Tenants) Update(ctx context.Context, id string, request TenantRequest) (Tenant, error) {
	record, err := t.store.GetEntryTenant(ctx, id)
	if err != nil {
		return Tenant{}, err
	}
	if request.Version != 0 {
		record.Version = request.Version
	}
	if request.Pools != nil {
		if record.Pools, err = normalizePools(request.Pools); err != nil {
			return Tenant{}, err
		}
	}
	if request.ResiQuotaBytes != nil {
		record.ResiQuotaBytes = max(*request.ResiQuotaBytes, 0)
	}
	if request.Enabled != nil {
		record.Enabled = *request.Enabled
	}
	if request.Note != nil {
		record.Note = strings.TrimSpace(*request.Note)
	}
	record.SecretSealed = nil
	secret := ""
	if request.RotateSecret {
		if secret, record.SecretSealed, err = t.newSecret(id); err != nil {
			return Tenant{}, err
		}
	}
	updated, err := t.store.UpdateEntryTenant(ctx, record)
	if err != nil {
		return Tenant{}, err
	}
	err = t.Reload(ctx)
	if t.changed != nil {
		t.changed(id, false)
	}
	return tenantView(updated, secret), err
}

// Delete removes a tenant.
func (t *Tenants) Delete(ctx context.Context, id string) error {
	if err := t.store.DeleteEntryTenant(ctx, id); err != nil {
		return err
	}
	err := t.Reload(ctx)
	t.mutex.Lock()
	delete(t.pending, id)
	t.mutex.Unlock()
	if t.changed != nil {
		t.changed(id, true)
	}
	return err
}

// ResetUsage starts a new residential billing period for a tenant.
func (t *Tenants) ResetUsage(ctx context.Context, id string) error {
	if err := t.store.ResetEntryTenantUsage(ctx, id); err != nil {
		return err
	}
	return t.Reload(ctx)
}

func (t *Tenants) newSecret(id string) (string, []byte, error) {
	secret, err := randomToken(24)
	if err != nil {
		return "", nil, err
	}
	sealed, err := t.cipher.Seal([]byte(secret), tenantAssociatedData(id))
	return secret, sealed, err
}

func normalizePools(pools []string) ([]string, error) {
	if len(pools) == 0 {
		return []string{PoolSubscription, PoolSubscriptionResidential}, nil
	}
	allowed := append(Pools(), "group")
	seen := map[string]bool{}
	normalized := make([]string, 0, len(pools))
	for _, pool := range pools {
		pool = strings.TrimSpace(pool)
		if name, isGroup := strings.CutPrefix(pool, "group:"); isGroup {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("%w: empty group name in %q", ErrInvalidTenant, pool)
			}
		} else {
			pool = strings.ToLower(pool)
		}
		if !containsString(allowed, pool) && !strings.HasPrefix(pool, "group:") {
			return nil, fmt.Errorf("%w: pool %q, supported: %s", ErrInvalidTenant, pool, strings.Join(allowed, ", "))
		}
		if !seen[pool] {
			seen[pool] = true
			normalized = append(normalized, pool)
		}
	}
	return normalized, nil
}

func tenantView(record store.EntryTenantRecord, secret string) Tenant {
	return Tenant{
		ID: record.ID, Name: record.Name, Pools: record.Pools, ResiQuotaBytes: record.ResiQuotaBytes,
		ResiUsedBytes: record.ResiUsedBytes, TotalBytes: record.TotalBytes, Enabled: record.Enabled,
		Note: record.Note, Version: record.Version, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		Secret: secret,
	}
}

func tenantAssociatedData(id string) []byte {
	return []byte("entry-tenant:" + id)
}

// randomToken returns URL-safe random text without padding.
func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// groupAllowed reports whether a tenant may use one proxy group: "group"
// grants every group, "group:<name>" grants that one group.
func groupAllowed(pools []string, group string) bool {
	for _, pool := range pools {
		if pool == "group" || strings.EqualFold(pool, "group:"+group) {
			return true
		}
	}
	return false
}
