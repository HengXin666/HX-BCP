package artifact

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindBackup Kind = "backup"
	KindExport Kind = "export"
)

const metadataSuffix = ".meta.json"

var ErrNotFound = errors.New("artifact not found")

type Record struct {
	SchemaVersion   int       `json:"schema_version"`
	ID              string    `json:"id"`
	Kind            Kind      `json:"kind"`
	CreatedAt       time.Time `json:"created_at"`
	Filename        string    `json:"filename"`
	ContentType     string    `json:"content_type"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	IncludesSecrets bool      `json:"includes_secrets"`
	Description     string    `json:"description,omitempty"`
}

type Catalog struct {
	dir  string
	now  func() time.Time
	rand io.Reader
	mu   sync.Mutex
}

func NewCatalog(dir string) (*Catalog, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("artifact directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure artifact directory: %w", err)
	}
	return &Catalog{dir: dir, now: time.Now, rand: rand.Reader}, nil
}

func (c *Catalog) Write(
	ctx context.Context,
	record Record,
	extension string,
	write func(io.Writer) error,
) (Record, error) {
	if write == nil {
		return Record{}, errors.New("artifact writer is required")
	}
	if err := validateKind(record.Kind); err != nil {
		return Record{}, err
	}
	if err := validateExtension(extension); err != nil {
		return Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if record.CreatedAt.IsZero() {
		record.CreatedAt = c.now().UTC()
	} else {
		record.CreatedAt = record.CreatedAt.UTC()
	}
	if record.ID == "" {
		id, err := c.newID(record.CreatedAt)
		if err != nil {
			return Record{}, err
		}
		record.ID = id
	}
	if err := validateID(record.ID); err != nil {
		return Record{}, err
	}

	record.SchemaVersion = 1
	record.Filename = string(record.Kind) + "-" + record.ID + extension
	finalPath := filepath.Join(c.dir, record.Filename)
	temporary, err := os.CreateTemp(c.dir, ".artifact-*.tmp")
	if err != nil {
		return Record{}, fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return Record{}, fmt.Errorf("secure temporary artifact: %w", err)
	}

	digest := sha256.New()
	counter := &countingWriter{writer: io.MultiWriter(temporary, digest)}
	if err := write(counter); err != nil {
		return Record{}, fmt.Errorf("write artifact: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if err := temporary.Sync(); err != nil {
		return Record{}, fmt.Errorf("sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Record{}, fmt.Errorf("close artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return Record{}, fmt.Errorf("publish artifact: %w", err)
	}

	record.Size = counter.count
	record.SHA256 = hex.EncodeToString(digest.Sum(nil))
	if err := c.writeMetadata(record); err != nil {
		_ = os.Remove(finalPath)
		return Record{}, err
	}
	if err := syncDirectory(c.dir); err != nil {
		_ = os.Remove(finalPath)
		_ = os.Remove(c.metadataPath(record.Filename))
		return Record{}, err
	}

	committed = true
	return record, nil
}

func (c *Catalog) List(kind Kind) ([]Record, error) {
	if kind != "" {
		if err := validateKind(kind); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), metadataSuffix) {
			continue
		}
		record, err := c.readMetadata(filepath.Join(c.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if kind == "" || record.Kind == kind {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, nil
}

func (c *Catalog) Get(id string) (Record, error) {
	if err := validateID(id); err != nil {
		return Record{}, ErrNotFound
	}
	records, err := c.List("")
	if err != nil {
		return Record{}, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, nil
		}
	}
	return Record{}, ErrNotFound
}

func (c *Catalog) Open(id string) (Record, *os.File, error) {
	record, err := c.Get(id)
	if err != nil {
		return Record{}, nil, err
	}
	file, err := os.Open(filepath.Join(c.dir, record.Filename))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, nil, ErrNotFound
	}
	if err != nil {
		return Record{}, nil, fmt.Errorf("open artifact: %w", err)
	}
	return record, file, nil
}

func (c *Catalog) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.getUnlocked(id)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(c.dir, record.Filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete artifact: %w", err)
	}
	if err := os.Remove(c.metadataPath(record.Filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete artifact metadata: %w", err)
	}
	return syncDirectory(c.dir)
}

func (c *Catalog) getUnlocked(id string) (Record, error) {
	if err := validateID(id); err != nil {
		return Record{}, ErrNotFound
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return Record{}, fmt.Errorf("list artifacts: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), metadataSuffix) {
			continue
		}
		record, err := c.readMetadata(filepath.Join(c.dir, entry.Name()))
		if err != nil {
			return Record{}, err
		}
		if record.ID == id {
			return record, nil
		}
	}
	return Record{}, ErrNotFound
}

func (c *Catalog) writeMetadata(record Record) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode artifact metadata: %w", err)
	}
	path := c.metadataPath(record.Filename)
	temporary, err := os.CreateTemp(c.dir, ".metadata-*.tmp")
	if err != nil {
		return fmt.Errorf("create metadata file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure metadata file: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write artifact metadata: %w", err)
	}
	if _, err := temporary.Write([]byte("\n")); err != nil {
		return fmt.Errorf("finalize artifact metadata: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync artifact metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close artifact metadata: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish artifact metadata: %w", err)
	}
	return nil
}

func (c *Catalog) readMetadata(path string) (Record, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Record{}, fmt.Errorf("stat artifact metadata: %w", err)
	}
	if info.Size() > 64<<10 {
		return Record{}, fmt.Errorf("artifact metadata %q exceeds 64 KiB", filepath.Base(path))
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("read artifact metadata: %w", err)
	}
	var record Record
	if err := json.Unmarshal(encoded, &record); err != nil {
		return Record{}, fmt.Errorf("decode artifact metadata %q: %w", filepath.Base(path), err)
	}
	if err := validateRecord(record); err != nil {
		return Record{}, fmt.Errorf("invalid artifact metadata %q: %w", filepath.Base(path), err)
	}
	return record, nil
}

func (c *Catalog) metadataPath(filename string) string {
	return filepath.Join(c.dir, filename+metadataSuffix)
}

func (c *Catalog) newID(now time.Time) (string, error) {
	var suffix [6]byte
	if _, err := io.ReadFull(c.rand, suffix[:]); err != nil {
		return "", fmt.Errorf("generate artifact id: %w", err)
	}
	return now.UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

func validateRecord(record Record) error {
	if record.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema version %d", record.SchemaVersion)
	}
	if err := validateID(record.ID); err != nil {
		return err
	}
	if err := validateKind(record.Kind); err != nil {
		return err
	}
	if record.CreatedAt.IsZero() {
		return errors.New("created_at is required")
	}
	if filepath.Base(record.Filename) != record.Filename || record.Filename == "." {
		return errors.New("invalid artifact filename")
	}
	if record.Size < 0 {
		return errors.New("invalid artifact size")
	}
	if len(record.SHA256) != sha256.Size*2 {
		return errors.New("invalid sha256")
	}
	return nil
}

func validateKind(kind Kind) error {
	switch kind {
	case KindBackup, KindExport:
		return nil
	default:
		return fmt.Errorf("unsupported artifact kind %q", kind)
	}
}

func validateID(id string) error {
	if len(id) < 8 || len(id) > 96 {
		return errors.New("invalid artifact id length")
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '.' {
			continue
		}
		return errors.New("invalid artifact id")
	}
	return nil
}

func validateExtension(extension string) error {
	if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, `/\\`) {
		return fmt.Errorf("invalid artifact extension %q", extension)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.count += int64(n)
	return n, err
}
