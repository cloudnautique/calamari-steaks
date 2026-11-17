package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The adapter keeps one catalog and inbox for every view. Widgets read the
// cache; at most one upstream read is in flight. While the upstream event
// stream is connected, the cache is invalidated by events instead of age.
const (
	catalogWatchedAge = 2 * time.Minute
	catalogWatchIdle  = 2 * time.Minute
)

// catalogPolledAge is how long widgets reuse the catalog without the event
// stream. A variable so tests do not depend on how fast the machine is.
var catalogPolledAge = 10 * time.Second

// catalogModelAge bounds how stale a catalog a model-facing tool may serve.
var catalogModelAge = 2 * time.Second

type catalogCache struct {
	mu        sync.Mutex
	inbox     discobox.Inbox
	fetched   time.Time
	inflight  chan struct{}
	err       error
	watching  bool
	watchCut  context.CancelFunc
	watchID   int
	backendID int // bumped when the connection generation changes
	lastRead  time.Time
	stale     bool
	supported bool
}

func (s *Server) invalidateCatalog() {
	s.catalog.mu.Lock()
	s.catalog.stale = true
	s.catalog.mu.Unlock()
}

// snapshot returns the cached inbox, refreshing it when older than maxAge.
func (s *Server) snapshot(ctx context.Context, maxAge time.Duration) (discobox.Inbox, error) {
	c := &s.catalog
	for {
		c.mu.Lock()
		c.lastRead = time.Now()
		if c.watching {
			maxAge = max(maxAge, catalogWatchedAge)
		}
		if !c.fetched.IsZero() && !c.stale && time.Since(c.fetched) < maxAge {
			inbox := c.inbox
			c.mu.Unlock()
			return inbox, nil
		}
		if c.inflight != nil {
			wait := c.inflight
			c.mu.Unlock()
			select {
			case <-wait:
				c.mu.Lock()
				inbox, err, fresh := c.inbox, c.err, !c.fetched.IsZero() && !c.stale
				c.mu.Unlock()
				if err != nil || fresh {
					return inbox, err
				}
				continue
			case <-ctx.Done():
				return discobox.Inbox{}, ctx.Err()
			}
		}
		done := make(chan struct{})
		c.inflight = done
		c.stale = false
		generation := c.backendID
		c.mu.Unlock()
		inbox, err := s.backend().Attention(ctx, s.managerOptions.Parent)
		if s.Onboarding != nil && ctx.Err() == nil {
			s.Onboarding.Observe(err == nil)
		}
		c.mu.Lock()
		c.inflight, c.err = nil, err
		if err == nil && generation == c.backendID {
			c.inbox, c.fetched = inbox, time.Now()
		}
		c.mu.Unlock()
		close(done)
		return inbox, err
	}
}

// watchCatalog keeps the upstream event stream open while widgets are
// reading the catalog, and closes it once they stop.
func (s *Server) watchCatalog() {
	c := &s.catalog
	c.mu.Lock()
	if c.watchCut != nil || s.backend() == nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.watchCut = cancel
	c.watchID++
	id, client := c.watchID, s.backend()
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			if c.watchID == id {
				c.watching, c.watchCut = false, nil
			}
			c.mu.Unlock()
			cancel()
		}()
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c.mu.Lock()
					idle := time.Since(c.lastRead) > catalogWatchIdle
					c.mu.Unlock()
					if idle {
						cancel()
						return
					}
				}
			}
		}()
		err := client.WatchSandboxEvents(ctx, func() {
			c.mu.Lock()
			if c.watchID == id {
				c.watching, c.stale = true, true
			}
			c.mu.Unlock()
		})
		if errors.Is(err, discobox.ErrEventsUnsupported) {
			// Fall back to age-based refresh for this connection.
			c.mu.Lock()
			if c.watchID == id {
				c.watchCut, c.watching = func() {}, false
				id = -1 // keep the placeholder; the deferred reset skips it
			}
			c.mu.Unlock()
		}
	}()
}

// boxSummary is the model-visible shape of a box: enough to choose one,
// bounded in size. Full task text stays in read_box and the widget.
type boxSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	State       string `json:"state"`
	Program     string `json:"program,omitempty"`
	Message     string `json:"message,omitempty"`
	ObservedAt  string `json:"observedAt,omitempty"`
	Harness     string `json:"harness,omitempty"`
	TaskPreview string `json:"taskPreview,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	NeedsLeader bool   `json:"requiresAdoption,omitempty"`
}

type listBoxesInput struct {
	IncludeArchived bool   `json:"includeArchived,omitempty" jsonschema:"Include archived boxes. Default false."`
	NeedsAttention  bool   `json:"needsAttention,omitempty" jsonschema:"Only boxes whose agent reported blocked, error or done."`
	Query           string `json:"query,omitempty" jsonschema:"Case-insensitive match on box name, ID or task."`
	Limit           int    `json:"limit,omitempty" jsonschema:"At most this many boxes, 1 to 200. Default 50."`
	Offset          int    `json:"offset,omitempty" jsonschema:"Skip this many matching boxes, for paging."`
}

func truncateText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func summarizeBox(box discobox.Manager) boxSummary {
	return boxSummary{ID: box.ID, Name: truncateText(box.Name, 120), Role: box.Role, State: box.State, Program: textOf(box.Program, "state"), Message: truncateText(textOf(box.Program, "msg"), 160),
		ObservedAt: box.ObservedAt, Harness: box.Harness, TaskPreview: truncateText(box.Task, 200), Archived: box.Archived, NeedsLeader: !box.Accessible}
}

func textOf(m map[string]any, key string) string { value, _ := m[key].(string); return value }

func needsAttention(box discobox.Manager) bool {
	switch textOf(box.Program, "state") {
	case "blocked", "error", "done":
		return true
	}
	return false
}

// boxPage filters and pages boxes for the model.
func boxPage(boxes []discobox.Manager, input listBoxesInput) (map[string]any, error) {
	if input.Limit == 0 {
		input.Limit = 50
	}
	if input.Limit < 1 || input.Limit > 200 || input.Offset < 0 {
		return nil, errors.New("limit must be 1 to 200 and offset at least 0")
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	matches := []boxSummary{}
	archived := 0
	for _, box := range boxes {
		if box.Archived && !input.IncludeArchived {
			archived++
			continue
		}
		if input.NeedsAttention && !needsAttention(box) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(box.Name+"\n"+box.ID+"\n"+box.Task), query) {
			continue
		}
		matches = append(matches, summarizeBox(box))
	}
	total := len(matches)
	start := min(input.Offset, total)
	end := min(start+input.Limit, total)
	page := map[string]any{"boxes": matches[start:end], "total": total}
	if end < total {
		page["nextOffset"] = end
	}
	if archived > 0 {
		page["archivedHidden"] = archived
	}
	return page, nil
}

func boxTable(boxes []boxSummary) string {
	var b strings.Builder
	for _, box := range boxes {
		fmt.Fprintf(&b, "- %s (%s) · %s · %s", box.Name, box.ID, box.Role, box.State)
		if box.Program != "" {
			fmt.Fprintf(&b, " · agent %s", box.Program)
		}
		if box.Message != "" {
			fmt.Fprintf(&b, ": %s", box.Message)
		}
		if box.Archived {
			b.WriteString(" · archived")
		}
		if box.TaskPreview != "" {
			fmt.Fprintf(&b, "\n  task: %s", box.TaskPreview)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// modelResult sends the model a short text summary plus bounded structured
// data, and the widget its full private payload in _meta. Setting Content
// stops the SDK from serializing structuredContent as a second text copy.
func modelResult(text string, structured map[string]any, widget map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	if widget != nil {
		result.Meta = mcp.Meta{"connection": widget}
	}
	return result, structured, nil
}
