package main

import (
	"context"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/sharedinbound"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// sharedInboundHook converges the aggregate listeners and migrates existing
// services before the data plane is asked to compile. main() installs the
// proxy-service implementation right after that service is constructed; the
// settings applier only ever runs at request time, so the late binding is safe.
var sharedInboundHook func(context.Context) error

// settingsSharedInboundHook adapts the late-bound hook to the settings
// applier's pre-apply signature.
func settingsSharedInboundHook() systemsettings.PreApplyFunc {
	return func(ctx context.Context) error {
		if sharedInboundHook == nil {
			return nil
		}
		return sharedInboundHook(ctx)
	}
}

// settingsSharedInboundSpec exposes the configured shared entry points.
type settingsSharedInboundSpec struct {
	settings *systemsettings.Service
}

// SharedInboundSpec reports the current shared-inbound configuration and
// whether it is enabled. It satisfies sharedinbound.SettingsReader.
func (spec settingsSharedInboundSpec) SharedInboundSpec(ctx context.Context) (listener.SharedInboundSpec, bool, error) {
	if spec.settings == nil {
		return listener.SharedInboundSpec{}, false, nil
	}
	current, err := spec.settings.Get(ctx)
	if err != nil {
		return listener.SharedInboundSpec{}, false, err
	}
	return listenerSpec(current.SharedInbound), current.SharedInbound.Enabled(), nil
}

// sharedInboundEndpointProvider exposes the client-facing shared endpoints to
// the subscription exporter, which needs them whenever an individual service
// is published through an aggregate listener.
func sharedInboundEndpointProvider(settings *systemsettings.Service) listener.SharedEndpointProvider {
	reader := settingsSharedInboundSpec{settings: settings}
	return func() (listener.SharedInboundSpec, bool) {
		spec, enabled, err := reader.SharedInboundSpec(context.Background())
		if err != nil {
			return listener.SharedInboundSpec{}, false
		}
		return spec, enabled
	}
}

// newSharedInboundMigrator builds the service that moves existing per-port
// services onto the shared entry points when the feature is switched on.
func newSharedInboundMigrator(
	database *store.Store,
	listeners *listener.Service,
	settings *systemsettings.Service,
) (*sharedinbound.Service, error) {
	return sharedinbound.NewService(database, listeners, settingsSharedInboundSpec{settings: settings})
}

// listenerSpec maps the persisted global settings onto the listener package's
// view of the shared inbound, keeping the two packages independent.
func listenerSpec(shared systemsettings.SharedInboundSettings) listener.SharedInboundSpec {
	return listener.SharedInboundSpec{
		Enabled:          shared.Enabled(),
		IncludeWebSocket: shared.AppliesToWebSocket(),
		MixedBindAddress: shared.MixedBindAddress,
		MixedPort:        shared.MixedPort,
		WSPort:           shared.WSPort,
		MixedPublicHost:  shared.MixedPublicHost,
		MixedPublicPort:  shared.MixedPublicPort,
		MixedPublicTLS:   shared.MixedPublicTLS,
		WSPublicHost:     shared.WSPublicHost,
		WSPublicPort:     shared.WSPublicPort,
	}
}
