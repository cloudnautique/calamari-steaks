package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPrivateUploadOrderingPermissionsAndCleanup(t *testing.T) {
	network := testhttp.NewNetwork(t)
	var revoked atomic.Bool
	var writes atomic.Int32
	const secret = "private file bytes that must never enter model content"
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing upstream authorization")
		}
		switch r.URL.Path {
		case "/projects/p/sandboxes/box":
			if revoked.Load() {
				w.WriteHeader(403)
				return
			}
			io.WriteString(w, `{"id":"box","parentSandboxId":"caller","runtime":{"desiredState":"present"}}`)
		case "/api/projects/p/sandboxes/box/files/content":
			if r.Method != "PUT" || r.URL.Query().Get("ifExists") != "fail" || r.URL.Query().Get("path") != "/workspace/report.txt" {
				t.Error("unsafe upload request")
			}
			data, _ := io.ReadAll(r.Body)
			if string(data) != secret {
				t.Error("upload lost file bytes")
			}
			if writes.Add(1) > 1 {
				w.WriteHeader(412)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"path": "/workspace/report.txt", "name": "report.txt", "type": "file", "size": len(data), "version": "v1"})
		default:
			t.Errorf("unexpected upload route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	client, _ := discobox.NewCatalog(backend.URL, "fixture", "p")
	s, _ := New(client, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{Parent: "caller"})
	t.Cleanup(s.CloseFileUploads)
	ctx := context.Background()
	mcpClient := mcpClient(t, ctx, s)
	tool := func(in uploadInput) (map[string]any, bool) {
		t.Helper()
		result, e := mcpClient.CallTool(ctx, &mcp.CallToolParams{Name: "file_upload", Arguments: in})
		if e != nil {
			t.Fatal(e)
		}
		visible, _ := json.Marshal(struct {
			Content    []mcp.Content
			Structured any
		}{result.Content, result.StructuredContent})
		if strings.Contains(string(visible), secret) || strings.Contains(string(visible), base64.StdEncoding.EncodeToString([]byte(secret))) || strings.Contains(string(visible), "uploadId") {
			t.Fatal("private upload leaked into model content")
		}
		b, _ := json.Marshal(result.Meta["upload"])
		var out map[string]any
		json.Unmarshal(b, &out)
		return out, result.IsError
	}
	begin := func() string {
		t.Helper()
		data, failed := tool(uploadInput{Action: "begin", BoxID: "box", Path: "/workspace/report.txt", Size: int64(len(secret))})
		if failed {
			t.Fatal("begin failed")
		}
		return data["uploadId"].(string)
	}
	id := begin()
	tmp := s.uploads[id].file.Name()
	stat, e := os.Stat(tmp)
	if e != nil || (runtime.GOOS != "windows" && stat.Mode().Perm()&0077 != 0) {
		t.Fatal("upload is not private", e)
	}
	if _, failed := tool(uploadInput{Action: "finish", UploadID: id}); !failed {
		t.Fatal("committed incomplete upload")
	}
	chunk := uploadInput{Action: "chunk", UploadID: id, Data: base64.StdEncoding.EncodeToString([]byte(secret))}
	if _, failed := tool(chunk); failed {
		t.Fatal("chunk failed")
	}
	if _, failed := tool(chunk); !failed {
		t.Fatal("duplicate chunk appended")
	}
	if _, failed := tool(uploadInput{Action: "finish", UploadID: id}); failed {
		t.Fatal("finish failed")
	}
	if _, e = os.Stat(tmp); !os.IsNotExist(e) {
		t.Fatal("completed upload was not removed")
	}
	if _, failed := tool(uploadInput{Action: "finish", UploadID: id}); !failed {
		t.Fatal("uncertain commit could be replayed")
	}
	// A second complete upload must refuse an existing remote path.
	id = begin()
	chunk.UploadID = id
	tool(chunk)
	if _, failed := tool(uploadInput{Action: "finish", UploadID: id}); !failed {
		t.Fatal("existing file was replaced")
	}
	// Authority is checked again at commit, not only at begin.
	id = begin()
	chunk.UploadID = id
	tool(chunk)
	revoked.Store(true)
	if _, failed := tool(uploadInput{Action: "finish", UploadID: id}); !failed {
		t.Fatal("revoked upload committed")
	}
	if writes.Load() != 2 {
		t.Fatal("unexpected writes", writes.Load())
	}
	revoked.Store(false)
	id = begin()
	tmp = s.uploads[id].file.Name()
	tool(uploadInput{Action: "cancel", UploadID: id})
	if _, e = os.Stat(tmp); !os.IsNotExist(e) {
		t.Fatal("cancelled upload retained bytes")
	}
	for _, size := range []int64{-1, maxUploadSize + 1} {
		if _, e := s.uploadFile(ctx, uploadInput{Action: "begin", BoxID: "box", Path: "/workspace/x", Size: size}); e == nil {
			t.Fatal("invalid size accepted")
		}
	}
	for i := 0; i < 4; i++ {
		begin()
	}
	if _, e := s.uploadFile(ctx, uploadInput{Action: "begin", BoxID: "box", Path: "/workspace/x"}); e == nil {
		t.Fatal("upload sessions are unbounded")
	}
	s.CloseFileUploads()
	if len(s.uploads) != 0 {
		t.Fatal("shutdown leaked uploads")
	}
}

// Regression for SECURITY-REVIEW.md H10: staging files left by a crash are
// swept, while fresh uploads, other files and symlinks are left alone.
func TestSweepStaleUploads(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	write := func(name string, modified time.Time) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write(uploadPrefix+"stale", old)
	fresh := write(uploadPrefix+"fresh", time.Now())
	other := write("unrelated-file", old)
	target := write("symlink-target", old)
	link := filepath.Join(dir, uploadPrefix+"link")
	linked := os.Symlink(target, link) == nil
	if removed := SweepStaleUploads(dir, time.Hour); removed != 1 {
		t.Fatalf("removed %d files, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staging file survived")
	}
	for _, path := range []string{fresh, other, target} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was removed", path)
		}
	}
	if linked {
		if _, err := os.Lstat(link); err != nil {
			t.Fatal("symlink was removed")
		}
	}
}
