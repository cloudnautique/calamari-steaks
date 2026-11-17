package discobox

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestWatchSandboxEventsResumesAndIgnoresCursorOnlyEvents(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var connections atomic.Int32
	resumed := make(chan string, 1)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/p/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if connections.Add(1) == 1 {
			w.Write([]byte("event: sandbox.created\nid: 1\ndata: {}\n\n: comment\n\nid: 2\n\nevent: sandbox.updated\nid: 3\ndata: {}\n\n"))
			return // drop the stream
		}
		resumed <- r.Header.Get("Last-Event-ID")
		<-r.Context().Done()
	}))
	c, err := NewCatalog(server.URL, "", "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var changes atomic.Int32
	done := make(chan error, 1)
	go func() { done <- c.WatchSandboxEvents(ctx, func() { changes.Add(1) }) }()
	select {
	case id := <-resumed:
		if id != "3" {
			t.Fatalf("resumed from %q", id)
		}
	case <-ctx.Done():
		t.Fatal("did not reconnect")
	}
	// Two sandbox events plus one for the dropped stream; the id-only event dispatches nothing.
	if got := changes.Load(); got != 3 {
		t.Fatalf("changes: %d", got)
	}
	cancel()
	<-done
}

func TestWatchSandboxEventsStopsOnOlderServers(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.NotFoundHandler())
	c, _ := NewCatalog(server.URL, "", "p")
	if err := c.WatchSandboxEvents(context.Background(), func() {}); err != ErrEventsUnsupported {
		t.Fatalf("got %v", err)
	}
}
