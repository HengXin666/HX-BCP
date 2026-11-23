package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"

	"github.com/HengXin666/hx-pxy/internal/api"
	"github.com/HengXin666/hx-pxy/internal/config"
	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/entry"
	"github.com/HengXin666/hx-pxy/internal/store"
)

const entryHubSecretKey = "entry_hub_secret"

// entryCatalogBox holds the last applied catalog until the entry service exists,
// because the data plane is built and applied before the residential service
// the entry depends on.
type entryCatalogBox struct {
	mutex  sync.Mutex
	last   *mihomo.EntryCatalog
	target func(mihomo.EntryCatalog)
}

func (b *entryCatalogBox) sink(catalog mihomo.EntryCatalog) {
	b.mutex.Lock()
	target := b.target
	if target == nil {
		b.last = &catalog
	}
	b.mutex.Unlock()
	if target != nil {
		target(catalog)
	}
}

func (b *entryCatalogBox) attach(target func(mihomo.EntryCatalog)) {
	b.mutex.Lock()
	b.target = target
	last := b.last
	b.last = nil
	b.mutex.Unlock()
	if last != nil {
		target(*last)
	}
}

// loadEntryHubSecret returns the hub secret, minting it on first start. It is
// stable across restarts so a reload never rewrites every hub user.
func loadEntryHubSecret(ctx context.Context, database *store.Store) (string, error) {
	secret, err := database.GetMetadata(ctx, entryHubSecretKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if len(secret) >= 32 {
		return secret, nil
	}
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	secret = hex.EncodeToString(buffer)
	return secret, database.SetMetadata(ctx, entryHubSecretKey, secret)
}

// entryAPIOption wires the entry routes only when the entry runs.
func entryAPIOption(service *entry.Service) api.Option {
	if service == nil {
		return nil
	}
	return api.WithEntry(service)
}

// startEntryService builds the unified entry. A failure leaves the entry off
// and is logged; the control plane and data plane keep starting.
func startEntryService(ctx context.Context, cfg config.Config, egressInterface string, database *store.Store, cipher entry.Cipher, gateways entry.GatewaySource, logger *slog.Logger) *entry.Service {
	service, err := entry.NewService(ctx, entry.Config{
		Listen:          cfg.EntryListen,
		PublicAddress:   cfg.EntryPublicAddress,
		ExitProbe:       cfg.EntryExitProbe,
		EgressInterface: egressInterface,
	}, database, cipher, gateways, logger)
	if err != nil {
		logger.Error("unified entry disabled", "component", "entry", "error", err)
		return nil
	}
	return service
}
