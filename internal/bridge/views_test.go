package bridge

import (
	"context"
	"encoding/json"
	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"testing"
	"time"
)

func TestViewSelectionIsScopedAndFilesNeedNoTerminal(t *testing.T) {
	network := testhttp.NewNetwork(t)
	execReads := 0
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("view selection mutated upstream")
		}
		switch r.URL.Path {
		case "/projects/p/sandboxes/box":
			w.Write([]byte(`{"id":"box","parentSandboxId":"caller","runtime":{"desiredState":"present","agentStatus":{"ports":[{"serviceId":"ai.discobox.desktop","port":6900,"protocol":"http"}]}}}`))
		case "/api/projects/p/sandboxes/box/execs":
			execReads++
			w.Write([]byte(`{"execs":[{"id":"e","primary":true}]}`))
		default:
			t.Errorf("unexpected navigation read: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	client, _ := discobox.NewCatalog(backend.URL, "", "p")
	server, _ := New(client, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{Parent: "caller"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mcpClient := mcpClient(t, ctx, server)
	for _, view := range []string{"files", "desktop", "terminal"} {
		result, err := mcpClient.CallTool(ctx, &mcp.CallToolParams{Name: "show_box_view", Arguments: map[string]any{"boxId": "box", "view": view}})
		if err != nil || result.IsError {
			t.Fatalf("view %s failed: %v %v", view, result, err)
		}
		var data map[string]any
		raw, _ := json.Marshal(result.StructuredContent)
		json.Unmarshal(raw, &data)
		if data["view"] != view || data["boxId"] != "box" {
			t.Fatal("navigation retargeted view")
		}
		if view != "terminal" && execReads != 0 {
			t.Fatal("file/desktop navigation depended on a terminal")
		}
	}
	result, err := mcpClient.CallTool(ctx, &mcp.CallToolParams{Name: "show_box_view", Arguments: map[string]any{"boxId": "box", "view": "unknown"}})
	if err != nil || !result.IsError {
		t.Fatal("accepted invalid view")
	}
}
