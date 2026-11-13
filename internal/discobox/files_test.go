package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestFileURIRoundTripAndScope(t *testing.T) {
	for _, file := range []string{"/workspace/report.md", "/workspace/space #?% + ü.md", "/"} {
		uri, err := FileURI("default", "sbx_a", file)
		if err != nil {
			t.Fatal(err)
		}
		box, target, err := ParseFileURI(uri, "default")
		if err != nil || box != "sbx_a" || target != file {
			t.Fatalf("roundtrip %q: %s %s %v", uri, box, target, err)
		}
	}
	for _, uri := range []string{"file:///etc/passwd", "discobox-file://other/sbx_a/workspace/x", "discobox-file://default/sbx_a/workspace/../x", "discobox-file://default/sbx_a/x?secret=1", "discobox-file://user@default/sbx_a/x", "discobox-file://default/sbx_a/x#fragment", "discobox-file://default/sbx_a/%00"} {
		if _, _, err := ParseFileURI(uri, "default"); err == nil {
			t.Fatalf("accepted invalid URI %q", uri)
		}
	}
}
func TestFilesUseAuthorizedBoxAPIWithoutAnyTerminal(t *testing.T) {
	network := testhttp.NewNetwork(t)
	contents := "Report ✓\n"
	version := "v1"
	changeVersion := false
	parent := "caller"
	oversize := false
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("file access wrote data: %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing upstream authentication")
		}
		switch r.URL.Path {
		case "/projects/p/sandboxes/box":
			json.NewEncoder(w).Encode(map[string]any{"id": "box", "parentSandboxId": parent, "runtime": map[string]any{"desiredState": "present"}})
		case "/api/projects/p/sandboxes/box/files/stat":
			json.NewEncoder(w).Encode(map[string]any{"path": "/workspace/report.md", "name": "report.md", "type": "file", "size": len(contents), "version": version})
		case "/api/projects/p/sandboxes/box/files/content":
			if r.URL.Query().Get("path") != "/workspace/report.md" {
				t.Error("escaped selected file")
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			length, _ := strconv.Atoi(r.URL.Query().Get("length"))
			end := min(len(contents), offset+length)
			if oversize {
				w.Write([]byte(strings.Repeat("x", length+1)))
			} else {
				w.Write([]byte(contents[offset:end]))
			}
			if changeVersion {
				version = "v2"
			}
		case "/api/projects/p/sandboxes/box/files/list":
			w.Write([]byte(`{"path":"/workspace","entries":[{"path":"/workspace/report.md","name":"report.md","type":"file","size":11,"version":"v1"},{"path":"/workspace/src","name":"src","type":"directory","size":0,"version":"v1"}]}`))
		default:
			t.Errorf("file operation depended on exec/attach or wrong route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewCatalog(server.URL, "test-token", "p")
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListFiles(context.Background(), "box", "caller", "/workspace")
	if err != nil || len(list.Entries) != 2 || list.Entries[0].Type != "directory" {
		t.Fatalf("list: %+v %v", list, err)
	}
	whole, err := client.ReadFile(context.Background(), "box", "caller", "/workspace/report.md", 0, 100)
	if err != nil || whole.Text == nil || *whole.Text != contents || whole.Truncated {
		t.Fatalf("read: %+v %v", whole, err)
	}
	partial, err := client.ReadFile(context.Background(), "box", "caller", "/workspace/report.md", 0, 3)
	if err != nil || partial.Text == nil || *partial.Text != "Rep" || !partial.Truncated || partial.NextOffset != 3 {
		t.Fatalf("range: %+v %v", partial, err)
	}
	oversize = true
	if _, err := client.ReadFile(context.Background(), "box", "caller", "/workspace/report.md", 0, 3); err == nil {
		t.Fatal("accepted oversized response")
	}
	oversize = false
	changeVersion = true
	if _, err := client.ReadFile(context.Background(), "box", "caller", "/workspace/report.md", 0, 100); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("mixed file versions")
	}
	changeVersion = false
	parent = "someone-else"
	if _, err := client.ReadFile(context.Background(), "box", "caller", "/workspace/report.md", 0, 10); err == nil {
		t.Fatal("read a box not led by the caller")
	}
	for _, target := range []string{"relative.md", "/workspace/\x00bad", "/workspace/a\\b"} {
		if _, err := client.ReadFile(context.Background(), "box", "", target, 0, 10); err == nil {
			t.Fatalf("accepted invalid path %q", target)
		}
	}
}
func TestFilesRejectForgedDirectoryEntries(t *testing.T) {
	network := testhttp.NewNetwork(t)
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/projects/p/sandboxes/box" {
			w.Write([]byte(`{"id":"box","runtime":{"desiredState":"present"}}`))
			return
		}
		w.Write([]byte(`{"path":"/workspace","entries":[{"name":"../other","path":"/other","type":"file","size":1}]}`))
	}))
	defer server.Close()
	client, _ := NewCatalog(server.URL, "", "p")
	if _, err := client.ListFiles(context.Background(), "box", "", "/workspace"); err == nil {
		t.Fatal("accepted directory traversal entry")
	}
}
