package discobox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

type SourceObservation struct {
	Repository     string `json:"repository,omitempty"`
	Branch         string `json:"branch,omitempty"`
	CheckoutCommit string `json:"checkoutCommit,omitempty"`
	Delivery       string `json:"delivery"`
	MaterializedAt string `json:"materializedAt,omitempty"`
	ObservedCommit string `json:"observedCommit,omitempty"`
}

type SetupReport struct {
	BoxID      string    `json:"boxId"`
	ObservedAt time.Time `json:"observedAt"`
	Setup      string    `json:"setup"`
	Watcher    string    `json:"watcher"`
	Worker     string    `json:"worker"`
	CI         string    `json:"ci"`
	Simulated  bool      `json:"simulated,omitempty"`
}

type SetupObservation struct {
	BoxID              string             `json:"boxId"`
	Project            string             `json:"project"`
	Role               string             `json:"role,omitempty"`
	State              string             `json:"state"`
	Source             *SourceObservation `json:"source,omitempty"`
	APIAccess          string             `json:"apiAccess"`
	Provisioning       string             `json:"provisioning,omitempty"`
	RuntimeObservedAt  string             `json:"runtimeObservedAt,omitempty"`
	ApprovalVisibility string             `json:"approvalVisibility"`
	CheckedAt          time.Time          `json:"checkedAt"`
	AgentObservedAt    string             `json:"agentObservedAt,omitempty"`
	APIHistory         []RequestSummary   `json:"apiHistory,omitempty"`
	CredentialHistory  []RequestSummary   `json:"credentialHistory,omitempty"`
	Report             *SetupReport       `json:"report,omitempty"`
	ReportStatus       string             `json:"reportStatus"`
}

const SetupReportPath = "/workspace/source/.discobox/setup-status.json"

func member(m map[string]any, key string) map[string]any {
	result, _ := m[key].(map[string]any)
	return result
}

// ObserveSetup reads one box and approval history without attaching a terminal
// or making a decision on behalf of the caller.
func (c *Client) ObserveSetup(ctx context.Context, id, parent string, detail bool) (SetupObservation, error) {
	result := SetupObservation{BoxID: id, Project: c.Session.Project,
		State: "unknown", APIAccess: "unknown", ApprovalVisibility: "not checked", ReportStatus: "not checked"}
	box, err := c.managerBox(ctx, id, parent)
	if err != nil {
		return result, err
	}
	if textField(box, "id") != id {
		return result, errors.New("upstream did not confirm the selected box ID")
	}
	result.CheckedAt = time.Now().UTC()
	result.Role = roleOf(box)
	runtime := member(box, "runtime")
	for _, state := range []string{textField(runtime, "displayState"), textField(box, "displayState"), textField(runtime, "runtimeState")} {
		if state != "" {
			result.State = truncate(state, 64)
			break
		}
	}
	result.AgentObservedAt = normalizedTime(textField(runtime, "agentStatusObservedAt"))
	result.RuntimeObservedAt = normalizedTime(textField(runtime, "stateReportedAt"))
	if textField(runtime, "state") == "awaiting_source" {
		result.Provisioning = "awaiting_source"
	}
	if enabled, ok := box["discoboxApi"].(bool); ok {
		result.APIAccess = "disabled"
		if enabled {
			result.APIAccess = "enabled"
		}
	}
	if source := member(member(box, "config"), "source"); source != nil {
		result.Source = observeSource(source, runtime)
	}
	if !detail {
		return result, nil
	}
	result.APIHistory, result.ApprovalVisibility = c.boxApprovalHistory(ctx, id, "api-access-requests", "apiAccessRequests", "requesterSandboxId")
	var credentialVisibility string
	result.CredentialHistory, credentialVisibility = c.boxApprovalHistory(ctx, id, "secret-requests", "secretRequests", "sandboxId")
	result.ApprovalVisibility = "API access " + result.ApprovalVisibility + ", credentials " + credentialVisibility
	if result.Source != nil && result.State == "running" {
		result.Report, result.ReportStatus = c.readSetupReport(ctx, id, parent)
	} else {
		result.ReportStatus = "unverified while box or source is unavailable"
	}
	return result, nil
}

func normalizedTime(value string) string {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func observeSource(source, runtime map[string]any) *SourceObservation {
	result := &SourceObservation{Delivery: "unknown"}
	if path, ok := strings.CutPrefix(textField(source, "url"), "https://github.com/"); ok {
		if repo := strings.TrimSuffix(path, ".git"); ValidRepository(repo) {
			result.Repository = repo
		}
	}
	checkout := member(source, "checkout")
	if branch := textField(checkout, "refName"); ValidBranch(branch) {
		result.Branch = branch
	}
	if commit := textField(checkout, "commit"); gitCommit.MatchString(commit) {
		result.CheckoutCommit = commit
	}
	switch status := textField(member(member(runtime, "sourceDeliveries"), "primary"), "status"); status {
	case "delivered", "needs_credential", "missing_commit", "unreachable":
		result.Delivery = status
	}
	states, _ := member(runtime, "agentStatus")["sourceStates"].([]any)
	for _, raw := range states {
		state, _ := raw.(map[string]any)
		if textField(state, "slug") == "primary" && textField(state, "state") == "materialized" {
			result.MaterializedAt = normalizedTime(textField(state, "updatedAt"))
			if commit := textField(state, "commit"); gitCommit.MatchString(commit) {
				result.ObservedCommit = commit
			}
			break
		}
	}
	return result
}

func (c *Client) boxApprovalHistory(ctx context.Context, id, route, key, boxField string) ([]RequestSummary, string) {
	raw, err := c.requestLimited(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/"+route, nil, listingResponseLimit)
	if limitedRead(err) {
		return nil, "limited by caller role or server"
	}
	if err != nil {
		return nil, "unknown (request failed)"
	}
	items, valid := raw[key].([]any)
	if _, present := raw[key]; !present || (!valid && raw[key] != nil) {
		return nil, "unknown (invalid response)"
	}
	list := []RequestSummary{}
	for _, entry := range items {
		value, _ := entry.(map[string]any)
		if textField(value, boxField) != id || textField(value, "projectId") != c.Session.Project {
			continue
		}
		requestID, status := textField(value, "id"), textField(value, "status")
		if len(requestID) > 128 || !identifier.MatchString(requestID) || !slices.Contains([]string{"pending", "approved", "denied"}, status) {
			continue
		}
		summary := RequestSummary{ID: requestID, BoxID: id, Status: status, CreatedAt: normalizedTime(textField(value, "createdAt")), DecidedAt: normalizedTime(textField(value, "decidedAt"))}
		if key == "secretRequests" {
			summary.Use = approvalOutcome(value, "use", status)
			summary.Delegate = approvalOutcome(value, "delegate", status)
		}
		list = append(list, summary)
	}
	slices.SortStableFunc(list, func(a, b RequestSummary) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })
	if len(list) > 5 {
		return list[:5], "visible (latest five shown)"
	}
	return list, "visible within caller scope"
}

func approvalOutcome(request map[string]any, part, status string) string {
	value := member(request, part)
	if value == nil {
		return "not requested"
	}
	switch outcome := textField(value, "outcome"); outcome {
	case "approved", "denied":
		return outcome
	}
	if status == "pending" {
		return "pending"
	}
	return "unknown"
}

func (c *Client) readSetupReport(ctx context.Context, id, parent string) (*SetupReport, string) {
	file, err := c.ReadFile(ctx, id, parent, SetupReportPath, 0, 2048)
	if err != nil || file.Text == nil || file.Truncated {
		return nil, "unverified (no readable setup report)"
	}
	var report SetupReport
	if json.Unmarshal([]byte(*file.Text), &report) != nil || report.BoxID != id ||
		report.ObservedAt.IsZero() || time.Until(report.ObservedAt) > time.Minute ||
		!slices.Contains([]string{"pending", "complete", "blocked"}, report.Setup) ||
		!slices.Contains([]string{"unknown", "pending", "verified", "failed"}, report.Watcher) ||
		!slices.Contains([]string{"unknown", "none", "pending", "running", "done", "failed"}, report.Worker) ||
		!slices.Contains([]string{"unknown", "none", "pending", "running", "passed", "failed"}, report.CI) {
		return nil, "unverified (invalid setup report)"
	}
	status := "box-reported evidence"
	if report.Simulated {
		status = "simulated fixture report"
	}
	if time.Since(report.ObservedAt) > 5*time.Minute {
		status = "stale " + status
	}
	return &report, status
}
