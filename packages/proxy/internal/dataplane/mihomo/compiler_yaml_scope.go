package mihomo

// sharedUserRule is one shared-inbound IN-USER rule.
// GroupID is the proxy group behind that username, or empty when the listener
// is pinned to a node. Callers must not recover that from the rule text: a
// group name may contain the "), " separator, and the last "), " is then not
// the action boundary.
// See .agents/notes/implemented/feature/2026-10-18-yaml-routing-intent.md.
type sharedUserRule struct {
	Text    string
	GroupID string
}

func scopeSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			set[id] = struct{}{}
		}
	}
	return set
}

func splitSharedUsers(rules []sharedUserRule) (pinned, grouped []sharedUserRule) {
	for _, rule := range rules {
		if rule.GroupID == "" {
			pinned = append(pinned, rule)
			continue
		}
		grouped = append(grouped, rule)
	}
	return pinned, grouped
}

// sharedUsersByScope splits group users into those the YAML document names and
// those it does not. An empty scope puts every user outside.
func sharedUsersByScope(rules []sharedUserRule, scope map[string]struct{}) (outside, inside []sharedUserRule) {
	for _, rule := range rules {
		if _, named := scope[rule.GroupID]; named {
			inside = append(inside, rule)
			continue
		}
		outside = append(outside, rule)
	}
	return outside, inside
}

func sharedUserTexts(rules []sharedUserRule) []string {
	if len(rules) == 0 {
		return nil
	}
	texts := make([]string, 0, len(rules))
	for _, rule := range rules {
		texts = append(texts, rule.Text)
	}
	return texts
}
