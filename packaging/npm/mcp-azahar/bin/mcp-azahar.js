#!/usr/bin/env node
// SPDX-License-Identifier: GPL-3.0-or-later
//
// npm wrapper for mcp-azahar (DECISIONS.md D10): runs the binary from the
// platform package npm installed (mcp-azahar-<platform>-<arch>, an
// optionalDependency), passing arguments, stdio, signals and the exit code
// through. Without arguments it runs `serve`, so MCP clients can start it as
// `npx -y mcp-azahar`.
"use strict";

const { spawn } = require("node:child_process");

const platforms = {
  "linux x64": "mcp-azahar-linux-x64",
  "linux arm64": "mcp-azahar-linux-arm64",
  "darwin x64": "mcp-azahar-darwin-x64",
  "darwin arm64": "mcp-azahar-darwin-arm64",
  "win32 x64": "mcp-azahar-win32-x64",
  "win32 arm64": "mcp-azahar-win32-arm64",
};

function binaryPath() {
  const pkg = platforms[`${process.platform} ${process.arch}`];
  if (!pkg) {
    throw new Error(`mcp-azahar has no binary for ${process.platform}/${process.arch}`);
  }
  const exe = process.platform === "win32" ? "mcp-azahar.exe" : "mcp-azahar";
  try {
    return require.resolve(`${pkg}/bin/${exe}`);
  } catch {
    throw new Error(
      `the package ${pkg} is not installed; reinstall mcp-azahar without --no-optional ` +
        "(npm skips optional dependencies with --omit=optional)",
    );
  }
}

let bin;
try {
  bin = binaryPath();
} catch (err) {
  process.stderr.write(`mcp-azahar: ${err.message}\n`);
  process.exit(1);
}

const args = process.argv.slice(2);
const child = spawn(bin, args.length > 0 ? args : ["serve"], { stdio: "inherit" });
for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(sig, () => child.kill(sig));
}
child.on("error", (err) => {
  process.stderr.write(`mcp-azahar: ${err.message}\n`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
