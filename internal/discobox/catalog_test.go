package discobox

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestExplicitCreateKeepsUncertainIntentAndNeverDuplicates(t *testing.T) {
	network := testhttp.NewNetwork(t)
	creates := 0
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/projects/p/sandboxes" {
			w.Write([]byte(`{"sandboxes":[]}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/projects/p/sandboxes" {
			creates++
			w.Write([]byte(`bad JSON after an uncertain create`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	client, err := NewCatalog(server.URL, "", "p")
	if err != nil {
		t.Fatal(err)
	}
	options := ManagerOptions{Parent: "caller", StatePath: filepath.Join(t.TempDir(), "manager.json")}
	if _, err := client.CreateManager(context.Background(), "intent-1", options); err == nil {
		t.Fatal("expected uncertain create")
	}
	if _, err := client.CreateManager(context.Background(), "intent-1", options); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected retained unknown intent: %v", err)
	}
	if creates != 1 {
		t.Fatalf("uncertain creation was repeated %d times", creates)
	}
}

func TestDeleteAcceptsDocumentedEmptySuccessResponses(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			network := testhttp.NewNetwork(t)
			server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			client, err := NewCatalog(server.URL, "", "p")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.request(context.Background(), http.MethodDelete, "/projects/p/sandboxes/test", nil); err != nil {
				t.Fatalf("empty DELETE success rejected: %v", err)
			}
			if status == http.StatusAccepted {
				if _, err := client.request(context.Background(), http.MethodPost, "/projects/p/sandboxes", nil); err == nil {
					t.Fatal("accepted an empty creation response as confirmed")
				}
			}
		})
	}
}
