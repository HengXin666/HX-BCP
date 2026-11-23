package proxygroup

import (
	"context"
	"fmt"
)

// A proxy group is a selection, not a handle for one node.
//
// The failure this rule prevents has been measured three times in this
// repository: 2026-09-26 found 61 groups, 57 of them `manual` groups holding
// exactly one node and covering only 28 distinct nodes (one node had been
// wrapped five times); 2026-10-01 found six more built over 12 days by a script
// that used a group as "point this service at that node"; 2026-10-18 found the
// same shape still reachable through `allow_empty`, a one-node subscription,
// and `limit: 1`. Both cleanups worked and the shape came back, because
// building the wrong shape was one field cheaper than building the right one.
//
// The right shape already exists: `listeners.node_id` pins a service to one node
// without a group (see
// .agents/notes/implemented/feature/2026-09-26-listener-pins-a-node.md). So the
// wrong shape is refused at the one choke point every caller passes through, and
// the refusal spells out what to do instead.
//
// The check counts the members the request actually produces, not just the
// length of `node_ids`. Counting only `node_ids` left the reported shape
// reachable: `{"node_ids":["n1"],"allow_empty":true}`, a subscription whose
// snapshot currently holds one node, and `{"subscription_ids":["sub-1"],
// "limit":1}` all create a one-member group today. The member count is resolved
// through the same pipeline the data plane uses (source_resolver.go), so those
// shapes are refused as well. A subscription that has not been refreshed yet
// resolves to zero members and is allowed to be created dormant; only a
// resolved count of exactly one is refused.
//
// Two pre-existing single-member shapes stay allowed because they are not a
// node dressed up as a group:
//
//   - `include_direct` alone. DIRECT is the server's own egress; "server direct"
//     is a real single-member policy and the cheapest legal anchor for pinned
//     listeners.
//   - a single `group_ids` entry with no node/subscription members. That is a
//     composition (a fallback chain of one group), not a wrapper around a node;
//     the members belong to the referenced group. A group with one node plus a
//     group reference has two members and is allowed.
//
// See .agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md
// and .agents/notes/implemented/architecture/2026-10-18-a-group-size-is-its-members.md.
func (s *Service) rejectSingleMemberGroup(ctx context.Context, spec SourceSpec, allowSingleNode bool) error {
	if allowSingleNode || isAllowedSingleShape(spec) {
		return nil
	}
	count, err := s.memberCount(ctx, spec)
	if err != nil {
		return err
	}
	if count != 1 {
		return nil
	}
	return fmt.Errorf("%w: 代理组节点必须 > 1：该 source_spec 当前只会解析出 1 个成员（node_ids 一个、单订阅/筛选后只剩一个、limit=1、allow_empty 配一个节点都算），不允许建。正确用法是建一个大节点组（通常一个订阅一个组、尽量少建组），再用 listener.node_id 把某个服务钉到具体节点上——POST /api/v1/listeners 与 PUT /api/v1/proxy-services 的 listener.node_id 都直接指定节点，不需要建组。禁止为每个节点批量创建代理组。", ErrInvalid)
}

// memberCount resolves the member set the group would carry if it were created
// now. Explicit node ids are already validated by normalize; subscription
// members go through the same rule pipeline the compiler uses.
//
// A group made only of group references counts each reference as one member:
// Mihomo treats a nested group as one proxy name, not as an expansion of its
// members. That is also why the single-group composition above is allowed while
// a single node is not — one is a policy reference, the other is a node handle.
func (s *Service) memberCount(ctx context.Context, spec SourceSpec) (int, error) {
	nodeCount := len(spec.NodeIDs)
	if len(spec.SubscriptionIDs) > 0 {
		candidates, err := s.repository.ListGroupNodeCandidates(ctx)
		if err != nil {
			return 0, fmt.Errorf("resolve group members: %w", err)
		}
		resolution, err := ResolveMembers(spec, "", candidates)
		if err != nil {
			return 0, fmt.Errorf("%w: resolve group members: %v", ErrInvalid, err)
		}
		nodeCount = len(resolution.NodeIDs)
	}
	count := nodeCount + len(spec.GroupIDs)
	if spec.IncludeDirect {
		count++
	}
	return count, nil
}

// isAllowedSingleShape names the two single-member shapes that are policies,
// not node handles. They are checked before resolving members so a DIRECT-only
// anchor does not pay for a node-candidate query.
func isAllowedSingleShape(spec SourceSpec) bool {
	if len(spec.NodeIDs) != 0 || len(spec.SubscriptionIDs) != 0 {
		return false
	}
	if spec.IncludeDirect {
		// DIRECT-only, or DIRECT plus any number of group references: DIRECT is
		// already a second member in the latter case.
		return true
	}
	return isSingleGroupComposition(spec)
}

// isSingleGroupComposition is a fallback chain of one group: a policy
// reference, not a node handle.
func isSingleGroupComposition(spec SourceSpec) bool {
	return !spec.IncludeDirect && len(spec.NodeIDs) == 0 && len(spec.SubscriptionIDs) == 0 && len(spec.GroupIDs) == 1
}

// legacySingleMember reports whether the stored group already resolves to one
// member. Such a row must stay editable (rename, disable, change strategy), or
// the operator cannot converge it; only *reaching* a one-member shape from a
// larger one is refused. DIRECT-only anchors are excluded so an anchor cannot
// be turned into a node handle in one edit.
func (s *Service) legacySingleMember(ctx context.Context, storedJSON string) bool {
	stored, err := DecodeSourceSpec(storedJSON)
	if err != nil || isDirectOnlyAnchor(stored) || isSingleGroupComposition(stored) {
		return false
	}
	count, err := s.memberCount(ctx, stored)
	return err == nil && count == 1
}

func isDirectOnlyAnchor(spec SourceSpec) bool {
	return spec.IncludeDirect && len(spec.NodeIDs) == 0 && len(spec.GroupIDs) == 0 && len(spec.SubscriptionIDs) == 0
}

// isSingleNodeGroup reports whether the spec describes exactly one explicit
// node and nothing else. It is retained for the update exemption that keeps a
// legacy one-node row editable even when its member list is now resolved
// differently (for example, its subscription shrank).
func isSingleNodeGroup(spec SourceSpec) bool {
	if spec.AllowEmpty {
		return false
	}
	if len(spec.NodeIDs) != 1 {
		return false
	}
	return len(spec.SubscriptionIDs) == 0 && len(spec.GroupIDs) == 0 && !spec.IncludeDirect
}

// sameSingleNode reports whether candidate describes the same one-node group the
// stored spec already describes, ignoring every other field. It is what lets an
// unrelated edit of a legacy one-node group through without letting a caller
// reach that shape from a two-node one.
func sameSingleNode(candidate SourceSpec, storedJSON string) bool {
	stored, err := DecodeSourceSpec(storedJSON)
	if err != nil || !isSingleNodeGroup(stored) || !isSingleNodeGroup(candidate) {
		return false
	}
	return stored.NodeIDs[0] == candidate.NodeIDs[0]
}

// updateMayKeepSingleNode decides whether an update is exempt from the one-node
// rule.
//
// An edit of a group that is *already* a one-node group must stay possible:
// renaming it, disabling it, or changing its strategy is how an operator retires
// the shape, and refusing the write would freeze the row instead of converging.
// The exemption is deliberately narrow — the member set has to be identical to
// the stored one — so it cannot be used to reach that shape in two calls (create
// with two nodes, then edit down to one).
func updateMayKeepSingleNode(request UpdateRequest, storedJSON string) bool {
	return request.AllowSingleNode || sameSingleNode(request.SourceSpec, storedJSON)
}
