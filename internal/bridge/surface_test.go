package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The default surface is part of the contract with hosts and models. Change
// these lists deliberately, together with docs/contract.md.
var (
	modelTools = []string{"archive_box", "close_service_forward", "connection_status", "control_box", "control_service", "get_backend_health", "get_connection_peer_id", "interrupt_box", "launch_box", "list_approval_requests", "list_attention", "list_box_files", "list_boxes", "list_service_forwards", "list_services", "open_approval", "open_box", "open_connection_setup", "open_service", "read_box", "read_box_file", "read_service_logs", "send_box", "setup_status", "test_connection", "wait_box"}
	appTools   = []string{"approval_decide", "approval_list", "approval_review", "catalog_read", "connection_save", "connection_test", "desktop_action", "desktop_open", "desktop_read", "file_mkdir", "file_read", "file_stat", "file_upload", "service_keepalive", "service_logs", "service_open_browser", "terminal_action", "terminal_open", "terminal_read"}
)

func TestToolSurfaceSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := New(nil, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := mcpClient(t, ctx, s)
	listed, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var model, app []string
	modelBytes := 0
	for _, tool := range listed.Tools {
		ui, _ := tool.Meta["ui"].(map[string]any)
		visibility, _ := ui["visibility"].([]any)
		if len(visibility) == 1 && visibility[0] == "app" {
			app = append(app, tool.Name)
			continue
		}
		model = append(model, tool.Name)
		encoded, _ := json.Marshal(tool)
		modelBytes += len(encoded)
	}
	sort.Strings(model)
	sort.Strings(app)
	if strings.Join(model, ",") != strings.Join(modelTools, ",") {
		t.Errorf("model-visible tools changed:\n got %v\nwant %v", model, modelTools)
	}
	if strings.Join(app, ",") != strings.Join(appTools, ",") {
		t.Errorf("app-only tools changed:\n got %v\nwant %v", app, appTools)
	}
	init := client.InitializeResult()
	total := modelBytes + len(init.Instructions)
	t.Logf("model-visible tool schemas %d B, instructions %d B", modelBytes, len(init.Instructions))
	if len(init.Instructions) > 1600 || total > 20<<10 {
		t.Errorf("model-visible surface grew: schemas %d B + instructions %d B", modelBytes, len(init.Instructions))
	}
	if init.ServerInfo.Name != "discobox" {
		t.Errorf("server name %q", init.ServerInfo.Name)
	}
	resources, err := client.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 2 {
		t.Fatalf("listed resources: %v %v", resources, err)
	}
}

func TestLegacyUIResourcesStillResolve(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := New(nil, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := mcpClient(t, ctx, s)
	for _, uri := range []string{uiResource, "ui://discobox/workspace-v16.html", "ui://discobox/workspace-v13.html", "ui://discobox/manager.html", setupResourceURI, "ui://discobox/connection-v2.html", "ui://discobox/connection-v1.html"} {
		result, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(result.Contents) != 1 || !strings.Contains(result.Contents[0].Text, "<") || result.Contents[0].URI != uri {
			t.Errorf("%s: %v", uri, err)
		}
	}
	if _, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://discobox/unknown.html"}); err == nil {
		t.Error("unknown ui:// resource resolved")
	}
}

func TestModelCatalogIsBoundedAndCached(t *testing.T) {
	// Under the race detector on a slow runner, serializing 100 large boxes
	// can outlast the default reuse window; caching is what is tested here.
	polled, model := catalogPolledAge, catalogModelAge
	catalogPolledAge, catalogModelAge = time.Hour, time.Hour
	t.Cleanup(func() { catalogPolledAge, catalogModelAge = polled, model })
	network := testhttp.NewNetwork(t)
	lists := 0
	task := strings.Repeat("Implement the feature carefully. ", 2000) // ~64 KiB
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api") {
		case "/projects/p/sandboxes":
			lists++
			items := []any{}
			for i := range 100 {
				desired := "present"
				if i%10 == 0 {
					desired = "archived"
				}
				state := "idle"
				if i == 7 {
					state = "blocked"
				}
				items = append(items, map[string]any{"id": fmt.Sprintf("box-%03d", i), "config": map[string]any{"name": fmt.Sprintf("task-%03d", i), "prompt": []string{task}}, "runtime": map[string]any{"desiredState": desired, "displayState": "running", "agentStatus": map[string]any{"sessions": []any{map[string]any{"primary": true, "programStatus": map[string]any{"state": state, "msg": "waiting"}}}}}})
			}
			json.NewEncoder(w).Encode(map[string]any{"sandboxes": items})
		case "/projects/p/approval-requests":
			w.Write([]byte(`{"approvalRequests":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	catalog, err := discobox.NewCatalog(backend.URL, "", "p")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := mcpClient(t, ctx, s)
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "list_boxes", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatal(err, result.Content[0].(*mcp.TextContent).Text)
	}
	visible, _ := json.Marshal(map[string]any{"content": result.Content, "structured": result.StructuredContent})
	t.Logf("model catalog page: %d B", len(visible))
	if len(visible) > 25<<10 {
		t.Fatalf("model-visible list_boxes is %d bytes for 100 boxes", len(visible))
	}
	var page struct {
		Boxes          []boxSummary `json:"boxes"`
		Total          int          `json:"total"`
		NextOffset     int          `json:"nextOffset"`
		ArchivedHidden int          `json:"archivedHidden"`
	}
	raw, _ := json.Marshal(result.StructuredContent)
	json.Unmarshal(raw, &page)
	if page.Total != 90 || len(page.Boxes) == 0 || page.NextOffset != len(page.Boxes) || page.ArchivedHidden != 10 {
		t.Fatalf("paging: total=%d page=%d next=%d archived=%d", page.Total, len(page.Boxes), page.NextOffset, page.ArchivedHidden)
	}
	if len(page.Boxes[0].TaskPreview) > 210 {
		t.Fatal("task preview not truncated")
	}
	attention, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "list_boxes", Arguments: map[string]any{"needsAttention": true}})
	if err != nil || !strings.Contains(attention.Content[0].(*mcp.TextContent).Text, "box-007") {
		t.Fatal("needsAttention filter", err)
	}
	for range 3 {
		appPage, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "catalog_read", Arguments: map[string]any{"offset": 20}})
		if err != nil || appPage.IsError {
			t.Fatal("app catalog page failed", err)
		}
		wire, _ := json.Marshal(appPage)
		boxes, _ := appPage.StructuredContent.(map[string]any)["boxes"].([]any)
		if len(boxes) != 20 || len(wire) > 24<<10 {
			t.Fatalf("app catalog page has %d boxes and %d bytes", len(boxes), len(wire))
		}
		t.Logf("app catalog page on the wire: %d B", len(wire))
	}
	if lists != 1 {
		t.Fatalf("cached catalog was fetched %d times", lists)
	}
	widget, _ := json.Marshal(result.Meta["connection"])
	if !strings.Contains(string(widget), `"view":"boxes"`) || len(widget) > 256 {
		t.Fatalf("widget catalog missing or unbounded: %d bytes", len(widget))
	}
}

func TestBrowserUIIsOptIn(t *testing.T) {
	s, err := New(nil, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler(http.NotFoundHandler())
	for _, path := range []string{"/", "/setup", "/api/session", "/terminal"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
		request.Header.Set("Authorization", "Bearer "+s.Token())
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("%s served without --browser-ui: %d", path, recorder.Code)
		}
	}
}

func TestBackendHealthToolUsesActiveProject(t *testing.T) {
	backend := testhttp.NewNetwork(t).NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/research/pools" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"pools":[{"id":"lab","resources":{"storage":{"filesystem":{"freeBytes":3221225472}}}}]}`))
	}))
	defer backend.Close()
	catalog, err := discobox.NewCatalog(backend.URL, "", "research")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	client := mcpClient(t, ctx, s)
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "get_backend_health", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal(result.Content)
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil || !strings.Contains(string(b), `"freeGiB":3`) {
		t.Fatalf("health result %s, %v", b, err)
	}
}
