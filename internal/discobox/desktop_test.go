package discobox

import (
	"context"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"net/http"
	"testing"
)

func TestDesktopDiscoveryRequiresDeclaredServiceAndParent(t *testing.T) {
	network := testhttp.NewNetwork(t)
	body := `{"id":"box","parentSandboxId":"caller","runtime":{"desiredState":"present","agentStatus":{"ports":[{"port":6900,"protocol":"http"}]}}}`
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/p/sandboxes/box" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer backend.Close()
	c, _ := NewCatalog(backend.URL, "", "p")
	if _, err := c.Desktop(context.Background(), "box", "caller"); err == nil {
		t.Fatal("guessed desktop from port number")
	}
	body = `{"id":"box","parentSandboxId":"other","runtime":{"agentStatus":{"ports":[{"serviceId":"ai.discobox.desktop","protocol":"http","port":6900}]}}}`
	if _, err := c.Desktop(context.Background(), "box", "caller"); err == nil {
		t.Fatal("accepted unrelated box")
	}
	body = `{"id":"box","parentSandboxId":"caller","runtime":{"desiredState":"archived","agentStatus":{"ports":[{"serviceId":"ai.discobox.desktop","protocol":"http","port":6900}]}}}`
	if _, err := c.Desktop(context.Background(), "box", "caller"); err == nil {
		t.Fatal("accepted archived box")
	}
	if _, err := c.Desktop(context.Background(), "../box", ""); err == nil {
		t.Fatal("accepted invalid ID")
	}
}
