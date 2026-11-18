# mcp-azahar
Unofficial MCP server and CLI for the Azahar 3DS emulator: automate testing of 3DS homebrew, locally or in CI.

> **Unofficial.** This project is not affiliated with or endorsed by the
> [Azahar](https://github.com/azahar-emu/azahar) project.

**Status:** early development (Phase 0 of [docs/PLAN.md](docs/PLAN.md)).
Only the project skeleton exists; there are no usable commands yet besides
`version`. Current status and blockers: [NOTES.md](NOTES.md).

## What it will do
One Go binary, two front ends over the same code:
- a CLI for humans, shell scripts, CI and agents using bash
- an MCP server (`mcp-azahar serve`) for MCP clients

Both drive Azahar through its built-in RPC server: launch homebrew, send
input, take screenshots, read/write memory, use save states, read logs and
assert on program state. Design rationale: [docs/DECISIONS.md](docs/DECISIONS.md).

## Requirements
- Azahar **2127.0** or newer (RPC input injection and save states).
  The dev container pins 2127.0-rc3.
- Go 1.27 to build from source.

## Development
The repository ships a dev container (`.devcontainer/`) with Go, Xvfb, Mesa
software rendering, Azahar extracted to `/opt/azahar`, golangci-lint and
goreleaser. Open the repo in it, then:

```sh
go build ./...
go test ./...
golangci-lint run ./...
go run ./cmd/mcp-azahar version
```

Tool versions are pinned in `.devcontainer/Dockerfile`; the install scripts
in `.devcontainer/` can also be run by hand (`-h` for help). Contributor and
agent conventions: [AGENTS.md](AGENTS.md).

## Security
Azahar's RPC server is unauthenticated. mcp-azahar will bind it to
127.0.0.1 only; never expose it on a network.

## License
GPL-3.0-or-later. See [LICENSE](LICENSE).
