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
// as the agents holding them do. M7's job launcher uses the same registry (see Mint and Bind).
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
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// EnvToken is the environment variable that carries an agent's token, and HeaderToken the
// request header the agent-side client sends it in.
const (
	EnvToken    = "CROWSNEST_TOKEN"
	HeaderToken = "X-Crowsnest-Token"
)

// Kinds of binding: where the token was handed out. A pane token sits in the pane's environment
// for its lifetime (every process in the pane inherits it, `crowsnest ask` included); an MCP token
// is written into the --mcp-config of one agent start, so the MCP server knows its agent even in a
// pane crowsnest did not create.
const (
	KindPane = "pane"
	KindMCP  = "mcp"
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
	Kind      string    `json:"kind"`             // KindPane or KindMCP
	CreatedAt time.Time `json:"created_at"`
}

// KV is the persistence the registry needs (store.Store).
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
	ListKV(ctx context.Context, prefix string) (map[string]string, error)
}

// Registry issues and resolves agent tokens. Build with New.
type Registry struct {
	kv  KV
	now func() time.Time
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
// herdr has since reused the id for); an MCP token revokes the pane's earlier MCP tokens (the
// agent was restarted, the old MCP server is gone).
func (r *Registry) Bind(ctx context.Context, token string, binding Binding) error {
	if binding.PaneID == "" || token == "" {
		return errors.New("binding an agent token needs a token and a pane id")
	}
	revokeKind := binding.Kind
	if binding.Kind == KindPane {
		revokeKind = ""
	}
	if err := r.RevokePane(ctx, binding.PaneID, revokeKind); err != nil {
		return err
	}
	binding.CreatedAt = r.now().UTC()
	encoded, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("encoding an agent token binding: %w", err)
	}
	return r.kv.SetKV(ctx, kvPrefix+hash(token), string(encoded))
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
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Binding{}, false, nil
		}
		return Binding{}, false, fmt.Errorf("looking up an agent token: %w", err)
	}
	var binding Binding
	if err := json.Unmarshal([]byte(value), &binding); err != nil || binding.PaneID == "" {
		return Binding{}, false, nil
	}
	return binding, true, nil
}

// RevokePane removes every token bound to paneID, or only those of kind when it is not empty.
func (r *Registry) RevokePane(ctx context.Context, paneID, kind string) error {
	all, err := r.kv.ListKV(ctx, kvPrefix)
	if err != nil {
		return fmt.Errorf("listing agent tokens: %w", err)
	}
	for key, value := range all {
		var binding Binding
		if json.Unmarshal([]byte(value), &binding) != nil || binding.PaneID != paneID || (kind != "" && binding.Kind != kind) {
			continue
		}
		if err := r.kv.DeleteKV(ctx, key); err != nil {
			return fmt.Errorf("revoking an agent token: %w", err)
		}
	}
	return nil
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
