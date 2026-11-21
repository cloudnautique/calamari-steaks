package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// managerConnection is the adapter's per-box state: the pinned session and
// the human focus owner. The pin changes only on an explicit reopen.
type managerConnection struct {
	client     atomic.Pointer[discobox.Client]
	mu         sync.Mutex
	controller string
	since      time.Time
	archived   bool
}

func newManagerConnection(client *discobox.Client) *managerConnection {
	m := &managerConnection{}
	if client != nil {
		m.client.Store(client)
	}
	return m
}

// Client returns the currently pinned session, or nil before one is resolved.
func (m *managerConnection) Client() *discobox.Client { return m.client.Load() }

type managerInput struct {
	ManagerID string `json:"managerId,omitempty" jsonschema:"Box ID for either role (legacy parameter name). Omit only when the adapter has an explicit startup default."`
}
type createInput struct {
	Source    *discobox.RepositorySource `json:"source,omitempty"`
	Setup     *setupInput                `json:"setup,omitempty" jsonschema:"Optional repository setup settings. Starts a manager and leaves grants to the upstream approval flow."`
	RequestID string                     `json:"requestId" jsonschema:"Stable key for this one creation intent. Reuse the same key after disconnection; never retry an uncertain creation with a different key."`
	Name      string                     `json:"name,omitempty" jsonschema:"Optional name: at most 64 letters, digits, underscores or hyphens"`
	Harness   string                     `json:"harness,omitempty"`
	Pool      string                     `json:"pool,omitempty"`
	Task      string                     `json:"task,omitempty" jsonschema:"Starting task. Required for launch_box."`
	Model     string                     `json:"model,omitempty" jsonschema:"Optional model override for this run only."`
}

// Launch returns well inside the hosts' ~60 s tool budget. A box whose
// session is not ready by then is reported as starting, not as an error.
const launchTimeout = 50 * time.Second
const launchPrimaryWait = 20 * time.Second

func (s *Server) defaultBox(id string) (string, error) {
	if id == "" && s.backend() != nil {
		id = s.backend().Session.Box
	}
	if id == "" {
		return "", errors.New("choose a box with list_boxes and pass its box ID")
	}
	return id, nil
}

// manager returns the pinned session for a box, resolving it on first use.
func (s *Server) manager(ctx context.Context, id string) (*managerConnection, error) {
	id, err := s.defaultBox(id)
	if err != nil {
		return nil, err
	}
	s.managersMu.Lock()
	existing := s.managers[id]
	s.managersMu.Unlock()
	if existing != nil && existing.Client() != nil {
		return existing, nil
	}
	client, err := s.backend().ConnectManager(ctx, id, s.managerOptions.Parent)
	if err != nil {
		return nil, err
	}
	return s.pin(id, client), nil
}

func (s *Server) pin(id string, client *discobox.Client) *managerConnection {
	s.managersMu.Lock()
	defer s.managersMu.Unlock()
	existing := s.managers[id]
	if existing == nil {
		existing = newManagerConnection(nil)
		s.managers[id] = existing
	}
	existing.client.CompareAndSwap(nil, client)
	return existing
}

// reopen is the explicit "open this box" path. It re-resolves the primary
// session so a box whose harness restarted is reachable again. It adopts a
// new session only while no person holds input, so typed input is never
// redirected; queued input on the old session fails rather than moving.
func (s *Server) reopen(ctx context.Context, id string) (*managerConnection, map[string]any, error) {
	id, err := s.defaultBox(id)
	if err != nil {
		return nil, nil, err
	}
	if id == s.backend().Session.Box && s.backend().Session.Exec != "" {
		// A session pinned on the command line stays pinned.
		manager, err := s.manager(ctx, id)
		return manager, map[string]any{"terminal": "ready"}, err
	}
	fresh, err := s.backend().ConnectManager(ctx, id, s.managerOptions.Parent)
	if errors.Is(err, discobox.ErrNoPrimary) {
		return nil, map[string]any{"terminal": "starting", "message": "The box has no terminal session yet; it may still be starting. Files and Desktop are available. The widget connects when the session appears; from text, call open_box again shortly."}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	info := map[string]any{"terminal": "ready"}
	manager := s.pin(id, fresh)
	current := manager.Client()
	if current == fresh || current.Session.Exec == fresh.Session.Exec {
		return manager, info, nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.controller != "" {
		return nil, nil, errors.New("this box has a new terminal session, but a person still holds input in a view of the old one; they need to leave that terminal or desktop before it can be reopened")
	}
	manager.client.Store(fresh)
	info["sessionChanged"] = true
	info["previousExec"] = current.Session.Exec
	return manager, info, nil
}

func (s *Server) listManagers(ctx context.Context) (map[string]any, error) {
	managers, err := s.backend().ListManagers(ctx, s.managerOptions.Parent)
	return map[string]any{"view": "managers", "managers": managers, "project": s.backend().Session.Project}, err
}
func (s *Server) connectManager(ctx context.Context, id string) (map[string]any, error) {
	id, err := s.defaultBox(id)
	if err != nil {
		return nil, err
	}
	manager, info, err := s.reopen(ctx, id)
	if err != nil {
		return nil, err
	}
	session := discobox.Session{Project: s.backend().Session.Project, Box: id}
	if manager != nil {
		session = manager.Client().Session
	}
	data := map[string]any{"view": "terminal", "session": session, "managerId": id, "boxId": id, "transport": "mcp"}
	for key, value := range info {
		data[key] = value
	}
	return data, nil
}
func (s *Server) createManager(ctx context.Context, input createInput) (map[string]any, error) {
	return s.launch(ctx, input, "")
}
func (s *Server) launch(ctx context.Context, input createInput, role string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	options := s.managerOptions
	options.Name = input.Name
	options.Task, options.Model, options.Role = input.Task, input.Model, role
	options.Source = input.Source
	if input.Setup != nil {
		if input.Source == nil {
			return nil, errors.New("repository setup needs an explicit source")
		}
		if err := input.Setup.validate(); err != nil {
			return nil, err
		}
		options.Task = input.Setup.prompt(input.Task)
		options.Role = "manager"
	}
	options.PrimaryWait = launchPrimaryWait
	if input.Harness != "" {
		options.Harness = input.Harness
	}
	if input.Pool != "" {
		options.Pool = input.Pool
	}
	client, err := s.backend().CreateManager(ctx, input.RequestID, options)
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	id := client.Session.Box
	if input.Source != nil {
		data := map[string]any{"view": "setup", "session": client.Session, "boxId": id,
			"requestedSource": input.Source, "returnTool": "open_box", "requestId": input.RequestID}
		if input.Setup != nil {
			data["requestedSetup"] = input.Setup
			data["requestedRole"] = "manager"
		}
		if client.RoleTagPending != "" {
			data["roleTagPending"] = client.RoleTagPending
		}
		observationCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		if observation, err := client.ObserveSetup(observationCtx, id, options.Parent, false); err == nil {
			data["observation"] = observation
		}
		return data, nil
	}
	if client.Session.Exec == "" {
		return map[string]any{"view": "terminal", "session": client.Session, "managerId": id, "boxId": id, "transport": "mcp", "terminal": "starting",
			"message": "Box created and still starting. The widget connects when its session appears. From text, call open_box or read_box shortly; wait_box needs a session."}, nil
	}
	manager := s.pin(id, client)
	data := map[string]any{"view": "terminal", "session": manager.Client().Session, "managerId": id, "boxId": id, "transport": "mcp", "terminal": "ready"}
	// A cursor taken now lets wait_box find the end of the first turn even
	// if the caller only starts waiting after it happened.
	cursorCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if cursor, err := manager.Client().Cursor(cursorCtx); err == nil && cursor != "" {
		data["resumeAfter"] = cursor
	}
	return data, nil
}
func (s *Server) archiveManager(ctx context.Context, id string) (map[string]any, error) {
	if id == "" {
		return nil, errors.New("managerId is required to archive a manager")
	}
	s.managersMu.Lock()
	manager := s.managers[id]
	s.managersMu.Unlock()
	if manager != nil {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if manager.controller != "" {
			return nil, errors.New("release human terminal control before archiving this manager")
		}
	}
	if err := s.backend().ArchiveManager(ctx, id, s.managerOptions.Parent); err != nil {
		return nil, err
	}
	if manager != nil {
		manager.archived = true
	}
	s.managersMu.Lock()
	delete(s.managers, id)
	s.managersMu.Unlock()
	// Views receive a closed stream; release ownership outside the manager lock.
	s.appMu.Lock()
	views := []string{}
	for key, view := range s.appTerminals {
		if view.box == id {
			views = append(views, key)
		}
	}
	s.appMu.Unlock()
	for _, key := range views {
		// No human holds this connection here, so cleanup cannot deadlock on the
		// lock above: defer cleanup until after this function returns.
		go s.closeAppTerminal(key)
	}
	s.desktopMu.Lock()
	for key, view := range s.desktops {
		if view.box == id {
			go s.closeDesktop(key)
		}
	}
	s.desktopMu.Unlock()
	return map[string]any{"view": "boxes", "managerId": id, "boxId": id, "state": "archiving", "message": "Archive requested; list boxes to see current state. Closing a view alone never archives it."}, nil
}

func workspaceResult(data map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	return &mcp.CallToolResult{Meta: mcp.Meta{"connection": data}}, data, nil
}
func (s *Server) workspaceTools(server *mcp.Server) {
	if !s.LegacyTools {
		return
	}
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	mcp.AddTool(server, &mcp.Tool{Name: "inspect_box", Description: "Legacy alias of read_box.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input boxInput) (*mcp.CallToolResult, map[string]any, error) {
		id, err := input.target()
		if err != nil {
			return nil, nil, err
		}
		data, err := s.inspectBox(ctx, id)
		return nil, data, err
	})

	mcp.AddTool(server, &mcp.Tool{Name: "list_managers", Description: "List boxes with the manager/coordinator role. A manager is itself a box, not a session nested inside a task box. Also opens a manager picker when the host supports MCP Apps. Does not create or connect a session.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.listManagers(ctx)
		if err != nil {
			return nil, nil, err
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "connect_manager", Description: "Connect to a manager ID and open its terminal in the canvas. Use the returned managerId in subsequent read/send/wait calls. Other views remain pinned to their managers.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input managerInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.connectManager(ctx, input.ManagerID)
		if err != nil {
			return nil, nil, err
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "create_manager", Description: "Explicitly create a persistent manager, with a durable creation journal. Name/harness/pool are optional. Keep requestId unchanged if the outcome is uncertain; never automatically submit a different creation intent.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input createInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.createManager(ctx, input)
		if err != nil {
			return nil, nil, err
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "archive_manager", Description: "Archive the specified manager when the person is finished with it. Keeps retained data; never permanently deletes. Release human control first. List managers after an uncertain result before retrying."}, func(ctx context.Context, _ *mcp.CallToolRequest, input managerInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.archiveManager(ctx, input.ManagerID)
		return nil, data, err
	})
}

// The standalone fallback uses the same manager operations as the MCP tools.
func (s *Server) workspaceHTTP(w http.ResponseWriter, r *http.Request) {
	var data map[string]any
	var err error
	switch r.PathValue("action") {
	case "boxes":
		var input catalogReadInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil || errors.Is(err, io.EOF) {
			data, err = s.listBoxes(r.Context(), input)
		}
	case "attention":
		data, err = s.attention(r.Context())
	case "message":
		var input struct {
			boxInput
			Text string `json:"text"`
		}
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&input)
		if err == nil {
			var manager *managerConnection
			var id string
			id, err = input.target()
			if err == nil {
				manager, err = s.manager(r.Context(), id)
			}
			if err == nil {
				err = manager.writeAs("mcp", func() error {
					var sendErr error
					data, sendErr = manager.Client().Send(r.Context(), input.Text, true)
					return sendErr
				})
			}
		}
	case "inspect":
		var input boxInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			var id string
			id, err = input.target()
			if err == nil {
				data, err = s.inspectBox(r.Context(), id)
			}
		}
	case "launch":
		var input createInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&input)
		if err == nil {
			data, err = s.launch(r.Context(), input, "box")
			if err == nil && input.Source != nil {
				data["receiptText"] = launchReceipt(data, input)
			}
		}
	case "setup-status":
		var input boxInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			var summary string
			data, summary, err = s.setupStatus(r.Context(), input.BoxID)
			if err == nil {
				data["receiptText"] = summary
			}
		}
	case "list":
		data, err = s.listManagers(r.Context())
	case "connect":
		var input boxInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			var id string
			id, err = input.target()
			if err == nil {
				data, err = s.connectManager(r.Context(), id)
			}
		}
	case "create":
		var input createInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			data, err = s.createManager(r.Context(), input)
		}
	case "archive":
		var input boxInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			var id string
			id, err = input.target()
			if err == nil {
				data, err = s.archiveManager(r.Context(), id)
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(data)
}

const widgetTaskLimit = 240

// attentionListLimit caps list_attention. Keep it well above the widget's
// inbox cap: an item left out has no request ID the model can open.
const attentionListLimit = 50

func widgetCatalog(inbox discobox.Inbox, input catalogReadInput, project string) map[string]any {
	const pageSize = 20
	boxes := []discobox.Manager{}
	offset := max(input.Offset, 0)
	total := 0
	for _, box := range inbox.Boxes {
		if box.Archived && !input.IncludeArchived {
			continue
		}
		total++
		if total <= offset || len(boxes) == pageSize {
			continue
		}
		box.Name = truncateText(box.Name, 120)
		box.Task = truncateText(box.Task, widgetTaskLimit)
		box.State = truncateText(box.State, 48)
		box.Harness = truncateText(box.Harness, 64)
		box.Model = ""
		box.Parent = "" // Accessible already captures the current caller's relationship.
		box.ObservedAt = truncateText(box.ObservedAt, 40)
		box.Program = map[string]any{"state": truncateText(textOf(box.Program, "state"), 32), "msg": truncateText(textOf(box.Program, "msg"), 120)}
		if payload, _ := json.Marshal(box); len(payload) > 768 {
			box.Task = ""
			box.Program = map[string]any{"state": truncateText(textOf(box.Program, "state"), 32)}
			box.Name = truncateText(box.Name, 48)
		}
		boxes = append(boxes, box)
	}
	items := make([]discobox.Attention, 0, min(12, len(inbox.Items)))
	for _, item := range inbox.Items[:min(12, len(inbox.Items))] {
		item.Reason = truncateText(item.Reason, 160)
		item.BoxName = truncateText(item.BoxName, 120)
		item.Title = truncateText(item.Title, 120)
		item.Scope, item.TTL = nil, nil // load the exact scope in approval review
		item.CreatedAt = truncateText(item.CreatedAt, 40)
		item.ObservedAt = truncateText(item.ObservedAt, 40)
		if payload, _ := json.Marshal(item); len(payload) > 512 {
			item.Reason = ""
			item.BoxName = truncateText(item.BoxName, 40)
			item.Title = truncateText(item.Title, 40)
		}
		items = append(items, item)
	}
	data := map[string]any{"view": "boxes", "boxes": boxes, "project": project, "offset": offset, "total": total,
		"inbox": map[string]any{"items": items, "total": len(inbox.Items), "coverage": inbox.Coverage, "warning": inbox.Warning}}
	if offset < total && total-offset > len(boxes) {
		data["nextOffset"] = offset + len(boxes)
	}
	return data
}

func (s *Server) listBoxes(ctx context.Context, input catalogReadInput) (map[string]any, error) {
	inbox, err := s.snapshot(ctx, catalogModelAge)
	return widgetCatalog(inbox, input, s.backend().Session.Project), err
}

func (s *Server) attention(ctx context.Context) (map[string]any, error) {
	data, err := s.listBoxes(ctx, catalogReadInput{})
	data["view"] = "attention"
	return data, err
}

func (s *Server) inspectBox(ctx context.Context, id string) (map[string]any, error) {
	// Resolve pins the exec but never attaches to it. Screen reads do not wake it.
	manager, err := s.manager(ctx, id)
	if err != nil {
		return nil, err
	}
	screen, err := manager.Client().Screen(ctx, 100)
	return map[string]any{"managerId": manager.Client().Session.Box, "boxId": manager.Client().Session.Box, "screen": screen}, err
}

type boxInput struct {
	BoxID     string `json:"boxId,omitempty" jsonschema:"Target box ID, regardless of role or creation source."`
	ManagerID string `json:"managerId,omitempty" jsonschema:"Legacy alias for boxId; do not supply conflicting IDs."`
}

func (input boxInput) target() (string, error) {
	if input.BoxID != "" && input.ManagerID != "" && input.BoxID != input.ManagerID {
		return "", errors.New("boxId and managerId identify different boxes")
	}
	if input.BoxID != "" {
		return input.BoxID, nil
	}
	return input.ManagerID, nil
}
func (s *Server) boxTools(server *mcp.Server) {
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	mcp.AddTool(server, &mcp.Tool{Name: "list_boxes", Description: "List boxes in this project, including ones created in the Discobox console or elsewhere: ID, name, role, state, agent status and a short task preview. Archived boxes are hidden unless includeArchived. Paged; filter with query or needsAttention. Opens the task launcher and box catalog where the host shows apps. Does not wake or attach boxes.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input listBoxesInput) (*mcp.CallToolResult, map[string]any, error) {
		inbox, err := s.snapshot(ctx, catalogModelAge)
		if err != nil {
			return nil, nil, err
		}
		page, err := boxPage(inbox.Boxes, input)
		if err != nil {
			return nil, nil, err
		}
		page["project"] = s.backend().Session.Project
		page["view"] = "boxes"
		boxes := page["boxes"].([]boxSummary)
		text := fmt.Sprintf("%d of %d matching boxes in project %s", len(boxes), page["total"], s.backend().Session.Project)
		if hidden, ok := page["archivedHidden"]; ok {
			text += fmt.Sprintf(" (%d archived hidden)", hidden)
		}
		if next, ok := page["nextOffset"]; ok {
			text += fmt.Sprintf("; more with offset %d", next)
		}
		text += ". Task previews are truncated; read_box shows a box's terminal.\n" + boxTable(boxes)
		return modelResult(text, page, map[string]any{"view": "boxes", "project": s.backend().Session.Project})
	})
	mcp.AddTool(server, &mcp.Tool{Name: "launch_box", Description: "Start a box from a GitHub source/branch or an empty workspace. Optional setup starts a manager using the repository entrypoint and intended settings. Grants are decided upstream. Keep requestId and settings for retries. For repository setup, use setup_status, then open_box or read_box with the returned boxId. No catalog lookup needed.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input createInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.launch(ctx, input, "box")
		if err != nil {
			return nil, nil, err
		}
		if input.Source != nil {
			text := launchReceipt(data, input)
			return modelResult(text, data, map[string]any{"view": "setup", "boxId": data["boxId"], "receiptText": text})
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_attention", Meta: ui, Description: "List what needs the person: pending approval requests and boxes whose agent reported blocked, error or done. Requests persist in Discobox independently of this widget. coverage/warning describe what this connection cannot see. Use open_approval to review a credential request by ID.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, map[string]any, error) {
		inbox, err := s.snapshot(ctx, catalogModelAge)
		if err != nil {
			return nil, nil, err
		}
		// The widget keeps a short inbox, but the model needs every request ID
		// it may pass to open_approval, so this list has its own, larger cap.
		items := inbox.Items[:min(attentionListLimit, len(inbox.Items))]
		var text strings.Builder
		fmt.Fprintf(&text, "%d item(s) need attention. Coverage: %s.", len(inbox.Items), inbox.Coverage)
		if inbox.Warning != "" {
			fmt.Fprintf(&text, " %s", inbox.Warning)
		}
		text.WriteString("\n")
		for _, item := range items {
			fmt.Fprintf(&text, "- %s · %s (%s)", item.Title, item.BoxName, item.BoxID)
			if item.Reason != "" {
				fmt.Fprintf(&text, ": %s", truncateText(item.Reason, 200))
			}
			if item.Kind == "credential" {
				fmt.Fprintf(&text, " [request %s]", item.ID)
			}
			text.WriteString("\n")
		}
		structured := map[string]any{"items": items, "total": len(inbox.Items), "coverage": inbox.Coverage, "warning": inbox.Warning}
		return modelResult(text.String(), structured, map[string]any{"view": "attention"})
	})
	mcp.AddTool(server, &mcp.Tool{Name: "catalog_read", Description: "App-only: the shared box catalog and inbox for the widget, served from the adapter's cache.", Meta: mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input catalogReadInput) (*mcp.CallToolResult, map[string]any, error) {
		s.watchCatalog()
		age := catalogPolledAge
		if input.Fresh {
			age = time.Second
		}
		inbox, err := s.snapshot(ctx, age)
		if err != nil {
			return nil, nil, err
		}
		return modelResult("Box catalog page for the widget.", widgetCatalog(inbox, input, s.backend().Session.Project), nil)
	})
}

type catalogReadInput struct {
	Fresh           bool `json:"fresh,omitempty"`
	Offset          int  `json:"offset,omitempty"`
	IncludeArchived bool `json:"includeArchived,omitempty"`
}
