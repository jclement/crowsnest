// What Settings shows of the layers: every layer with where its text comes from (GET
// /instructions), and one layer with its kept revisions (GET /instructions/{scope}/{key}).
// Crowsnest serves the text of its own layers only (role override files included: they are
// crowsnest's configuration); the user's own CLAUDE.md files are never served (DESIGN.md 21.3).

package instructions

import (
	"context"
	"strconv"
	"time"
)

// Layer is one layer as Settings shows it.
type Layer struct {
	Scope string `json:"scope"` // global | role | project
	Key   string `json:"key"`   // '' (global), the role name, or the project id
	Name  string `json:"name"`  // for display: "Global", the role name, the project's name
	// Source is where EffectiveMD comes from: none | builtin | db | file.
	Source      string `json:"source"`
	EffectiveMD string `json:"effective_md"` // the text an agent started now gets
	SavedMD     string `json:"saved_md"`     // the app's saved text ("" when none), even when a file wins
	BuiltinMD   string `json:"builtin_md"`   // a built-in role's text; "" otherwise
	FilePath    string `json:"file_path"`    // the override file, when Source is file
	// Editable is false for a role whose file wins: saving would change nothing.
	Editable  bool       `json:"editable"`
	Revision  int        `json:"revision"` // the saved revision, 0 when nothing is saved
	UpdatedAt *time.Time `json:"updated_at"`
	Size      int        `json:"size"` // bytes of EffectiveMD
}

// Listing is GET /instructions.
type Listing struct {
	Global   Layer   `json:"global"`
	Roles    []Layer `json:"roles"`
	Projects []Layer `json:"projects"`
	MaxBytes int     `json:"max_bytes"`
}

// Revision is one kept version of a layer.
type Revision struct {
	Revision  int       `json:"revision"`
	BodyMD    string    `json:"body_md"`
	Deleted   bool      `json:"deleted"` // a reset (role) or a clear (global, project)
	CreatedAt time.Time `json:"created_at"`
	Size      int       `json:"size"`
}

// Detail is one layer with its revisions, newest first.
type Detail struct {
	Layer     Layer      `json:"layer"`
	Revisions []Revision `json:"revisions"`
}

// List returns every layer: global, each role (built-ins first), and each live project (an
// archived one only while it still has text saved).
func (s *Service) List(ctx context.Context) (Listing, error) {
	roles := s.roleSet()
	out := Listing{Roles: []Layer{}, Projects: []Layer{}, MaxBytes: MaxBodyBytes}
	global, err := s.resolve(ctx, roles, ScopeGlobal, "")
	if err != nil {
		return Listing{}, err
	}
	out.Global = layerView(ScopeGlobal, "", "Global", global)
	for _, name := range roles.Names() {
		role, err := s.resolve(ctx, roles, ScopeRole, name)
		if err != nil {
			return Listing{}, err
		}
		out.Roles = append(out.Roles, layerView(ScopeRole, name, name, role))
	}
	projects, err := s.opts.Store.ListProjects(ctx, true)
	if err != nil {
		return Listing{}, err
	}
	for _, project := range projects {
		key := strconv.FormatInt(project.ID, 10)
		layer, err := s.resolve(ctx, roles, ScopeProject, key)
		if err != nil {
			return Listing{}, err
		}
		if !project.ArchivedAt.IsZero() && layer.Source == SourceNone {
			continue
		}
		out.Projects = append(out.Projects, layerView(ScopeProject, key, project.Name, layer))
	}
	return out, nil
}

// Get returns one layer with its kept revisions.
func (s *Service) Get(ctx context.Context, scope, key string) (Detail, error) {
	roles, err := s.checkTarget(ctx, scope, key)
	if err != nil {
		return Detail{}, err
	}
	layer, err := s.resolve(ctx, roles, scope, key)
	if err != nil {
		return Detail{}, err
	}
	detail := Detail{Layer: layerView(scope, key, s.displayName(ctx, scope, key), layer), Revisions: []Revision{}}
	revisions, err := s.opts.Store.ListInstructionRevisions(ctx, scope, key)
	if err != nil {
		return Detail{}, err
	}
	for _, r := range revisions {
		detail.Revisions = append(detail.Revisions, Revision{Revision: r.Revision, BodyMD: r.BodyMD, Deleted: r.Deleted, CreatedAt: r.CreatedAt, Size: len(r.BodyMD)})
	}
	return detail, nil
}

// displayName names a layer for a person.
func (s *Service) displayName(ctx context.Context, scope, key string) string {
	switch scope {
	case ScopeGlobal:
		return "Global"
	case ScopeProject:
		return s.projectName(ctx, key)
	}
	return key
}

// projectName is a project's name, or "project <id>" when it cannot be read.
func (s *Service) projectName(ctx context.Context, key string) string {
	if id, err := strconv.ParseInt(key, 10, 64); err == nil {
		if project, err := s.opts.Store.GetProject(ctx, id); err == nil {
			return project.Name
		}
	}
	return "project " + key
}

func layerView(scope, key, name string, layer resolved) Layer {
	view := Layer{
		Scope: scope, Key: key, Name: name, Source: layer.Source, EffectiveMD: layer.Text, SavedMD: layer.Saved,
		BuiltinMD: layer.Builtin, FilePath: layer.FilePath, Editable: layer.Source != SourceFile,
		Revision: layer.Revision, Size: len(layer.Text),
	}
	if !layer.UpdatedAt.IsZero() {
		at := layer.UpdatedAt
		view.UpdatedAt = &at
	}
	return view
}
