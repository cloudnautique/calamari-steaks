package bridge

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type boxViewInput struct {
	BoxID    string `json:"boxId"`
	View     string `json:"view" jsonschema:"terminal, files or desktop"`
	FilePath string `json:"filePath,omitempty" jsonschema:"Optional absolute file path for Files; omitted opens the folder browser."`
}

func (s *Server) viewTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "show_box_view", Description: "SHOW UI: request Terminal, Files, or Desktop for an explicit box. Prefer an existing widget's select_view app tool when the host exposes it. This server tool may open a new widget if the host does not reuse one. Does not take keyboard control, type input, or return desktop pixels. Selection is requested, not proof the view loaded; widget context receipts report the selected view.", Meta: mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}}, func(ctx context.Context, _ *mcp.CallToolRequest, input boxViewInput) (*mcp.CallToolResult, map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		if input.BoxID == "" {
			return nil, nil, errors.New("boxId is required for view navigation")
		}
		if input.FilePath != "" && input.View != "files" {
			return nil, nil, errors.New("filePath applies only to the Files view")
		}
		data := map[string]any{"view": input.View, "boxId": input.BoxID, "project": s.Client.Session.Project, "navigate": true}
		switch input.View {
		case "terminal":
			connected, err := s.connectManager(ctx, input.BoxID)
			if err != nil {
				return nil, nil, err
			}
			for key, value := range connected {
				data[key] = value
			}
			data["navigate"] = true
		case "files":
			if input.FilePath != "" {
				info, err := s.Client.StatFile(ctx, input.BoxID, s.managerOptions.Parent, input.FilePath)
				if err != nil {
					return nil, nil, err
				}
				if info.Type != "file" {
					return nil, nil, errors.New("filePath must identify a regular file; omit it to browse folders")
				}
				data["file"] = info
			} else {
				if err := s.Client.BoxAccess(ctx, input.BoxID, s.managerOptions.Parent); err != nil {
					return nil, nil, err
				}
				data["directory"] = "/workspace"
			}
		case "desktop":
			if _, err := s.Client.Desktop(ctx, input.BoxID, s.managerOptions.Parent); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, errors.New("view must be terminal, files or desktop")
		}
		return workspaceResult(data)
	})
}
