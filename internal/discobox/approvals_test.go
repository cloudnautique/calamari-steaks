package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestApprovalProjectAliasesDoNotPermitCrossProjectRequests(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var mismatch atomic.Bool
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("request lookup mutated state")
			http.Error(w, "unexpected mutation", 500)
			return
		}
		switch r.URL.Path {
		case "/projects/default/secret-requests/request":
			project := "canonical-project"
			if mismatch.Load() {
				project = "other-project"
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "request", "projectId": project, "sandboxId": "box", "status": "denied"})
		case "/projects/default/sandboxes/box":
			w.Write([]byte(`{"id":"box","projectId":"canonical-project","parentSandboxId":"caller"}`))
		default:
			t.Errorf("lookup escaped project or attached an exec: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := NewCatalog(server.URL, "", "default")
	if _, err := client.CredentialRequest(context.Background(), "request", "caller"); err != nil {
		t.Fatal("default alias rejected canonical project", err)
	}
	mismatch.Store(true)
	if _, err := client.CredentialRequest(context.Background(), "request", "caller"); err == nil {
		t.Fatal("accepted request from another canonical project")
	}
}
