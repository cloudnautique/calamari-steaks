#!/bin/sh
#---
# id: discobox-mcp
# name: Discobox MCP server
# description: Runs this workspace's built MCP adapter on port 8080.
# ports: 8080
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
  --listen 127.0.0.1:8080 \
  --access-token-file .discobox-mcp/access-token --request-log
