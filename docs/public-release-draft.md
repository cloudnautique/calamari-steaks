# Proposed first public release of discobox-mcp

Keep CI artifacts on main for testers. Publish versioned downloads on GitHub Releases from a tag matching the MCPB manifest version. Give the README one stable Releases link.

| Platform | Standalone download | Claude Desktop bundle |
| --- | --- | --- |
| macOS | One universal tar.gz for Apple Silicon and Intel | darwin-universal.mcpb |
| Linux | tar.gz for amd64 and arm64 (glibc) | Matching .mcpb for compatible hosts |
| Windows | ZIP for amd64 and arm64 | Matching .mcpb |

Use a common filename prefix, for example `discobox-mcp-<version>-darwin-universal.tar.gz`. Include the executable and applicable license and third-party notices in each archive. Generate one SHA256SUMS file covering every published asset.

## Build once, distribute twice

The packager already stages the complete executable under `dist/mcpb-<target>/server/`. Archive those exact bytes after signing and before publishing. Preserve executable permissions in tar archives. Extract each archive in CI, compare its binary hash with the MCPB’s executable, and smoke-test startup and MCP discovery on compatible runners. Upload to a draft release after all platforms pass, then publish the complete set together.

## Before announcing it

- Update repository metadata, Go module/import paths, and active install links for the final repository name. Keep historical review records intact.
- Choose a project license and check redistribution notices for the Go and frontend dependencies. The repository currently has no top-level license file.
- Document signing status accurately. The current Mac executable is ad-hoc signed. For a smoother downloaded-app experience, evaluate Developer ID signing and notarization.
- Replace the README’s temporary MCPB extraction instructions with a direct standalone download. Keep source builds and advanced HTTP configuration one link away.
