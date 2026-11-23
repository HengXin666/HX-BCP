package transfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/HengXin666/hx-pxy/internal/artifact"
	"github.com/HengXin666/hx-pxy/internal/bundle"
)

type DatabaseSnapshotter interface {
	BackupTo(context.Context, string) error
}

type Service struct {
	catalog            *artifact.Catalog
	base               *bundle.Service
	baseSources        []bundle.Source
	database           DatabaseSnapshotter
	stagingRoot        string
	applicationVersion string
}

func NewService(
	catalog *artifact.Catalog,
	baseSources []bundle.Source,
	database DatabaseSnapshotter,
	stagingRoot string,
	applicationVersion string,
) (*Service, error) {
	if catalog == nil {
		return nil, errors.New("artifact catalog is required")
	}
	if database == nil {
		return nil, errors.New("database snapshotter is required")
	}
	if stagingRoot == "" {
		return nil, errors.New("staging root is required")
	}
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create transfer staging root: %w", err)
	}
	if err := os.Chmod(stagingRoot, 0o700); err != nil {
		return nil, fmt.Errorf("secure transfer staging root: %w", err)
	}
	base, err := bundle.NewService(catalog, baseSources, applicationVersion)
	if err != nil {
		return nil, err
	}
	return &Service{
		catalog:            catalog,
		base:               base,
		baseSources:        append([]bundle.Source(nil), baseSources...),
		database:           database,
		stagingRoot:        stagingRoot,
		applicationVersion: applicationVersion,
	}, nil
}

func (s *Service) Create(
	ctx context.Context,
	kind artifact.Kind,
	options bundle.CreateOptions,
) (artifact.Record, error) {
	if kind == artifact.KindExport {
		return s.base.Create(ctx, kind, options)
	}
	if kind != artifact.KindBackup {
		return artifact.Record{}, fmt.Errorf("unsupported transfer kind %q", kind)
	}

	stagingDirectory, err := os.MkdirTemp(s.stagingRoot, ".backup-staging-")
	if err != nil {
		return artifact.Record{}, fmt.Errorf("create backup staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDirectory)
	if err := os.Chmod(stagingDirectory, 0o700); err != nil {
		return artifact.Record{}, fmt.Errorf("secure backup staging directory: %w", err)
	}

	databaseSnapshot := filepath.Join(stagingDirectory, "hx-pxy.db")
	if err := s.database.BackupTo(ctx, databaseSnapshot); err != nil {
		return artifact.Record{}, fmt.Errorf("create database snapshot: %w", err)
	}

	sources := append([]bundle.Source(nil), s.baseSources...)
	sources = append(sources, bundle.Source{
		Name:     "database",
		Path:     databaseSnapshot,
		Scope:    bundle.ScopeBackup,
		Required: true,
	})
	backup, err := bundle.NewService(s.catalog, sources, s.applicationVersion)
	if err != nil {
		return artifact.Record{}, err
	}
	return backup.Create(ctx, kind, options)
}

func (s *Service) List(kind artifact.Kind) ([]artifact.Record, error) {
	return s.base.List(kind)
}

func (s *Service) Open(id string) (artifact.Record, *os.File, error) {
	return s.base.Open(id)
}

func (s *Service) Delete(id string) error {
	return s.base.Delete(id)
}

func (s *Service) Verify(ctx context.Context, id string) (bundle.VerifyResult, error) {
	return s.base.Verify(ctx, id)
}
