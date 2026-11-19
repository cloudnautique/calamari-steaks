package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These are the person's intended destinations, not repository policy or
// evidence that a credential or role actually permits the operation.
type setupInput struct {
	Entrypoint            string `json:"entrypoint" jsonschema:"Repository setup slash command, for example /setup."`
	RequestAPI            bool   `json:"requestApi,omitempty" jsonschema:"Have the new box request its own worker-management API access. A human decides in Discobox."`
	SettingsRepository    string `json:"settingsRepository,omitempty"`
	IssueRepository       string `json:"issueRepository,omitempty"`
	PullRequestRepository string `json:"pullRequestRepository,omitempty"`
	PullRequestBranch     string `json:"pullRequestBranch,omitempty"`
	WorkerPushRepository  string `json:"workerPushRepository,omitempty"`
	Actor                 string `json:"actor,omitempty" jsonschema:"Intended GitHub login. Confirm the actual authenticated identity during setup."`
	TriageOnly            bool   `json:"triageOnly,omitempty" jsonschema:"Ask the repository workflow to start with human-confirmed triage only."`
}

func (input setupInput) validate() error {
	command, ok := strings.CutPrefix(input.Entrypoint, "/")
	if !ok || len(command) > 64 || !discobox.ValidBranch(command) || strings.Contains(command, "/") {
		return errors.New("setup entrypoint must be a single slash command, for example /setup")
	}
	for _, repo := range []string{input.SettingsRepository, input.IssueRepository, input.PullRequestRepository, input.WorkerPushRepository} {
		if repo != "" && !discobox.ValidRepository(repo) {
			return errors.New("setup repository settings must use GitHub owner/repository")
		}
	}
	if (input.PullRequestRepository == "") != (input.PullRequestBranch == "") ||
		(input.PullRequestBranch != "" && !discobox.ValidBranch(input.PullRequestBranch)) {
		return errors.New("draft pull request destination needs both repository and branch")
	}
	if input.Actor != "" && !discobox.ValidRepository(input.Actor+"/repo") {
		return errors.New("actor must be a GitHub login")
	}
	return nil
}

func (input setupInput) prompt(task string) string {
	settings, _ := json.Marshal(input)
	prompt := "In this checkout, read its repository instructions and follow its own " + input.Entrypoint +
		" skill for setup. Confirm the intended actor and destinations with the person and check real permissions before action. Ask before writes, starting workers, or enabling automation. Treat setup and starting work as separate. " +
		"Never show credential values. If the repository cannot represent a setting, report the blocker in this session. " +
		"Do not assume a sample or replace an explicitly named repository. The person's requested settings are " + string(settings) + "."
	if input.RequestAPI {
		prompt += " If API access is disabled, request it from this box using the repository's setup flow (or discobox api-access request). Report the request ID when available and check the decision before managing workers."
	}
	if input.TriageOnly {
		prompt += " Start with triage only, one worker at a time with human confirmation. Leave delivery and CI jobs off. Keep the requested draft PR and branch destinations for later review."
	}
	prompt += " For MCP setup status, publish an atomic JSON file at " + discobox.SetupReportPath +
		" with boxId, observedAt (UTC RFC3339), setup (pending/complete/blocked), watcher (unknown/pending/verified/failed), worker (unknown/none/pending/running/done/failed), and ci (unknown/none/pending/running/passed/failed). " +
		"Record verified only after checking the actual watcher and its event consumer. For discobox-monitor use status and logs for inspection. Leave the event queue to its existing consumer. Update on transitions. Leave the report untracked and include no secrets or free text."
	return prompt + "\n\n" + task
}

func (s *Server) setupStatus(ctx context.Context, id string) (map[string]any, string, error) {
	if id == "" {
		return nil, "", errors.New("setup_status requires the boxId from launch_box")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	observation, err := s.backend().ObserveSetup(ctx, id, s.managerOptions.Parent, true)
	if err != nil {
		return nil, "", err
	}
	data := map[string]any{"view": "setup", "boxId": id, "observation": observation, "returnTool": "open_box"}
	text := observationText(observation)
	if s.managerOptions.Parent != "" {
		text += "\nCaller is sandbox " + s.managerOptions.Parent + ". Its child access and credential delegation are limited to what Discobox grants this caller."
	}
	return data, text, nil
}

func consoleHandoff(project, id, requestType string) string {
	return fmt.Sprintf("In Discobox console, select the same server and project %s. Open Secrets (F4) → approval inbox. Match the %s and box ID %s (and request ID when shown). Return with setup_status for the same box ID to verify the decision.", project, requestType, id)
}

func observationText(observation discobox.SetupObservation) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Box %s in project %s · %s · Discobox API %s. Checked %s.\n",
		observation.BoxID, observation.Project, observation.State, observation.APIAccess, observation.CheckedAt.Format(time.RFC3339))
	if source := observation.Source; source != nil {
		fmt.Fprintf(&text, "Source %s@%s · delivery %s", source.Repository, source.Branch, source.Delivery)
		if source.CheckoutCommit != "" {
			fmt.Fprintf(&text, " · checkout pin %s", source.CheckoutCommit)
		}
		if source.MaterializedAt != "" {
			fmt.Fprintf(&text, " · materialized %s", source.MaterializedAt)
		}
		if source.ObservedCommit != "" {
			fmt.Fprintf(&text, " · reported revision %s", source.ObservedCommit)
			if source.CheckoutCommit != "" && source.CheckoutCommit != source.ObservedCommit {
				text.WriteString(" (differs from checkout pin)")
			}
		}
		text.WriteString(".\n")
		switch source.Delivery {
		case "needs_credential", "missing_commit", "unreachable":
			fmt.Fprintf(&text, "Source delivery needs a host with repository access. Open this existing box ID in Discobox console or run discobox attach %s --git-credentials=local on that host, then refresh setup status.\n", observation.BoxID)
		}
	}
	if observation.Provisioning != "" {
		fmt.Fprintf(&text, "Provisioning · %s.\n", observation.Provisioning)
	}
	if observation.RuntimeObservedAt != "" {
		fmt.Fprintf(&text, "Last pool runtime observation %s.\n", observation.RuntimeObservedAt)
	}
	if observation.AgentObservedAt != "" {
		fmt.Fprintf(&text, "Last agent observation %s.\n", observation.AgentObservedAt)
	}
	fmt.Fprintf(&text, "Approval visibility · %s.\n", observation.ApprovalVisibility)
	needsAPIHandoff := observation.APIAccess != "enabled" && observation.ApprovalVisibility != "not checked"
	for _, request := range observation.APIHistory {
		fmt.Fprintf(&text, "API access request %s · %s.\n", request.ID, request.Status)
		needsAPIHandoff = needsAPIHandoff || request.Status == "pending"
	}
	for _, request := range observation.CredentialHistory {
		fmt.Fprintf(&text, "Credential request %s · %s · use %s · delegation %s. Review with open_approval for that request ID.\n",
			request.ID, request.Status, request.Use, request.Delegate)
		if request.Status == "pending" {
			text.WriteString(consoleHandoff(observation.Project, observation.BoxID, "credential request "+request.ID) + "\n")
		}
	}
	if needsAPIHandoff {
		if len(observation.APIHistory) > 0 && observation.APIHistory[0].Status == "denied" {
			text.WriteString("The latest API access request was denied. Worker management remains blocked while access is disabled.\n")
		} else {
			text.WriteString(consoleHandoff(observation.Project, observation.BoxID, "Discobox API access request") + "\n")
		}
	}
	fmt.Fprintf(&text, "Setup status · %s.\n", observation.ReportStatus)
	if r := observation.Report; r != nil {
		fmt.Fprintf(&text, "Setup %s · watcher %s · child worker %s · CI %s · reported %s.\n",
			r.Setup, r.Watcher, r.Worker, r.CI, r.ObservedAt.Format(time.RFC3339))
	} else {
		text.WriteString("Setup, watcher, child worker and CI are unverified. Read the same manager for its current setup question.\n")
	}
	fmt.Fprintf(&text, "Return to this box with open_box or read_box using boxId %s.", observation.BoxID)
	return text.String()
}

func (s *Server) setupTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "setup_status", Description: "Recheck one repository-backed box by ID after launch or console approval. Shows source delivery, API access, visible approval history and box-reported setup/watcher/worker/CI status. Use open_box or read_box with the same ID to continue.", Meta: mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
			BoxID string `json:"boxId"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			data, text, err := s.setupStatus(ctx, input.BoxID)
			if err != nil {
				return nil, nil, err
			}
			return modelResult(text, data, map[string]any{"view": "setup", "boxId": input.BoxID, "receiptText": text})
		})
}

func launchReceipt(data map[string]any, input createInput) string {
	id := data["boxId"]
	var text strings.Builder
	fmt.Fprintf(&text, "Box %s accepted · requested source %s@%s. Creation reference %s.\n", id, input.Source.Repository, input.Source.Branch, input.RequestID)
	if observation, ok := data["observation"].(discobox.SetupObservation); ok {
		fmt.Fprintf(&text, "Box state %s · Discobox API %s.\n", observation.State, observation.APIAccess)
		if observation.Source != nil && observation.Source.CheckoutCommit != "" {
			fmt.Fprintf(&text, "Checkout pin %s · delivery %s.\n", observation.Source.CheckoutCommit, observation.Source.Delivery)
		}
	}
	if setup := input.Setup; setup != nil {
		fmt.Fprintf(&text, "Manager setup requested · %s · GitHub actor %s (requested).\n", setup.Entrypoint, displaySetting(setup.Actor))
		fmt.Fprintf(&text, "Settings repo %s · issue watch %s · draft PRs %s · worker branches %s.\n",
			displaySetting(setup.SettingsRepository), displaySetting(setup.IssueRepository),
			displaySetting(strings.TrimRight(setup.PullRequestRepository+":"+setup.PullRequestBranch, ":")), displaySetting(setup.WorkerPushRepository))
		if setup.TriageOnly {
			text.WriteString("Start with triage only, subject to human confirmation.\n")
		}
		if setup.RequestAPI {
			text.WriteString("The manager is asked to request worker-management API access from inside the box. Use setup_status to check the request and verified grant state.\n")
		}
	}
	fmt.Fprintf(&text, "Continue with setup_status and open_box on boxId %s.", id)
	return text.String()
}

func displaySetting(value string) string {
	if value == "" {
		return "to confirm in setup"
	}
	return value
}
