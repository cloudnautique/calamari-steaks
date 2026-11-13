package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestTaskLaunchKeepsExactIntentWithoutManagerPrivileges(t *testing.T) {
	network := testhttp.NewNetwork(t)
	creates := 0
	var box map[string]any
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api")
		switch {
		case r.Method == "GET" && path == "/projects/p/sandboxes":
			json.NewEncoder(w).Encode(map[string]any{"sandboxes": []any{}})
		case r.Method == "POST" && path == "/projects/p/sandboxes":
			creates++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["discoboxApi"] != false {
				t.Error("ordinary task received manager API access")
			}
			if _, ok := body["bindings"]; ok {
				t.Error("task received explicit credential bindings")
			}
			if body["harnessName"] != "codex" || body["poolId"] != "pool-a" {
				t.Errorf("lost run settings: %v", body)
			}
			config := body["config"].(map[string]any)
			prompt := config["prompt"].([]any)
			if len(prompt) != 1 || prompt[0] != "Review exactly.\nDo not edit." || config["model"] != "test-model" {
				t.Errorf("task/settings changed: %v", config)
			}
			box = map[string]any{"id": "task-a", "parentSandboxId": "caller", "config": config, "runtime": map[string]any{"desiredState": "present"}}
			json.NewEncoder(w).Encode(box)
		case path == "/projects/p/sandboxes/task-a/meta":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["setTags"].(map[string]any)[managerTagKey] != "box" {
				t.Error("task tagged as manager")
			}
			box["meta"] = map[string]any{"tags": body["setTags"]}
			json.NewEncoder(w).Encode(box)
		case path == "/projects/p/sandboxes/task-a":
			json.NewEncoder(w).Encode(box)
		case path == "/projects/p/sandboxes/task-a/execs":
			w.Write([]byte(`{"execs":[{"id":"exec-a","primary":true}]}`))
		default:
			t.Errorf("unexpected operation %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := NewCatalog(server.URL, "", "p")
	options := ManagerOptions{Parent: "caller", StatePath: filepath.Join(t.TempDir(), "state.json"), Name: "review", Role: "box", Task: "Review exactly.\nDo not edit.", Model: "test-model", Harness: "codex", Pool: "pool-a"}
	first, err := client.CreateManager(context.Background(), "same-task", options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CreateManager(context.Background(), "same-task", options)
	if err != nil {
		t.Fatal(err)
	}
	if creates != 1 || first.Session != second.Session {
		t.Fatal("retry changed the box or exec")
	}
	options.Task = "Different task"
	if _, err := client.CreateManager(context.Background(), "same-task", options); err == nil {
		t.Fatal("changed task reused an intent")
	}
	options.Task = "Review exactly.\nDo not edit."
	options.Role = ""
	if _, err := client.CreateManager(context.Background(), "same-task", options); err == nil {
		t.Fatal("task intent silently acquired manager role")
	}
	if creates != 1 {
		t.Fatal("intent mismatch created another box")
	}
}

func TestAttentionUsesDurableRequestsAndDoesNotWakeBoxes(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatalf("attention mutated state: %s", r.Method)
		}
		switch r.URL.Path {
		case "/projects/p/sandboxes":
			if r.URL.RawQuery != "" {
				t.Error("all-box listing sent a tag filter")
			}
			w.Write([]byte(`{"sandboxes":[
   {"id":"owned","parentSandboxId":"caller","config":{"name":"Review","prompt":["Review code"]},"meta":{"tags":{"discobox-mcp-role":"box"}},"runtime":{"desiredState":"present","displayState":"stopped","runtimeState":"running","agentStatusObservedAt":"2026-11-12T10:00:00Z","agentStatus":{"sessions":[{"primary":true,"programStatus":{"state":"blocked","msg":"Which branch?","secret":"must-not-forward"}}]}}},
   {"id":"foreign","parentSandboxId":"other","meta":{"tags":{"discobox-mcp-role":"manager"}},"runtime":{"desiredState":"present"}},
   {"id":"archived","parentSandboxId":"caller","meta":{"tags":{"discobox-mcp-role":"box"}},"runtime":{"desiredState":"archived"}},
   {"id":"unrelated","parentSandboxId":"caller","runtime":{"desiredState":"present"}}
  ]}`))
		case "/projects/p/secret-requests":
			w.Write([]byte(`{"secretRequests":[
   {"id":"req-owned","sandboxId":"owned","status":"pending","name":"GitHub","justification":"Read repository","ttlSeconds":3600,"secretValue":"never expose","use":{"routes":[{"url":"https://github.com/acme"}],"secret":"do not expose"}},
   {"id":"req-foreign","sandboxId":"foreign","status":"pending"},
   {"id":"req-archived","sandboxId":"archived","status":"pending"},
   {"id":"req-closed","sandboxId":"owned","status":"denied"}
  ]}`))
		default:
			t.Errorf("attention attached or read outside inventory: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := NewCatalog(server.URL, "", "p")
	boxes, err := client.ListBoxes(context.Background(), "caller")
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 4 {
		t.Fatalf("visible boxes: %d", len(boxes))
	}
	for _, box := range boxes {
		if box.ID == "owned" && box.State != "stopped" {
			t.Error("ignored authoritative runtime displayState")
		}
	}
	inbox, err := client.Attention(context.Background(), "caller")
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Items) != 2 || inbox.Warning == "" {
		t.Fatalf("wrong scope/coverage: %+v", inbox)
	}
	encoded, _ := json.Marshal(inbox)
	if strings.Contains(string(encoded), "must-not-forward") || strings.Contains(string(encoded), "secretValue") || strings.Contains(string(encoded), "do not expose") {
		t.Fatal("forwarded credential or opaque program fields")
	}
	if inbox.Items[0].ObservedAt == "" {
		t.Error("lost cached status timestamp")
	}
	// A new client sees the same pending request after the first view disappears.
	restored, _ := NewCatalog(server.URL, "", "p")
	again, err := restored.Attention(context.Background(), "caller")
	if err != nil || len(again.Items) != 2 {
		t.Fatal("inbox depended on a live widget")
	}
}

func TestAttentionUnavailableIsNotReportedAsEmptyCompleteInbox(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sandboxes") {
			w.Write([]byte(`{"sandboxes":[]}`))
			return
		}
		w.WriteHeader(403)
	}))
	defer server.Close()
	client, _ := NewCatalog(server.URL, "", "p")
	inbox, err := client.Attention(context.Background(), "")
	if err != nil || inbox.Warning == "" || inbox.Coverage != "reported box status only" {
		t.Fatalf("permission gap hidden: %+v %v", inbox, err)
	}
}
