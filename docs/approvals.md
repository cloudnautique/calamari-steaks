# Credential request review

Needs you → Requests & history opens the approval widget. It lists Pending,
Approved, Denied or All box credential requests (up to 200 entries), and Find
request reads a known request ID directly. `open_approval(requestId)` opens that
same view from chat. `list_approval_requests(status)` exposes safe status
summaries and opens the history. Empty pending results are not evidence that a
past request is unavailable.

A review shows the request's box, timestamps, recorded decision actor, reason,
requested use/delegation routes, and requester-supplied policy descriptions.
Settled requests remain read-only. This integration never resubmits a denied
request, asks for a replacement credential automatically, or resumes the box.
A new request or resumption requires a separate explicit instruction.

## Decisions

For a pending request, choose an explicit Approve or Deny for every requested
part. Use in this box and Pass access to workers are independent decisions.
Approval selects an existing credential visible to this connection and a duration
between one second and 30 days, within the credential's maximum lifetime. The
initial duration is the requested duration, capped at 30 days, or one hour when
none is requested. This version does not create secrets or accept raw values.

Review decision displays the exact box/request, credential ID, duration, use
replacement/addition semantics, and delegation decision. Confirm decision is the
only action submitting it. Field changes invalidate the confirmation. A private
five-minute review token can be submitted once; the server re-reads the request
and rejects a changed or settled snapshot. Every part is sent explicitly. The
widget permits requested-route scope only, with no hosts/anywhere widening and
no raw binding. Empty requested routes are explicitly shown as having no extra
restriction beyond the credential and parent delegation.

The server consumes the review token before attempting the decision. It does not
retry uncertain writes. A failed or unconfirmed submission requires the person
to refresh the request's actual status before deciding what to do next. Denial
requires no credential. Simultaneous reviewers remain subject to Discobox's
pending-request check and upstream authorization.

## Identity and credential privacy

The widget does not acquire human identity by bypassing the model. It uses the
MCP server's existing Discobox connection. A sandbox connection can answer only
requests of boxes it leads, and approval is limited to credentials delegated to
it. An authorized host connection acts with that connection's rights. The UI
states which mode it uses; upstream Discobox enforces its own current ceilings.

The current sandbox exposes no selectable credentials. Request lookup/history
works, but the widget cannot grant GitHub access through that connection. Use
the Discobox console, an appropriately delegated credential, or a host adapter
with the required authority. API access, adoption, host trust and credential
refresh remain human-only console flows in this first version. A box can have
listing visibility without authority to answer a particular request.

The widget calls app-only approval_list/review/decide tools. Full review metadata,
credential choices (ID/name/lifetime ceiling), and review tokens are returned only
in `_meta.approval`; their model-facing content is a constant acknowledgement.
Credential values, OAuth configuration and raw bindings are never fetched or
passed through. Public tools return only request/box/status/part-outcome
summaries. Model context receives request-reviewed/decided status receipts,
never credential choices, review tokens, routes, or private reasons. The MCP host
still transports these calls and may log them; app visibility is not a separate
security principal or proof of human interaction.

The raw credential stays in Discobox. The decision refers to its credential ID;
Discobox makes the binding using its normal sentinel/delegation mechanism.
No raw token needs to travel through the widget for this workflow.

## Validation

Go/race tests use a fixture API to verify history, direct lookup of settled
requests, exclusion of credential values, metadata-only reviews, stale-snapshot
rejection, explicit use/delegation decisions, requested-route scope, and single-use
submission. Node tests cover decision validation, unavailable credentials,
settled reviews, duration limits, and confirmation content.

An opaque Chromium iframe verified denied-history display, actor visibility,
escaping requester markup, explicit delegation decisions, a confirmation step
before mutation, single submission on double-click, privacy of context updates,
and narrow bounded layout with no direct iframe network access or exceptions.
All browser decisions were fixtures. The real sreq_8cdvw6v7svedfjfz was retrieved
read-only; it was denied by user_default on 2026-11-13 at 18:48:48 UTC.
No live request was approved, denied, resubmitted, or resumed during implementation.
Native Claude/OpenAI desktop validation remains pending.
