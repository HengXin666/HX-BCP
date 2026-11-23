package residential

import (
	"context"
	"errors"
	"fmt"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// ExportByShareToken resolves a channel's primary listener token. The boolean
// distinguishes a residential-owned token from an unrelated legacy listener
// token so the API can avoid an unsafe fallback for direct entry points.
func (s *Service) ExportByShareToken(
	ctx context.Context,
	token, requestHost string,
) (listener.ShareBundle, bool, error) {
	return s.ShareBundleByShareToken(ctx, token, requestHost)
}

// ShareBundleByShareToken is ExportByShareToken under the name the programmatic
// node listing uses. Both callers get the same bundle, so /sub/ and
// GET /api/v1/nodes cannot disagree about which nodes a channel publishes.
func (s *Service) ShareBundleByShareToken(
	ctx context.Context,
	token, requestHost string,
) (listener.ShareBundle, bool, error) {
	record, err := s.repository.GetListenerByShareToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return listener.ShareBundle{}, false, nil
	}
	if err != nil {
		return listener.ShareBundle{}, false, err
	}
	channel, err := s.repository.GetResidentialChannelByListenerID(ctx, record.ID)
	if errors.Is(err, store.ErrNotFound) {
		return listener.ShareBundle{}, false, nil
	}
	if err != nil {
		return listener.ShareBundle{}, false, err
	}
	if record.ID != channel.ListenerID || !channel.Enabled {
		return listener.ShareBundle{}, true, listener.ErrShareDisabled
	}
	if channel.Mode == ModePassthrough {
		export, err := s.listeners.ExportByID(ctx, channel.ListenerID, requestHost)
		if err != nil {
			return listener.ShareBundle{}, true, err
		}
		return listener.NewShareBundle(channel.Name, []listener.ShareExport{export}), true, nil
	}
	if channel.Mode != ModeSticky || channel.SessionCount < 1 {
		return listener.ShareBundle{}, true, listener.ErrShareDisabled
	}
	exports, err := s.channelShareExports(ctx, channel, requestHost)
	if err != nil {
		return listener.ShareBundle{}, true, err
	}
	return listener.NewShareBundle(channel.Name, exports), true, nil
}

// ShareExportsByShareToken resolves a channel's published nodes from its share
// token, in the shape the programmatic node listing renders
// (docs/CONSUMER_INTEGRATION_CONTRACT.md). It doubles as the ownership test:
// the boolean reports whether the token belongs to a residential channel at
// all, so a plain listener token falls through to the listener export.
//
// It duplicates nothing: ShareBundleByShareToken is the same resolution with
// the bundle wrapper applied, and both read channelShareExports.
func (s *Service) ShareExportsByShareToken(
	ctx context.Context,
	token, requestHost string,
) ([]listener.ShareExport, string, bool, error) {
	bundle, matched, err := s.ShareBundleByShareToken(ctx, token, requestHost)
	if err != nil || !matched {
		return nil, "", matched, err
	}
	return bundle.Exports, bundle.Name, true, nil
}

func (s *Service) channelShareExports(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	requestHost string,
) ([]listener.ShareExport, error) {
	sessions, err := s.repository.ListResidentialClientSessions(ctx, channel.ID)
	if err != nil {
		return nil, err
	}
	byIndex := make(map[int]store.ResidentialClientSessionRecord, len(sessions))
	for _, session := range sessions {
		if session.DeclaredIndex > 0 {
			byIndex[session.DeclaredIndex] = session
		}
	}
	nodes := make([]listener.ShareNode, 0, channel.SessionCount)
	for index := 1; index <= channel.SessionCount; index++ {
		session, exists := byIndex[index]
		if !exists {
			return nil, fmt.Errorf("declared session %s is missing", declaredSessionID(index))
		}
		password, err := s.cipher.Open(
			session.AuthPasswordEncrypted,
			clientSessionAssociatedData(channel.ID, session.SessionID),
		)
		if err != nil {
			return nil, fmt.Errorf("decrypt declared session %s: %w", session.SessionID, err)
		}
		nodes = append(nodes, listener.ShareNode{
			Name: DeclaredNodeName(channel.Name, index),
			Auth: &listener.Auth{Username: session.AuthUsername, Password: string(password)},
		})
	}
	hasWSAndDirect := channel.DirectListenerID != ""
	exports := make([]listener.ShareExport, 0, 2)
	primaryNodes := suffixShareNodes(nodes, hasWSAndDirect, "-ws")
	primary, err := s.listeners.ExportWithNodes(
		ctx,
		channel.ListenerID,
		requestHost,
		channel.Name,
		primaryNodes,
	)
	if err != nil && (!errors.Is(err, listener.ErrShareDisabled) || channel.DirectListenerID == "") {
		return nil, err
	}
	if err == nil {
		exports = append(exports, primary)
	}
	if channel.DirectListenerID != "" {
		direct, err := s.listeners.ExportWithNodes(
			ctx,
			channel.DirectListenerID,
			requestHost,
			channel.Name+"-direct",
			suffixShareNodes(nodes, true, "-direct"),
		)
		if err != nil {
			return nil, err
		}
		exports = append(exports, direct)
	}
	if len(exports) == 0 {
		return nil, listener.ErrShareDisabled
	}
	return exports, nil
}

func suffixShareNodes(nodes []listener.ShareNode, enabled bool, suffix string) []listener.ShareNode {
	result := make([]listener.ShareNode, len(nodes))
	for index, node := range nodes {
		result[index] = node
		if enabled {
			result[index].Name += suffix
		}
	}
	return result
}
