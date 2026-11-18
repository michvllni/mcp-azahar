# mcp-azahar
Unofficial MCP server and CLI for the Azahar 3DS emulator: automate testing of 3DS homebrew, locally or in CI.

> **Unofficial.** This project is not affiliated with or endorsed by the
> [Azahar](https://github.com/azahar-emu/azahar) project.

**Status:** early development ([docs/PLAN.md](docs/PLAN.md): Phase 0
tooling, Phase 1 protocol research and Phase 2 RPC client done). There are
no usable commands yet besides `version`. Current status and open questions:
[NOTES.md](NOTES.md). The Azahar RPC protocol as used by this tool is
documented in [docs/PROTOCOL.md](docs/PROTOCOL.md).

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
The repository ships a dev container (`.devcontainer/`, Debian 13 trixie)
with Go, Xvfb, Mesa software rendering, Azahar extracted to `/opt/azahar`,
golangci-lint, goreleaser, actionlint and shellcheck. Open the repo in it, then:

```sh
go build ./...
go test ./...
golangci-lint run ./...
go run ./cmd/mcp-azahar version
```

CI (`.github/workflows/ci.yml`) runs tests, linters and a cross-platform
snapshot build on every pull request; [AGENTS.md](AGENTS.md#build-test-lint)
lists the same checks for running locally. Dependencies, actions and tool
versions are kept current by [Renovate](https://docs.renovatebot.com/)
(`.github/renovate.json5`).

Tool versions are pinned in `.devcontainer/Dockerfile`; the install scripts
in `.devcontainer/` can also be run by hand (`-h` for help). Contributor and
agent conventions: [AGENTS.md](AGENTS.md).

## Releases
Releases start in the GitHub UI: after the version bump in
`cmd/mcp-azahar/main.go` (`const version = "X.Y.Z"`) is merged to `main`,
create a new release with a new tag `vX.Y.Z` and publish it. The release
workflow then builds Linux, macOS and Windows binaries (amd64 and arm64)
with goreleaser and attaches them to that release, keeping its title and
notes. It fails without uploading anything if
- the tag is not exactly `v` + the version in `main.go`, or
- the "Set as a pre-release" checkbox does not match the version
  (`X.Y.Z-rc.1` must be a pre-release, `X.Y.Z` must not).

In that case delete the release and its tag, fix the cause and publish
again. `mcp-azahar version` (or `--version`) prints the version, commit and
build date. No release has been published yet.

## Contributing
Bug reports and feature requests use the issue forms; emulator bugs belong
to [Azahar](https://github.com/azahar-emu/azahar/issues). Pull requests use
Conventional Commit messages and must update the docs (see
[AGENTS.md](AGENTS.md)).

## Security
Azahar's RPC server is unauthenticated and, as of Azahar 2127, always
listens on UDP port 45987 on **all interfaces** (not configurable). While
an Azahar started by mcp-azahar runs, anyone who can reach that port can
read and write emulated memory. Block the port with a firewall or run in
an isolated container, and never on an untrusted network. For the same
reason only one Azahar can run per host
([DECISIONS.md D9](docs/DECISIONS.md)); a configurable port and bind
address is being proposed upstream. Report vulnerabilities
privately, see [SECURITY.md](SECURITY.md).

## License
GPL-3.0-or-later. See [LICENSE](LICENSE).
