// Projects on the agent socket (DESIGN.md 21.2): the orchestrator, the session's front door,
// lists the projects and their leads and reads one before handing it a job (job_create with
// project). Read-only; any caller may read, as with jobs (R2-I3).
//
//	GET /api/v1/projects          {text}: every live project, its lead and its job counts
//	GET /api/v1/projects/{ref}    {text}: one project (id or slug), its defaults and live jobs

package agentapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProjectReader renders projects for agents (serve adapts internal/projects to it).
type ProjectReader interface {
	ProjectsText(ctx context.Context) (string, error)
	ProjectText(ctx context.Context, ref string) (string, error)
	// ProjectID resolves a project's id or slug to its id (job_create's project argument).
	ProjectID(ctx context.Context, ref string) (int64, error)
}

// ErrNoProject is a ProjectReader's answer for a ref that names no project.
var ErrNoProject = errors.New("no such project")

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Projects are not available in this server.")
		return
	}
	text, err := s.projects.ProjectsText(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "The projects could not be read; try again.")
		return
	}
	writeData(w, http.StatusOK, ContextResponse{Text: text})
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Projects are not available in this server.")
		return
	}
	text, err := s.projects.ProjectText(r.Context(), r.PathValue("ref"))
	var id int64
	if err == nil {
		id, err = s.projects.ProjectID(r.Context(), r.PathValue("ref"))
	}
	switch {
	case errors.Is(err, ErrNoProject):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "No project "+r.PathValue("ref")+"; project_list shows them.")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "The project could not be read; try again.")
	default:
		writeData(w, http.StatusOK, ProjectResponse{Text: text, ID: id})
	}
}

// ProjectResponse is GET /projects/{ref}: the project rendered for an agent, and its id.
type ProjectResponse struct {
	Text string `json:"text"`
	ID   int64  `json:"id"`
}

// Projects is project_list.
func (c *Client) Projects(ctx context.Context) (string, error) {
	var out ContextResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/projects", nil, &out)
	return out.Text, err
}

// Project is project_get.
func (c *Client) Project(ctx context.Context, ref string) (string, error) {
	var out ProjectResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(ref), nil, &out)
	return out.Text, err
}

// ProjectID resolves a project's short name or id to its id.
func (c *Client) ProjectID(ctx context.Context, ref string) (int64, error) {
	var out ProjectResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(ref), nil, &out)
	return out.ID, err
}

// ProjectGetArgs are project_get's arguments.
type ProjectGetArgs struct {
	Project string `json:"project" jsonschema:"the project's short name (slug) or id, as project_list shows it"`
}

const (
	projectListDescription = "List the projects: each one's short name, directory, its lead (the standing agent that takes the project's jobs) and whether it is running, and its jobs that need the human or are running. " +
		"Give a project work with job_create(project=<short name>): with no dir it goes to the project's lead; with to=\"job\" (or a dir inside the project) a job agent runs it directly."
	projectGetDescription = "Read one project: its directory, defaults (role, engine, model), lead and live jobs."
)

func init() {
	registerTool("project_list", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "project_list", Title: "List projects", Description: projectListDescription, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				text, err := client.Projects(ctx)
				if err != nil {
					return errorResult(toolError("The projects could not be read", err)), nil, nil
				}
				return textResult(text), nil, nil
			})
	})
	registerTool("project_get", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "project_get", Title: "Read a project", Description: projectGetDescription, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(ctx context.Context, _ *mcp.CallToolRequest, args ProjectGetArgs) (*mcp.CallToolResult, any, error) {
				text, err := client.Project(ctx, args.Project)
				if err != nil {
					return errorResult(toolError("The project could not be read", err)), nil, nil
				}
				return textResult(text), nil, nil
			})
	})
}
