package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	maximumProviderDepth = 3
	maximumProviders     = 32
	maximumProviderBytes = 32 << 20
)

func (s *Service) parseNodeImports(
	ctx context.Context,
	content []byte,
	contentType string,
	sourceType SourceType,
	sourceConfig SourceConfig,
) (ParseSummary, []store.NodeImportRecord, error) {
	if s.parser == nil {
		summary := inspectSource(content, contentType)
		return summary, nil, nil
	}
	parsed, err := s.parser(content)
	if err != nil {
		return ParseSummary{}, nil, fmt.Errorf("parse subscription nodes: %w", err)
	}
	providerCount := 0
	providerBytes := 0
	s.expandProviders(ctx, &parsed, sourceType, sourceConfig, 0, &providerCount, &providerBytes)
	unique := make(map[string]nodeparse.Node, len(parsed.Nodes))
	for _, node := range parsed.Nodes {
		if _, exists := unique[node.Fingerprint]; !exists {
			unique[node.Fingerprint] = node
		}
	}
	if len(unique) == 0 {
		return ParseSummary{}, nil, errors.New("subscription contains no valid nodes")
	}
	imports := make([]store.NodeImportRecord, 0, len(unique))
	for _, node := range unique {
		encoded, err := json.Marshal(node.Canonical)
		if err != nil {
			return ParseSummary{}, nil, fmt.Errorf("encode canonical node: %w", err)
		}
		encrypted, err := s.cipher.Seal(encoded, nodeAssociatedData(node.Fingerprint))
		if err != nil {
			return ParseSummary{}, nil, fmt.Errorf("encrypt canonical node: %w", err)
		}
		nodeID, err := newNodeID()
		if err != nil {
			return ParseSummary{}, nil, err
		}
		imports = append(imports, store.NodeImportRecord{
			ID:                       nodeID,
			Fingerprint:              node.Fingerprint,
			DisplayName:              node.DisplayName,
			Protocol:                 node.Protocol,
			CanonicalConfigEncrypted: encrypted,
			SourceName:               node.DisplayName,
		})
	}
	return ParseSummary{
		DetectedFormat: parsed.DetectedFormat,
		EstimatedNodes: len(imports),
		ParsedNodes:    len(imports),
		FailedNodes:    len(parsed.Failures),
		Failures:       parsed.Failures,
	}, imports, nil
}

func (s *Service) expandProviders(
	ctx context.Context,
	result *nodeparse.Result,
	parentType SourceType,
	parentConfig SourceConfig,
	depth int,
	providerCount *int,
	providerBytes *int,
) {
	providers := append([]nodeparse.ProviderReference(nil), result.Providers...)
	result.Providers = nil
	for _, provider := range providers {
		if depth >= maximumProviderDepth {
			result.Failures = append(result.Failures, nodeparse.Failure{Name: provider.Name, Reason: "provider nesting is too deep"})
			continue
		}
		if *providerCount >= maximumProviders {
			result.Failures = append(result.Failures, nodeparse.Failure{Name: provider.Name, Reason: "provider count limit exceeded"})
			continue
		}
		*providerCount++

		sourceType, config, ok := providerSource(parentType, parentConfig, provider)
		if !ok {
			result.Failures = append(result.Failures, nodeparse.Failure{Name: provider.Name, Reason: "provider source is not allowed in this subscription context"})
			continue
		}
		loaded, err := s.loader.Load(ctx, sourceType, config, FetchCondition{})
		if err != nil {
			result.Failures = append(result.Failures, nodeparse.Failure{Name: provider.Name, Reason: "provider source could not be loaded"})
			continue
		}
		*providerBytes += len(loaded.Content)
		if *providerBytes > maximumProviderBytes {
			result.Failures = append(result.Failures, nodeparse.Failure{Name: provider.Name, Reason: "provider content size limit exceeded"})
			continue
		}
		parsed, err := s.parser(loaded.Content)
		if err != nil {
			result.Failures = append(result.Failures, nodeparse.Failure{Name: provider.Name, Reason: "provider content is not supported"})
			continue
		}
		s.expandProviders(ctx, &parsed, sourceType, config, depth+1, providerCount, providerBytes)
		result.Nodes = append(result.Nodes, parsed.Nodes...)
		for _, failure := range parsed.Failures {
			if failure.Name == "" {
				failure.Name = provider.Name
			} else {
				failure.Name = provider.Name + ": " + failure.Name
			}
			result.Failures = append(result.Failures, failure)
		}
		if !strings.Contains(result.DetectedFormat, "+providers") {
			result.DetectedFormat += "+providers"
		}
	}
}

func providerSource(parentType SourceType, parent SourceConfig, provider nodeparse.ProviderReference) (SourceType, SourceConfig, bool) {
	switch provider.Type {
	case "http":
		headers := make(map[string]string, len(parent.Headers)+len(provider.Headers))
		for key, value := range parent.Headers {
			headers[key] = value
		}
		for key, value := range provider.Headers {
			headers[key] = value
		}
		userAgent := provider.UserAgent
		if userAgent == "" {
			userAgent = parent.UserAgent
		}
		return SourceRemote, SourceConfig{
			URL: provider.URL, Headers: headers, UserAgent: userAgent,
			TimeoutSeconds: parent.TimeoutSeconds, AllowPrivate: parent.AllowPrivate,
		}, true
	case "file":
		if parentType != SourceFile || strings.TrimSpace(parent.FilePath) == "" {
			return "", SourceConfig{}, false
		}
		path := provider.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(parent.FilePath), path)
		}
		return SourceFile, SourceConfig{FilePath: filepath.Clean(path)}, true
	default:
		return "", SourceConfig{}, false
	}
}

func newNodeID() (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	return "node-" + strings.TrimPrefix(id, "sub-"), nil
}

func nodeAssociatedData(fingerprint string) []byte {
	return []byte("node:" + fingerprint)
}
