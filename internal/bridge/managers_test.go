package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagerWorkspaceLifecycleAndScopedChat(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var mu sync.Mutex
	boxes := map[string]map[string]any{}
	managerBox := func(id, parent string) map[string]any {
		return map[string]any{"id": id, "parentSandboxId": parent, "config": map[string]any{"name": id}, "runtime": map[string]any{"desiredState": "present", "runtimeState": "running"}, "meta": map[string]any{"tags": map[string]any{"discobox-mcp-role": "manager"}}}
	}
	boxes["alpha"] = managerBox("alpha", "caller")
	boxes["beta"] = managerBox("beta", "caller")
	boxes["foreign"] = managerBox("foreign", "someone-else")
	creates := 0
	primary := map[string]int{}
	inputs := map[string]int{}
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api")
		const root = "/projects/p/sandboxes"
		reply := func(value any) { json.NewEncoder(w).Encode(value) }
		if path == root {
			switch r.Method {
			case "GET":
				items := []any{}
				for _, box := range boxes {
					items = append(items, box)
				}
				reply(map[string]any{"sandboxes": items})
			case "POST":
				creates++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				id := "created-" + string(rune('a'+creates-1))
				box := managerBox(id, "caller")
				config := body["config"].(map[string]any)
				box["config"] = config
				boxes[id] = box
				reply(box)
			default:
				t.Errorf("unexpected method %s", r.Method)
			}
			return
		}
		suffix := strings.TrimPrefix(path, root+"/")
		parts := strings.Split(suffix, "/")
		id := parts[0]
		box := boxes[id]
		if box == nil {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if box["parentSandboxId"] != "caller" {
			http.Error(w, "not led by caller", 403)
			return
		}
		if len(parts) == 1 {
			if r.Method == "DELETE" {
				box["runtime"] = map[string]any{"desiredState": "archived"}
				w.WriteHeader(http.StatusAccepted)
				return
			}
			reply(box)
			return
		}
		if parts[1] == "meta" {
			reply(box)
			return
		}
		if len(parts) == 2 && parts[1] == "execs" {
			primary[id]++
			reply(map[string]any{"execs": []any{map[string]any{"id": "exec-" + id, "primary": true}}})
			return
		}
		if len(parts) != 4 || parts[2] != "exec-"+id {
			t.Errorf("escaped pinned exec: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch parts[3] {
		case "screen":
			reply(map[string]any{"cols": 80, "rows": 24, "lines": []string{id + " ready"}})
		case "input":
			inputs[id]++
			reply(map[string]any{"resumeAfter": "cursor-" + id})
		case "wait":
			reply(map[string]any{"reason": "timeout", "resumeAfter": "cursor-" + id})
		case "attach":
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			// An attached stream must not hold the fake control-plane lock.
			mu.Unlock()
			conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
			for {
				var header [5]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					break
				}
				size := binary.BigEndian.Uint32(header[1:])
				if _, err := io.CopyN(io.Discard, conn, int64(size)); err != nil {
					break
				}
				if header[0] == discobox.Ready {
					payload := []byte(id + " ready")
					frame := append([]byte{discobox.Stdout, 0, 0, 0, byte(len(payload))}, payload...)
					if _, err := conn.Write(frame); err != nil {
						break
					}
				}
			}
			ws.CloseNow()
			mu.Lock()
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	catalog, err := discobox.NewCatalog(backend.URL, "", "p")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{Parent: "caller", StatePath: filepath.Join(t.TempDir(), "startup.json")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcpClient(t, ctx, s)
	call := func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != wantError {
			t.Fatalf("%s error=%v content=%v", name, result.IsError, result.Content)
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		var data map[string]any
		json.Unmarshal(encoded, &data)
		return data
	}
	boxCatalog := call("list_boxes", map[string]any{}, false)
	if len(boxCatalog["boxes"].([]any)) != 3 || boxCatalog["listingScope"] == nil || boxCatalog["roleMeaning"] == nil {
		t.Fatal("box listing lost its scope or role semantics")
	}
	if _, legacy := boxCatalog["managers"]; legacy {
		t.Fatal("box catalog mislabeled boxes as managers")
	}
	listed := call("list_managers", map[string]any{}, false)
	if len(listed["managers"].([]any)) != 3 || creates != 0 {
		t.Fatal("listing created or lost managers")
	}
	call("connect_manager", map[string]any{"managerId": "foreign"}, true)
	call("connect_manager", map[string]any{"managerId": "../alpha"}, true)
	call("read_manager", map[string]any{}, true)
	call("connect_manager", map[string]any{"managerId": "alpha"}, false)
	call("connect_manager", map[string]any{"managerId": "beta"}, false)
	read := call("read_manager", map[string]any{"managerId": "alpha"}, false)
	if read["lines"].([]any)[0] != "alpha ready" {
		t.Fatal("connection switched managers")
	}
	view := call("terminal_open", map[string]any{"managerId": "alpha"}, false)["terminalId"].(string)
	defer s.closeAppTerminal(view)
	call("terminal_action", map[string]any{"terminalId": view, "action": "claim"}, false)
	call("send_manager", map[string]any{"managerId": "alpha", "text": "blocked", "submit": true}, true)
	call("send_manager", map[string]any{"managerId": "beta", "text": "other manager can work", "submit": true}, false)
	call("archive_manager", map[string]any{"managerId": "alpha"}, true)
	call("terminal_action", map[string]any{"terminalId": view, "action": "message", "text": "direct user message"}, false)
	call("terminal_action", map[string]any{"terminalId": view, "action": "release"}, false)
	connection, _ := s.manager(ctx, "alpha")
	call("archive_manager", map[string]any{"managerId": "alpha"}, false)
	if err := connection.writeAs("mcp", func() error { t.Error("write reached an archived connection"); return nil }); err == nil {
		t.Fatal("stale view could write after archive")
	}
	call("send_manager", map[string]any{"managerId": "alpha", "text": "must not reach archived session", "submit": true}, true)
	create := map[string]any{"requestId": "one-intent", "name": "new-review"}
	first := call("create_manager", create, false)
	second := call("create_manager", create, false)
	if first["managerId"] != second["managerId"] || creates != 1 {
		t.Fatal("creation retry duplicated a manager")
	}
	call("create_manager", map[string]any{"requestId": "one-intent", "name": "changed-intent"}, true)
	mu.Lock()
	defer mu.Unlock()
	if inputs["alpha"] != 1 || inputs["beta"] != 1 {
		t.Fatalf("cross-manager input: %#v", inputs)
	}
	if primary["beta"] != 1 {
		t.Fatal("replaced pinned exec")
	}
	if catalog.Session.Box != "" {
		t.Fatal("catalog acquired a mutable global selection")
	}
}
