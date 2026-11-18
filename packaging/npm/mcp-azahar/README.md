<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# mcp-azahar

Unofficial MCP server and CLI for the [Azahar](https://azahar-emu.org) 3DS
emulator: launch homebrew, send input, take screenshots, read memory and
variables, use save states and run scripted tests. Not affiliated with Azahar.

```sh
npx -y mcp-azahar            # MCP server over stdio (same as `mcp-azahar serve`)
npx -y mcp-azahar --help     # the CLI
```

MCP client configuration:

```json
{"mcpServers": {"azahar": {"command": "npx", "args": ["-y", "mcp-azahar"]}}}
```

Tools only touch files in the server's working directory; allow more with
`"args": ["-y", "mcp-azahar", "serve", "--allow-dir", "/path/to/project"]`
(flags alone also start the server).

You need Azahar 2127.0 or newer; `launch` runs it headless with Xvfb on
Linux. This package installs a prebuilt binary for your platform (Linux,
macOS, Windows; x64 and arm64) through an optional dependency. Documentation,
the container image with Azahar included, and sources:
https://github.com/michvllni/mcp-azahar. License: GPL-3.0-or-later.
