// Gate questions (R5-S1, R5-S2). Some questions are crowsnest's own gates: a land request, a
// delegation approval, a scheduled task file waiting for review. Answering one changes what agents
// may do, so two things must hold. First, only crowsnest can make one: the context keys that mark
// a gate, and that the settle callbacks act on, are reserved, and a question an agent asks through
// the socket (or the MCP tool, or `crowsnest ask`) may not carry them. Second, a gate is answered
// only from the Inbox card, where the human sees the whole question: it gets no notification
// buttons and no action token, and the push action endpoint refuses it.

package questions

import (
	"sort"
	"strings"
)

// The context keys crowsnest sets on its own questions. The gate keys mark a gate; the others
// travel with one. All of them are reserved.
const (
	// ContextLandFor names the job a land request is for (DESIGN.md 6.11).
	ContextLandFor = "land_for"
	// ContextLandAction is the land action asked for: merge, pr or discard.
	ContextLandAction = "land_action"
	// ContextLandToken binds a land question to the one request it approves (R5-C01): the job
	// stores the same random token when the request is reserved.
	ContextLandToken = "land_token"
	// ContextApprovalFor names the job held for the human's approval (R2-H1).
	ContextApprovalFor = "approval_for"
	// ContextRequestedBy is who asked for the held job, in words.
	ContextRequestedBy = "requested_by"
	// ContextTaskReview names a scheduled task file waiting for the human's review (R5-S3).
	ContextTaskReview = "task_review"
	// ContextTaskHash is the content hash of the task file that question approves.
	ContextTaskHash = "task_hash"
	// ContextGate marks any other crowsnest gate; reserved for future gates so none of them
	// needs another change here to be safe.
	ContextGate = "crowsnest_gate"
)

// gateKeys mark a question as a gate.
var gateKeys = []string{ContextLandFor, ContextApprovalFor, ContextTaskReview, ContextGate}

// reservedKeys may only be set by crowsnest itself.
var reservedKeys = map[string]bool{
	ContextLandFor: true, ContextLandAction: true, ContextLandToken: true,
	ContextApprovalFor: true, ContextRequestedBy: true,
	ContextTaskReview: true, ContextTaskHash: true, ContextGate: true,
}

// IsGate reports whether a question is one of crowsnest's own gates. Since an agent cannot set the
// keys that mark one (CheckAgentContext), a question carrying them was made by crowsnest.
func IsGate(context map[string]string) bool {
	for _, key := range gateKeys {
		if context[key] != "" {
			return true
		}
	}
	return false
}

// grantingOptionKeys are option keys that grant something. A question offering one is treated
// as a gate for notification purposes, because an agent can word any question to look like one
// (R3-I4): one tap on a lock screen must never grant anything.
var grantingOptionKeys = map[string]bool{"allow": true, "approve": true, "merge": true, "land": true, "push": true, "grant": true}

// GateShaped reports whether a question must be answered in the app, never from a notification:
// one of crowsnest's own gates (IsGate), or any question offering an option that allows,
// approves, merges, lands, pushes or grants. It is the one rule both the push notices (which
// then carry no buttons and no token) and the push action endpoint (which refuses) apply; the
// endpoint is the authority.
func GateShaped(context map[string]string, optionKeys []string) bool {
	if IsGate(context) {
		return true
	}
	for _, key := range optionKeys {
		if grantingOptionKeys[strings.ToLower(strings.TrimSpace(key))] {
			return true
		}
	}
	return false
}

// CheckAgentContext refuses a question from an agent whose context carries a key crowsnest
// reserves for its own gates. The comparison ignores case and surrounding space, so a near-miss
// spelling that a human could mistake for the real key is refused too.
func CheckAgentContext(input AskInput) error {
	var bad []string
	for key := range input.Context {
		if reservedKeys[strings.ToLower(strings.TrimSpace(key))] {
			bad = append(bad, key)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return invalid("context key %s is reserved for crowsnest's own questions; use another name", strings.Join(bad, ", "))
}
