## What and why
<!-- What does this change, and why? Link related issues. -->

## How was it tested?
<!-- Commands you ran, e.g. go test ./..., integration tests, manual steps. -->

## Checklist
- [ ] Commit messages follow Conventional Commits (`feat: …`, `fix: …`, `docs: …`)
- [ ] `go test ./...` and `golangci-lint run ./...` pass
- [ ] Docs updated in this PR: `README.md`, `AGENTS.md`, and `NOTES.md` / `docs/` where relevant (mandatory, see AGENTS.md)
- [ ] New source files carry `SPDX-License-Identifier: GPL-3.0-or-later`
- [ ] CLI and MCP stay in sync (a new command has its MCP tool and vice versa)
- [ ] If this is a release prep: `const version` in `cmd/mcp-azahar/main.go` is bumped
