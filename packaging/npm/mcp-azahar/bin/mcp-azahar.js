#!/usr/bin/env node
// SPDX-License-Identifier: GPL-3.0-or-later
//
// npm wrapper for mcp-azahar (DECISIONS.md D10): runs the binary from the
// platform package npm installed (mcp-azahar-<platform>-<arch>, an
// optionalDependency), passing arguments, stdio, signals and the exit code
// through. Without a command (no arguments, or only flags such as
// `--allow-dir DIR`) it runs `serve`, so MCP clients can start it as
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

// withCommand prepends `serve` unless the arguments name a command or ask
// for help or the version. Flags of serve and of the root (--session) mix.
function withCommand(args) {
  const info = new Set(["-h", "--help", "-v", "--version"]);
  if (args.some((a) => info.has(a))) return args;
  // A non-flag argument right after a flag that takes a value is that
  // value, not a command: `--session x` or `--allow-dir /p`.
  const valued = new Set(["-s", "--session", "--allow-dir", "--timeout"]);
  for (let i = 0; i < args.length; i++) {
    if (!args[i].startsWith("-")) return args;
    if (valued.has(args[i])) i++;
  }
  return ["serve", ...args];
}

const child = spawn(bin, withCommand(process.argv.slice(2)), { stdio: "inherit" });
const forwarded = ["SIGINT", "SIGTERM", "SIGHUP"];
// On Windows the console already delivers Ctrl+C to the child, and
// child.kill() would terminate it hard; forward only elsewhere.
if (process.platform !== "win32") {
  for (const sig of forwarded) {
    process.on(sig, () => child.kill(sig));
  }
}
child.on("error", (err) => {
  process.stderr.write(`mcp-azahar: ${err.message}\n`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    // Die of the same signal; our forwarding handler would swallow it.
    process.removeAllListeners(signal);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
