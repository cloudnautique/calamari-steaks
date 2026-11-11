package discobox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// LocalConfig names this sandbox's pool-forwarded API. Authentication belongs
// to the pool proxy, which identifies the caller and strips bearer credentials.
type LocalConfig struct {
	Endpoint string
	Project  string
	Parent   string
}

func LocalConnection() (LocalConfig, error) {
	api := strings.TrimSpace(os.Getenv("DISCOBOX_API_URL"))
	if api == "" {
		api = strings.TrimSpace(os.Getenv("DISCOBOX_SERVER"))
	}
	if api == "" {
		return LocalConfig{}, errors.New("--local requires a Discobox sandbox with DISCOBOX_API_URL or DISCOBOX_SERVER; on the host use --discobox unix:///absolute/server.sock")
	}
	data, err := os.ReadFile("/etc/discobox/sandbox.json")
	if err != nil {
		return LocalConfig{}, fmt.Errorf("--local requires this sandbox's /etc/discobox/sandbox.json: %w", err)
	}
	return parseLocalConnection(api, os.Getenv("DISCOBOX_PROJECT"), data)
}

func parseLocalConnection(api, project string, data []byte) (LocalConfig, error) {
	u, err := url.Parse(api)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return LocalConfig{}, errors.New("the local Discobox API must be an HTTP(S) origin without credentials, a path, a query or a fragment")
	}
	var identity struct {
		SandboxID string `json:"sandboxId"`
	}
	if err := json.Unmarshal(data, &identity); err != nil || !identifier.MatchString(identity.SandboxID) {
		return LocalConfig{}, errors.New("local sandbox configuration contains no valid sandboxId")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		project = "default"
	}
	if !identifier.MatchString(project) {
		return LocalConfig{}, errors.New("invalid DISCOBOX_PROJECT")
	}
	return LocalConfig{Endpoint: strings.TrimRight(api, "/"), Project: project, Parent: identity.SandboxID}, nil
}
