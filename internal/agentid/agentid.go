// Package agentid is who is calling the agent socket (R1b N2, N7). The socket used to believe an
// X-Crowsnest-Pane header the caller set from HERDR_PANE_ID, so any local process could claim to
// be any pane, including `orchestrator`, and have an "answer" typed into it. Now crowsnest mints a
// random token for each agent it starts, hands it to that agent only (CROWSNEST_TOKEN in the
// pane's environment and in its MCP server's), and keeps the token's SHA-256 mapped to the pane
// and job it was issued for. The socket derives the caller from the token alone; a caller without
// a known token is unidentified and its questions have nowhere to be typed back to.
//
// What this does and does not stop, plainly: a process that merely sets an environment variable
// cannot pick its identity any more. A process running as the same user that reads another
// agent's environment (/proc/<pid>/environ on Linux, `ps eww` on macOS) or the 0600 MCP config
// can still borrow that agent's token: same-uid processes are not a security boundary from each
// other, and only a separate uid would be (DESIGN.md section 4).
//
// Tokens live in the kv table (agent_token:<sha256 hex>), so they survive a crowsnest restart,
// as the agents holding them do. The orchestrator and the job launcher (internal/jobs) both mint
// them here: NewToken then Bind for a pane's environment (the pane id is known only after herdr
// creates it), Mint for a start.
//
// Replacing a pane's tokens is one step (R2-C15): Bind holds the registry's lock while it lists
// what to revoke, and deletes those and stores the new binding in one transaction, so neither two
// concurrent binds nor a crash can leave two "exclusive" start tokens valid. A start token for a
// pane whose pane token now belongs to another job is refused: that start is stale. Tokens end
// with the work (R2-L1): RevokeJob when a job is finished, RevokeGonePanes for panes herdr no
// longer has.
package agentid

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// EnvToken is the environment variable that carries an agent's token, and HeaderToken the
// request header the agent-side client sends it in.
const (
	EnvToken    = "CROWSNEST_TOKEN"
	HeaderToken = "X-Crowsnest-Token" //nolint:gosec // a header name, not a credential
)

// Kinds of binding: where the token was handed out. A pane token sits in the pane's environment
// for its lifetime (every process in the pane inherits it, `crowsnest ask` included); a start
// token is minted for one agent start and written into its --mcp-config (the MCP server's
// environment) and a 0600 token file its hooks read (`crowsnest hook --token-file`), so both know
// their agent even in a pane crowsnest did not create.
const (
	KindPane  = "pane"
	KindStart = "start"
)

const (
	kvPrefix    = "agent_token:"
	tokenPrefix = "cnt_" // recognisable in a leak, and by internal/redact's prefixed-token rule
	tokenBytes  = 32
	// maxTokenLen bounds what Resolve will hash; a real token is 47 characters.
	maxTokenLen = 128
)

// Binding is what a token stands for.
type Binding struct {
	PaneID    string    `json:"pane_id"`          // the herdr pane id answers are typed into
	JobID     string    `json:"job_id,omitempty"` // the job the agent was started for (M7); empty for the orchestrator
	Kind      string    `json:"kind"`             // KindPane or KindStart
	CreatedAt time.Time `json:"created_at"`
}

// KV is the persistence the registry needs (store.Store).
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
	ListKV(ctx context.Context, prefix string) (map[string]string, error)
	// ReplaceKV deletes remove and sets key, in one transaction.
	ReplaceKV(ctx context.Context, remove []string, key, value string) error
}

// ErrStaleStart refuses a start token for a pane that now belongs to another job.
var ErrStaleStart = errors.New("the pane now belongs to another job; this agent start is stale")

// Registry issues and resolves agent tokens. Build with New.
type Registry struct {
	kv  KV
	now func() time.Time
	// mu serialises every change to the bindings, so a bind's revoke-then-store cannot
	// interleave with another's.
	mu sync.Mutex
}

// New returns a registry over kv. now nil means time.Now.
func New(kv KV, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{kv: kv, now: now}
}

// NewToken returns a fresh random token that is not yet bound to anything. Use it when the token
// must exist before the pane does (herdr takes a pane's environment at creation), then Bind it to
// the pane id herdr returns.
func NewToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating an agent token: %w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Bind records that token stands for binding. A pane token bound to a pane first revokes every
// earlier token of that pane (the pane is new: anything bound to its id belonged to an older pane
// herdr has since reused the id for); a start token revokes the pane's earlier start tokens (the
// agent was restarted; its old MCP server and hooks are gone).
func (r *Registry) Bind(ctx context.Context, token string, binding Binding) error {
	if binding.PaneID == "" || token == "" {
		return errors.New("binding an agent token needs a token and a pane id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	all, err := r.list(ctx)
	if err != nil {
		return err
	}
	var remove []string
	for key, existing := range all {
		if existing.PaneID != binding.PaneID {
			continue
		}
		if binding.Kind == KindStart && existing.Kind == KindPane && existing.JobID != binding.JobID {
			return fmt.Errorf("binding a start token to pane %s for job %q (the pane's token is for %q): %w", binding.PaneID, binding.JobID, existing.JobID, ErrStaleStart)
		}
		if binding.Kind == KindPane || existing.Kind == binding.Kind {
			remove = append(remove, key)
		}
	}
	binding.CreatedAt = r.now().UTC()
	encoded, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("encoding an agent token binding: %w", err)
	}
	return r.kv.ReplaceKV(ctx, remove, kvPrefix+hash(token), string(encoded))
}

// list returns every stored binding by its kv key; damaged records are listed as empty bindings
// (they identify nobody, and a revoke of their pane cannot see them, so they are left alone).
func (r *Registry) list(ctx context.Context) (map[string]Binding, error) {
	all, err := r.kv.ListKV(ctx, kvPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing agent tokens: %w", err)
	}
	out := make(map[string]Binding, len(all))
	for key, value := range all {
		binding, _ := decodeBinding(value)
		out[key] = binding
	}
	return out, nil
}

// revokeWhere deletes, in one transaction, every binding match selects (r.mu held), and returns
// how many.
func (r *Registry) revokeWhere(ctx context.Context, match func(Binding) bool) (int, error) {
	all, err := r.list(ctx)
	if err != nil {
		return 0, err
	}
	var remove []string
	for key, binding := range all {
		if binding.PaneID != "" && match(binding) {
			remove = append(remove, key)
		}
	}
	if len(remove) == 0 {
		return 0, nil
	}
	if err := r.kv.ReplaceKV(ctx, remove, "", ""); err != nil {
		return 0, fmt.Errorf("revoking agent tokens: %w", err)
	}
	return len(remove), nil
}

// RevokeJob removes every token issued for jobID (R2-L1): a finished job's agent can no longer
// act, ask with its identity, or create jobs. The orchestrator's tokens name no job and stay.
func (r *Registry) RevokeJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.revokeWhere(ctx, func(b Binding) bool { return b.JobID == jobID })
	return err
}

// RevokeGonePanes removes the tokens of panes that no longer exist (live reports false) and
// were bound before cutoff (R2-L1): herdr reuses pane ids, and a token outliving its pane would
// let its holder have answers typed into whatever pane gets the id next. The cutoff spares a
// token bound a moment ago to a pane the caller's snapshot does not show yet.
func (r *Registry) RevokeGonePanes(ctx context.Context, live func(paneID string) bool, cutoff time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revokeWhere(ctx, func(b Binding) bool { return !live(b.PaneID) && b.CreatedAt.Before(cutoff) })
}

// Mint returns a new token bound to binding: NewToken then Bind.
func (r *Registry) Mint(ctx context.Context, binding Binding) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := r.Bind(ctx, token, binding); err != nil {
		return "", err
	}
	return token, nil
}

// Resolve returns what token stands for. ok is false for an empty, malformed or unknown token,
// which the caller treats as unidentified.
func (r *Registry) Resolve(ctx context.Context, token string) (Binding, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLen || !strings.HasPrefix(token, tokenPrefix) {
		return Binding{}, false, nil
	}
	value, err := r.kv.GetKV(ctx, kvPrefix+hash(token))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Binding{}, false, nil
	case err != nil:
		return Binding{}, false, fmt.Errorf("looking up an agent token: %w", err)
	}
	binding, ok := decodeBinding(value)
	return binding, ok, nil
}

// decodeBinding reads a stored binding; a damaged record identifies nobody.
func decodeBinding(value string) (Binding, bool) {
	var binding Binding
	if json.Unmarshal([]byte(value), &binding) != nil || binding.PaneID == "" {
		return Binding{}, false
	}
	return binding, true
}

// RevokePane removes every token bound to paneID, or only those of kind when it is not empty.
func (r *Registry) RevokePane(ctx context.Context, paneID, kind string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.revokeWhere(ctx, func(b Binding) bool { return b.PaneID == paneID && (kind == "" || b.Kind == kind) })
	return err
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ReadTokenFile returns the token in a token file (first line), for `crowsnest hook`.
func ReadTokenFile(path string) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // a path crowsnest wrote into the hook command
	if err != nil {
		return "", fmt.Errorf("reading the agent token file: %w", err)
	}
	line, _, _ := strings.Cut(string(body), "\n")
	return strings.TrimSpace(line), nil
}
