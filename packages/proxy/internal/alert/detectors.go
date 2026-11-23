package alert

import (
	"context"
	"fmt"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
)

const subscriptionFailureThreshold = 3

// SubscriptionRepository provides the health rows the subscription
// detectors evaluate.
type SubscriptionRepository interface {
	ListSubscriptionHealth(context.Context) ([]store.SubscriptionHealthRow, error)
}

// SubscriptionDetector fires when an enabled subscription keeps failing to
// refresh or its active snapshot lost every node.
type SubscriptionDetector struct {
	repository SubscriptionRepository
}

func NewSubscriptionDetector(repository SubscriptionRepository) *SubscriptionDetector {
	return &SubscriptionDetector{repository: repository}
}

func (d *SubscriptionDetector) Name() string { return "subscription" }

func (d *SubscriptionDetector) Detect(ctx context.Context) ([]Finding, error) {
	rows, err := d.repository.ListSubscriptionHealth(ctx)
	if err != nil {
		return nil, err
	}
	findings := make([]Finding, 0)
	for _, row := range rows {
		if row.ConsecutiveFailures >= subscriptionFailureThreshold {
			findings = append(findings, Finding{
				Rule:       "subscription-refresh-failing",
				TargetID:   row.ID,
				TargetName: row.Name,
				Severity:   SeverityWarning,
				Message:    fmt.Sprintf("subscription refresh failed %d times in a row; the last successful snapshot stays active", row.ConsecutiveFailures),
			})
		}
		if row.ActiveNodeCount != nil && *row.ActiveNodeCount == 0 {
			findings = append(findings, Finding{
				Rule:       "subscription-no-nodes",
				TargetID:   row.ID,
				TargetName: row.Name,
				Severity:   SeverityCritical,
				Message:    "the active snapshot of this subscription contains zero nodes",
			})
		}
	}
	return findings, nil
}

// GroupRepository provides proxy group definitions and node candidates.
type GroupRepository interface {
	ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error)
	ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error)
}

// EmptyGroupDetector fires when an enabled proxy group resolves to zero
// members and does not fall back to DIRECT.
type EmptyGroupDetector struct {
	repository GroupRepository
}

func NewEmptyGroupDetector(repository GroupRepository) *EmptyGroupDetector {
	return &EmptyGroupDetector{repository: repository}
}

func (d *EmptyGroupDetector) Name() string { return "proxy-group" }

func (d *EmptyGroupDetector) Detect(ctx context.Context) ([]Finding, error) {
	groups, err := d.repository.ListProxyGroups(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := d.repository.ListGroupNodeCandidates(ctx)
	if err != nil {
		return nil, err
	}
	findings := make([]Finding, 0)
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		spec, err := proxygroup.DecodeSourceSpec(group.SourceSpecJSON)
		if err != nil {
			findings = append(findings, Finding{
				Rule:       "proxy-group-invalid-spec",
				TargetID:   group.ID,
				TargetName: group.Name,
				Severity:   SeverityCritical,
				Message:    "the stored source spec cannot be decoded; the group compiles to its empty behavior",
			})
			continue
		}
		if spec.IncludeDirect {
			continue
		}
		members := proxygroup.ResolveNodeIDs(spec, candidates)
		if len(members) == 0 {
			severity := SeverityCritical
			message := "the group resolves to zero nodes and rejects connections (fail-closed)"
			if group.EmptyBehavior == "direct" {
				severity = SeverityWarning
				message = "the group resolves to zero nodes and currently falls back to DIRECT"
			}
			findings = append(findings, Finding{
				Rule:       "proxy-group-empty",
				TargetID:   group.ID,
				TargetName: group.Name,
				Severity:   severity,
				Message:    message,
			})
		}
	}
	return findings, nil
}

// DataPlaneStatus is the minimal view the detector needs.
type DataPlaneStatus struct {
	Available bool
	Running   bool
	LastError string
}

// DataPlaneDetector fires when Mihomo is expected but not running, or the
// last apply failed.
type DataPlaneDetector struct {
	status func() DataPlaneStatus
}

func NewDataPlaneDetector(status func() DataPlaneStatus) *DataPlaneDetector {
	return &DataPlaneDetector{status: status}
}

func (d *DataPlaneDetector) Name() string { return "dataplane" }

func (d *DataPlaneDetector) Detect(context.Context) ([]Finding, error) {
	status := d.status()
	if !status.Available {
		// Mihomo is not installed; treated as a deployment state, not an alert.
		return nil, nil
	}
	findings := make([]Finding, 0, 1)
	if !status.Running {
		message := "the Mihomo data plane process is not running"
		if status.LastError != "" {
			message += ": " + status.LastError
		}
		findings = append(findings, Finding{
			Rule:       "dataplane-not-running",
			TargetID:   "mihomo",
			TargetName: "Mihomo data plane",
			Severity:   SeverityCritical,
			Message:    message,
		})
	} else if status.LastError != "" {
		findings = append(findings, Finding{
			Rule:       "dataplane-apply-failed",
			TargetID:   "mihomo",
			TargetName: "Mihomo data plane",
			Severity:   SeverityWarning,
			Message:    "the last configuration apply failed: " + status.LastError,
		})
	}
	return findings, nil
}
