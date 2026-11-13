package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFileToolsAndResourceShareTheSameAuthorizedContent(t *testing.T) {
	network := testhttp.NewNetwork(t)
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/p/sandboxes/box":
			w.Write([]byte(`{"id":"box","parentSandboxId":"caller","runtime":{"desiredState":"present"}}`))
		case "/api/projects/p/sandboxes/box/files/stat":
			if r.URL.Query().Get("path") == "/workspace/empty.md" {
				w.Write([]byte(`{"path":"/workspace/empty.md","name":"empty.md","type":"file","size":0,"version":"v1"}`))
				return
			}
			w.Write([]byte(`{"path":"/workspace/report.md","name":"report.md","type":"file","size":9,"version":"v1"}`))
		case "/api/projects/p/sandboxes/box/files/content":
			if r.URL.Query().Get("path") == "/workspace/empty.md" {
				return
			}
			w.Write([]byte("# Report\n"))
		case "/api/projects/p/sandboxes/box/files/list":
			w.Write([]byte(`{"path":"/workspace","entries":[{"path":"/workspace/report.md","name":"report.md","type":"file","size":9,"version":"v1"}]}`))
		default:
			t.Errorf("unexpected file operation or terminal connection: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	catalog, _ := discobox.NewCatalog(backend.URL, "", "p")
	server, err := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{Parent: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcpClient(t, ctx, server)
	uri := "discobox-file://p/box/workspace/report.md"
	defaultFolder, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "list_box_files", Arguments: map[string]any{"boxId": "box"}})
	if err != nil || defaultFolder.IsError {
		t.Fatalf("default directory: %v %v", defaultFolder, err)
	}

	for _, name := range []string{"list_box_files", "read_box_file", "open_box_file"} {
		target := "/workspace/report.md"
		if name == "list_box_files" {
			target = "/workspace"
		}
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"boxId": "box", "path": target}})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %v", name, result, err)
		}
		if name == "read_box_file" {
			encoded, _ := json.Marshal(result)
			if strings.Count(string(encoded), "# Report") != 1 {
				t.Fatal("file contents duplicated in model response")
			}
		}
		linkFound := false
		for _, content := range result.Content {
			if link, ok := content.(*mcp.ResourceLink); ok && link.URI == uri {
				linkFound = true
			}
		}
		if !linkFound {
			t.Fatalf("%s omitted resource link", name)
		}
		if name == "open_box_file" && result.Meta["connection"] == nil {
			t.Fatal("open file omitted canvas target")
		}
	}
	resource, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil || len(resource.Contents) != 1 || resource.Contents[0].Text != "# Report\n" {
		t.Fatalf("resource read: %v %v", resource, err)
	}
	if _, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "discobox-file://other/box/workspace/report.md"}); err == nil {
		t.Fatal("resource escaped project")
	}
	empty, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: "discobox-file://p/box/workspace/empty.md"})
	if err != nil {
		t.Fatal(err)
	}
	encodedEmpty, _ := json.Marshal(empty)
	if !strings.Contains(string(encodedEmpty), `"blob":""`) {
		t.Fatalf("empty resource lacks protocol content field: %s", encodedEmpty)
	}
	templates, err := client.ListResourceTemplates(ctx, nil)
	if err != nil || len(templates.ResourceTemplates) != 1 {
		t.Fatalf("file template unavailable: %v %v", templates, err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/files/read", strings.NewReader(`{"boxId":"box","path":"/workspace/report.md"}`))
	request.Header.Set("Authorization", "Bearer "+server.Token())
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "# Report") {
		t.Fatalf("standalone file read: %d %s", response.Code, response.Body.String())
	}
}

func TestDirectoryPagesBoundModelPayloads(t *testing.T) {
	entries := make([]discobox.FileInfo, 1000)
	list := discobox.FileList{BoxID: "box", Path: "/workspace", Entries: entries}
	page, err := pageFileList(list, 0, 0)
	if err != nil || len(page["entries"].([]discobox.FileInfo)) != 100 || page["hasMore"] != true {
		t.Fatalf("default page: %v %v", page, err)
	}
	last, err := pageFileList(list, 950, 100)
	if err != nil || len(last["entries"].([]discobox.FileInfo)) != 50 || last["hasMore"] != false {
		t.Fatalf("last page: %v %v", last, err)
	}
	if _, err := pageFileList(list, -1, 100); err == nil {
		t.Fatal("accepted negative offset")
	}
	if _, err := pageFileList(list, 0, 1000); err == nil {
		t.Fatal("accepted unbounded directory limit")
	}
}
