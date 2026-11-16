package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/connection"
	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSetupAvailableWithNoBackend(t *testing.T) {
	ctx := context.Background()
	s, e := New(nil, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if e != nil {
		t.Fatal(e)
	}
	s.Onboarding = connection.New(connection.Config{Endpoint: "unix://", Project: "default"}, filepath.Join(t.TempDir(), "settings.json"), filepath.Join(t.TempDir(), "identity"), false, func(context.Context, connection.Config) connection.Result {
		return connection.Result{State: "socket_missing", Message: "Missing fixture socket"}
	}, errors.New("fixture startup failure"))
	client := mcpClient(t, ctx, s)
	listed, e := client.ListTools(ctx, nil)
	if e != nil || len(listed.Tools) != 51 {
		t.Fatalf("list tools: %v %v", listed, e)
	}
	for _, name := range []string{"connection_status", "open_connection_setup", "get_connection_peer_id", "test_connection"} {
		result, e := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if e != nil || result.IsError {
			t.Fatalf("%s unavailable: %v %+v", name, e, result)
		}
	}
	result, e := client.CallTool(ctx, &mcp.CallToolParams{Name: "read_box", Arguments: map[string]any{"boxId": "test"}})
	if e != nil || !result.IsError {
		t.Fatalf("backend call was not gated: %v %+v", e, result)
	}
	html, e := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: setupResource})
	if e != nil || !strings.Contains(html.Contents[0].Text, "Connect to Discobox") {
		t.Fatal("setup resource unavailable", e)
	}
	_, e = client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "discobox-file://default/test/workspace/a.md"})
	if e == nil {
		t.Fatal("ungated file resource")
	}
	result, e = client.CallTool(ctx, &mcp.CallToolParams{Name: "connection_save", Arguments: map[string]any{"endpoint": "unix:///tmp/fixture.sock", "project": "default"}})
	if e != nil || result.IsError {
		t.Fatalf("save unavailable: %v %+v", e, result)
	}
	raw, _ := json.Marshal(result.Content)
	if strings.Contains(string(raw), "fixture.sock") {
		t.Fatal("private save details were put in model-visible acknowledgement")
	}
	if result.Meta["onboarding"] == nil {
		t.Fatal("missing app-only status payload")
	}
}
