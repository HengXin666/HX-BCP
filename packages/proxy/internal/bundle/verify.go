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
	"strings"
)

func (s *Service) Verify(ctx context.Context, id string) (VerifyResult, error) {
	record, file, err := s.catalog.Open(id)
	if err != nil {
		return VerifyResult{}, err
	}
	defer file.Close()

	artifactDigest := sha256.New()
	if _, err := copyWithContext(ctx, artifactDigest, file, nil); err != nil {
		return VerifyResult{}, fmt.Errorf("hash artifact: %w", err)
	}
	artifactHash := hex.EncodeToString(artifactDigest.Sum(nil))
	if artifactHash != record.SHA256 {
		return VerifyResult{}, errors.New("artifact checksum does not match catalog metadata")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return VerifyResult{}, fmt.Errorf("rewind artifact: %w", err)
	}

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)

	actual := make(map[string]ManifestEntry)
	var manifest Manifest
	manifestSeen := false
	for count := 0; ; count++ {
		if count > s.maxFiles+1 {
			return VerifyResult{}, errors.New("archive contains too many entries")
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return VerifyResult{}, fmt.Errorf("read archive entry: %w", err)
		}
		if err := validateArchivePath(header.Name); err != nil {
			return VerifyResult{}, err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if header.Size < 0 || header.Size > s.maxFileSize {
			return VerifyResult{}, fmt.Errorf("archive entry %q has invalid size", header.Name)
		}
		if header.Name == "manifest.json" {
			if manifestSeen {
				return VerifyResult{}, errors.New("archive contains duplicate manifest")
			}
			if header.Size > 4<<20 {
				return VerifyResult{}, errors.New("archive manifest exceeds 4 MiB")
			}
			limited := io.LimitReader(tarReader, header.Size)
			if err := json.NewDecoder(limited).Decode(&manifest); err != nil {
				return VerifyResult{}, fmt.Errorf("decode archive manifest: %w", err)
			}
			manifestSeen = true
			continue
		}
		if !strings.HasPrefix(header.Name, "payload/") {
			return VerifyResult{}, fmt.Errorf("unexpected archive entry %q", header.Name)
		}
		if _, duplicate := actual[header.Name]; duplicate {
			return VerifyResult{}, fmt.Errorf("duplicate archive entry %q", header.Name)
		}
		digest := sha256.New()
		written, err := copyWithContext(ctx, digest, io.LimitReader(tarReader, header.Size), nil)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("hash archive entry %q: %w", header.Name, err)
		}
		if written != header.Size {
			return VerifyResult{}, fmt.Errorf("archive entry %q is truncated", header.Name)
		}
		actual[header.Name] = ManifestEntry{
			Path:   header.Name,
			Size:   header.Size,
			SHA256: hex.EncodeToString(digest.Sum(nil)),
		}
	}

	if !manifestSeen {
		return VerifyResult{}, errors.New("archive manifest is missing")
	}
	if manifest.SchemaVersion != 1 {
		return VerifyResult{}, fmt.Errorf("unsupported manifest version %d", manifest.SchemaVersion)
	}
	if manifest.Application != "HX-Pxy" || manifest.Kind != record.Kind {
		return VerifyResult{}, errors.New("archive manifest identity does not match catalog metadata")
	}
	if manifest.IncludesSecrets != record.IncludesSecrets {
		return VerifyResult{}, errors.New("archive secret flag does not match catalog metadata")
	}
	if len(manifest.Entries) != len(actual) {
		return VerifyResult{}, errors.New("archive file count does not match manifest")
	}
	for _, expected := range manifest.Entries {
		observed, exists := actual[expected.Path]
		if !exists {
			return VerifyResult{}, fmt.Errorf("manifest entry %q is missing", expected.Path)
		}
		if observed.Size != expected.Size || observed.SHA256 != expected.SHA256 {
			return VerifyResult{}, fmt.Errorf("manifest checksum mismatch for %q", expected.Path)
		}
	}
	return VerifyResult{
		ArtifactID:      record.ID,
		Kind:            record.Kind,
		Valid:           true,
		FilesChecked:    len(actual),
		ArtifactSHA256:  artifactHash,
		ManifestVersion: manifest.SchemaVersion,
	}, nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader, buffer []byte) (int64, error) {
	if buffer == nil {
		buffer = make([]byte, 128<<10)
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
