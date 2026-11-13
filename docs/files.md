# Files in a box

The first Files view is read-only. It browses directories, renders Markdown or
plain text, opens relative file links within the same box, exposes a canonical
MCP resource link, and shares file contents with chat on request. Raw HTML and
external images are not rendered. Source view shows the exact text.

Reconnect the desktop MCP connection after upgrading so its tool list is fresh.
Ask the assistant to open a file, naming its box ID and absolute path, for example:

```
Open /workspace/report.md from box sbx_example in the Files canvas.
```

The assistant calls `open_box_file`, which stats the actual file and returns
both its resource link and the existing self-contained MCP App in Files mode.
This does not need primary-exec discovery or terminal attachment. File API reads
can wake a stopped box. Archived boxes must be restored in Discobox first.

Inside an open box workspace, Files and Terminal are two views of the same box.
Switching views keeps an existing terminal stream alive. Files-only entry does
not start one; selecting Terminal resolves and attaches it explicitly. Opening
another box invalidates outstanding file reads so late results cannot replace
the new selection. Directory navigation never retargets an already-started read.

## Tools and resources

- `list_box_files(boxId, path, offset?, limit?)`: default directory `/workspace`;
  directory pages default to 100 entries and allow at most 200. The result gives
  total/nextOffset/hasMore and canonical URIs, plus up to 20 typed resource links.
- `read_box_file(boxId, path, offset?, length?)`: default 64 KiB and at most
  256 KiB per byte range. Actual content appears once in the tool content;
  structured metadata describes the box, file, version, offset, and truncation.
- `open_box_file(boxId, path)`: open a regular file in the Files canvas.
- App-only `file_read` returns bounded preview chunks once in structured data;
  `file_stat` checks metadata/version. These calls are hidden from the model.

A canonical URI is, for example:

```
discobox-file://default/sbx_example/workspace/report.md
```

The server advertises a resource template and reads matching URIs from its
configured project and endpoint. This is not a local `file://` URL and cannot
access the adapter's filesystem. URI parsing rejects other projects, userinfo,
queries, fragments, and noncanonical paths. Upstream Discobox permissions and
parent checks apply to every listing/read. No role or adapter tag is required.
The viewer currently previews regular files, not symlinks or special files.

Resources/downloads through this adapter have a 1 MiB limit. Larger files use
explicit byte ranges with the read tool. The preview loads 256 KiB initially,
then offers Load more up to 1 MiB. Changed versions during a read are rejected
rather than mixing content; open previews check their version every five
seconds while visible. Uploads, edits, recursive transfers, and delete/rename
are not implemented.

A generic MCP resource link identifies content; the host determines what clicking
it does. `open_box_file` explicitly opens our MCP App viewer when supported.
Download uses the host's download bridge, when available, or an authenticated
standalone-browser read. Copy link falls back to selectable URI text when the
iframe cannot write to the clipboard. No OS URL handler is registered.

## Context and transport limits

Explicit Share with chat attaches the preview and its box/path/version identity
to app context. Partial previews are labeled excerpts. Context attachments are
bounded to 64 KiB and larger ones become explicit excerpts with instructions to
read the file through its tools. Subsequent selection context retains the share.
Only one pending context update is kept while the host is slow; recent receipts
are coalesced instead of building an unbounded promise queue. The complete file
is not duplicated in model-facing content and structured output.

The opaque canvas iframe never fetches localhost or Discobox directly. It calls
MCP tools through the host bridge, as the terminal does. The standalone page uses
the same authenticated methods at `/api/files/list`, `/api/files/read`, and
`/api/files/stat`. All operations are GETs upstream. File bodies are never used
as instructions or sent to another box.

## Verification

Go/race tests cover scope, URI encoding, parent permissions, range limits,
oversized upstream responses, version changes, unsafe directory entries, typed
resource links, resource reads, and the authenticated browser fallback. Node
tests cover stale-response isolation, relative link scoping, UTF-8 across ranges,
automatic version updates, bounded context, and slow-host coalescing.

An opaque Chromium iframe with `connect-src 'none'` verified rendered Markdown,
relative file navigation, sanitization of scripts/iframes/remote images/executable
links, context sharing, automatic updates, terminal continuity, and narrow-pane
layout with no direct HTTP requests or browser exceptions. It used isolated
fixtures: the earlier research box had been archived by the time final viewer
validation ran. Its real stat/list/content API had been verified during triage.
Native Claude and OpenAI desktop file-view validation is still pending.
