// Projects for agents (DESIGN.md 21.2): the text the orchestrator reads through project_list and
// project_get (and `crowsnest project list|get`), and the CLI commands themselves.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/termtext"
)

// projectReader renders projects for the agent socket.
type projectReader struct {
	w    *projectWiring
	jobs *jobs.Service
}

func (p projectReader) leadLine(ctx context.Context, project projects.Project) string {
	leads := p.w.leads.Load()
	if leads == nil {
		return "off"
	}
	status, err := leads.Status(ctx, project.ID)
	if err != nil || !status.Running {
		return "off (start policy: " + project.LeadStart + ")"
	}
	return fmt.Sprintf("%s, agent %s", status.State, status.Name)
}

func (p projectReader) ProjectsText(ctx context.Context) (string, error) {
	list, err := p.w.service.List(ctx, false)
	if err != nil {
		return "", err
	}
	counts, _ := p.w.service.JobCounts(ctx)
	if len(list) == 0 {
		return "No projects yet. The human creates them in the web app (Projects).\n", nil
	}
	var out strings.Builder
	out.WriteString("# Projects\n\n")
	for _, project := range list {
		c := counts[project.ID]
		fmt.Fprintf(&out, "- **%s** (`%s`, id %d): %s\n  lead: %s; jobs: %d need the human, %d running\n",
			termtext.Clean(project.Name), project.Slug, project.ID, project.Dir, p.leadLine(ctx, project), c.NeedsYou, c.Running)
	}
	c := counts[0]
	fmt.Fprintf(&out, "\nUnfiled (yours): %d need the human, %d running.\n", c.NeedsYou, c.Running)
	return out.String(), nil
}

func (p projectReader) ProjectText(ctx context.Context, ref string) (string, error) {
	project, err := p.w.service.Get(ctx, ref)
	if errors.Is(err, projects.ErrNotFound) {
		return "", agentapi.ErrNoProject
	}
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# Project %s (`%s`, id %d)\n\n", termtext.Clean(project.Name), project.Slug, project.ID)
	fmt.Fprintf(&out, "- Directory: %s\n- Lead: %s\n", project.Dir, p.leadLine(ctx, project))
	if project.Description != "" {
		fmt.Fprintf(&out, "- About: %s\n", termtext.Clean(project.Description))
	}
	fmt.Fprintf(&out, "- Defaults: role %s, engine %s, model %s\n", orNone(project.DefaultRole), orNone(project.DefaultEngine), orNone(project.DefaultModel))
	if project.Archived {
		out.WriteString("- Archived: it takes no jobs.\n")
	}
	if p.jobs != nil {
		live, _ := p.jobs.List(ctx, []jobs.State{jobs.StateQueued, jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked, jobs.StateInReview})
		var lines []string
		for _, job := range live {
			if job.ProjectID == project.ID {
				lines = append(lines, fmt.Sprintf("- %s (%s): %s", job.ID, job.State, termtext.Quote(job.Title)))
			}
		}
		if len(lines) > 0 {
			out.WriteString("\n## Live jobs\n\n" + strings.Join(lines, "\n") + "\n")
		}
	}
	return out.String(), nil
}

func (p projectReader) ProjectID(ctx context.Context, ref string) (int64, error) {
	project, err := p.w.service.Get(ctx, ref)
	if errors.Is(err, projects.ErrNotFound) {
		return 0, agentapi.ErrNoProject
	}
	return project.ID, err
}

func orNone(value string) string {
	if value == "" {
		return "(default)"
	}
	return value
}

// ---- crowsnest project list|get ------------------------------------------------------------

func newProjectCommand() *cobra.Command {
	command := &cobra.Command{Use: "project", Short: "List projects and read one (for agents and scripts; the agent socket)"}
	command.AddCommand(&cobra.Command{
		Use: "list", Short: "List projects with their leads and job counts", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			text, err := client.Projects(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}, &cobra.Command{
		Use: "get <slug or id>", Short: "Print one project: directory, defaults, lead and live jobs", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			text, err := client.Project(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	})
	return command
}
