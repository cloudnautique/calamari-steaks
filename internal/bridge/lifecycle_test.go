package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// lifecycleBackend serves boxes whose primary exec can be replaced.
type lifecycleBackend struct {
	mu      sync.Mutex
	primary map[string]string
	inputs  map[string]int // by exec
}

func newLifecycleFixture(t *testing.T) (*Server, *mcp.ClientSession, *lifecycleBackend, context.Context) {
	t.Helper()
	b := &lifecycleBackend{primary: map[string]string{}, inputs: map[string]int{}}
	network := testhttp.NewNetwork(t)
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api")
		parts := strings.Split(strings.TrimPrefix(path, "/projects/p/sandboxes/"), "/")
		box := parts[0]
		b.mu.Lock()
		exec, known := b.primary[box]
		b.mu.Unlock()
		if !known {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1:
			json.NewEncoder(w).Encode(map[string]any{"id": box, "runtime": map[string]any{"desiredState": "present"}})
		case len(parts) == 2 && parts[1] == "execs":
			list := []any{}
			if exec != "" {
				list = append(list, map[string]any{"id": exec, "primary": true})
			}
			json.NewEncoder(w).Encode(map[string]any{"execs": list})
		case len(parts) == 4 && parts[1] == "execs":
			if parts[2] != exec {
				http.NotFound(w, r) // a replaced session is gone
				return
			}
			switch parts[3] {
			case "screen":
				w.Write([]byte(`{"cols":80,"rows":24,"lines":["ready"]}`))
			case "input":
				b.mu.Lock()
				b.inputs[exec]++
				b.mu.Unlock()
				w.Write([]byte(`{"resumeAfter":"c1"}`))
			case "wait":
				w.Write([]byte(`{"reason":"timeout","resumeAfter":"c0"}`))
			case "attach":
				ws, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer ws.CloseNow()
				conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
				for {
					var header [5]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						return
					}
					if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(header[1:]))); err != nil {
						return
					}
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	catalog, err := discobox.NewCatalog(backend.URL, "", "p")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return s, mcpClient(t, ctx, s), b, ctx
}

func structured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	value := result.StructuredContent
	if private, ok := result.Meta["terminal"]; ok {
		value = private // terminal transport results are app-private
	}
	raw, _ := json.Marshal(value)
	var data map[string]any
	json.Unmarshal(raw, &data)
	return data
}

func callTool(t *testing.T, ctx context.Context, client *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	if result.IsError && len(result.Content) > 0 {
		text = result.Content[0].(*mcp.TextContent).Text
	}
	return result, text
}

func TestReopenAdoptsAReplacedSessionWithoutReplayingInput(t *testing.T) {
	s, client, b, ctx := newLifecycleFixture(t)
	b.primary["box"] = "e1"
	opened, _ := callTool(t, ctx, client, "open_box", map[string]any{"boxId": "box"})
	if structured(t, opened)["session"].(map[string]any)["exec"] != "e1" {
		t.Fatal("did not pin e1")
	}
	b.mu.Lock()
	b.primary["box"] = "e2"
	b.mu.Unlock()
	result, text := callTool(t, ctx, client, "send_box", map[string]any{"boxId": "box", "text": "hello", "submit": true})
	if !result.IsError || !strings.Contains(text, "open_box") {
		t.Fatalf("stale session error not actionable: %s", text)
	}
	reopened, _ := callTool(t, ctx, client, "open_box", map[string]any{"boxId": "box"})
	data := structured(t, reopened)
	if data["sessionChanged"] != true || data["previousExec"] != "e1" || data["session"].(map[string]any)["exec"] != "e2" {
		t.Fatalf("reopen did not adopt the new session: %v", data)
	}
	if result, text := callTool(t, ctx, client, "send_box", map[string]any{"boxId": "box", "text": "hello", "submit": true}); result.IsError {
		t.Fatal(text)
	}
	if b.inputs["e1"] != 0 || b.inputs["e2"] != 1 {
		t.Fatalf("input delivery: %v", b.inputs)
	}
	// A person holding input in a view of the old session blocks adoption.
	view, _ := callTool(t, ctx, client, "terminal_open", map[string]any{"managerId": "box"})
	id := structured(t, view)["terminalId"].(string)
	callTool(t, ctx, client, "terminal_action", map[string]any{"terminalId": id, "action": "claim"})
	b.mu.Lock()
	b.primary["box"] = "e3"
	b.mu.Unlock()
	if result, text := callTool(t, ctx, client, "open_box", map[string]any{"boxId": "box"}); !result.IsError || !strings.Contains(text, "holds input") {
		t.Fatalf("adopted a session under a person's input: %s", text)
	}
	if result, text := callTool(t, ctx, client, "send_box", map[string]any{"boxId": "box", "text": "x", "submit": true}); !result.IsError || !strings.Contains(text, "human_has_control") {
		t.Fatalf("missing human_has_control code: %s", text)
	}
	_ = s
}

func TestOpenBoxReportsAStartingBox(t *testing.T) {
	_, client, b, ctx := newLifecycleFixture(t)
	b.primary["box"] = ""
	result, text := callTool(t, ctx, client, "open_box", map[string]any{"boxId": "box"})
	if result.IsError {
		t.Fatal(text)
	}
	if data := structured(t, result); data["terminal"] != "starting" || data["boxId"] != "box" {
		t.Fatalf("starting box: %v", data)
	}
}

func TestTerminalViewsAreLeasedPerBoxAndEvictedByAge(t *testing.T) {
	_, client, b, ctx := newLifecycleFixture(t)
	ids := []string{}
	for _, box := range []string{"b0", "b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8"} {
		b.primary[box] = "e-" + box
		result, text := callTool(t, ctx, client, "terminal_open", map[string]any{"managerId": box})
		if result.IsError {
			t.Fatalf("%s: %s", box, text)
		}
		ids = append(ids, structured(t, result)["terminalId"].(string))
	}
	read, _ := callTool(t, ctx, client, "terminal_read", map[string]any{"terminalId": ids[0], "after": 0})
	if data := structured(t, read); data["closed"] != true || data["evicted"] != evictedCapacity {
		// The read may also find the view already gone.
		if _, text := callTool(t, ctx, client, "terminal_read", map[string]any{"terminalId": ids[0], "after": 0}); !strings.Contains(text, "paused to make room") {
			t.Fatalf("oldest view was not evicted: %v %s", data, text)
		}
	}
	// At capacity, a new view of an open box replaces that box's older view
	// rather than pausing another box.
	again, _ := callTool(t, ctx, client, "terminal_open", map[string]any{"managerId": "b8"})
	if again.IsError {
		t.Fatal("reopen of b8 failed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, text := callTool(t, ctx, client, "terminal_read", map[string]any{"terminalId": ids[8], "after": 0})
		if strings.Contains(text, "newer view") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("older view of b8 was not superseded: %s", text)
		}
	}
	if result, text := callTool(t, ctx, client, "terminal_read", map[string]any{"terminalId": ids[1], "after": 0}); result.IsError {
		t.Fatalf("another box was paused instead: %s", text)
	}
}

func TestNewConnectionGenerationLeavesOpenViewsAlone(t *testing.T) {
	s, client, b, ctx := newLifecycleFixture(t)
	b.primary["box"] = "e1"
	view, text := callTool(t, ctx, client, "terminal_open", map[string]any{"managerId": "box"})
	if view.IsError {
		t.Fatal(text)
	}
	id := structured(t, view)["terminalId"].(string)
	other, _, b2, _ := newLifecycleFixture(t)
	b2.primary["box"] = "e9"
	s.UseBackend(other.backend())
	// New operations use the new connection...
	opened, _ := callTool(t, ctx, client, "open_box", map[string]any{"boxId": "box"})
	if exec := structured(t, opened)["session"].(map[string]any)["exec"]; exec != "e9" {
		t.Fatalf("new generation not used: %v", exec)
	}
	// ...while the open view keeps its stream and input path.
	callTool(t, ctx, client, "terminal_action", map[string]any{"terminalId": id, "action": "claim"})
	if result, text := callTool(t, ctx, client, "terminal_action", map[string]any{"terminalId": id, "action": "message", "text": "still here"}); result.IsError {
		t.Fatal(text)
	}
	if b.inputs["e1"] != 1 || b2.inputs["e9"] != 0 {
		t.Fatalf("input moved to the new connection: %v %v", b.inputs, b2.inputs)
	}
}
