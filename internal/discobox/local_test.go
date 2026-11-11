package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestLocalConnectionConfig(t *testing.T) {
	const identity = `{"sandboxId":"sbx-caller"}`
	config, err := parseLocalConnection("https://api.discobox.internal/", "", []byte(identity))
	if err != nil {
		t.Fatal(err)
	}
	if config.Endpoint != "https://api.discobox.internal" || config.Project != "default" || config.Parent != "sbx-caller" {
		t.Fatalf("unexpected configuration: %+v", config)
	}
	config, err = parseLocalConnection("https://api.discobox.internal", "proj-real", []byte(identity))
	if err != nil || config.Project != "proj-real" {
		t.Fatalf("project: %+v %v", config, err)
	}
	for _, api := range []string{"unix:///server.sock", "discobox://peer", "https://user:secret@host", "https://host/path", "https://host?key=x", "https://host?", "https://host#fragment"} {
		if _, err := parseLocalConnection(api, "", []byte(identity)); err == nil {
			t.Errorf("accepted %s", api)
		}
	}
	for _, data := range []string{`{}`, `null`, `{"sandboxId":"../other"}`, `invalid`} {
		if _, err := parseLocalConnection("https://host", "", []byte(data)); err == nil {
			t.Errorf("accepted config %s", data)
		}
	}
}

func TestLocalManagerCreateUsesDefaultsAndReusesState(t *testing.T) {
	network := testhttp.NewNetwork(t)
	creates := 0
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("local API sent a bearer token")
		}
		if emptyDiscoveryAndTag(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /projects/default/sandboxes":
			creates++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["harnessName"] != nil || body["poolId"] != nil || body["discoboxApi"] != true {
				t.Errorf("wrong create: %#v", body)
			}
			w.Write([]byte(`{"id":"sbx-manager","parentSandboxId":"sbx-caller"}`))
		case "GET /projects/default/sandboxes/sbx-manager":
			w.Write([]byte(`{"id":"sbx-manager","parentSandboxId":"sbx-caller","meta":{"tags":{"discobox-mcp-role":"manager"}}}`))
		case "GET /api/projects/default/sandboxes/sbx-manager/execs":
			w.Write([]byte(`{"execs":[{"id":"exec-1","primary":true}]}`))
		default:
			t.Errorf("local bootstrap used forbidden/unexpected endpoint: %s %s", r.Method, r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	defer server.Close()
	options := ManagerOptions{StatePath: filepath.Join(t.TempDir(), "manager.json"), Parent: "sbx-caller"}
	for range 2 {
		client, err := EnsureManager(context.Background(), server.URL, "", "default", options)
		if err != nil {
			t.Fatal(err)
		}
		if client.Session.Box != "sbx-manager" || client.Session.Exec != "exec-1" {
			t.Fatalf("session: %+v", client.Session)
		}
	}
	if creates != 1 {
		t.Fatalf("created %d managers", creates)
	}
	record, err := readManagerRecord(options.StatePath)
	if err != nil || record.Parent != options.Parent {
		t.Fatalf("parent not persisted: %+v %v", record, err)
	}
}

func TestLocalDiscoveryRequiresCurrentParent(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "adoption-required", true: "select-owned"}[owned], func(t *testing.T) {
			network := testhttp.NewNetwork(t)
			server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("discovery mutated backend")
					http.Error(w, "unexpected", 500)
					return
				}
				switch r.URL.Path {
				case "/projects/default/sandboxes":
					boxes := []any{map[string]any{"id": "foreign", "meta": map[string]any{"tags": map[string]string{managerTagKey: managerTagValue}}}}
					if owned {
						boxes = append(boxes, map[string]any{"id": "owned", "parentSandboxId": "sbx-caller", "meta": map[string]any{"tags": map[string]string{managerTagKey: managerTagValue}}})
					}
					json.NewEncoder(w).Encode(map[string]any{"sandboxes": boxes})
				case "/api/projects/default/sandboxes/owned/execs":
					w.Write([]byte(`{"execs":[{"id":"exec-1","primary":true}]}`))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "manager.json")
			client, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: path, Parent: "sbx-caller"})
			if owned {
				if err != nil {
					t.Fatal(err)
				}
				if client.Session.Box != "owned" {
					t.Fatal("selected foreign manager")
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "discobox adopt") {
					t.Fatalf("expected adoption instruction: %v", err)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("discovery reserved a create")
				}
			}
		})
	}
}

func TestLocalSavedManagerLosingParentNeverCreatesReplacement(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/projects/default/sandboxes/manager" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		w.Write([]byte(`{"id":"manager","parentSandboxId":"new-parent"}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "manager.json")
	record := managerRecord{Version: 1, Endpoint: server.URL, Project: "default", Box: "manager", Exec: "exec-1", Parent: "sbx-caller"}
	if err := reserveManager(path, record); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: path, Parent: "sbx-caller"}); err == nil || !strings.Contains(err.Error(), "not currently led") {
		t.Fatalf("expected lost-parent refusal: %v", err)
	}
	saved, err := readManagerRecord(path)
	if err != nil || saved != record {
		t.Fatal("saved manager state changed")
	}
}
