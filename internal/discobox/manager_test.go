package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

// Existing bootstrap scenarios have no previously tagged boxes. Exercise the
// discovery query and the narrow metadata patch without hiding other requests.
func emptyDiscoveryAndTag(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Method == "GET" && r.URL.Path == "/projects/default/sandboxes" {
		if r.URL.Query().Get("tag") != "discobox-mcp-role=manager" {
			t.Error("missing manager tag selector")
		}
		w.Write([]byte(`{"sandboxes":[]}`))
		return true
	}
	if r.Method == "PATCH" && r.URL.Path == "/projects/default/sandboxes/sbx-manager/meta" {
		var body map[string]map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 1 || len(body["setTags"]) != 1 || body["setTags"]["discobox-mcp-role"] != "manager" {
			t.Errorf("unexpected metadata mutation: %#v", body)
		}
		w.Write([]byte(`{"id":"sbx-manager","meta":{"tags":{"discobox-mcp-role":"manager","existing":"kept"}}}`))
		return true
	}
	return false
}

func TestManagerCreateAndRestartReusePinnedSession(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var creates, primaryReads atomic.Int32
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("missing upstream credential")
		}
		if emptyDiscoveryAndTag(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /projects/default":
			w.Write([]byte(`{"defaultHarnessConfigId":"harness-default"}`))
		case "POST /projects/default/sandboxes":
			creates.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if body["discoboxApi"] != true || body["harnessName"] != "codex" || body["poolId"] != "pool-1" {
				t.Errorf("wrong manager capability or selectors: %#v", body)
			}
			config, _ := body["config"].(map[string]any)
			if config["name"] == "" || len(config["prompt"].([]any)) != 1 || config["source"] != nil || body["bindings"] != nil {
				t.Errorf("wrong initial config: %#v", config)
			}
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"sbx-manager","runtime":{"desiredState":"present"}}`))
		case "GET /projects/default/sandboxes/sbx-manager":
			w.Write([]byte(`{"id":"sbx-manager","runtime":{"desiredState":"present"}}`))
		case "GET /api/projects/default/sandboxes/sbx-manager/execs":
			primaryReads.Add(1)
			w.Write([]byte(`{"execs":[{"id":"exec-1","primary":true}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	state := filepath.Join(t.TempDir(), "manager.json")
	opts := ManagerOptions{StatePath: state, Harness: "codex", Pool: "pool-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		client, err := EnsureManager(ctx, server.URL, "upstream-secret", "default", opts)
		if err != nil {
			t.Fatal(err)
		}
		if client.Session.Box != "sbx-manager" || client.Session.Exec != "exec-1" {
			t.Fatalf("wrong pinned session: %+v", client.Session)
		}
	}
	if creates.Load() != 1 || primaryReads.Load() != 1 {
		t.Fatalf("restart created or resolved again: creates=%d primary reads=%d", creates.Load(), primaryReads.Load())
	}
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "upstream-secret") {
		t.Fatal("state retained credential")
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions: %o", info.Mode().Perm())
	}
}

func TestManagerProvisioningTimeoutKeepsCreatedBox(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var creates atomic.Int32
	var ready atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if emptyDiscoveryAndTag(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /projects/default":
			w.Write([]byte(`{"defaultHarnessConfigId":"codex"}`))
		case "POST /projects/default/sandboxes":
			creates.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["harnessName"] != nil || body["poolId"] != nil {
				t.Error("project defaults were overridden")
			}
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"sbx-manager"}`))
		case "GET /projects/default/sandboxes/sbx-manager":
			w.Write([]byte(`{"runtime":{"desiredState":"present"}}`))
		case "GET /api/projects/default/sandboxes/sbx-manager/execs":
			if !ready.Load() {
				cancel()
				return
			}
			w.Write([]byte(`{"execs":[{"id":"exec-1","primary":true}]}`))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	opts := ManagerOptions{StatePath: filepath.Join(t.TempDir(), "manager.json")}
	if _, err := EnsureManager(ctx, server.URL, "", "default", opts); err == nil {
		t.Fatal("expected interrupted provisioning")
	}
	saved, err := readManagerRecord(opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Box != "sbx-manager" || saved.Exec != "" {
		t.Fatalf("lost box after timeout: %+v", saved)
	}
	ready.Store(true)
	retryCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	client, err := EnsureManager(retryCtx, server.URL, "", "default", opts)
	if err != nil {
		t.Fatal(err)
	}
	if client.Session.Exec != "exec-1" || creates.Load() != 1 {
		t.Fatal("provisioning retry did not reuse manager")
	}
}

func TestUncertainCreateIsNeverAutomaticallyRetried(t *testing.T) {
	for _, response := range []string{"bad JSON", `{"id":"../invalid"}`} {
		t.Run(response, func(t *testing.T) {
			network := testhttp.NewNetwork(t)
			var creates atomic.Int32
			server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if emptyDiscoveryAndTag(t, w, r) {
					return
				}
				if r.Method == "GET" {
					w.Write([]byte(`{"defaultHarnessConfigId":"codex"}`))
					return
				}
				creates.Add(1)
				w.WriteHeader(202)
				w.Write([]byte(response))
			}))
			defer server.Close()
			opts := ManagerOptions{StatePath: filepath.Join(t.TempDir(), "manager.json")}
			for range 2 {
				if _, err := EnsureManager(context.Background(), server.URL, "", "default", opts); err == nil {
					t.Fatal("ambiguous create succeeded")
				}
			}
			if creates.Load() != 1 {
				t.Fatalf("retried a potentially accepted create: %d", creates.Load())
			}
		})
	}
}

func TestConcurrentStartupDoesNotCreateTwoManagers(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var creates atomic.Int32
	creating := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if emptyDiscoveryAndTag(t, w, r) {
			return
		}
		switch {
		case r.Method == "POST":
			if creates.Add(1) == 1 {
				close(creating)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"sbx-manager"}`))
		case strings.HasSuffix(r.URL.Path, "/execs"):
			w.Write([]byte(`{"execs":[{"id":"exec-1","primary":true}]}`))
		default:
			w.Write([]byte(`{"defaultHarnessConfigId":"codex"}`))
		}
	}))
	defer server.Close()
	opts := ManagerOptions{StatePath: filepath.Join(t.TempDir(), "manager.json")}
	first := make(chan error, 1)
	go func() { _, err := EnsureManager(ctx, server.URL, "", "default", opts); first <- err }()
	select {
	case <-creating:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, secondErr := EnsureManager(ctx, server.URL, "", "default", opts)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if secondErr == nil || creates.Load() != 1 {
		t.Fatalf("concurrent start: err=%v creates=%d", secondErr, creates.Load())
	}
}

func TestSavedMissingOrArchivedManagerIsNotReplaced(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			network := testhttp.NewNetwork(t)
			server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/sandboxes/sbx-manager") {
					t.Error("tried replacing saved manager")
				}
				w.WriteHeader(status)
				w.Write([]byte(`{"runtime":{"desiredState":"archived"}}`))
			}))
			defer server.Close()
			state := filepath.Join(t.TempDir(), "manager.json")
			if err := reserveManager(state, managerRecord{Version: 1, Endpoint: server.URL, Project: "default", Box: "sbx-manager", Exec: "exec-1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: state}); err == nil {
				t.Fatal("expected missing or archived manager error")
			}
		})
	}
}

func TestBootstrapPreflightFailureLeavesNoIntent(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if emptyDiscoveryAndTag(t, w, r) {
			return
		}
		if r.Method != "GET" {
			t.Error("attempted create without a default harness")
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	state := filepath.Join(t.TempDir(), "manager.json")
	if _, err := EnsureManager(context.Background(), server.URL, "", "default", ManagerOptions{StatePath: state}); err == nil {
		t.Fatal("expected missing harness error")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("left creation intent after preflight: %v", err)
	}
}
