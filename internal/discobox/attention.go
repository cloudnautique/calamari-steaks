package discobox

import (
	"context"
	"errors"
	"net/http"
)

// Attention is a projection of durable upstream requests and reported program
// status. Reading it never attaches, wakes a box, or marks a result as seen.
type Attention struct {
	ID         string         `json:"id"`
	BoxID      string         `json:"boxId"`
	BoxName    string         `json:"boxName"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title"`
	Reason     string         `json:"reason,omitempty"`
	CreatedAt  string         `json:"createdAt,omitempty"`
	ObservedAt string         `json:"observedAt,omitempty"`
	TTL        any            `json:"ttlSeconds,omitempty"`
	Scope      map[string]any `json:"scope,omitempty"`
	Action     string         `json:"action"`
}
type Inbox struct {
	Items    []Attention `json:"items"`
	Boxes    []Manager   `json:"boxes"`
	Coverage string      `json:"coverage"`
	Warning  string      `json:"warning,omitempty"`
}

func textField(m map[string]any, key string) string { value, _ := m[key].(string); return value }

func (c *Client) Attention(ctx context.Context, parent string) (Inbox, error) {
	inbox := Inbox{Items: []Attention{}, Coverage: "box-specific approval requests and reported box status", Warning: "Only requests tied to accessible integration boxes are shown. Project-wide credential refresh requests remain in the Discobox console."}
	boxes, err := c.ListBoxes(ctx, parent)
	if err != nil {
		return inbox, err
	}
	inbox.Boxes = boxes
	allowed := map[string]Manager{}
	for _, box := range boxes {
		if !box.Accessible || box.Archived {
			continue
		}
		allowed[box.ID] = box
		state := textField(box.Program, "state")
		kind, title := "", ""
		switch state {
		case "blocked":
			kind, title = "question", "Needs your attention"
		case "error":
			kind, title = "error", "Agent reported an error"
		case "done":
			kind, title = "result", "Ready for review"
		}
		if kind != "" {
			inbox.Items = append(inbox.Items, Attention{ID: "program-" + box.ID, BoxID: box.ID, BoxName: box.Name, Kind: kind, Title: title, Reason: textField(box.Program, "msg"), ObservedAt: box.ObservedAt, Action: "open"})
		}
	}
	path, key := "/projects/"+c.Session.Project+"/approval-requests?status=pending", "approvalRequests"
	if parent != "" {
		path, key = "/projects/"+c.Session.Project+"/secret-requests?status=pending", "secretRequests"
		inbox.Coverage = "credential requests and reported box status"
		inbox.Warning = "This sandbox connection cannot list the human-only API, adoption, trust and refresh inbox. Check the Discobox console for those requests."
	}
	result, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		var status *HTTPError
		if errors.As(err, &status) && (status.Status == 403 || status.Status == 404) {
			inbox.Warning = "Approval inbox unavailable through this connection. Check the Discobox console; reported box status is still shown."
			inbox.Coverage = "reported box status only"
			return inbox, nil
		}
		return inbox, err
	}
	raw, present := result[key]
	items, valid := raw.([]any)
	if !present || (raw != nil && !valid) {
		return inbox, errors.New("invalid approval inbox response")
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return inbox, errors.New("invalid approval request")
		}
		kind, request := "credential", item
		if key == "approvalRequests" {
			kind = textField(item, "kind")
			request, _ = item[kind].(map[string]any)
		}
		id, boxID := textField(request, "id"), textField(request, "sandboxId")
		box, known := allowed[boxID]
		if !known || !identifier.MatchString(id) || textField(request, "status") != "pending" {
			continue
		}
		title := "Review " + kind + " request"
		if name := textField(request, "name"); name != "" {
			title += ": " + name
		}
		scope := map[string]any{}
		for _, key := range []string{"use", "delegate"} {
			if part, ok := request[key].(map[string]any); ok {
				safe := map[string]any{}
				for _, field := range []string{"routes", "add"} {
					if v, present := part[field]; present {
						safe[field] = v
					}
				}
				scope[key] = safe
			}
		}
		inbox.Items = append(inbox.Items, Attention{ID: id, BoxID: boxID, BoxName: box.Name, Kind: kind, Title: title, Reason: requestReason(request), CreatedAt: textField(request, "createdAt"), TTL: request["ttlSeconds"], Scope: scope, Action: "console"})
	}
	return inbox, nil
}

func requestReason(request map[string]any) string {
	if value := textField(request, "justification"); value != "" {
		return value
	}
	return textField(request, "reason")
}
