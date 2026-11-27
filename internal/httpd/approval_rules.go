// The approval policy in the browser API (DESIGN.md 8.7, "Policy").
//
//	GET    /api/v1/projects/{ref}/approval-rules           the project's rules, each with what it matched recently
//	POST   /api/v1/projects/{ref}/approval-rules           {kind, pattern, max_risk?, note?}: add one
//	POST   /api/v1/projects/{ref}/approval-rules/preview   {kind, pattern, max_risk?}: what it would have matched
//	DELETE /api/v1/approval-rules/{id}                     remove (revoke) a project or job rule
//	GET    /api/v1/jobs/{id}/approval-rules                a job's remembered rules
//	POST   /api/v1/approvals/batch-allow                   {ids}: allow several low-risk calls of one job
//
// All of them need the browser session (and Origin and X-Crowsnest on a write), like every
// browser route. None exists on the agent socket: an agent must not be able to widen its own
// policy. Every change is journalled (kind approval).

package httpd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/risk"
)

// RuleRequest is the body of POST /projects/{ref}/approval-rules (and its preview).
type RuleRequest struct {
	Kind    string     `json:"kind"`
	Pattern string     `json:"pattern"`
	MaxRisk risk.Level `json:"max_risk"`
	Note    string     `json:"note"`
}

// RuleView is a rule as the project page lists it: the rule and what it matched recently.
type RuleView struct {
	approvals.Rule
	Recent *approvals.Preview `json:"recent,omitempty"`
}

// BatchAllowRequest is the body of POST /approvals/batch-allow.
type BatchAllowRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) registerApprovalRuleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/projects/{ref}/approval-rules", s.requireAuth(http.HandlerFunc(s.handleProjectRules)))
	mux.Handle("POST /api/v1/projects/{ref}/approval-rules", s.requireAuth(http.HandlerFunc(s.handleProjectRuleAdd)))
	mux.Handle("POST /api/v1/projects/{ref}/approval-rules/preview", s.requireAuth(http.HandlerFunc(s.handleProjectRulePreview)))
	mux.Handle("DELETE /api/v1/approval-rules/{id}", s.requireAuth(http.HandlerFunc(s.handleRuleRevoke)))
	mux.Handle("GET /api/v1/jobs/{id}/approval-rules", s.requireAuth(http.HandlerFunc(s.handleJobRules)))
	mux.Handle("POST /api/v1/approvals/batch-allow", s.requireAuth(http.HandlerFunc(s.handleBatchAllow)))
}

// ruleProject resolves {ref} for a rule route, writing the error when it cannot.
func (s *Server) ruleProject(w http.ResponseWriter, r *http.Request) (*approvals.Service, int64, string, bool) {
	service := s.approvalService(w)
	if service == nil {
		return nil, 0, "", false
	}
	if s.deps.Projects == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Projects are not available on this server.")
		return nil, 0, "", false
	}
	project, ok := s.findProject(w, r)
	if !ok {
		return nil, 0, "", false
	}
	return service, project.ID, project.Name, true
}

func (s *Server) handleProjectRules(w http.ResponseWriter, r *http.Request) {
	service, projectID, _, ok := s.ruleProject(w, r)
	if !ok {
		return
	}
	rules, err := service.ListRules(r.Context(), approvals.RuleFilter{ProjectID: projectID})
	if err != nil {
		s.internalError(w, r, "listing approval rules", err)
		return
	}
	views := make([]RuleView, 0, len(rules))
	for _, rule := range rules {
		view := RuleView{Rule: rule}
		if preview, err := service.PreviewRule(r.Context(), projectID, rule.Kind, rule.Pattern, rule.MaxRisk); err == nil {
			view.Recent = &preview
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, views)
}

func decodeRule(w http.ResponseWriter, r *http.Request) (RuleRequest, bool) {
	var request RuleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send the rule as JSON: {kind: bash|edit|tool, pattern, max_risk: low|elevated, note}.")
		return RuleRequest{}, false
	}
	return request, true
}

func (s *Server) handleProjectRuleAdd(w http.ResponseWriter, r *http.Request) {
	service, projectID, projectName, ok := s.ruleProject(w, r)
	if !ok {
		return
	}
	request, ok := decodeRule(w, r)
	if !ok {
		return
	}
	rule, err := service.AddProjectRule(r.Context(), projectID, approvals.RuleInput{Kind: request.Kind, Pattern: request.Pattern, MaxRisk: request.MaxRisk, Note: request.Note})
	if err != nil {
		s.writePolicyError(w, err)
		return
	}
	s.deps.Journal.Add(r.Context(), journal.KindApproval, fmt.Sprintf("You added rule %s for project %s: %s%s", rule.ID, projectName, rule.Sentence, elevatedNote(rule)),
		map[string]any{"rule": rule.ID, "project_id": projectID, "kind": rule.Kind, "pattern": rule.Pattern, "max_risk": rule.MaxRisk})
	view := RuleView{Rule: rule}
	if preview, err := service.PreviewRule(r.Context(), projectID, rule.Kind, rule.Pattern, rule.MaxRisk); err == nil {
		view.Recent = &preview
	}
	writeData(w, http.StatusCreated, view)
}

func (s *Server) handleProjectRulePreview(w http.ResponseWriter, r *http.Request) {
	service, projectID, _, ok := s.ruleProject(w, r)
	if !ok {
		return
	}
	request, ok := decodeRule(w, r)
	if !ok {
		return
	}
	preview, err := service.PreviewRule(r.Context(), projectID, request.Kind, request.Pattern, request.MaxRisk)
	if err != nil {
		s.writePolicyError(w, err)
		return
	}
	writeData(w, http.StatusOK, preview)
}

func (s *Server) handleRuleRevoke(w http.ResponseWriter, r *http.Request) {
	service := s.approvalService(w)
	if service == nil {
		return
	}
	rule, err := service.RevokeRule(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writePolicyError(w, err)
		return
	}
	where := "job " + rule.JobID
	if rule.Scope == approvals.ScopeProject {
		where = s.projectLabel(r, rule.ProjectID)
	}
	s.deps.Journal.Add(r.Context(), journal.KindApproval, fmt.Sprintf("You removed rule %s for %s: %s", rule.ID, where, rule.Sentence),
		map[string]any{"rule": rule.ID, "scope": rule.Scope, "project_id": rule.ProjectID, "job": rule.JobID, "pattern": rule.Pattern})
	writeData(w, http.StatusOK, rule)
}

func (s *Server) handleJobRules(w http.ResponseWriter, r *http.Request) {
	service := s.approvalService(w)
	if service == nil {
		return
	}
	rules, err := service.ListRules(r.Context(), approvals.RuleFilter{JobID: r.PathValue("id")})
	if err != nil {
		s.internalError(w, r, "listing a job's approval rules", err)
		return
	}
	writeData(w, http.StatusOK, rules)
}

func (s *Server) handleBatchAllow(w http.ResponseWriter, r *http.Request) {
	service := s.approvalService(w)
	if service == nil {
		return
	}
	var request BatchAllowRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with ids: the approvals to allow.")
		return
	}
	allowed, err := service.BatchAllow(r.Context(), request.IDs)
	if err != nil {
		s.writePolicyError(w, err)
		return
	}
	ids := make([]string, 0, len(allowed))
	summaries := make([]string, 0, len(allowed))
	for _, approval := range allowed {
		ids = append(ids, approval.ID)
		summaries = append(summaries, approval.Summary)
	}
	job := ""
	if len(allowed) > 0 {
		job = allowed[0].JobID
	}
	s.deps.Journal.Add(r.Context(), journal.KindAnswer, fmt.Sprintf("You allowed %d low-risk calls from %s in one batch: %s", len(allowed), job, strings.Join(ids, ", ")),
		map[string]any{"approvals": ids, "summaries": summaries, "job": job, "decision": "allowed", "via": approvals.ViaBrowser})
	writeData(w, http.StatusOK, allowed)
}

// journalRemember records "Allow and remember for this job".
func (s *Server) journalRemember(r *http.Request, approval approvals.Approval, pattern string) {
	s.deps.Journal.Add(r.Context(), journal.KindApproval, fmt.Sprintf("You allowed %s and remembered `%s` for job %s: %s", approval.ID, strings.TrimSpace(pattern), approval.JobID, approval.Summary),
		map[string]any{"approval": approval.ID, "job": approval.JobID, "pattern": strings.TrimSpace(pattern), "risk": approval.Risk})
}

// projectLabel is "project <name>" for a journal line.
func (s *Server) projectLabel(r *http.Request, id int64) string {
	if s.deps.Projects != nil {
		if project, err := s.deps.Projects.ByID(r.Context(), id); err == nil {
			return "project " + project.Name
		}
	}
	return fmt.Sprintf("project %d", id)
}

func elevatedNote(rule approvals.Rule) string {
	if rule.MaxRisk == risk.Elevated {
		return " (also when elevated)"
	}
	return ""
}

// writePolicyError answers the policy's refusals (409 with the reason, 400 for a bad rule) and
// falls back to the approvals' own errors.
func (s *Server) writePolicyError(w http.ResponseWriter, err error) {
	var refused *approvals.RefusedError
	var invalid *approvals.RuleError
	switch {
	case errors.As(err, &refused):
		writeError(w, http.StatusConflict, codeConflict, refused.Message)
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, codeValidation, invalid.Message)
	case errors.Is(err, approvals.ErrRuleNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	default:
		s.writeApprovalError(w, err)
	}
}
