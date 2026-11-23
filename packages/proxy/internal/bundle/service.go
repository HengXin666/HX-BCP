package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/artifact"
)

type Scope uint8

const (
	ScopeBackup Scope = 1 << iota
	ScopeExport
)

var ErrSecretBundleDisabled = errors.New("secret bundles require an encryption provider")

type Source struct {
	Name      string
	Path      string
	Scope     Scope
	Required  bool
	Sensitive bool
}

type CreateOptions struct {
	Description    string `json:"description"`
	IncludeSecrets bool   `json:"include_secrets"`
}

type Manifest struct {
	SchemaVersion   int             `json:"schema_version"`
	Application     string          `json:"application"`
	ApplicationVer  string          `json:"application_version"`
	Kind            artifact.Kind   `json:"kind"`
	CreatedAt       time.Time       `json:"created_at"`
	IncludesSecrets bool            `json:"includes_secrets"`
	Entries         []ManifestEntry `json:"entries"`
	Skipped         []SkippedEntry  `json:"skipped,omitempty"`
}

type ManifestEntry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    int64     `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	SHA256  string    `json:"sha256"`
}

type SkippedEntry struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type VerifyResult struct {
	ArtifactID      string        `json:"artifact_id"`
	Kind            artifact.Kind `json:"kind"`
	Valid           bool          `json:"valid"`
	FilesChecked    int           `json:"files_checked"`
	ArtifactSHA256  string        `json:"artifact_sha256"`
	ManifestVersion int           `json:"manifest_version"`
}

type Service struct {
	catalog     *artifact.Catalog
	sources     []Source
	appVersion  string
	now         func() time.Time
	maxFileSize int64
	maxFiles    int
}

func NewService(catalog *artifact.Catalog, sources []Source, appVersion string) (*Service, error) {
	if catalog == nil {
		return nil, errors.New("artifact catalog is required")
	}
	validated := append([]Source(nil), sources...)
	seen := make(map[string]struct{}, len(validated))
	for i := range validated {
		validated[i].Name = strings.TrimSpace(validated[i].Name)
		validated[i].Path = filepath.Clean(validated[i].Path)
		if err := validateSource(validated[i]); err != nil {
			return nil, fmt.Errorf("source %d: %w", i, err)
		}
		if _, exists := seen[validated[i].Name]; exists {
			return nil, fmt.Errorf("duplicate source name %q", validated[i].Name)
		}
		seen[validated[i].Name] = struct{}{}
	}
	sort.Slice(validated, func(i, j int) bool { return validated[i].Name < validated[j].Name })
	return &Service{
		catalog:     catalog,
		sources:     validated,
		appVersion:  appVersion,
		now:         time.Now,
		maxFileSize: 4 << 30,
		maxFiles:    100_000,
	}, nil
}

func (s *Service) Create(ctx context.Context, kind artifact.Kind, options CreateOptions) (artifact.Record, error) {
	if kind != artifact.KindBackup && kind != artifact.KindExport {
		return artifact.Record{}, fmt.Errorf("unsupported bundle kind %q", kind)
	}
	if options.IncludeSecrets {
		return artifact.Record{}, ErrSecretBundleDisabled
	}
	createdAt := s.now().UTC()
	record := artifact.Record{
		Kind:            kind,
		CreatedAt:       createdAt,
		ContentType:     "application/gzip",
		Description:     strings.TrimSpace(options.Description),
		IncludesSecrets: false,
	}

	return s.catalog.Write(ctx, record, ".tar.gz", func(destination io.Writer) error {
		gzipWriter, err := gzip.NewWriterLevel(destination, gzip.BestSpeed)
		if err != nil {
			return fmt.Errorf("create gzip writer: %w", err)
		}
		gzipWriter.Header.ModTime = createdAt
		gzipWriter.Header.Name = string(kind) + ".tar"
		tarWriter := tar.NewWriter(gzipWriter)

		manifest := Manifest{
			SchemaVersion:   1,
			Application:     "HX-Pxy",
			ApplicationVer:  s.appVersion,
			Kind:            kind,
			CreatedAt:       createdAt,
			IncludesSecrets: false,
		}
		if err := s.writeSources(ctx, tarWriter, kind, &manifest); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return err
		}
		if err := writeJSONEntry(tarWriter, "manifest.json", createdAt, manifest); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return err
		}
		if err := tarWriter.Close(); err != nil {
			_ = gzipWriter.Close()
			return fmt.Errorf("close tar archive: %w", err)
		}
		if err := gzipWriter.Close(); err != nil {
			return fmt.Errorf("close gzip archive: %w", err)
		}
		return nil
	})
}

func (s *Service) List(kind artifact.Kind) ([]artifact.Record, error) {
	return s.catalog.List(kind)
}

func (s *Service) Open(id string) (artifact.Record, *os.File, error) {
	return s.catalog.Open(id)
}

func (s *Service) Delete(id string) error {
	return s.catalog.Delete(id)
}

func (s *Service) writeSources(ctx context.Context, writer *tar.Writer, kind artifact.Kind, manifest *Manifest) error {
	for _, source := range s.sources {
		if !source.appliesTo(kind) {
			continue
		}
		if source.Sensitive {
			manifest.Skipped = append(manifest.Skipped, SkippedEntry{
				Source: source.Name,
				Reason: "sensitive source omitted because archive encryption is not configured",
			})
			continue
		}
		info, err := os.Lstat(source.Path)
		if errors.Is(err, os.ErrNotExist) && !source.Required {
			manifest.Skipped = append(manifest.Skipped, SkippedEntry{Source: source.Name, Reason: "source does not exist"})
			continue
		}
		if err != nil {
			return fmt.Errorf("stat source %q: %w", source.Name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source %q is a symbolic link", source.Name)
		}
		if info.IsDir() {
			if err := s.writeDirectory(ctx, writer, source, manifest); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source %q is not a regular file or directory", source.Name)
		}
		archivePath := path.Join("payload", source.Name, filepath.Base(source.Path))
		entry, err := s.writeFile(ctx, writer, source.Path, archivePath, info)
		if err != nil {
			return fmt.Errorf("archive source %q: %w", source.Name, err)
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return nil
}

func (s *Service) writeDirectory(ctx context.Context, writer *tar.Writer, source Source, manifest *Manifest) error {
	return filepath.WalkDir(source.Path, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk source %q: %w", source.Name, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %q: %w", currentPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source %q contains symbolic link %q", source.Name, currentPath)
		}
		relative, err := filepath.Rel(source.Path, currentPath)
		if err != nil {
			return fmt.Errorf("resolve relative path: %w", err)
		}
		if relative == "." {
			return nil
		}
		archivePath := path.Join("payload", source.Name, filepath.ToSlash(relative))
		if info.IsDir() {
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("create directory header: %w", err)
			}
			header.Name = archivePath + "/"
			header.Uid, header.Gid = 0, 0
			header.Uname, header.Gname = "", ""
			if err := writer.WriteHeader(header); err != nil {
				return fmt.Errorf("write directory header: %w", err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source %q contains unsupported file %q", source.Name, currentPath)
		}
		if len(manifest.Entries) >= s.maxFiles {
			return fmt.Errorf("archive exceeds %d files", s.maxFiles)
		}
		manifestEntry, err := s.writeFile(ctx, writer, currentPath, archivePath, info)
		if err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, manifestEntry)
		return nil
	})
}

func (s *Service) writeFile(ctx context.Context, writer *tar.Writer, filePath, archivePath string, info os.FileInfo) (ManifestEntry, error) {
	if info.Size() > s.maxFileSize {
		return ManifestEntry{}, fmt.Errorf("file %q exceeds maximum size", filePath)
	}
	if err := validateArchivePath(archivePath); err != nil {
		return ManifestEntry{}, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("create file header: %w", err)
	}
	header.Name = archivePath
	header.Uid, header.Gid = 0, 0
	header.Uname, header.Gname = "", ""
	if err := writer.WriteHeader(header); err != nil {
		return ManifestEntry{}, fmt.Errorf("write file header: %w", err)
	}

	digest := sha256.New()
	written, err := copyWithContext(ctx, io.MultiWriter(writer, digest), file, make([]byte, 128<<10))
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("write file contents: %w", err)
	}
	if written != info.Size() {
		return ManifestEntry{}, fmt.Errorf("file changed while archiving: expected %d bytes, wrote %d", info.Size(), written)
	}
	return ManifestEntry{
		Path:    archivePath,
		Size:    written,
		Mode:    int64(info.Mode().Perm()),
		ModTime: info.ModTime().UTC(),
		SHA256:  hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func (source Source) appliesTo(kind artifact.Kind) bool {
	switch kind {
	case artifact.KindBackup:
		return source.Scope&ScopeBackup != 0
	case artifact.KindExport:
		return source.Scope&ScopeExport != 0
	default:
		return false
	}
}

func validateSource(source Source) error {
	if source.Name == "" {
		return errors.New("name is required")
	}
	if source.Path == "" || source.Path == "." {
		return errors.New("path is required")
	}
	if source.Scope == 0 || source.Scope&^(ScopeBackup|ScopeExport) != 0 {
		return errors.New("scope is invalid")
	}
	for _, char := range source.Name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return errors.New("name may contain only letters, digits, hyphens, and underscores")
	}
	return nil
}

func validateArchivePath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != strings.TrimSuffix(name, "/") {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	return nil
}

func writeJSONEntry(writer *tar.Writer, name string, modTime time.Time, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	encoded = append(encoded, '\n')
	header := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    int64(len(encoded)),
		ModTime: modTime,
	}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write %s header: %w", name, err)
	}
	if _, err := writer.Write(encoded); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
