package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestPrimaryPinAndManagerLoop(t *testing.T) {
	network := testhttp.NewNetwork(t)
	const root = "/api/projects/default/sandboxes/manager/execs"
	primaryReads := 0
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("missing upstream authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case root:
			primaryReads++
			w.Write([]byte(`{"execs":[{"id":"exec-123","primary":true}]}`))
		case root + "/exec-123/screen":
			if r.Method != "GET" || r.URL.Query().Get("scrollback") != "25" {
				t.Errorf("unexpected screen request: %s", r.URL)
			}
			w.Write([]byte(`{"cols":80,"rows":24,"lines":["ready"],"observedAt":"cached"}`))
		case root + "/exec-123/input":
			var body struct {
				Input []map[string]string `json:"input"`
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid input request")
			}
			if len(body.Input) != 2 || body.Input[0]["text"] != "review the patch" || body.Input[1]["key"] != "Enter" {
				t.Errorf("input order: %#v", body.Input)
			}
			w.Write([]byte(`{"resumeAfter":"opaque:123"}`))
		case root + "/exec-123/wait":
			var body struct {
				Timeout int `json:"timeoutSeconds"`
				Until   struct {
					After  string              `json:"after"`
					Exit   bool                `json:"exit"`
					States []map[string]string `json:"programStates"`
				} `json:"until"`
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid wait request")
			}
			if body.Timeout != 10 || body.Until.After != "opaque:123" || !body.Until.Exit || len(body.Until.States) != 4 {
				t.Errorf("incorrect wait predicate: %+v", body)
			}
			w.Write([]byte(`{"reason":"timeout","resumeAfter":"opaque:124"}`))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "upstream-secret", Session{Project: "default", Box: "manager", Exec: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.ResolvePrimary(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.ResolvePrimary(ctx); err != nil {
		t.Fatal(err)
	}
	if client.Session.Exec != "exec-123" || primaryReads != 1 {
		t.Fatal("primary exec was not pinned")
	}
	if _, err := client.Screen(ctx, 25); err != nil {
		t.Fatal(err)
	}
	sent, err := client.Send(ctx, "review the patch", true)
	if err != nil {
		t.Fatal(err)
	}
	waited, err := client.Wait(ctx, sent["resumeAfter"].(string), 10)
	if err != nil {
		t.Fatal(err)
	}
	if waited["reason"] != "timeout" || waited["resumeAfter"] != "opaque:124" {
		t.Fatalf("wait changed semantics: %#v", waited)
	}
}

func TestRejectsUnsafeTargetsAndRedirects(t *testing.T) {
	network := testhttp.NewNetwork(t)
	session := Session{Project: "default", Box: "manager", Exec: "exec-1"}
	for _, endpoint := range []string{"http://user:secret@host", "http://host/path", "http://host?token=x", "file:///tmp/socket", "unix://host/tmp/socket"} {
		if _, err := New(endpoint, "", session); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	bad := session
	bad.Box = "../another-box"
	if _, err := New("http://localhost", "", bad); err == nil {
		t.Fatal("accepted traversal")
	}
	target := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed credential-bearing redirect") }))
	defer target.Close()
	upstream := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		w.Write([]byte("do not expose upstream-secret"))
	}))
	defer upstream.Close()
	c, err := New(upstream.URL, "upstream-secret", session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Screen(context.Background(), 0); err == nil || strings.Contains(err.Error(), "upstream-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	if _, err := c.Send(context.Background(), " ", true); err == nil {
		t.Error("empty input accepted")
	}
	if _, err := c.Wait(context.Background(), "", 1); err == nil {
		t.Error("wait without cursor accepted")
	}
	if _, err := c.Screen(context.Background(), 501); err == nil {
		t.Error("unbounded screen accepted")
	}
}
