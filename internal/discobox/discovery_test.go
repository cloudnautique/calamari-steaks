package discobox

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestDiscoveryRecoversManagerWithoutLocalState(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("discovery mutated backend: %s", r.Method)
			http.Error(w, "unexpected mutation", 500)
			return
		}
		switch r.URL.Path {
		case "/projects/default/sandboxes":
			if r.URL.Query().Get("tag") != "discobox-mcp-role=manager" {
				t.Error("wrong tag query")
			}
			// API-enabled workers must not count as managers. Stopped managers
			// are eligible; archived and deleted boxes are not.
			w.Write([]byte(`{"sandboxes":[
			{"id":"worker","discoboxApi":true},
			{"id":"old","meta":{"tags":{"discobox-mcp-role":"manager"}},"runtime":{"desiredState":"archived"}},
			{"id":"gone","meta":{"tags":{"discobox-mcp-role":"manager"}},"runtime":{"desiredState":"deleted"}},
			{"id":"sbx-manager","meta":{"tags":{"discobox-mcp-role":"manager"}},"runtime":{"desiredState":"present","runtimeState":"stopped"}}
			]}`))
		case "/api/projects/default/sandboxes/sbx-manager/execs":
			w.Write([]byte(`{"execs":[{"id":"exec-existing","primary":true}]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "manager.json")
	client, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: path, Harness: "unused-create-option"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Session.Box != "sbx-manager" || client.Session.Exec != "exec-existing" {
		t.Fatalf("wrong session: %+v", client.Session)
	}
	record, err := readManagerRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.Box != client.Session.Box || record.Exec != client.Session.Exec {
		t.Fatalf("discovery not persisted: %+v", record)
	}
}

func TestAmbiguousOrFailedDiscoveryNeverCreates(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"multiple", `{"sandboxes":[{"id":"box-b","meta":{"tags":{"discobox-mcp-role":"manager"}}},{"id":"box-a","meta":{"tags":{"discobox-mcp-role":"manager"}}}]}`, "box-a, box-b; choose one with --box ID", 200},
		{"unavailable", `{}`, "HTTP 503", 503},
		{"missing-list", `{}`, "invalid manager discovery response", 200},
		{"malformed-list", `{"sandboxes":{}}`, "invalid manager discovery response", 200},
		{"malformed-item", `{"sandboxes":[null]}`, "invalid sandbox", 200},
		{"bad-id", `{"sandboxes":[{"id":"../other","meta":{"tags":{"discobox-mcp-role":"manager"}}}]}`, "invalid tagged manager ID", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			network := testhttp.NewNetwork(t)
			server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/projects/default/sandboxes" {
					t.Errorf("unexpected discovery fallback: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "manager.json")
			_, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: path})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("discovery failure wrote state: %v", err)
			}
		})
	}
}

func TestSavedManagerWinsOverDiscovery(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/projects/default/sandboxes/saved" {
			t.Errorf("saved manager should skip discovery and mutations: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"id":"saved","meta":{"tags":{"discobox-mcp-role":"manager"}},"runtime":{"desiredState":"present"}}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := reserveManager(path, managerRecord{Version: 1, Endpoint: server.URL, Project: "default", Box: "saved", Exec: "pinned-exec"}); err != nil {
		t.Fatal(err)
	}
	c, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if c.Session.Box != "saved" || c.Session.Exec != "pinned-exec" {
		t.Fatalf("saved session replaced: %+v", c.Session)
	}
}

func TestTagFailureRetainsManagerAndRetriesWithoutCreating(t *testing.T) {
	network := testhttp.NewNetwork(t)
	creates, patches := 0, 0
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates++
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"sbx-manager"}`))
			return
		}
		if r.Method == "PATCH" {
			patches++
			if patches == 1 {
				http.Error(w, "not reachable yet", 503)
				return
			}
		}
		if emptyDiscoveryAndTag(t, w, r) {
			return
		}
		switch r.URL.Path {
		case "/projects/default":
			w.Write([]byte(`{"defaultHarnessConfigId":"codex"}`))
		case "/projects/default/sandboxes/sbx-manager":
			w.Write([]byte(`{"id":"sbx-manager","runtime":{"desiredState":"present"},"meta":{"tags":{"existing":"kept"}}}`))
		case "/api/projects/default/sandboxes/sbx-manager/execs":
			w.Write([]byte(`{"execs":[{"id":"exec-1","primary":true}]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	opts := ManagerOptions{StatePath: filepath.Join(t.TempDir(), "manager.json")}
	_, err := EnsureManager(context.Background(), server.URL, "", "default", opts)
	if err == nil || !strings.Contains(err.Error(), "discovery tag could not be set") {
		t.Fatalf("tag failure: %v", err)
	}
	record, err := readManagerRecord(opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if record.Box != "sbx-manager" || record.Exec != "exec-1" {
		t.Fatalf("tag failure lost identity: %+v", record)
	}
	if _, err := EnsureManager(context.Background(), server.URL, "", "default", opts); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || patches != 2 {
		t.Fatalf("creates=%d patches=%d", creates, patches)
	}
}
