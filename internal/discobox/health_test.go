package discobox

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestBackendHealth(t *testing.T) {
	status := http.StatusOK
	body := `{"pools":[{"id":"full","ready":false,"availableStorageBytes":0,"hostCredential":"private-marker","conditions":"private-marker","resources":{"reportedAt":"2026-11-18T08:00:00Z","cpu":{"vcpus":0},"storage":{"filesystem":{"totalBytes":4294967296,"usedBytes":2147483648,"freeBytes":1073741824}}}},{"id":"unknown"}]}`
	server := testhttp.NewNetwork(t).NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/projects/p/pools" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer server.Close()
	client, err := NewCatalog(server.URL, "", "p")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.BackendHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pools := got["pools"].([]map[string]any)
	disk := pools[0]["disk"].(map[string]float64)
	if disk["freeGiB"] != 1 || disk["usedPercent"] != 50 || pools[0]["ready"] != false {
		t.Fatal(pools)
	}
	if value, ok := pools[0]["placement"].(map[string]float64)["availableStorageBytes"]; !ok || value != 0 {
		t.Fatal(pools)
	}
	if len(pools[1]["disk"].(map[string]float64)) != 0 {
		t.Fatal(pools)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "private-marker") {
		t.Fatal("private fields escaped projection")
	}
	status = http.StatusForbidden
	if _, err := client.BackendHealth(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("denied: %v", err)
	}
}
