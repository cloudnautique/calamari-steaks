package bridge

import (
	"context"
	"errors"
	"path"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type boxViewInput struct {
	BoxID    string `json:"boxId"`
	View     string `json:"view" jsonschema:"terminal, files, services or desktop"`
	FilePath string `json:"filePath,omitempty" jsonschema:"Optional absolute file path for Files; omitted opens the folder browser."`
}

type openBoxInput struct {
	BoxID     string `json:"boxId,omitempty" jsonschema:"Box ID from list_boxes or launch_box, for any role or creation source."`
	ManagerID string `json:"managerId,omitempty" jsonschema:"Legacy alias for boxId; do not supply conflicting IDs."`
	View      string `json:"view,omitempty" jsonschema:"terminal (default), files, services or desktop."`
	FilePath  string `json:"filePath,omitempty" jsonschema:"With view=files: absolute path of a file to preview, e.g. /workspace/REPORT.md. Omit to browse folders."`
}

// boxView is the shared implementation of open_box and the legacy view tools.
// It validates access and returns routing data for the widget; it never takes
// keyboard focus, types input or returns desktop pixels.
func (s *Server) boxView(ctx context.Context, boxID, view, filePath string) (*mcp.CallToolResult, map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if boxID == "" {
		return nil, nil, errors.New("boxId is required; choose a box with list_boxes")
	}
	if view == "" {
		view = "terminal"
	}
	if filePath != "" && view != "files" {
		return nil, nil, errors.New("filePath applies only to view=files")
	}
	data := map[string]any{"view": view, "boxId": boxID, "project": s.Client.Session.Project}
	var content []mcp.Content
	switch view {
	case "terminal":
		connected, err := s.connectManager(ctx, boxID)
		if err != nil {
			return nil, nil, err
		}
		for key, value := range connected {
			data[key] = value
		}
		data["view"] = "workspace"
	case "files":
		if filePath != "" {
			info, err := s.Client.StatFile(ctx, boxID, s.managerOptions.Parent, filePath)
			if err != nil {
				return nil, nil, err
			}
			if info.Type != "file" {
				return nil, nil, errors.New("filePath must be a regular file; omit it to browse folders, or list them with list_box_files")
			}
			data["file"] = info
			data["directory"] = path.Dir(info.Path)
			content = append(content, fileLink(info))
		} else {
			if err := s.Client.BoxAccess(ctx, boxID, s.managerOptions.Parent); err != nil {
				return nil, nil, err
			}
			data["directory"] = "/workspace"
		}
	case "services":
		if err := s.Client.BoxAccess(ctx, boxID, s.managerOptions.Parent); err != nil {
			return nil, nil, err
		}
	case "desktop":
		info, err := s.Client.Desktop(ctx, boxID, s.managerOptions.Parent)
		if err != nil {
			return nil, nil, err
		}
		data["project"] = info.Project
		content = append(content, &mcp.TextContent{Text: "Desktop viewer requested. Desktop content stays in the widget, outside model context; opening it does not let you see the screen."})
	default:
		return nil, nil, errors.New("view must be terminal, files, services or desktop")
	}
	result := &mcp.CallToolResult{Meta: mcp.Meta{"connection": data}, Content: content}
	return result, data, nil
}

func (s *Server) viewTools(server *mcp.Server) {
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	mcp.AddTool(server, &mcp.Tool{Name: "open_box", Description: "SHOW UI: open the interactive workspace for a box (any role) when the person asks to see, show or open it. view selects terminal (default, connects live and may wake the box), files (optionally previewing filePath), services or desktop. Where hosts reuse a widget, prefer its select_view tool for switching tabs. Opening a view does not take keyboard focus, type, or show you the desktop. For text, use read_box/send_box/wait_box.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input openBoxInput) (*mcp.CallToolResult, map[string]any, error) {
		id, err := boxInput{BoxID: input.BoxID, ManagerID: input.ManagerID}.target()
		if err != nil {
			return nil, nil, err
		}
		return s.boxView(ctx, id, input.View, input.FilePath)
	})
	if !s.LegacyTools {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: "show_box_view", Description: "Legacy alias of open_box with view.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input boxViewInput) (*mcp.CallToolResult, map[string]any, error) {
		result, data, err := s.boxView(ctx, input.BoxID, input.View, input.FilePath)
		if data != nil {
			data["navigate"] = true
		}
		return result, data, err
	})
	readonly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(server, &mcp.Tool{Name: "open_box_file", Description: "Legacy alias of open_box with view=files and filePath.", Meta: ui, Annotations: readonly}, func(ctx context.Context, _ *mcp.CallToolRequest, input fileTargetInput) (*mcp.CallToolResult, map[string]any, error) {
		if input.Path == "" {
			return nil, nil, errors.New("path is required")
		}
		return s.boxView(ctx, input.BoxID, "files", input.Path)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "open_box_desktop", Description: "Legacy alias of open_box with view=desktop.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, input desktopTarget) (*mcp.CallToolResult, map[string]any, error) {
		return s.boxView(ctx, input.BoxID, "desktop", "")
	})
}
