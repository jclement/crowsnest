// Validation of questions and answers. Every rule lives here so the socket, the MCP tool and the
// browser all refuse the same things with the same words. Messages are for the agent or the human
// who sent the request: they say what to change.

package questions

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/termtext"
)

// Limits. They keep one question readable on a phone and bound what a misbehaving agent can put
// in the database.
const (
	MaxTitleRunes      = 200
	MaxBodyBytes       = 64 << 10
	MaxOptions         = 12
	MaxOptionKeyLen    = 32
	MaxOptionLabel     = 200
	MaxOptionDetail    = 1000
	MaxContextEntries  = 16
	MaxContextKeyLen   = 32
	MaxContextValueLen = 200
	MaxFiles           = 20
	MaxFileSideBytes   = 256 << 10 // per original, per final
	MaxFilenameLen     = 300
	MaxNoteBytes       = 8 << 10
	// DefaultExpiryHours applies when the asker gives none; MaxExpiryHours is a week.
	DefaultExpiryHours = 24.0
	MaxExpiryHours     = 168.0
	// MinExpiryHours is five minutes. An expiry is typed back to the asker, so a question that
	// expires at the next sweep was a way to have crowsnest type a line with no human involved
	// (R1b N2); five minutes also gives the human a chance to see it.
	MinExpiryHours = 5.0 / 60
	// MaxRequestBytes bounds a whole ask request: every file at its limit plus the rest.
	MaxRequestBytes = MaxFiles*2*MaxFileSideBytes + MaxBodyBytes + 256<<10
)

// ValidationError is a request the caller must fix. Its message is safe to show.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// IsValidation reports whether err is a ValidationError.
func IsValidation(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

var optionKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// normalise fills defaults into an ask (kind, urgency, confirm's yes/no, expiry) and validates
// it. It returns the input as it will be stored.
func normalise(input AskInput) (AskInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Kind == "" {
		input.Kind = defaultKind(input)
	}
	if input.Urgency == "" {
		input.Urgency = UrgencyNormal
	}
	if input.ExpiresInHours == 0 {
		input.ExpiresInHours = DefaultExpiryHours
	}
	if input.Kind == KindConfirm && len(input.Options) == 0 {
		input.Options = []Option{{Key: "yes", Label: "Yes"}, {Key: "no", Label: "No"}}
	}
	for _, check := range []func(AskInput) error{checkText, checkKind, checkOptions, checkRecommend, checkContext, checkFiles, checkExpiry} {
		if err := check(input); err != nil {
			return AskInput{}, err
		}
	}
	return input, nil
}

// defaultKind guesses a kind for an ask that named none: options mean a choice, none an ack.
func defaultKind(input AskInput) Kind {
	if len(input.Options) > 0 {
		return KindChoice
	}
	return KindAck
}

func checkText(input AskInput) error {
	switch {
	case input.Title == "":
		return invalid("title is required: one line saying what you need decided")
	case utf8.RuneCountInString(input.Title) > MaxTitleRunes:
		return invalid("title is longer than %d characters; put the detail in body_md", MaxTitleRunes)
	case strings.ContainsAny(input.Title, "\r\n"):
		return invalid("title must be one line; put the detail in body_md")
	case len(input.BodyMD) > MaxBodyBytes:
		return invalid("body_md is longer than %d KB; link to a file instead", MaxBodyBytes>>10)
	case !utf8.ValidString(input.Title) || !utf8.ValidString(input.BodyMD):
		return invalid("title and body_md must be UTF-8 text")
	case termtext.HasBidiControl(input.Title):
		return invalid("title holds a bidirectional control character, which makes it read differently from what it says; remove it")
	}
	return nil
}

func checkKind(input AskInput) error {
	switch input.Kind {
	case KindConfirm, KindChoice:
	case KindAck, KindManual, KindNotice:
		if len(input.Options) > 0 {
			return invalid("kind %q takes no options; use kind \"choice\" to offer options", input.Kind)
		}
	default:
		return invalid("kind must be one of confirm, choice, ack, manual, notice (got %q)", input.Kind)
	}
	if input.Multi && input.Kind != KindChoice {
		return invalid("multi is only for kind \"choice\"")
	}
	switch input.Urgency {
	case UrgencyLow, UrgencyNormal, UrgencyBlocking:
	default:
		return invalid("urgency must be low, normal or blocking (got %q)", input.Urgency)
	}
	return nil
}

func checkOptions(input AskInput) error {
	if input.Kind == KindChoice && len(input.Options) < 2 {
		return invalid("a choice needs at least two options")
	}
	if len(input.Options) > MaxOptions {
		return invalid("at most %d options; split the question", MaxOptions)
	}
	seen := map[string]bool{}
	for index, option := range input.Options {
		switch {
		case !optionKeyPattern.MatchString(option.Key) || len(option.Key) > MaxOptionKeyLen:
			return invalid("option %d: key %q must be 1-%d letters, digits, '_', '.' or '-'", index+1, option.Key, MaxOptionKeyLen)
		case seen[option.Key]:
			return invalid("option keys must be unique; %q appears twice", option.Key)
		case strings.TrimSpace(option.Label) == "":
			return invalid("option %q needs a label", option.Key)
		case utf8.RuneCountInString(option.Label) > MaxOptionLabel:
			return invalid("option %q: label is longer than %d characters; use detail", option.Key, MaxOptionLabel)
		case termtext.HasBidiControl(option.Label) || termtext.HasBidiControl(option.Detail):
			return invalid("option %q holds a bidirectional control character; remove it", option.Key)
		case utf8.RuneCountInString(option.Detail) > MaxOptionDetail:
			return invalid("option %q: detail is longer than %d characters", option.Key, MaxOptionDetail)
		}
		seen[option.Key] = true
	}
	return nil
}

func checkRecommend(input AskInput) error {
	needs := input.Kind == KindConfirm || input.Kind == KindChoice
	switch {
	case needs && input.Recommend == "":
		return invalid("recommend is required for kind %q: name the option key you would pick", input.Kind)
	case !needs && input.Recommend != "":
		return invalid("recommend is only for kinds confirm and choice")
	case needs && !hasKey(input.Options, input.Recommend):
		return invalid("recommend %q is not one of the option keys", input.Recommend)
	}
	return nil
}

func checkContext(input AskInput) error {
	if len(input.Context) > MaxContextEntries {
		return invalid("context has more than %d entries", MaxContextEntries)
	}
	for key, value := range input.Context {
		if key == "" || len(key) > MaxContextKeyLen || utf8.RuneCountInString(value) > MaxContextValueLen {
			return invalid("context %q: keys are 1-%d characters and values at most %d", key, MaxContextKeyLen, MaxContextValueLen)
		}
	}
	return nil
}

func checkFiles(input AskInput) error {
	if len(input.Files) > MaxFiles {
		return invalid("at most %d files", MaxFiles)
	}
	for index, file := range input.Files {
		switch {
		case strings.TrimSpace(file.Filename) == "" || len(file.Filename) > MaxFilenameLen:
			return invalid("file %d needs a filename of at most %d characters", index+1, MaxFilenameLen)
		case len(file.Original) > MaxFileSideBytes || len(file.Final) > MaxFileSideBytes:
			return invalid("file %q: original and final are limited to %d KB each", file.Filename, MaxFileSideBytes>>10)
		}
	}
	return nil
}

func checkExpiry(input AskInput) error {
	if input.ExpiresInHours < MinExpiryHours || input.ExpiresInHours > MaxExpiryHours {
		return invalid("expires_in_hours must be between %.3f (five minutes) and %.0f (a week)", MinExpiryHours, MaxExpiryHours)
	}
	return nil
}

func hasKey(options []Option, key string) bool {
	for _, option := range options {
		if option.Key == key {
			return true
		}
	}
	return false
}

// checkAnswer validates an answer against its question and returns the value to store (JSON)
// and the trimmed note.
func checkAnswer(q Question, answer AnswerInput) (json.RawMessage, string, error) {
	note := strings.TrimSpace(answer.Note)
	if len(note) > MaxNoteBytes {
		return nil, "", invalid("the note is longer than %d KB", MaxNoteBytes>>10)
	}
	switch q.Kind {
	case KindConfirm, KindChoice:
		value, err := checkChoice(q, answer.Value)
		return value, note, err
	case KindAck:
		return json.RawMessage(`"ack"`), note, nil
	case KindManual:
		if note == "" {
			return nil, "", invalid("this question wants a written answer; type it in the note")
		}
		return json.RawMessage(`null`), note, nil
	default: // notice: dismissing it
		return json.RawMessage(`"dismissed"`), note, nil
	}
}

// checkChoice accepts a key for a single choice, or an array of distinct keys for a multi.
func checkChoice(q Question, raw json.RawMessage) (json.RawMessage, error) {
	if q.Multi {
		var keys []string
		if err := json.Unmarshal(raw, &keys); err != nil || len(keys) == 0 {
			return nil, invalid("pick at least one option (answer_value is an array of option keys)")
		}
		seen := map[string]bool{}
		for _, key := range keys {
			if !hasKey(q.Options, key) || seen[key] {
				return nil, invalid("%q is not an option of this question, or is repeated", key)
			}
			seen[key] = true
		}
		return json.Marshal(keys)
	}
	var key string
	if err := json.Unmarshal(raw, &key); err != nil || key == "" {
		return nil, invalid("pick an option (answer_value is one option key)")
	}
	if !hasKey(q.Options, key) {
		return nil, invalid("%q is not an option of this question", key)
	}
	return json.Marshal(key)
}
