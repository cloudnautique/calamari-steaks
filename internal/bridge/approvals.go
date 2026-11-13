package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type approvalInput struct {
	RequestID string `json:"requestId"`
}
type approvalListInput struct {
	Status string `json:"status,omitempty" jsonschema:"pending, approved, denied or all; defaults to pending"`
}
type approvalDecision struct {
	ReviewToken string `json:"reviewToken"`
	SecretID    string `json:"secretId,omitempty"`
	Use         string `json:"use,omitempty" jsonschema:"approve or deny, explicitly for each requested part"`
	Delegate    string `json:"delegate,omitempty" jsonschema:"approve or deny, explicitly for each requested part"`
	TTL         int64  `json:"ttlSeconds,omitempty"`
}
type approvalReview struct {
	request discobox.CredentialRequest
	expires time.Time
}

func approvalPrivate(data map[string]any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Meta: mcp.Meta{"approval": data}, Content: []mcp.Content{&mcp.TextContent{Text: "Approval review response for the widget."}}}
}
func (s *Server) approvalTools(server *mcp.Server) {
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	private := mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "open_approval", Description: "SHOW UI: review a credential request by ID, including approved or denied history. Shows separate use/delegation scope and available credentials in the widget. Does not approve, deny, resubmit, or resume a box. Returns status metadata only; credential values are never read.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalInput) (*mcp.CallToolResult, map[string]any, error) {
		request, err := s.Client.CredentialRequest(ctx, input.RequestID, s.managerOptions.Parent)
		if err != nil {
			return nil, nil, err
		}
		data := map[string]any{"view": "approvals", "requestId": request.ID, "summary": request.Summary()}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_approval_requests", Description: "SHOW UI: list box credential requests by status, including denied/approved history. Empty pending results do not establish that no past request exists. Human-only API, trust, adoption and refresh requests remain in the Discobox console. Does not decide or resubmit requests.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalListInput) (*mcp.CallToolResult, map[string]any, error) {
		list, err := s.Client.CredentialRequests(ctx, input.Status, s.managerOptions.Parent)
		if err != nil {
			return nil, nil, err
		}
		data := map[string]any{"view": "approvals", "requests": list, "status": input.Status}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "approval_list", Description: "App-only credential request history list.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalListInput) (*mcp.CallToolResult, any, error) {
		list, err := s.Client.CredentialRequests(ctx, input.Status, s.managerOptions.Parent)
		if err != nil {
			return nil, nil, err
		}
		return approvalPrivate(map[string]any{"requests": list}), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "approval_review", Description: "App-only request detail, credential metadata and a short-lived review token. No credential values. A token is not a human identity or additional upstream authority.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalInput) (*mcp.CallToolResult, any, error) {
		request, err := s.Client.CredentialRequest(ctx, input.RequestID, s.managerOptions.Parent)
		if err != nil {
			return nil, nil, err
		}
		choices := []discobox.CredentialChoice{}
		warning := ""
		if request.Status == "pending" {
			choices, err = s.Client.CredentialChoices(ctx)
			if err != nil {
				choices = []discobox.CredentialChoice{}
				warning = "Credential choices unavailable through this connection. Use the Discobox console to review access."
			}
		}
		mode := "Authorized API connection; decisions use the server connection's identity."
		if s.managerOptions.Parent != "" {
			mode = "Sandbox delegation: decisions are limited to credentials delegated to this adapter. This is not your human identity."
		}
		data := map[string]any{"request": request, "credentials": choices, "authority": mode, "warning": warning}
		if request.Status == "pending" {
			var key [24]byte
			if _, err = rand.Read(key[:]); err != nil {
				return nil, nil, err
			}
			token := hex.EncodeToString(key[:])
			expires := time.Now().Add(5 * time.Minute)
			s.approvalMu.Lock()
			if s.approvalReviews == nil {
				s.approvalReviews = map[string]approvalReview{}
			}
			for id, review := range s.approvalReviews {
				if time.Now().After(review.expires) {
					delete(s.approvalReviews, id)
				}
			}
			if len(s.approvalReviews) >= 64 {
				s.approvalMu.Unlock()
				return nil, nil, errors.New("too many open approval reviews; wait for an old review to expire")
			}
			s.approvalReviews[token] = approvalReview{request: request, expires: expires}
			s.approvalMu.Unlock()
			data["reviewToken"] = token
			data["expiresAt"] = expires.UTC().Format(time.RFC3339)
		}
		return approvalPrivate(data), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "approval_decide", Description: "App-only submission after explicit widget confirmation. Requires a private single-use review token and explicit use/delegate decisions. Never retry an uncertain decision or resubmit a denied request.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, input approvalDecision) (*mcp.CallToolResult, any, error) {
		s.approvalMu.Lock()
		review, found := s.approvalReviews[input.ReviewToken]
		delete(s.approvalReviews, input.ReviewToken)
		s.approvalMu.Unlock()
		if !found || time.Now().After(review.expires) {
			return nil, nil, errors.New("approval review expired or was already submitted; refresh the request to inspect its current status")
		}
		request, err := s.Client.DecideCredentialRequest(ctx, review.request.ID, s.managerOptions.Parent, review.request.Fingerprint, input.SecretID, input.Use, input.Delegate, input.TTL)
		if err != nil {
			return nil, nil, errors.New("Decision was not confirmed: " + err.Error() + ". Refresh the request before any further attempt; nothing was retried.")
		}
		return approvalPrivate(map[string]any{"summary": request.Summary()}), nil, nil
	})
}
