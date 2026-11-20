#!/bin/sh
#---
# id: discobox-mcp
# name: Discobox MCP server
# description: Runs this workspace's built MCP adapter on port 8181.
# ports: 8181
# protocol: http
#---
set -eu
CDPATH= cd -- "$(dirname -- "$0")/../.."
if [ ! -x ./bin/discobox-mcp ]; then
  echo 'Build bin/discobox-mcp before starting this workspace service.' >&2
  exit 1
fi
if [ ! -s .discobox-mcp/access-token ]; then
  echo 'Set up the private .discobox-mcp/access-token file before starting this workspace service.' >&2
  exit 1
fi
exec ./bin/discobox-mcp --local --browse --transport http \
  --listen 127.0.0.1:8181 \
  --mcp-forward-port 8081 --ui-origin http://127.0.0.1:8081 \
  --access-token-file .discobox-mcp/access-token --request-log
