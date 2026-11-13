package discobox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

type RequestPolicy struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	File        string `json:"file,omitempty"`
}
type RequestPart struct {
	Routes        []map[string]any `json:"routes"`
	Policies      []RequestPolicy  `json:"policies,omitempty"`
	Add           bool             `json:"add,omitempty"`
	Outcome       string           `json:"outcome,omitempty"`
	ApprovedAs    string           `json:"approvedAs,omitempty"`
	ApprovedScope string           `json:"approvedScope,omitempty"`
}
type CredentialRequest struct {
	ID          string       `json:"id"`
	ProjectID   string       `json:"projectId"`
	BoxID       string       `json:"sandboxId"`
	Name        string       `json:"name"`
	EnvName     string       `json:"envName,omitempty"`
	Reason      string       `json:"justification,omitempty"`
	Status      string       `json:"status"`
	CreatedAt   string       `json:"createdAt"`
	UpdatedAt   string       `json:"updatedAt"`
	DecidedAt   string       `json:"decidedAt,omitempty"`
	DecidedBy   string       `json:"decidedBy,omitempty"`
	TTL         int64        `json:"ttlSeconds,omitempty"`
	Use         *RequestPart `json:"use,omitempty"`
	Delegate    *RequestPart `json:"delegate,omitempty"`
	Fingerprint string       `json:"-"`
}
type CredentialChoice struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	MaxTTL int64  `json:"maxBindingTTLSeconds"`
}
type RequestSummary struct {
	ID        string `json:"id"`
	BoxID     string `json:"boxId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	DecidedAt string `json:"decidedAt,omitempty"`
	DecidedBy string `json:"decidedBy,omitempty"`
	Use       string `json:"use,omitempty"`
	Delegate  string `json:"delegate,omitempty"`
}

func (r CredentialRequest) Summary() RequestSummary {
	result := RequestSummary{ID: r.ID, BoxID: r.BoxID, Name: r.Name, Status: r.Status, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt, DecidedBy: r.DecidedBy}
	if r.Use != nil {
		result.Use = r.Use.Outcome
		if result.Use == "" {
			result.Use = "pending"
			if r.Status != "pending" {
				result.Use = "unknown"
			}
		}
	}
	if r.Delegate != nil {
		result.Delegate = r.Delegate.Outcome
		if result.Delegate == "" {
			result.Delegate = "pending"
			if r.Status != "pending" {
				result.Delegate = "unknown"
			}
		}
	}
	return result
}
func (c *Client) requestProjection(raw map[string]any) (CredentialRequest, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return CredentialRequest{}, err
	}
	var result CredentialRequest
	if err = json.Unmarshal(encoded, &result); err != nil {
		return result, err
	}
	if !identifier.MatchString(result.ID) || !identifier.MatchString(result.ProjectID) || !identifier.MatchString(result.BoxID) {
		return result, errors.New("request identity is invalid or not tied to a box in this project")
	}
	if result.Status != "pending" && result.Status != "approved" && result.Status != "denied" {
		return result, errors.New("invalid request status")
	}
	digest := sha256.Sum256(encoded)
	result.Fingerprint = hex.EncodeToString(digest[:])
	return result, nil
}
func (c *Client) CredentialRequest(ctx context.Context, id, parent string) (CredentialRequest, error) {
	if !identifier.MatchString(id) || len(id) > 128 {
		return CredentialRequest{}, errors.New("invalid request ID")
	}
	raw, err := c.request(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/secret-requests/"+id, nil)
	if err != nil {
		return CredentialRequest{}, err
	}
	result, err := c.requestProjection(raw)
	if err != nil {
		return result, err
	}
	if result.ID != id {
		return result, errors.New("request ID changed in upstream response")
	}
	// History remains useful for stopped/archived boxes; no wake or exec attach.
	box, err := c.managerBox(ctx, result.BoxID, parent)
	if err != nil {
		return result, err
	}
	project := textField(box, "projectId")
	if project == "" {
		project = c.Session.Project
	}
	if result.ProjectID != project {
		return result, errors.New("request and box belong to different projects")
	}
	return result, nil
}
func (c *Client) CredentialRequests(ctx context.Context, status, parent string) ([]RequestSummary, error) {
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "approved" && status != "denied" && status != "all" {
		return nil, errors.New("invalid request status filter")
	}
	route := "/projects/" + c.Session.Project + "/secret-requests"
	if status != "all" {
		route += "?" + url.Values{"status": []string{status}}.Encode()
	}
	raw, err := c.request(ctx, http.MethodGet, route, nil)
	if err != nil {
		return nil, err
	}
	items, valid := raw["secretRequests"].([]any)
	if !valid && raw["secretRequests"] != nil {
		return nil, errors.New("invalid request list")
	}
	if _, present := raw["secretRequests"]; !present {
		return nil, errors.New("request list missing")
	}
	boxes, err := c.ListBoxes(ctx, parent)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	projects := map[string]string{}
	for _, box := range boxes {
		allowed[box.ID] = box.Accessible
		projects[box.ID] = box.projectID
	}
	result := []RequestSummary{}
	for _, item := range items {
		value, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid request list entry")
		}
		if !allowed[textField(value, "sandboxId")] {
			continue
		}
		request, err := c.requestProjection(value)
		if err != nil {
			return nil, err
		}
		project := projects[request.BoxID]
		if project == "" {
			project = c.Session.Project
		}
		if request.ProjectID != project {
			return nil, errors.New("request and box belong to different projects")
		}
		if allowed[request.BoxID] && (status == "all" || request.Status == status) {
			result = append(result, request.Summary())
			if len(result) >= 200 {
				break
			}
		}
	}
	return result, nil
}
func (c *Client) CredentialChoices(ctx context.Context) ([]CredentialChoice, error) {
	raw, err := c.request(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/secrets", nil)
	if err != nil {
		return nil, err
	}
	if _, present := raw["secrets"]; !present {
		return nil, errors.New("credential catalog missing")
	}
	items, valid := raw["secrets"].([]any)
	if !valid && raw["secrets"] != nil {
		return nil, errors.New("invalid credential catalog")
	}
	result := []CredentialChoice{}
	for _, item := range items {
		value, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid credential metadata")
		}
		// Never pass through OAuth/exchange configuration, values, or raw bindings.
		id, name := textField(value, "id"), textField(value, "name")
		if !identifier.MatchString(id) {
			return nil, errors.New("invalid credential ID")
		}
		if name == "" {
			name = id
		}
		maxTTL, _ := value["maxBindingTTLSeconds"].(float64)
		result = append(result, CredentialChoice{ID: id, Name: name, MaxTTL: int64(maxTTL)})
		if len(result) >= 200 {
			break
		}
	}
	return result, nil
}

// DecideCredentialRequest submits exactly one reviewed decision. The upstream
// checks live delegation and credential ceilings; UI visibility grants no power.
func (c *Client) DecideCredentialRequest(ctx context.Context, id, parent, fingerprint, secret, use, delegate string, ttl int64) (CredentialRequest, error) {
	request, err := c.CredentialRequest(ctx, id, parent)
	if err != nil {
		return request, err
	}
	if request.Status != "pending" {
		return request, errors.New("request is already settled; it cannot be approved again or resubmitted here")
	}
	if request.Fingerprint != fingerprint {
		return request, errors.New("request changed since review; reload it before deciding")
	}
	approved := false
	body := map[string]any{}
	for _, part := range []struct {
		name     string
		request  *RequestPart
		decision string
	}{{"use", request.Use, use}, {"delegate", request.Delegate, delegate}} {
		if part.request == nil {
			if part.decision != "" {
				return request, errors.New("decision names a part that was not requested")
			}
			continue
		}
		if part.decision != "approve" && part.decision != "deny" {
			return request, errors.New("choose an explicit decision for each requested part")
		}
		answer := map[string]any{"decision": part.decision}
		if part.decision == "approve" {
			approved = true
			if ttl < 1 || ttl > 30*24*3600 {
				return request, errors.New("grant duration must be between 1 second and 30 days")
			}
			answer["scope"] = "routes"
			if len(part.request.Routes) > 0 {
				answer["routes"] = part.request.Routes
			}
			answer["ttlSeconds"] = ttl
			if part.name == "use" {
				answer["as"] = "replace"
				if part.request.Add {
					answer["as"] = "add"
				}
			}
		}
		body[part.name] = answer
	}
	if request.Use == nil && request.Delegate == nil {
		return request, errors.New("request has no reviewable parts")
	}
	route := "/projects/" + c.Session.Project + "/secret-requests/" + id
	if approved {
		if !identifier.MatchString(secret) {
			return request, errors.New("choose an existing credential")
		}
		choices, err := c.CredentialChoices(ctx)
		if err != nil {
			return request, err
		}
		found := false
		for _, choice := range choices {
			if choice.ID == secret {
				found = true
				if choice.MaxTTL > 0 && ttl > choice.MaxTTL {
					return request, errors.New("chosen duration exceeds the credential lifetime ceiling")
				}
			}
		}
		if !found {
			return request, errors.New("credential is not available to this connection")
		}
		body["secretId"] = secret
		if _, err = c.request(ctx, http.MethodPost, route+"/approve", body); err != nil {
			return request, err
		}
	} else {
		if _, err = c.request(ctx, http.MethodPost, route+"/deny", nil); err != nil {
			return request, err
		}
	}
	return c.CredentialRequest(ctx, id, parent)
}
