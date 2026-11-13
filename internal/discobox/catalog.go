package discobox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
)

// Manager is a visible box, independent of its role, tags, or connected views.
// The type name is retained for compatibility with the original adapter.
type Manager struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	State      string         `json:"state"`
	Parent     string         `json:"parent,omitempty"`
	Accessible bool           `json:"accessible"`
	Archived   bool           `json:"archived"`
	Role       string         `json:"role"`
	Task       string         `json:"task,omitempty"`
	Harness    string         `json:"harness,omitempty"`
	Model      string         `json:"model,omitempty"`
	Program    map[string]any `json:"program,omitempty"`
	ObservedAt string         `json:"observedAt,omitempty"`
}

func NewCatalog(endpoint, token, project string) (*Client, error) {
	if !identifier.MatchString(project) {
		return nil, errors.New("invalid project ID")
	}
	return newClient(endpoint, token, Session{Project: project})
}

func (c *Client) ListManagers(ctx context.Context, parent string) ([]Manager, error) {
	return c.listBoxes(ctx, parent, true)
}
func (c *Client) ListBoxes(ctx context.Context, parent string) ([]Manager, error) {
	return c.listBoxes(ctx, parent, false)
}
func (c *Client) listBoxes(ctx context.Context, parent string, managersOnly bool) ([]Manager, error) {
	path := "/projects/" + c.Session.Project + "/sandboxes"
	if managersOnly {
		path += "?tag=" + managerTagKey + "%3D" + managerTagValue
	}
	result, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	raw, present := result["sandboxes"]
	items, valid := raw.([]any)
	if !present || (raw != nil && !valid) {
		return nil, errors.New("invalid box listing response")
	}
	managers := []Manager{}
	for _, item := range items {
		box, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid box in listing")
		}
		if managersOnly && !hasManagerTag(box) {
			continue
		}
		id, _ := box["id"].(string)
		if !identifier.MatchString(id) {
			return nil, errors.New("invalid box ID in listing")
		}
		runtime, _ := box["runtime"].(map[string]any)
		desired, _ := runtime["desiredState"].(string)
		if desired == "deleted" {
			continue
		}
		name := id
		if config, ok := box["config"].(map[string]any); ok {
			if value, ok := config["name"].(string); ok && value != "" {
				name = value
			}
		}
		if meta, ok := box["meta"].(map[string]any); ok {
			if value, ok := meta["name"].(string); ok && value != "" {
				name = value
			}
		}
		// Match the Discobox console's authoritative human-facing name. It may
		// contain the primary harness's task title; routing always uses the ID.
		if value, ok := box["displayName"].(string); ok && value != "" {
			name = value
		}
		state, _ := runtime["displayState"].(string)
		if state == "" {
			state, _ = box["displayState"].(string)
		}
		if state == "" && desired == "archived" {
			state = "archived"
		}
		if state == "" {
			state, _ = runtime["runtimeState"].(string)
		}
		if state == "" {
			state = desired
		}
		if state == "" {
			state = "unknown"
		}
		parentID := entryParent(box)
		entry := Manager{ID: id, Name: name, State: state, Parent: parentID, Accessible: parent == "" || parentID == parent, Archived: desired == "archived", Role: roleOf(box)}
		config, _ := box["config"].(map[string]any)
		if entry.Role != managerTagValue {
			entry.Role = "box"
			if prompt, ok := config["prompt"].([]any); ok {
				for _, part := range prompt {
					if text, ok := part.(string); ok {
						entry.Task += text + "\n"
					}
				}
			}
		}
		entry.Model, _ = config["model"].(string)
		harness, _ := box["harnessConfig"].(map[string]any)
		entry.Harness, _ = harness["name"].(string)
		entry.ObservedAt, _ = runtime["agentStatusObservedAt"].(string)
		agent, _ := runtime["agentStatus"].(map[string]any)
		sessions, _ := agent["sessions"].([]any)
		for _, raw := range sessions {
			session, _ := raw.(map[string]any)
			if session["primary"] == true {
				entry.Program = safeProgram(session["programStatus"])
				if entry.ObservedAt == "" {
					entry.ObservedAt = textField(entry.Program, "reportedAt")
				}
				break
			}
		}
		managers = append(managers, entry)
	}
	sort.Slice(managers, func(i, j int) bool {
		if managers[i].Name == managers[j].Name {
			return managers[i].ID < managers[j].ID
		}
		return managers[i].Name < managers[j].Name
	})
	return managers, nil
}

func (c *Client) managerBox(ctx context.Context, id, parent string) (map[string]any, error) {
	if !identifier.MatchString(id) {
		return nil, errors.New("invalid box ID")
	}
	box, err := c.request(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/sandboxes/"+id, nil)
	if err != nil {
		return nil, err
	}
	if err := checkManagerParent(box, parent); err != nil {
		return nil, err
	}
	return box, nil
}

// ConnectManager returns an independently pinned session. It never redirects
// another connected view or silently replaces a manager's exec.
func (c *Client) ConnectManager(ctx context.Context, id, parent string) (*Client, error) {
	box, err := c.managerBox(ctx, id, parent)
	if err != nil {
		return nil, err
	}
	if err := managerLifecycleError(box); err != nil {
		return nil, err
	}
	connected := &Client{endpoint: c.endpoint, base: c.base, http: c.http, token: c.token, Session: Session{Project: c.Session.Project, Box: id, Exec: "primary"}}
	if err := connected.ResolvePrimary(ctx); err != nil {
		return nil, err
	}
	return connected, nil
}

// CreateManager journals a creation intent under a caller-supplied key. An
// uncertain create retains its reservation and is never automatically retried.
func (c *Client) CreateManager(ctx context.Context, requestID string, options ManagerOptions) (*Client, error) {
	if !identifier.MatchString(requestID) || len(requestID) > 128 {
		return nil, errors.New("requestId must be a stable key containing 1 to 128 letters, digits, underscores or hyphens")
	}
	// Verify API reachability before reserving a new creation. The sandbox
	// role permits listing, while reading project settings is not permitted.
	if _, err := c.ListManagers(ctx, options.Parent); err != nil {
		return nil, err
	}
	base, err := DefaultManagerState(c.endpoint, c.Session.Project)
	if err != nil {
		return nil, err
	}
	if options.StatePath != "" {
		base = options.StatePath
	}
	key := sha256.Sum256([]byte(c.endpoint + "\n" + c.Session.Project + "\n" + options.Parent + "\n" + requestID))
	intentData, err := json.Marshal([]string{options.Name, options.Harness, options.Pool, options.Task, options.Model, options.Role})
	if err != nil {
		return nil, err
	}
	intent := sha256.Sum256(intentData)
	// Existing manager intents retain their original hash across adapter upgrades.
	if options.Task == "" && options.Model == "" && options.Role == "" {
		intent = sha256.Sum256([]byte(options.Name + "\n" + options.Harness + "\n" + options.Pool))
	}
	options.StatePath = filepath.Join(filepath.Dir(base), "creations", hex.EncodeToString(key[:16])+".json")
	options.CreateOnly = true
	options.Intent = hex.EncodeToString(intent[:])
	return EnsureManager(ctx, c.endpoint, c.token, c.Session.Project, options)
}

func (c *Client) ArchiveManager(ctx context.Context, id, parent string) error {
	box, err := c.managerBox(ctx, id, parent)
	if err != nil {
		return err
	}
	if managerLifecycleError(box) != nil {
		return nil
	}
	_, err = c.request(ctx, http.MethodDelete, "/projects/"+c.Session.Project+"/sandboxes/"+id, nil)
	if err != nil {
		return fmt.Errorf("archive was not confirmed; list boxes to check its outcome before retrying: %w", err)
	}
	return nil
}

// Only presentation fields are exposed; never forward opaque runtime or harness configuration.
func safeProgram(raw any) map[string]any {
	value, _ := raw.(map[string]any)
	if value == nil {
		return nil
	}
	result := map[string]any{}
	for _, key := range []string{"state", "kind", "msg", "title", "reportedAt", "progress"} {
		if v, ok := value[key]; ok {
			result[key] = v
		}
	}
	return result
}
