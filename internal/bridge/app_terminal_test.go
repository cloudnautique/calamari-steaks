package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppTerminalUsesMCPForReplayAndOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	backend := newBackend(t)
	s, _ := testServer(t, backend)
	client := mcpClient(t, ctx, s)
	call := func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != wantError {
			t.Fatalf("%s error=%t content=%v", name, result.IsError, result.Content)
		}
		var value map[string]any
		data, _ := json.Marshal(result.StructuredContent)
		json.Unmarshal(data, &value)
		return value
	}
	opened := call("terminal_open", map[string]any{}, false)
	id := opened["terminalId"].(string)
	defer s.closeAppTerminal(id)
	first := call("terminal_read", map[string]any{"terminalId": id, "after": 0}, false)
	repeated := call("terminal_read", map[string]any{"terminalId": id, "after": 0}, false)
	if first["cursor"] != repeated["cursor"] {
		t.Fatal("repeating a read lost output")
	}
	chunks := first["chunks"].([]any)
	if len(chunks) == 0 {
		t.Fatal("no replay")
	}
	data, err := base64.StdEncoding.DecodeString(chunks[0].(string))
	if err != nil || string(data) != "manager ready\r\n" {
		t.Fatalf("replay=%q %v", data, err)
	}
	action := func(action string, wantError bool) {
		call("terminal_action", map[string]any{"terminalId": id, "action": action, "data": base64.StdEncoding.EncodeToString([]byte("x"))}, wantError)
	}
	action("input", true)
	resized := call("terminal_action", map[string]any{"terminalId": id, "action": "resize", "cols": 110, "rows": 32}, false)
	if resized["resized"] != true {
		t.Fatal("visible viewer could not fit without claiming input")
	}
	action("input", true)
	action("claim", false)
	action("input", false)
	call("send_manager", map[string]any{"text": "must be blocked", "submit": true}, true)
	observer := call("terminal_open", map[string]any{}, false)["terminalId"].(string)
	defer s.closeAppTerminal(observer)
	call("terminal_action", map[string]any{"terminalId": observer, "action": "claim"}, true)
	resized = call("terminal_action", map[string]any{"terminalId": observer, "action": "resize", "cols": 60, "rows": 12}, false)
	if resized["resized"] != false {
		t.Fatal("observer resized underneath a human controller")
	}
	action("release", false)
	call("send_manager", map[string]any{"text": "now allowed", "submit": true}, false)
	action("close", false)
	call("terminal_read", map[string]any{"terminalId": id, "after": 0}, true)
}

func TestAppResourceIsSelfContainedAndAppToolsAreHidden(t *testing.T) {
	if err := web.Ready(); err != nil {
		t.Skip("UI assets have not been built")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	backend := newBackend(t)
	s, _ := testServer(t, backend)
	client := mcpClient(t, ctx, s)
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "terminal_") || tool.Name == "file_read" || tool.Name == "file_stat" {
			data, _ := json.Marshal(tool.Meta)
			if !strings.Contains(string(data), `"visibility":["app"]`) {
				t.Fatalf("terminal tool exposed to model: %s", data)
			}
		}
	}
	resource, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: uiResource})
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 {
		t.Fatal("missing app resource")
	}
	html := resource.Contents[0].Text
	legacy, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://discobox/manager.html"})
	if err != nil {
		t.Fatalf("restored client cannot read its published resource: %v", err)
	}
	if len(legacy.Contents) != 1 || legacy.Contents[0].URI != "ui://discobox/manager.html" || legacy.Contents[0].Text != html {
		t.Fatal("original resource URI did not return the current self-contained terminal")
	}
	if strings.Contains(html, s.Origin) || strings.Contains(html, s.Token()) || strings.Contains(html, "__ASSET_BASE__") || strings.Contains(html, "<script src=") {
		t.Fatal("resource depends on local assets or exposes an access token")
	}
	if !strings.Contains(html, "WebAssembly") || !strings.Contains(html, "terminal_open") {
		t.Fatal("resource lacks bundled renderer or MCP transport")
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "open_manager", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.Meta)
	if strings.Contains(string(encoded), s.Token()) || strings.Contains(string(encoded), s.Origin) {
		t.Fatal("embedded terminal requires localhost credentials")
	}
}
