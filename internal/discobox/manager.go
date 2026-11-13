package discobox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const managerPrompt = `You are the persistent Discobox manager for a desktop assistant and its user. Coordinate work by creating and driving worker discoboxes through the Discobox API available to you. Follow the user's task and the permissions and credentials already granted to you. Report progress, results, and blockers clearly. Do not create workers or begin a project until a task arrives. For now, acknowledge that you are ready and wait for instructions in this session.`

const managerTagKey = "discobox-mcp-role"
const managerTagValue = "manager"

type ManagerOptions struct {
	StatePath string
	Harness   string
	Task      string
	Model     string
	Role      string // Empty preserves the legacy manager launch. "box" launches a task without API access.
	Pool      string
	// Parent is the calling sandbox in local API mode. Reuse only managers
	// currently led by it; the server enforces authority again per request.
	Parent     string
	CreateOnly bool
	Name       string
	Intent     string
	// Progress goes to the operator's stderr, never the MCP stdout stream.
	Progress func(string)
}

type managerRecord struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	Project  string `json:"project"`
	Name     string `json:"name"`
	Box      string `json:"box,omitempty"`
	Exec     string `json:"exec,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Intent   string `json:"intent,omitempty"`
}

// DefaultManagerState keeps one persistent manager per endpoint and project.
// The hash avoids storing credentials or unsafe endpoint characters in paths.
func DefaultManagerState(endpoint, project string) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(strings.TrimRight(endpoint, "/") + "\n" + project))
	return filepath.Join(root, "discobox-mcp", "managers", hex.EncodeToString(key[:16])+".json"), nil
}

// EnsureManager uses saved state, then discovers a tagged manager, then creates
// one. Call with a deadline. An unavailable saved box or uncertain create stops
// startup rather than silently replacing the manager.
func EnsureManager(ctx context.Context, endpoint, token, project string, options ManagerOptions) (*Client, error) {
	if !identifier.MatchString(project) {
		return nil, errors.New("invalid project ID")
	}
	if options.Parent != "" && !identifier.MatchString(options.Parent) {
		return nil, errors.New("invalid manager parent ID")
	}
	if options.StatePath == "" {
		return nil, errors.New("manager state path is required")
	}
	if options.Name != "" && (!identifier.MatchString(options.Name) || len(options.Name) > 64) {
		return nil, errors.New("manager name must contain 1 to 64 letters, digits, underscores or hyphens")
	}
	if options.Role != "" && options.Role != "box" {
		return nil, errors.New("invalid launch role")
	}
	if len(options.Task) > 64<<10 || !utf8.ValidString(options.Task) || (options.Role == "box" && strings.TrimSpace(options.Task) == "") {
		return nil, errors.New("task must contain 1 to 65536 bytes of valid text")
	}
	if len(options.Model) > 128 {
		return nil, errors.New("model must be at most 128 bytes")
	}
	c, err := newClient(endpoint, token, Session{Project: project, Exec: "primary"})
	if err != nil {
		return nil, err
	}
	endpoint = strings.TrimRight(endpoint, "/")
	progress := func(message string) {
		if options.Progress != nil {
			options.Progress(message)
		}
	}
	needsTag := true
	record, err := readManagerRecord(options.StatePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read manager state: %w", err)
	}
	if err == nil {
		if record.Version != 1 || record.Endpoint != endpoint || record.Project != project || record.Parent != options.Parent || record.Intent != options.Intent {
			return nil, errors.New("manager state belongs to a different endpoint/project, parent, creation settings or format; use the matching configuration or a different --manager-state path")
		}
		if record.Box == "" {
			return nil, fmt.Errorf("manager creation is pending or its outcome is unknown (name %q, state %s); inspect Discobox and reconnect with --box ID; no create was retried", record.Name, options.StatePath)
		}
		if record.Exec == "" {
			record.Exec = "primary"
		}
		c.Session.Box, c.Session.Exec = record.Box, record.Exec
		if err := c.Session.Validate(); err != nil {
			return nil, fmt.Errorf("invalid saved manager: %w", err)
		}
		// A deleted/archived manager must not trigger an automatic replacement.
		box, err := c.request(ctx, http.MethodGet, "/projects/"+project+"/sandboxes/"+record.Box, nil)
		if err != nil {
			return nil, fmt.Errorf("saved manager %s unavailable; state retained: %w", record.Box, err)
		}
		if err := managerLifecycleError(box); err != nil {
			return nil, err
		}
		if err := checkManagerParent(box, options.Parent); err != nil {
			return nil, err
		}
		needsTag = roleOf(box) != launchRole(options)
		progress("Reusing manager " + record.Box)
	} else if !options.CreateOnly {
		box, err := c.discoverManager(ctx, options.Parent)
		if err != nil {
			return nil, err
		}
		if box != nil {
			id := box["id"].(string) // discoverManager validates every match.
			record = managerRecord{Version: 1, Endpoint: endpoint, Project: project, Box: id, Parent: options.Parent}
			if err := reserveManager(options.StatePath, record); err != nil {
				return nil, fmt.Errorf("save discovered manager before attaching: %w", err)
			}
			c.Session.Box = id
			needsTag = false
			progress("Discovered manager " + id + " via " + managerTagKey + "=" + managerTagValue)
		}
	}
	if c.Session.Box == "" {
		// Check reachability before reserving a create intent. This avoids leaving
		// a pending marker when the daemon is not even reachable.
		// A sandbox cannot read project settings. Successful discovery already
		// checked reachability; the create API resolves harness/pool defaults.
		if options.Parent == "" {
			projectInfo, err := c.request(ctx, http.MethodGet, "/projects/"+project, nil)
			if err != nil {
				return nil, fmt.Errorf("connect to Discobox before creating manager: %w", err)
			}
			if options.Harness == "" {
				if harness, _ := projectInfo["defaultHarnessConfigId"].(string); harness == "" {
					return nil, errors.New("project has no default harness; choose an agent with --harness or configure a project default")
				}
			}
		}
		var nonce [6]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		name := options.Name
		if name == "" {
			name = "mcp-manager-" + hex.EncodeToString(nonce[:])
			if options.Role == "box" {
				name = "mcp-task-" + hex.EncodeToString(nonce[:])
			}
		}
		record = managerRecord{Version: 1, Endpoint: endpoint, Project: project, Name: name, Parent: options.Parent, Intent: options.Intent}
		if err := reserveManager(options.StatePath, record); err != nil {
			return nil, fmt.Errorf("reserve manager state before creation: %w", err)
		}
		prompt := managerPrompt
		description := "Persistent manager for the Discobox MCP adapter"
		if options.Role == "box" {
			prompt = options.Task
			description = "Task launched through the Discobox MCP adapter"
		} else if options.Task != "" {
			prompt += "\n\nFirst task from the person:\n" + options.Task
		}
		config := map[string]any{"name": record.Name, "description": description, "prompt": []string{prompt}}
		if options.Model != "" {
			config["model"] = options.Model
		}
		body := map[string]any{"discoboxApi": options.Role != "box", "config": config}
		if options.Harness != "" {
			body["harnessName"] = options.Harness
		}
		if options.Pool != "" {
			body["poolId"] = options.Pool
		}
		progress("Creating manager " + record.Name + " with Discobox API access")
		box, err := c.request(ctx, http.MethodPost, "/projects/"+project+"/sandboxes", body)
		if err != nil {
			// Even an error response could follow a committed create. Leave the
			// intent in place and require reconciliation rather than creating twice.
			return nil, fmt.Errorf("manager create did not complete; inspect Discobox for %q before retrying (intent saved in %s): %w", record.Name, options.StatePath, err)
		}
		record.Box, _ = box["id"].(string)
		if !identifier.MatchString(record.Box) {
			return nil, fmt.Errorf("manager create returned no valid box ID; inspect Discobox for %q; creation will not be retried", record.Name)
		}
		c.Session.Box = record.Box
		// Persist immediately, before waiting for provisioning or the first exec.
		if err := saveManagerRecord(options.StatePath, record); err != nil {
			return nil, fmt.Errorf("manager %s was created but could not be saved; reconnect with --box %s: %w", record.Box, record.Box, err)
		}
		if err := checkManagerParent(box, options.Parent); err != nil {
			return nil, fmt.Errorf("created manager %s saved: %w", record.Box, err)
		}
		progress("Created manager " + record.Box + "; waiting for its primary session")
	}
	if c.Session.Exec == "primary" {
		if err := c.awaitPrimary(ctx); err != nil {
			return nil, fmt.Errorf("manager %s saved; primary session not ready (retry reuses it): %w", record.Box, err)
		}
		record.Exec = c.Session.Exec
		if err := saveManagerRecord(options.StatePath, record); err != nil {
			return nil, fmt.Errorf("could not save manager session; use --box %s --exec %s: %w", c.Session.Box, c.Session.Exec, err)
		}
	}
	if needsTag {
		// Tags live inside the sandbox and cannot be supplied at create time.
		// Save the box and exec first, so a failed tag write can be retried on
		// the same manager, including managers created by older adapter versions.
		box, err := c.request(ctx, http.MethodPatch, "/projects/"+project+"/sandboxes/"+c.Session.Box+"/meta", map[string]any{
			"setTags": map[string]string{managerTagKey: launchRole(options)},
		})
		if err != nil {
			return nil, fmt.Errorf("manager %s saved, but discovery tag could not be set; retry reuses it: %w", c.Session.Box, err)
		}
		if roleOf(box) != launchRole(options) {
			return nil, fmt.Errorf("manager %s saved, but Discobox did not confirm its discovery tag; retry reuses it", c.Session.Box)
		}
		progress("Tagged manager " + c.Session.Box + " for future discovery")
	}
	return c, nil
}

func launchRole(options ManagerOptions) string {
	if options.Role == "box" {
		return "box"
	}
	return managerTagValue
}
func roleOf(box map[string]any) string {
	meta, _ := box["meta"].(map[string]any)
	tags, _ := meta["tags"].(map[string]any)
	role, _ := tags[managerTagKey].(string)
	return role
}
func hasManagerTag(box map[string]any) bool {
	meta, _ := box["meta"].(map[string]any)
	tags, _ := meta["tags"].(map[string]any)
	return tags[managerTagKey] == managerTagValue
}

func (c *Client) discoverManager(ctx context.Context, parent string) (map[string]any, error) {
	query := url.Values{"tag": []string{managerTagKey + "=" + managerTagValue}}
	result, err := c.request(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/sandboxes?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("discover manager before creation: %w", err)
	}
	raw, present := result["sandboxes"]
	items, valid := raw.([]any)
	// null is the API's valid empty list; missing/wrong-shaped data is not.
	if !present || (raw != nil && !valid) {
		return nil, errors.New("invalid manager discovery response; no manager created")
	}
	matches := make(map[string]map[string]any)
	var unled []string
	for _, item := range items {
		box, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid sandbox in discovery response; no manager created")
		}
		// Verify the tag even if an older server ignored the query filter.
		if !hasManagerTag(box) {
			continue
		}
		if managerLifecycleError(box) != nil {
			continue
		}
		id, _ := box["id"].(string)
		if !identifier.MatchString(id) {
			return nil, errors.New("invalid tagged manager ID; no manager created")
		}
		if parent != "" && entryParent(box) != parent {
			unled = append(unled, id)
			continue
		}
		matches[id] = box
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for id := range matches {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("multiple managers tagged %s=%s in project %s: %s; choose one with --box ID; no manager created", managerTagKey, managerTagValue, c.Session.Project, strings.Join(ids, ", "))
	}
	for _, box := range matches {
		return box, nil
	}
	if len(unled) > 0 {
		sort.Strings(unled)
		return nil, fmt.Errorf("tagged manager(s) %s are not led by this sandbox; use discobox adopt BOX --reason REASON and obtain approval before reconnecting; no manager created", strings.Join(unled, ", "))
	}
	return nil, nil
}

func entryParent(box map[string]any) string {
	parent, _ := box["parentSandboxId"].(string)
	return parent
}

func checkManagerParent(box map[string]any, parent string) error {
	if parent != "" && entryParent(box) != parent {
		return errors.New("manager is not currently led by this sandbox; obtain adoption approval before reconnecting; no replacement created")
	}
	return nil
}

// CheckManagerParent validates an explicit --box in local API mode.
func (c *Client) CheckManagerParent(ctx context.Context, parent string) error {
	box, err := c.request(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/sandboxes/"+c.Session.Box, nil)
	if err != nil {
		return err
	}
	if err := managerLifecycleError(box); err != nil {
		return err
	}
	return checkManagerParent(box, parent)
}

func managerLifecycleError(box map[string]any) error {
	runtime, _ := box["runtime"].(map[string]any)
	if state, _ := runtime["desiredState"].(string); state == "archived" || state == "deleted" {
		return fmt.Errorf("saved manager is %s; restore it in Discobox or explicitly select a different manager", state)
	}
	return nil
}

func (c *Client) awaitPrimary(ctx context.Context) error {
	for {
		err := c.ResolvePrimary(ctx)
		if err == nil {
			return nil
		}
		var status *HTTPError
		transient := errors.Is(err, errNoPrimary) || (errors.As(err, &status) && (status.Status == 404 || status.Status == 409 || status.Status == 502 || status.Status == 503))
		if !transient {
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w (last observation: %v)", ctx.Err(), err)
		case <-timer.C:
		}
	}
}

func readManagerRecord(path string) (managerRecord, error) {
	var record managerRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	return record, nil
}

func reserveManager(path string, record managerRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("another adapter reserved this manager; retry after it finishes")
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return err
	}
	return file.Sync()
}

func saveManagerRecord(path string, record managerRecord) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".manager-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
