# discobox-mcp

> “Start a Codex box to research this library. When it’s done, open the report.”

Discobox runs the workload. This MCP server lets your assistant launch the box, follow its output, and bring the result back into the conversation. In a client with MCP Apps, you can take over the terminal, upload a file directly, or open the desktop. Text-only clients can list boxes, send prompts, and read results.

## Get connected

Start with a running Discobox server that you have access to.

- **Claude Desktop:** Install the bundle for your machine, leave the optional settings blank, and ask **“Connect to Discobox.”** The setup screen can find a local installation or walk you through enrolling with a remote one. [Bundle download and installation instructions](docs/desktop-extension.md)
- **Clients that launch a command:** Use the executable inside the bundle, or build it from this checkout with the commands below. Configure your client to launch the executable by absolute path with arguments `--browse --transport stdio`. On Windows, use `discobox-mcp.exe`.

```sh
npm ci
npm run build
go build -o ./bin/discobox-mcp ./cmd/discobox-mcp
```

The frontend build embeds the workspace UI in the executable. Go and Node are build-time dependencies.

## Try it

1. **“Show my boxes.”** Pick a workspace or ask the assistant to launch one with a task.
2. **“Open its terminal.”** Follow the harness as it works.
3. **“Show me the report in its workspace.”** Open the artifact in the Files view.

For an HTTP-only client, run the executable with `--browse --transport http --listen 127.0.0.1:8080`, then configure the client with `http://127.0.0.1:8080/mcp` and the bearer token printed at startup. [Connection details](docs/connection.md)
