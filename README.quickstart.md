# discobox-mcp

Use your MCP client to launch a task in Discobox, watch its terminal, and open the files it produces. Clients that render MCP Apps can also show the box’s desktop and running services in chat.

You’ll need a running Discobox installation, either on this computer or on a remote server you’re authorized to use.

## Claude Desktop

1. Download the `.mcpb` for your machine from the project’s [CI builds](https://github.com/cloudnautique/calamari-steaks/actions/workflows/ci.yml). Extract the downloaded artifact ZIP.
2. In Claude Desktop, open **Settings → Extensions → Advanced settings → Install Extension** and select the `.mcpb`. Leave the connection overrides blank.
3. Ask **“Connect to Discobox.”** Choose the local connection or follow the remote enrollment steps. Then ask **“Show my boxes.”**

## Codex and other stdio clients

The bundle contains a standalone executable with the UI embedded. Extract the `.mcpb` as a ZIP and move `server/discobox-mcp` (Windows: `server/discobox-mcp.exe`) to a permanent location. On macOS or Linux, ensure the executable has execute permission.

With the Codex CLI, register it once using its absolute path:

```sh
codex mcp add discobox -- /absolute/path/to/discobox-mcp --browse
```

For another stdio client, set its server command to the same executable and its arguments to `["--browse"]`. The client starts it. Ask **“List my Discobox boxes.”** For a text-only client connecting to an enrolled remote server, add `--discobox discobox://<server-peer-id>` to the arguments.

See the [connection guide](docs/connection.md) for enrollment, authentication, and Streamable HTTP, and the [tool reference](docs/contract.md) for what you can do in a box.
