// Composing the layers an agent is started with, recording that start, and telling whether the
// layers have changed since (DESIGN.md 21.3).
//
// The composed text goes into the prompt file crowsnest already writes for the agent
// (--append-system-prompt-file), so nothing lands in a repository or the user's own settings.
// The manifest lists every layer that applies to the agent, empty ones included, so a layer
// set after the start (a global text written later) still shows as a change.

package instructions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/store"
)

// Kinds of agent a composition is for.
const (
	KindJob          = "job"
	KindOrchestrator = "orchestrator"
	KindLead         = "lead"
)

// Subject names the agent a composition is for.
type Subject struct {
	Kind        string // job | orchestrator | lead
	Subject     string // "j_12", "orchestrator" or "lead:<slug>": the agent_layers key
	Role        string // the role layer to include; "" for none
	ProjectID   int64  // the project layer to include; 0 for none
	ProjectName string // for the heading; "" looks it up
	BriefMD     string // a job's brief, listed in the manifest (it travels as brief.md)
}

// ManifestLayer is one layer an agent was started with.
type ManifestLayer struct {
	Layer    string `json:"layer"`    // for display: "global", "role builder", "project crowsnest", "job j_12"
	Scope    string `json:"scope"`    // global | role | project | job
	Key      string `json:"key"`      // as in the layer's scope; the job id for job
	Revision int    `json:"revision"` // the saved revision in effect, 0 for none, built-in, file or brief
	Source   string `json:"source"`   // none | builtin | db | file | brief
	Size     int    `json:"size"`     // bytes
	Hash     string `json:"hash"`     // sha256 of the layer's text, hex; "" for an empty layer
}

// Manifest is what an agent was started with: its layers and a hash of the composed text.
type Manifest struct {
	Hash   string          `json:"hash"`
	Layers []ManifestLayer `json:"layers"`
}

// subjectPattern is what an agent_layers subject may be.
var subjectPattern = regexp.MustCompile(`^(j_[1-9][0-9]{0,18}|orchestrator|lead:[a-z0-9][a-z0-9-]{0,63})$`)

// ValidSubject reports whether subject names an agent: j_<n>, orchestrator or lead:<slug>.
func ValidSubject(subject string) bool { return subjectPattern.MatchString(subject) }

// Compose returns the markdown crowsnest adds to the agent's prompt (the global, role and
// project layers, in that order, each under its own heading; empty layers are left out) and the
// manifest of every layer that applies, the job's brief included.
func (s *Service) Compose(ctx context.Context, subject Subject) (string, Manifest, error) {
	roles := s.roleSet()
	var sections []string
	var manifest Manifest
	for _, target := range targetsOf(subject) {
		layer, err := s.resolve(ctx, roles, target.scope, target.key)
		if err != nil {
			return "", Manifest{}, fmt.Errorf("reading the %s layer: %w", target.scope, err)
		}
		label := s.layerLabel(ctx, subject, target)
		manifest.Layers = append(manifest.Layers, manifestEntry(label, target, layer.Source, layer.Revision, layer.Text))
		if section := sectionOf(target, label, layer.Text); section != "" {
			sections = append(sections, section)
		}
	}
	if subject.Kind == KindJob {
		manifest.Layers = append(manifest.Layers, manifestEntry("job "+subject.Subject,
			layerTarget{scope: ScopeJob, key: subject.Subject}, SourceBrief, 0, subject.BriefMD))
	}
	text := strings.Join(sections, "\n\n")
	manifest.Hash = hashOf(text)
	return text, manifest, nil
}

// layerTarget is one stored layer that applies to a subject.
type layerTarget struct{ scope, key string }

// targetsOf lists the stored layers that apply to a subject, in composition order.
func targetsOf(subject Subject) []layerTarget {
	targets := []layerTarget{{scope: ScopeGlobal}}
	if subject.Role != "" {
		targets = append(targets, layerTarget{scope: ScopeRole, key: subject.Role})
	}
	if subject.ProjectID > 0 {
		targets = append(targets, layerTarget{scope: ScopeProject, key: strconv.FormatInt(subject.ProjectID, 10)})
	}
	return targets
}

func (s *Service) layerLabel(ctx context.Context, subject Subject, target layerTarget) string {
	switch target.scope {
	case ScopeRole:
		return "role " + target.key
	case ScopeProject:
		name := subject.ProjectName
		if name == "" {
			name = s.projectName(ctx, target.key)
		}
		return "project " + name
	}
	return target.scope
}

// sectionOf is one layer's part of the prompt. The human's layers get a heading saying they are
// the human's; a role's text keeps its own heading ("# Role: builder") when it has one.
func sectionOf(target layerTarget, label, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch target.scope {
	case ScopeGlobal:
		return "## Instructions from the human (global)\n\n" + text
	case ScopeProject:
		return "## Instructions from the human (" + label + ")\n\n" + text
	}
	if strings.HasPrefix(text, "#") {
		return text
	}
	return "# Role: " + target.key + "\n\n" + text
}

func manifestEntry(label string, target layerTarget, source string, revision int, text string) ManifestLayer {
	entry := ManifestLayer{Layer: label, Scope: target.scope, Key: target.key, Revision: revision, Source: source, Size: len(text)}
	if strings.TrimSpace(text) != "" {
		entry.Hash = hashOf(text)
	}
	return entry
}

func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ---- Recording a start ---------------------------------------------------------------------

// RecordStart replaces the subject's record of what it was started with.
func (s *Service) RecordStart(ctx context.Context, subject string, manifest Manifest) error {
	body, err := json.Marshal(manifest.Layers)
	if err != nil {
		return fmt.Errorf("encoding the layers of %s: %w", subject, err)
	}
	return s.opts.Store.SetAgentLayers(ctx, store.AgentLayers{Subject: subject, StartedAt: s.opts.Now(), Hash: manifest.Hash, LayersJSON: string(body)})
}

// JobLayers implements jobs.InstructionLayers: the composition for a job's agent, recorded
// under the job's id when record is set.
func (s *Service) JobLayers(ctx context.Context, job jobs.Job, record bool) (string, error) {
	text, manifest, err := s.Compose(ctx, Subject{Kind: KindJob, Subject: job.ID, Role: job.Role, ProjectID: job.ProjectID, BriefMD: job.BriefMD})
	if err != nil {
		return "", err
	}
	if record {
		if err := s.RecordStart(ctx, job.ID, manifest); err != nil {
			return "", err
		}
	}
	return text, nil
}

// ForStandingAgent composes and records the layers for a standing agent: the orchestrator
// (subject "orchestrator", projectID 0) or a project's lead (subject "lead:<slug>"). It
// carries the global layer and, with a project, that project's layer; no role layer, because a
// standing agent's own template already says what it is. projectName may be "" (it is looked
// up). The caller puts the text into the agent's prompt file at every start.
func (s *Service) ForStandingAgent(ctx context.Context, subject string, projectID int64, projectName string) (string, Manifest, error) {
	if !ValidSubject(subject) || strings.HasPrefix(subject, "j_") {
		return "", Manifest{}, failure(ErrInvalid, "A standing agent is \"orchestrator\" or \"lead:<slug>\", not %q.", subject)
	}
	kind := KindOrchestrator
	if strings.HasPrefix(subject, "lead:") {
		kind = KindLead
	}
	text, manifest, err := s.Compose(ctx, Subject{Kind: kind, Subject: subject, ProjectID: projectID, ProjectName: projectName})
	if err != nil {
		return "", Manifest{}, err
	}
	if err := s.RecordStart(ctx, subject, manifest); err != nil {
		return "", Manifest{}, err
	}
	return text, manifest, nil
}

// ---- Changed since start -------------------------------------------------------------------

// Change is one layer that differs from what the agent was started with.
type Change struct {
	Layer       string `json:"layer"`
	Scope       string `json:"scope"`
	Key         string `json:"key"`
	WasSource   string `json:"was_source"`
	NowSource   string `json:"now_source"` // "missing" for a role that no longer exists
	WasRevision int    `json:"was_revision"`
	NowRevision int    `json:"now_revision"`
}

// Recorded returns the manifest the subject was last started with, or ErrNotFound.
func (s *Service) Recorded(ctx context.Context, subject string) (store.AgentLayers, []ManifestLayer, error) {
	record, err := s.opts.Store.GetAgentLayers(ctx, subject)
	if errors.Is(err, store.ErrNotFound) {
		return store.AgentLayers{}, nil, failure(ErrNotFound, "Crowsnest has no record of what %s was started with.", subject)
	}
	if err != nil {
		return store.AgentLayers{}, nil, err
	}
	var layers []ManifestLayer
	if err := json.Unmarshal([]byte(record.LayersJSON), &layers); err != nil {
		return store.AgentLayers{}, nil, fmt.Errorf("reading the layers of %s: %w", subject, err)
	}
	return record, layers, nil
}

// Changed compares each stored layer the subject was started with against the layer as it
// applies now. A subject with no record has not changed (nothing to compare).
func (s *Service) Changed(ctx context.Context, subject string) (bool, []Change, error) {
	_, layers, err := s.Recorded(ctx, subject)
	if errors.Is(err, ErrNotFound) {
		return false, []Change{}, nil
	}
	if err != nil {
		return false, nil, err
	}
	return s.diff(ctx, layers)
}

func (s *Service) diff(ctx context.Context, layers []ManifestLayer) (bool, []Change, error) {
	roles := s.roleSet()
	changes := []Change{}
	for _, was := range layers {
		if was.Scope == ScopeJob {
			continue // the brief does not change after the start
		}
		change := Change{Layer: was.Layer, Scope: was.Scope, Key: was.Key, WasSource: was.Source, WasRevision: was.Revision}
		now, err := s.resolve(ctx, roles, was.Scope, was.Key)
		switch {
		case errors.Is(err, ErrNotFound):
			change.NowSource = "missing"
			changes = append(changes, change)
			continue
		case err != nil:
			return false, nil, err
		}
		current := manifestEntry(was.Layer, layerTarget{scope: was.Scope, key: was.Key}, now.Source, now.Revision, now.Text)
		if current.Hash != was.Hash {
			change.NowSource, change.NowRevision = current.Source, current.Revision
			changes = append(changes, change)
		}
	}
	return len(changes) > 0, changes, nil
}
