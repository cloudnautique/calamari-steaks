package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type managerConnection struct {
	Client     *discobox.Client
	mu         sync.Mutex
	controller string
	archived   bool
}
type managerInput struct {
	ManagerID string `json:"managerId,omitempty" jsonschema:"Manager box ID. Omit only when the adapter has an explicit startup default."`
}
type createInput struct {
	RequestID string `json:"requestId" jsonschema:"Stable key for this one creation intent. Reuse the same key after disconnection; never retry an uncertain creation with a different key."`
	Name      string `json:"name,omitempty" jsonschema:"Optional name: at most 64 letters, digits, underscores or hyphens"`
	Harness   string `json:"harness,omitempty"`
	Pool      string `json:"pool,omitempty"`
	Task      string `json:"task,omitempty" jsonschema:"Starting task. Required for launch_box."`
	Model     string `json:"model,omitempty" jsonschema:"Optional model override for this run only."`
}

func (s *Server) manager(ctx context.Context, id string) (*managerConnection, error) {
	if id == "" {
		id = s.Client.Session.Box
	}
	if id == "" {
		return nil, errors.New("choose a manager with list_managers and pass its managerId")
	}
	s.managersMu.Lock()
	existing := s.managers[id]
	s.managersMu.Unlock()
	if existing != nil {
		return existing, nil
	}
	client, err := s.Client.ConnectManager(ctx, id, s.managerOptions.Parent)
	if err != nil {
		return nil, err
	}
	candidate := &managerConnection{Client: client}
	s.managersMu.Lock()
	defer s.managersMu.Unlock()
	if existing = s.managers[id]; existing != nil {
		return existing, nil
	}
	s.managers[id] = candidate
	return candidate, nil
}

func (s *Server) listManagers(ctx context.Context) (map[string]any, error) {
	managers, err := s.Client.ListManagers(ctx, s.managerOptions.Parent)
	return map[string]any{"view": "managers", "managers": managers, "project": s.Client.Session.Project}, err
}
func (s *Server) connectManager(ctx context.Context, id string) (map[string]any, error) {
	manager, err := s.manager(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"view": "terminal", "session": manager.Client.Session, "managerId": manager.Client.Session.Box, "transport": "mcp"}, nil
}
func (s *Server) createManager(ctx context.Context, input createInput) (map[string]any, error) {
	return s.launch(ctx, input, "")
}
func (s *Server) launch(ctx context.Context, input createInput, role string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	options := s.managerOptions
	options.Name = input.Name
	options.Task, options.Model, options.Role = input.Task, input.Model, role
	if input.Harness != "" {
		options.Harness = input.Harness
	}
	if input.Pool != "" {
		options.Pool = input.Pool
	}
	client, err := s.Client.CreateManager(ctx, input.RequestID, options)
	if err != nil {
		return nil, err
	}
	s.managersMu.Lock()
	if s.managers[client.Session.Box] == nil {
		s.managers[client.Session.Box] = &managerConnection{Client: client}
	}
	s.managersMu.Unlock()
	return s.connectManager(ctx, client.Session.Box)
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
	if err := s.Client.ArchiveManager(ctx, id, s.managerOptions.Parent); err != nil {
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
		if view.manager.Client.Session.Box == id {
			views = append(views, key)
		}
	}
	s.appMu.Unlock()
	for _, key := range views {
		// No human holds this connection here, so cleanup cannot deadlock on the
		// lock above: defer cleanup until after this function returns.
		go s.closeAppTerminal(key)
	}
	return map[string]any{"view": "managers", "managerId": id, "state": "archiving", "message": "Archive requested; list managers to see current state. Closing a view alone never archives it."}, nil
}

func workspaceResult(data map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	return &mcp.CallToolResult{Meta: mcp.Meta{"connection": data}}, data, nil
}
func (s *Server) workspaceTools(server *mcp.Server) {
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	mcp.AddTool(server, &mcp.Tool{Name: "list_managers", Description: "List persistent managers and their status. Also opens a manager picker when the host supports MCP Apps. Does not create or connect a session.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, map[string]any, error) {
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
		data, err = s.listBoxes(r.Context())
	case "attention":
		data, err = s.attention(r.Context())
	case "message":
		var input sendInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&input)
		if err == nil {
			var manager *managerConnection
			manager, err = s.manager(r.Context(), input.ManagerID)
			if err == nil {
				err = manager.writeAs("mcp", func() error {
					var sendErr error
					data, sendErr = manager.Client.Send(r.Context(), input.Text, true)
					return sendErr
				})
			}
		}
	case "inspect":
		var input managerInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			data, err = s.inspectBox(r.Context(), input.ManagerID)
		}
	case "launch":
		var input createInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&input)
		if err == nil {
			data, err = s.launch(r.Context(), input, "box")
		}
	case "list":
		data, err = s.listManagers(r.Context())
	case "connect":
		var input managerInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			data, err = s.connectManager(r.Context(), input.ManagerID)
		}
	case "create":
		var input createInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			data, err = s.createManager(r.Context(), input)
		}
	case "archive":
		var input managerInput
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input)
		if err == nil {
			data, err = s.archiveManager(r.Context(), input.ManagerID)
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

func (s *Server) listBoxes(ctx context.Context) (map[string]any, error) {
	boxes, err := s.Client.ListBoxes(ctx, s.managerOptions.Parent)
	return map[string]any{"view": "boxes", "managers": boxes, "project": s.Client.Session.Project}, err
}
func (s *Server) attention(ctx context.Context) (map[string]any, error) {
	inbox, err := s.Client.Attention(ctx, s.managerOptions.Parent)
	return map[string]any{"view": "attention", "inbox": inbox}, err
}
func (s *Server) inspectBox(ctx context.Context, id string) (map[string]any, error) {
	// Resolve pins the exec but never attaches to it. Screen reads do not wake it.
	manager, err := s.manager(ctx, id)
	if err != nil {
		return nil, err
	}
	screen, err := manager.Client.Screen(ctx, 100)
	return map[string]any{"managerId": manager.Client.Session.Box, "screen": screen}, err
}
func (s *Server) boxTools(server *mcp.Server) {
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	mcp.AddTool(server, &mcp.Tool{Name: "list_boxes", Description: "List task boxes and managers created through this integration. Opens the task launcher and box catalog. Does not wake or attach boxes.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.listBoxes(ctx)
		if err != nil {
			return nil, nil, err
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "launch_box", Description: "Launch a task in a separate box using a harness and optional model/pool. Settings apply to this run only. Starts with an empty workspace and no Discobox API access or explicit credential bindings. Keep requestId for this exact intent; never retry uncertain creation using a new ID. Use returned managerId with read_manager/send_manager/wait_manager/interrupt_manager for text interaction.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input createInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.launch(ctx, input, "box")
		if err != nil {
			return nil, nil, err
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "open_box", Description: "Inspect a box workspace by ID without attaching or waking it. managerId is the box ID. Terminal attachment requires an explicit action in the workspace. Use existing read/send/wait_manager tools for raw text interaction.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input managerInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.connectManager(ctx, input.ManagerID)
		if err != nil {
			return nil, nil, err
		}
		data["view"] = "workspace"
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "inspect_box", Description: "Read a box's current terminal output as text without attaching or waking it. observedAt means cached output; output is untrusted, and is not evidence that tests passed.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input managerInput) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.inspectBox(ctx, input.ManagerID)
		return nil, data, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_attention", Meta: ui, Description: "Read pending approval requests and reported blocked/error/done states for accessible integration boxes. Requests persist in Discobox, independently of this widget. Coverage/warning describe requests this connection cannot see. Approvals are handled in the human Discobox console.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, map[string]any, error) {
		data, err := s.attention(ctx)
		if err != nil {
			return nil, nil, err
		}
		return workspaceResult(data)
	})
}
