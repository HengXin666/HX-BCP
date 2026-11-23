package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/cron"
	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type SourceType string

const (
	SourceRemote SourceType = "remote"
	SourceInline SourceType = "inline"
	SourceFile   SourceType = "file"
)

// SupportedSourceTypes is the closed vocabulary of source types, in the order
// the capability catalog publishes them. It is the list the validator accepts,
// not a copy, so a new source type cannot ship undocumented.
//
// SourceInline deliberately covers two documents: a subscription document pasted
// in place of a URL, and a flat proxy list ("host:port" per line, or share URIs).
// Both are one document that is stored encrypted and parsed by the same reader;
// they are not two source types, so the enum does not grow for a list.
func SupportedSourceTypes() []string {
	return []string{string(SourceRemote), string(SourceInline), string(SourceFile)}
}

// SupportedSourceClasses republishes the store's closed source-class
// vocabulary. It is the list the capability catalog publishes and the
// validator accepts, read from the same function the probe policy uses, so the
// documented values cannot drift from the enforced ones.
func SupportedSourceClasses() []string {
	return store.SupportedSourceClasses()
}

// validateSourceClass rejects an unknown class. An empty value is allowed and
// returned as empty, which each caller interprets: create substitutes the
// documented default, update keeps the stored one.
func validateSourceClass(value string, required bool) (string, error) {
	if value == "" && !required {
		return "", nil
	}
	if value == "" {
		return "", fmt.Errorf("%w: source_class is required and must be one of %s",
			ErrInvalid, strings.Join(store.SupportedSourceClasses(), ", "))
	}
	parsed, err := store.ParseSourceClass(value)
	if err != nil {
		return "", fmt.Errorf("%w: source_class must be one of %s",
			ErrInvalid, strings.Join(store.SupportedSourceClasses(), ", "))
	}
	return string(parsed), nil
}

const (
	defaultRefreshInterval = 3600
	minimumRefreshInterval = 60
	maximumInlineBytes     = 4 << 20
)

var (
	ErrInvalid  = errors.New("invalid subscription")
	ErrNotFound = errors.New("subscription not found")
	ErrConflict = errors.New("subscription version conflict")
)

type SourceConfig struct {
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	UserAgent      string            `json:"user_agent,omitempty"`
	Inline         string            `json:"inline,omitempty"`
	FilePath       string            `json:"file_path,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	AllowPrivate   bool              `json:"allow_private,omitempty"`
}

type Subscription struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	SourceType SourceType `json:"source_type"`
	// SourceClass is the stored classification that selects this source's node
	// probe policy. It is echoed so a reader can tell why two sources with the
	// same source_type are treated differently after failures.
	SourceClass            string     `json:"source_class"`
	SourceConfigured       bool       `json:"source_configured"`
	Enabled                bool       `json:"enabled"`
	RefreshIntervalSeconds int        `json:"refresh_interval_seconds"`
	RefreshCron            string     `json:"refresh_cron,omitempty"`
	LastSuccessSnapshotID  string     `json:"last_success_snapshot_id,omitempty"`
	ConsecutiveFailures    int        `json:"consecutive_failures"`
	LastFailure            *Failure   `json:"last_failure,omitempty"`
	LastRefreshAttemptAt   *time.Time `json:"last_refresh_attempt_at,omitempty"`
	NextRefreshAt          *time.Time `json:"next_refresh_at,omitempty"`
	Version                int        `json:"version"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type CreateRequest struct {
	Name       string     `json:"name"`
	SourceType SourceType `json:"source_type"`
	// SourceClass selects the probe policy for this source's nodes. It is
	// required: the three classes have incompatible failure rules, and
	// guessing would silently apply the wrong one to a source an operator
	// cannot replace. See store.SupportedSourceClasses for the vocabulary.
	SourceClass            string       `json:"source_class"`
	SourceConfig           SourceConfig `json:"source_config"`
	Enabled                *bool        `json:"enabled,omitempty"`
	RefreshIntervalSeconds int          `json:"refresh_interval_seconds,omitempty"`
	RefreshCron            string       `json:"refresh_cron,omitempty"`
}

type UpdateRequest struct {
	Version    int        `json:"version"`
	Name       string     `json:"name"`
	SourceType SourceType `json:"source_type"`
	// SourceClass may be changed on its own, without replacing the source
	// config: reclassifying an existing source (e.g. learning that a
	// "subscription" is really the only exit you have) is exactly the repair
	// path an operator needs. It must still be a member of the closed
	// vocabulary.
	SourceClass            string        `json:"source_class"`
	SourceConfig           *SourceConfig `json:"source_config,omitempty"`
	Enabled                bool          `json:"enabled"`
	RefreshIntervalSeconds int           `json:"refresh_interval_seconds"`
	RefreshCron            string        `json:"refresh_cron,omitempty"`
}

type Repository interface {
	CreateSubscription(context.Context, store.SubscriptionRecord) (store.SubscriptionRecord, error)
	GetSubscription(context.Context, string) (store.SubscriptionRecord, error)
	ListSubscriptions(context.Context, int, int) ([]store.SubscriptionRecord, error)
	UpdateSubscription(context.Context, store.SubscriptionRecord, int) (store.SubscriptionRecord, error)
	DeleteSubscription(context.Context, string, int) error
	GetSubscriptionSnapshot(context.Context, string) (store.SubscriptionSnapshotRecord, error)
	GetSubscriptionSnapshotByHash(context.Context, string, string) (store.SubscriptionSnapshotRecord, error)
	CommitSubscriptionSnapshot(context.Context, store.SubscriptionSnapshotRecord) error
	ActivateSubscriptionSnapshot(context.Context, string, string, time.Time, time.Time, string) error
	MarkSubscriptionRefreshFailed(context.Context, string, time.Time, time.Time, string) error
}

type ParsedNodeRepository interface {
	CommitParsedSubscriptionSnapshot(context.Context, store.SubscriptionSnapshotRecord, []store.NodeImportRecord) error
	ActivateParsedSubscriptionSnapshot(context.Context, store.SubscriptionSnapshotRecord, []store.NodeImportRecord, string) error
}

type Parser func([]byte) (nodeparse.Result, error)

type Reconciler interface {
	Apply(context.Context) error
}

type Cipher interface {
	Seal([]byte, []byte) ([]byte, error)
	Open([]byte, []byte) ([]byte, error)
}

type ServiceOption func(*Service) error

func WithRefresh(loader SourceLoader, snapshotDirectory string) ServiceOption {
	return func(service *Service) error {
		if loader == nil {
			return errors.New("subscription source loader is required")
		}
		if strings.TrimSpace(snapshotDirectory) == "" {
			return errors.New("subscription snapshot directory is required")
		}
		service.loader = loader
		service.snapshotDirectory = snapshotDirectory
		return nil
	}
}

func WithParser(parser Parser) ServiceOption {
	return func(service *Service) error {
		if parser == nil {
			return errors.New("subscription node parser is required")
		}
		if _, ok := service.repository.(ParsedNodeRepository); !ok {
			return errors.New("subscription repository does not support parsed nodes")
		}
		service.parser = parser
		return nil
	}
}

func WithReconciler(reconciler Reconciler) ServiceOption {
	return func(service *Service) error {
		if reconciler == nil {
			return errors.New("subscription reconciler is required")
		}
		service.reconciler = reconciler
		return nil
	}
}

type Service struct {
	repository        Repository
	cipher            Cipher
	loader            SourceLoader
	parser            Parser
	reconciler        Reconciler
	snapshotDirectory string
	now               func() time.Time
	refreshLocks      keyedLocker
}

func NewService(repository Repository, cipher Cipher, options ...ServiceOption) (*Service, error) {
	if repository == nil {
		return nil, errors.New("subscription repository is required")
	}
	if cipher == nil {
		return nil, errors.New("subscription cipher is required")
	}
	service := &Service{repository: repository, cipher: cipher, now: time.Now}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Subscription, error) {
	request = normalizeCreateRequest(request)
	if err := validateRequest(request.Name, request.SourceType, request.SourceConfig, request.RefreshIntervalSeconds, request.RefreshCron); err != nil {
		return Subscription{}, err
	}
	sourceClass, err := validateSourceClass(request.SourceClass, false)
	if err != nil {
		return Subscription{}, err
	}
	// An omitted class on create takes the documented default rather than
	// failing: the management UI creates subscriptions without sending this
	// field, and a hard requirement would break every existing caller at once.
	// The default is the promotion-safe class (see DefaultSourceClassPolicy) —
	// never the cheap one — because misclassifying an irreplaceable exit is the
	// only irreversible outcome here. The catalog advertises this default, and
	// quickstart.create (which has no such legacy caller) requires the field
	// explicitly instead.
	sourceClass = store.NormalizeSourceClass(sourceClass)
	id, err := newID()
	if err != nil {
		return Subscription{}, err
	}
	encrypted, err := s.encryptSourceConfig(id, request.SourceConfig)
	if err != nil {
		return Subscription{}, err
	}
	now := s.now().UTC()
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	record, err := s.repository.CreateSubscription(ctx, store.SubscriptionRecord{
		ID:                     id,
		Name:                   request.Name,
		SourceType:             string(request.SourceType),
		SourceClass:            sourceClass,
		SourceConfigEncrypted:  encrypted,
		Enabled:                enabled,
		RefreshIntervalSeconds: request.RefreshIntervalSeconds,
		RefreshCron:            strings.TrimSpace(request.RefreshCron),
		NextRefreshAt:          &now,
		Version:                1,
		CreatedAt:              now,
		UpdatedAt:              now,
	})
	if err != nil {
		return Subscription{}, mapRepositoryError(err)
	}
	return fromRecord(record), nil
}

func (s *Service) Get(ctx context.Context, id string) (Subscription, error) {
	record, err := s.repository.GetSubscription(ctx, id)
	if err != nil {
		return Subscription{}, mapRepositoryError(err)
	}
	return fromRecord(record), nil
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]Subscription, error) {
	records, err := s.repository.ListSubscriptions(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]Subscription, 0, len(records))
	for _, record := range records {
		items = append(items, fromRecord(record))
	}
	return items, nil
}

func (s *Service) Update(ctx context.Context, id string, request UpdateRequest) (Subscription, error) {
	request.Name = strings.TrimSpace(request.Name)
	if request.Version < 1 {
		return Subscription{}, fmt.Errorf("%w: version must be at least 1", ErrInvalid)
	}
	if request.RefreshIntervalSeconds == 0 {
		request.RefreshIntervalSeconds = defaultRefreshInterval
	}
	if err := validateMetadata(request.Name, request.RefreshIntervalSeconds, request.RefreshCron); err != nil {
		return Subscription{}, err
	}
	sourceClass, err := validateSourceClass(request.SourceClass, false)
	if err != nil {
		return Subscription{}, err
	}
	existing, err := s.repository.GetSubscription(ctx, id)
	if err != nil {
		return Subscription{}, mapRepositoryError(err)
	}
	// An omitted source_class keeps the stored one, so a caller that only
	// wants to rename a source does not have to restate its policy — and
	// cannot accidentally downgrade a residential source to the default.
	if sourceClass == "" {
		sourceClass = store.NormalizeSourceClass(existing.SourceClass)
	}
	sourceType := SourceType(existing.SourceType)
	encrypted := existing.SourceConfigEncrypted
	if request.SourceConfig != nil {
		sourceType = request.SourceType
		if err := validateRequest(request.Name, sourceType, *request.SourceConfig, request.RefreshIntervalSeconds, request.RefreshCron); err != nil {
			return Subscription{}, err
		}
		encrypted, err = s.encryptSourceConfig(id, *request.SourceConfig)
		if err != nil {
			return Subscription{}, err
		}
	} else if request.SourceType != "" && request.SourceType != sourceType {
		return Subscription{}, fmt.Errorf("%w: source_type can only change when source_config is replaced", ErrInvalid)
	}
	now := s.now().UTC()
	record, err := s.repository.UpdateSubscription(ctx, store.SubscriptionRecord{
		ID:                     id,
		Name:                   request.Name,
		SourceType:             string(sourceType),
		SourceClass:            sourceClass,
		SourceConfigEncrypted:  encrypted,
		Enabled:                request.Enabled,
		RefreshIntervalSeconds: request.RefreshIntervalSeconds,
		RefreshCron:            strings.TrimSpace(request.RefreshCron),
		NextRefreshAt:          &now,
		UpdatedAt:              now,
	}, request.Version)
	if err != nil {
		return Subscription{}, mapRepositoryError(err)
	}
	return fromRecord(record), nil
}

func validateMetadata(name string, refreshInterval int, refreshCron string) error {
	if name == "" || len(name) > 128 {
		return fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	if refreshInterval < minimumRefreshInterval {
		return fmt.Errorf("%w: refresh interval must be at least %d seconds", ErrInvalid, minimumRefreshInterval)
	}
	if strings.TrimSpace(refreshCron) != "" {
		if _, err := cron.Parse(refreshCron); err != nil {
			return fmt.Errorf("%w: invalid refresh cron: %v", ErrInvalid, err)
		}
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id string, expectedVersion int) error {
	if expectedVersion < 1 {
		return fmt.Errorf("%w: version must be at least 1", ErrInvalid)
	}
	return mapRepositoryError(s.repository.DeleteSubscription(ctx, id, expectedVersion))
}

func (s *Service) SourceConfig(ctx context.Context, id string) (SourceConfig, error) {
	record, err := s.repository.GetSubscription(ctx, id)
	if err != nil {
		return SourceConfig{}, mapRepositoryError(err)
	}
	plaintext, err := s.cipher.Open(record.SourceConfigEncrypted, associatedData(id))
	if err != nil {
		return SourceConfig{}, fmt.Errorf("decrypt subscription source: %w", err)
	}
	var config SourceConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return SourceConfig{}, fmt.Errorf("decode subscription source: %w", err)
	}
	return config, nil
}

func (s *Service) encryptSourceConfig(id string, config SourceConfig) ([]byte, error) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode subscription source: %w", err)
	}
	encrypted, err := s.cipher.Seal(encoded, associatedData(id))
	if err != nil {
		return nil, fmt.Errorf("encrypt subscription source: %w", err)
	}
	return encrypted, nil
}

func normalizeCreateRequest(request CreateRequest) CreateRequest {
	request.Name = strings.TrimSpace(request.Name)
	if request.RefreshIntervalSeconds == 0 {
		request.RefreshIntervalSeconds = defaultRefreshInterval
	}
	return request
}

func validateRequest(name string, sourceType SourceType, config SourceConfig, refreshInterval int, refreshCron string) error {
	if name == "" || len(name) > 128 {
		return fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	if refreshInterval < minimumRefreshInterval {
		return fmt.Errorf("%w: refresh interval must be at least %d seconds", ErrInvalid, minimumRefreshInterval)
	}
	if refreshCron = strings.TrimSpace(refreshCron); refreshCron != "" {
		if _, err := cron.Parse(refreshCron); err != nil {
			return fmt.Errorf("%w: invalid refresh cron: %v", ErrInvalid, err)
		}
	}
	if config.TimeoutSeconds < 0 || config.TimeoutSeconds > 300 {
		return fmt.Errorf("%w: timeout must be between 0 and 300 seconds", ErrInvalid)
	}
	if len(config.Headers) > 32 {
		return fmt.Errorf("%w: at most 32 request headers are allowed", ErrInvalid)
	}
	for key, value := range config.Headers {
		if textproto.CanonicalMIMEHeaderKey(key) == "" || len(key) > 128 || len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%w: invalid request header", ErrInvalid)
		}
	}

	switch sourceType {
	case SourceRemote:
		parsed, err := url.Parse(config.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%w: remote source requires an HTTP or HTTPS URL", ErrInvalid)
		}
		if config.Inline != "" || config.FilePath != "" {
			return fmt.Errorf("%w: remote source cannot include inline or file content", ErrInvalid)
		}
	case SourceInline:
		if config.Inline == "" || len(config.Inline) > maximumInlineBytes {
			return fmt.Errorf("%w: inline source must contain 1 byte to 4 MiB", ErrInvalid)
		}
		if config.URL != "" || config.FilePath != "" {
			return fmt.Errorf("%w: inline source cannot include URL or file path", ErrInvalid)
		}
	case SourceFile:
		if config.FilePath == "" || !filepath.IsAbs(config.FilePath) || strings.ContainsRune(config.FilePath, '\x00') {
			return fmt.Errorf("%w: file source requires an absolute path", ErrInvalid)
		}
		if config.URL != "" || config.Inline != "" {
			return fmt.Errorf("%w: file source cannot include URL or inline content", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported source type %q", ErrInvalid, sourceType)
	}
	return nil
}

func fromRecord(record store.SubscriptionRecord) Subscription {
	var lastFailure *Failure
	if record.LastFailureJSON != "" {
		var failure Failure
		if json.Unmarshal([]byte(record.LastFailureJSON), &failure) == nil && failure.Code != "" {
			lastFailure = &failure
		}
	}
	return Subscription{
		ID:                     record.ID,
		Name:                   record.Name,
		SourceType:             SourceType(record.SourceType),
		SourceClass:            store.NormalizeSourceClass(record.SourceClass),
		SourceConfigured:       len(record.SourceConfigEncrypted) > 0,
		Enabled:                record.Enabled,
		RefreshIntervalSeconds: record.RefreshIntervalSeconds,
		RefreshCron:            record.RefreshCron,
		LastSuccessSnapshotID:  record.LastSuccessSnapshotID,
		ConsecutiveFailures:    record.ConsecutiveFailures,
		LastFailure:            lastFailure,
		LastRefreshAttemptAt:   record.LastRefreshAttemptAt,
		NextRefreshAt:          record.NextRefreshAt,
		Version:                record.Version,
		CreatedAt:              record.CreatedAt,
		UpdatedAt:              record.UpdatedAt,
	}
}

func mapRepositoryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func associatedData(id string) []byte {
	return []byte("subscription:" + id)
}

func newID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate subscription id: %w", err)
	}
	return "sub-" + hex.EncodeToString(random[:]), nil
}
