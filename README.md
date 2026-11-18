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

CI (`.github/workflows/ci.yml`) runs tests, linters and a cross-platform
snapshot build on every pull request; [AGENTS.md](AGENTS.md#build-test-lint)
lists the same checks for running locally.

Tool versions are pinned in `.devcontainer/Dockerfile`; the install scripts
in `.devcontainer/` can also be run by hand (`-h` for help). Contributor and
agent conventions: [AGENTS.md](AGENTS.md).

## Releases
Releases are built by goreleaser for Linux, macOS and Windows (amd64 and
arm64) when a `v*` tag is pushed. The release workflow fails unless the tag
matches `const version` in `cmd/mcp-azahar/main.go`, so bump that first:

```sh
# after the version bump is merged to main
git tag v0.1.0 && git push origin v0.1.0
```
`mcp-azahar version` (or `--version`) prints the version, commit and build
date. No release has been published yet.

## Contributing
Bug reports and feature requests use the issue forms; emulator bugs belong
to [Azahar](https://github.com/azahar-emu/azahar/issues). Pull requests use
Conventional Commit messages and must update the docs (see
[AGENTS.md](AGENTS.md)).

## Security
Azahar's RPC server is unauthenticated. mcp-azahar will bind it to
127.0.0.1 only; never expose it on a network. Report vulnerabilities
privately, see [SECURITY.md](SECURITY.md).

## License
GPL-3.0-or-later. See [LICENSE](LICENSE).
