package proxygroup

import (
	"fmt"
	"strings"
)

// Telling a proxy group from a node.
//
// They are different objects with different jobs — a group selects among nodes,
// a node is the thing selected — but both are opaque "id" strings on the wire,
// both are listed by a GET that returns `id`, and the field names differ by one
// plural `s` (`node_id` vs `node_ids`, and `group_ids` right beside them). An
// agent that has just read GET /api/v1/proxy-groups therefore holds a string
// that looks exactly like the one node_ids wants.
//
// Measured 2026-10-18: posting a group id in source_spec.node_ids was answered
// with "a group needs more than one node". That reply is about a different
// problem, and its remedy (add another node) makes the request worse. The
// caller's actual mistake is a category error, so the error has to name the
// category — which the id prefix already encodes (`node-`, `group-`, `sub-`).
//
// See .agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md.

// referenceKind is the object kind encoded in a control-plane id's prefix.
type referenceKind struct {
	// field is the source_spec field that does accept this kind.
	field string
	// label is what to call it in a sentence.
	label string
	// listPath is the endpoint that lists these objects.
	listPath string
}

var referenceKinds = map[string]referenceKind{
	"node":     {field: "node_ids", label: "节点", listPath: "GET /api/v1/nodes"},
	"group":    {field: "group_ids", label: "代理组", listPath: "GET /api/v1/proxy-groups"},
	"sub":      {field: "subscription_ids", label: "订阅", listPath: "GET /api/v1/subscriptions"},
	"snap":     {field: "subscription_ids", label: "订阅快照", listPath: "GET /api/v1/subscriptions"},
	"listener": {field: "", label: "监听入口", listPath: "GET /api/v1/listeners"},
}

// kindOfID reads the object kind out of an id's prefix. An id with no recognisable
// prefix returns the zero value, which callers treat as "cannot classify".
func kindOfID(id string) referenceKind {
	prefix, _, found := strings.Cut(strings.TrimSpace(id), "-")
	if !found {
		return referenceKind{}
	}
	return referenceKinds[prefix]
}

// misplacedReferenceError explains that id does not belong in field, naming the
// field that does accept it. It returns nil when the id's kind is unknown or is
// in fact the right kind for the field, so the caller can fall back to a generic
// message instead of guessing.
func misplacedReferenceError(field, id string) error {
	kind := kindOfID(id)
	if kind.field == "" || kind.field == field {
		return nil
	}
	return fmt.Errorf(
		"%w: %s 里放的是%s ID %q，但 %s 只接受%s ID（%s 返回的 id）。代理组和节点是两种对象：组是在一批节点里挑一个，节点是被挑中的那一个，两者的 id 不能互换。这个%s请改放到 %s；若目标是让某个服务固定从一个节点出网，不要建组，改用 listener.node_id 直接钉节点。",
		ErrInvalid, field, kind.label, id, field, fieldKindLabel(field), fieldListPath(field),
		kind.label, kind.field,
	)
}

// fieldKindLabel names the object a source_spec field accepts, so the message
// can say "node_ids only accepts node ids" rather than just "invalid".
func fieldKindLabel(field string) string {
	if kind, ok := fieldKinds[field]; ok {
		return kind.label
	}
	return "该字段接受的"
}

// fieldListPath names the endpoint that lists the objects a field accepts. It is
// the field's own kind, not the kind of the id that was sent: telling a caller
// "node_ids only takes node ids, see GET /api/v1/proxy-groups" would send it back
// to the very list it copied the wrong id from.
func fieldListPath(field string) string {
	if kind, ok := fieldKinds[field]; ok {
		return kind.listPath
	}
	return "GET /api/v1/capabilities"
}

// fieldKinds indexes referenceKinds by the field that accepts each kind, so the
// per-field lookups above cannot drift from the id-prefix table.
var fieldKinds = func() map[string]referenceKind {
	index := make(map[string]referenceKind, len(referenceKinds))
	for _, kind := range referenceKinds {
		if kind.field != "" {
			index[kind.field] = kind
		}
	}
	return index
}()

// rejectMisplacedReferences refuses a source_spec whose ids belong to a different
// object kind. It runs before every other membership check so a category error is
// reported as itself: the one-node rule would otherwise answer a group id in
// node_ids with "add another node", which is advice to make the mistake worse.
func rejectMisplacedReferences(spec SourceSpec) error {
	for _, field := range []struct {
		name string
		ids  []string
	}{
		{"node_ids", spec.NodeIDs},
		{"group_ids", spec.GroupIDs},
		{"subscription_ids", spec.SubscriptionIDs},
	} {
		for _, id := range field.ids {
			if err := misplacedReferenceError(field.name, id); err != nil {
				return err
			}
		}
	}
	return nil
}
