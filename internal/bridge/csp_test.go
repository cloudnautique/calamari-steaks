package bridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/connection"
	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/web"
)

// Regression for SECURITY-REVIEW.md H7: the HTTP pages allow only their own
// inlined bundle by hash, never arbitrary inline script.
func TestPagesAllowOnlyTheirOwnInlineScript(t *testing.T) {
	if err := web.Ready(); err != nil {
		t.Skip("UI assets have not been built")
	}
	s, err := New(nil, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.Onboarding = connection.New(connection.Config{Endpoint: "unix://", Project: "default"}, t.TempDir()+"/c.json", t.TempDir()+"/id", false, nil, nil)
	handler := s.Handler(nil)
	for _, page := range []string{"/", "/setup"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+page, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d", page, response.Code)
		}
		policy := response.Header().Get("Content-Security-Policy")
		var scriptSrc string
		for _, directive := range strings.Split(policy, ";") {
			if fields := strings.Fields(directive); len(fields) > 0 && fields[0] == "script-src" {
				scriptSrc = directive
			}
		}
		hashes := web.ScriptHashes(response.Body.String())
		if scriptSrc == "" || strings.Contains(scriptSrc, "'unsafe-inline'") || hashes == "" || !strings.Contains(scriptSrc, hashes) {
			t.Fatalf("%s: script-src %q does not pin the inline bundle %q", page, scriptSrc, hashes)
		}
	}
}
