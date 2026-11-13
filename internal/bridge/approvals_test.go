package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestApprovalReviewHistoryPrivacyAndSingleUseDecision(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var mu sync.Mutex
	status, updated := "pending", "v1"
	posts := 0
	var body map[string]any
	request := func(id string) map[string]any {
		state := status
		if id == "old" {
			state = "denied"
		}
		outcome := ""
		if state == "denied" {
			outcome = "denied"
		}
		return map[string]any{"id": id, "projectId": "canonical-p", "sandboxId": "box", "name": "github", "status": state, "createdAt": "2026-11-13T18:00:00Z", "updatedAt": updated, "decidedBy": "fixture-user", "justification": "Review this task", "secretValue": "MUST_NOT_ESCAPE", "use": map[string]any{"routes": []any{map[string]any{"hosts": []string{"api.github.com"}, "methods": []string{"GET"}, "paths": []string{"/repos/example/**"}}}, "outcome": outcome}, "delegate": map[string]any{"routes": []any{map[string]any{"hosts": []string{"github.com"}, "methods": []string{"GET"}}}, "outcome": outcome}}
	}
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/projects/p/sandboxes":
			json.NewEncoder(w).Encode(map[string]any{"sandboxes": []any{map[string]any{"id": "box", "projectId": "canonical-p", "parentSandboxId": "caller", "runtime": map[string]any{"desiredState": "present"}}}})
		case "/projects/p/sandboxes/box":
			w.Write([]byte(`{"id":"box","projectId":"canonical-p","parentSandboxId":"caller"}`))
		case "/projects/p/secret-requests":
			json.NewEncoder(w).Encode(map[string]any{"secretRequests": []any{request("current"), request("old")}})
		case "/projects/p/secret-requests/current":
			json.NewEncoder(w).Encode(request("current"))
		case "/projects/p/secret-requests/old":
			json.NewEncoder(w).Encode(request("old"))
		case "/projects/p/secrets":
			w.Write([]byte(`{"secrets":[{"id":"secret-a","name":"GitHub","maxBindingTTLSeconds":7200,"value":"MUST_NOT_ESCAPE","oauth":{"clientSecret":"MUST_NOT_ESCAPE"}}]}`))
		case "/projects/p/secret-requests/current/approve":
			if r.Method != http.MethodPost {
				t.Error("wrong approval method")
			}
			posts++
			json.NewDecoder(r.Body).Decode(&body)
			status = "approved"
			json.NewEncoder(w).Encode(request("current"))
		default:
			t.Errorf("unexpected mutation, secret-value read, wake or request resubmission: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	catalog, _ := discobox.NewCatalog(backend.URL, "", "p")
	server, _ := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{Parent: "caller"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpClient(t, ctx, server)
	call := func(name string, args map[string]any, wantError bool) *mcp.CallToolResult {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError != wantError {
			t.Fatalf("%s failed: %v %v", name, result, err)
		}
		return result
	}
	history := call("list_approval_requests", map[string]any{"status": "all"}, false)
	encoded, _ := json.Marshal(history)
	if !strings.Contains(string(encoded), "denied") {
		t.Fatal("history omitted settled requests")
	}
	old := call("open_approval", map[string]any{"requestId": "old"}, false)
	encoded, _ = json.Marshal(old)
	if !strings.Contains(string(encoded), "denied") || strings.Contains(string(encoded), "MUST_NOT_ESCAPE") {
		t.Fatal("old request missing or private data leaked")
	}
	review := call("approval_review", map[string]any{"requestId": "current"}, false)
	private := review.Meta["approval"].(map[string]any)
	token := private["reviewToken"].(string)
	encoded, _ = json.Marshal(review)
	if strings.Contains(string(encoded), "MUST_NOT_ESCAPE") {
		t.Fatal("raw secret fields passed through review")
	}
	visible, _ := json.Marshal(map[string]any{"content": review.Content, "structuredContent": review.StructuredContent})
	if strings.Contains(string(visible), token) || strings.Contains(string(visible), "api.github.com") {
		t.Fatal("private approval material entered model output")
	}
	mu.Lock()
	updated = "v2"
	mu.Unlock()
	call("approval_decide", map[string]any{"reviewToken": token, "use": "approve", "delegate": "deny", "secretId": "secret-a", "ttlSeconds": 3600}, true)
	mu.Lock()
	if posts != 0 {
		t.Fatal("stale review mutated state")
	}
	mu.Unlock()
	review = call("approval_review", map[string]any{"requestId": "current"}, false)
	token = review.Meta["approval"].(map[string]any)["reviewToken"].(string)
	decision := map[string]any{"reviewToken": token, "use": "approve", "delegate": "deny", "secretId": "secret-a", "ttlSeconds": 3600}
	call("approval_decide", decision, false)
	call("approval_decide", decision, true)
	mu.Lock()
	if posts != 1 {
		t.Fatalf("decision retried: %d", posts)
	}
	use := body["use"].(map[string]any)
	delegate := body["delegate"].(map[string]any)
	if use["scope"] != "routes" || use["as"] != "replace" || delegate["decision"] != "deny" {
		t.Fatal("approval widened scope or omitted explicit delegation decision")
	}
	mu.Unlock()
	settled := call("approval_review", map[string]any{"requestId": "old"}, false)
	if settled.Meta["approval"].(map[string]any)["reviewToken"] != nil {
		t.Fatal("settled request received an approval capability")
	}
}
