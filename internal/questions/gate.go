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
	"unicode"

	"golang.org/x/text/unicode/norm"
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

// grantingWords are words that grant something, with their inflections. A question whose option
// keys, option labels or title use one is treated as a gate for notification purposes, because an
// agent can word any question to look like one (R3-I4, R6-S9): one tap on a lock screen must never
// look like it grants anything. "go ahead" reads as a grant too (R7-S6); "ship" does not, since
// "Ship?" is the plain confirm of many ordinary questions. R10-S10 added enable and execute; ok and
// run stay out for the same reason as ship ("Is it OK?", "Run the tests again?" are plain).
var grantingWords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`allow allows allowed allowing approve approves approved approving approval
		merge merges merged merging land lands landed landing push pushes pushed pushing deploy deploys deployed
		deploying deployment delete deletes deleted deleting deletion grant grants granted granting permit permits
		permitted permitting proceed proceeds proceeded proceeding authorize authorizes authorized authorizing
		authorise authorises authorised authorising authorization authorisation accept accepts accepted accepting
		confirm confirms confirmed confirming publish publishes published publishing apply applies applied
		applying enable enables enabled enabling execute executes executed executing goahead`) {
		grantingWords[word] = true
	}
}

// grantingPhrases are granting words of more than one word, matched on the normalised words.
var grantingPhrases = []string{"go ahead"}

// confusables folds the Cyrillic and Greek letters that look like Latin ones onto those (a small
// part of Unicode's UTS #39 skeleton: the letters an agent would use to spell "Allow" so that a
// word list does not see it, R7-S6).
var confusables = map[rune]rune{
	'\u0430': 'a', '\u0435': 'e', '\u043e': 'o', '\u0440': 'p', '\u0441': 'c', '\u0443': 'y', '\u0445': 'x',
	'\u0456': 'i', '\u0458': 'j', '\u0455': 's', '\u0501': 'd', '\u04cf': 'l', '\u0261': 'g', '\u0251': 'a',
	'\u0410': 'a', '\u0415': 'e', '\u041e': 'o', '\u0420': 'p', '\u0421': 'c', '\u0425': 'x', '\u0406': 'I',
	'\u0408': 'j', '\u0405': 's', '\u041c': 'm', '\u041d': 'h', '\u0422': 't', '\u0412': 'b', '\u041a': 'k',
	'\u03bf': 'o', '\u03b1': 'a', '\u03b5': 'e', '\u03b9': 'i', '\u03ba': 'k', '\u03bd': 'v', '\u03c1': 'p',
	'\u03c4': 't', '\u03c5': 'u', '\u039f': 'o', '\u0391': 'a', '\u0392': 'b', '\u0395': 'e', '\u0397': 'h',
	'\u0399': 'I', '\u039a': 'k', '\u039c': 'm', '\u039d': 'n', '\u03a1': 'p', '\u03a4': 't', '\u03a7': 'x',
	'\u0131': 'i',
	// R9-S10: Latin small capitals, the Cherokee letters that draw as Latin capitals, and the
	// stroked and Greek or Cyrillic letters that read as l and w.
	'\u1d00': 'a', '\u0299': 'b', '\u1d04': 'c', '\u1d05': 'd', '\u1d07': 'e', '\ua730': 'f', '\u0262': 'g',
	'\u029c': 'h', '\u026a': 'i', '\u1d0a': 'j', '\u1d0b': 'k', '\u029f': 'l', '\u1d0d': 'm', '\u0274': 'n',
	'\u1d0f': 'o', '\u1d18': 'p', '\u0280': 'r', '\ua731': 's', '\u1d1b': 't', '\u1d1c': 'u', '\u1d20': 'v',
	'\u1d21': 'w', '\u028f': 'y', '\u1d22': 'z',
	'\u13aa': 'a', '\u13f4': 'b', '\u13df': 'c', '\u13a0': 'd', '\u13ac': 'e', '\u13c0': 'g', '\u13bb': 'h',
	'\u13a5': 'i', '\u13e7': 'j', '\u13e6': 'k', '\u13de': 'l', '\u13b7': 'm', '\u13be': 'o', '\u13e2': 'p',
	'\u13a1': 'r', '\u13da': 's', '\u13a2': 't', '\u13d9': 'v', '\u13b3': 'w', '\u13bd': 'y', '\u13c3': 'z',
	'\u0142': 'l', '\u0141': 'l', '\u03c9': 'w', '\u051d': 'w', '\u051c': 'w', '\u0461': 'w',
	// R10-S10: the Cyrillic and Greek capital I above fold to a Latin capital I (read as l between
	// letters, as I is); the palochka too; the slashed o has no decomposition.
	'\u04c0': 'I', '\u00f8': 'o', '\u00d8': 'o',
}

// digitLetters are the digits that stand for letters inside a word (A11ow, appr0ve, R10-S10).
var digitLetters = map[rune]rune{'0': 'o', '1': 'l', '3': 'e', '4': 'a', '5': 's', '7': 't'}

// wordJoiners join the letters on either side of them into one word (Al.low, a-l-l-o-w, R10-S10):
// one of these between two letters is a disguise more often than a word break.
const wordJoiners = ".-_·'’"

// normalisedWords is text as GrantingText reads it (R7-S6, R9-S10): compatibility-normalised and
// decomposed (NFKD, so full-width and styled letters are plain and an accent is a mark of its
// own), with every format character (zero-width space and joiners, bidi controls, soft hyphen)
// and every combining mark (`Allów`) removed so neither can split or disguise a word, look-alike
// letters folded onto Latin ones, digits beside letters read as letters and joiners between
// letters dropped (R10-S10: `A11ow`, `Al.low`), a capital I read as an l where it sits between
// letters (`AIIow`: a sans-serif I and l look the same), lowercased and split into words of letters.
func normalisedWords(text string) []string {
	var folded []rune
	for _, r := range norm.NFKD.String(text) {
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Mn, r) {
			continue
		}
		if latin, ok := confusables[r]; ok {
			r = latin
		}
		folded = append(folded, r)
	}
	folded = joinDisguisedWords(folded)
	var b strings.Builder
	for i, r := range folded {
		if r == 'I' && i > 0 && unicode.IsLetter(folded[i-1]) {
			r = 'l'
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.FieldsFunc(b.String(), func(r rune) bool { return !unicode.IsLetter(r) })
}

// joinDisguisedWords reads a digit beside a letter as the letter it stands for, and drops a joiner
// (wordJoiners) between two letters, so "A11ow" and "Al.low" are the word they spell (R10-S10).
// A digit with no letter beside it (a version, a count) is left to split words as before.
func joinDisguisedWords(runes []rune) []rune {
	isLetter := func(i int) bool { return i >= 0 && i < len(runes) && unicode.IsLetter(runes[i]) }
	out := make([]rune, 0, len(runes))
	for i, r := range runes {
		if letter, ok := digitLetters[r]; ok && (isLetter(i-1) || isLetter(i+1)) {
			runes[i] = letter
			out = append(out, letter)
			continue
		}
		if strings.ContainsRune(wordJoiners, r) && isLetter(i-1) && isLetter(i+1) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// GrantingText reports whether text uses a granting word or phrase (whole words, any case, after
// normalisation).
func GrantingText(text string) bool {
	words := normalisedWords(text)
	for _, word := range words {
		if grantingWords[word] {
			return true
		}
	}
	joined := " " + strings.Join(words, " ") + " "
	for _, phrase := range grantingPhrases {
		if strings.Contains(joined, " "+phrase+" ") {
			return true
		}
	}
	return false
}

// GateShaped reports whether a question must be answered in the app, never from a notification:
// one of crowsnest's own gates (IsGate), or any question whose option keys or labels, or title,
// allow, approve, merge, land, push, deploy, delete or grant (texts: the keys, labels and title).
// It is the one rule both the push notices (which then carry no buttons and no token) and the
// push action endpoint (which refuses) apply; the endpoint is the authority.
func GateShaped(context map[string]string, texts []string) bool {
	if IsGate(context) {
		return true
	}
	for _, text := range texts {
		if GrantingText(text) {
			return true
		}
	}
	return false
}

// QuestionTexts are the texts GateShaped reads for a question: each option's key and label, and
// the title.
func QuestionTexts(q Question) []string {
	texts := []string{q.Title}
	for _, option := range q.Options {
		texts = append(texts, option.Key, option.Label)
	}
	return texts
}

// CheckAgentContext refuses a question from an agent whose context carries a key crowsnest
// reserves for its own gates. The comparison ignores case and surrounding space, so a near-miss
// spelling that a human could mistake for the real key is refused too.
func CheckAgentContext(input AskInput) error {
	var bad []string
	for key := range input.Context {
		if reservedKey(key) {
			bad = append(bad, key)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return invalid("context key %s is reserved for crowsnest's own questions; use another name", strings.Join(bad, ", "))
}

// reservedPrefixes are whole families of keys crowsnest keeps for its gates (R6-I1): a future
// land_head or task_owner is reserved without another change here.
var reservedPrefixes = []string{"land_", "task_", "approval_", "crowsnest_"}

// reservedKey reports whether an agent may not set key (case and surrounding space ignored).
func reservedKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if reservedKeys[key] {
		return true
	}
	for _, prefix := range reservedPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
