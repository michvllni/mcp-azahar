# Security policy

## Reporting a vulnerability
Please report vulnerabilities privately through
[GitHub security advisories](https://github.com/michvllni/mcp-azahar/security/advisories/new),
not as a public issue.

## Scope notes
- Azahar's RPC server is unauthenticated. mcp-azahar binds it to 127.0.0.1;
  anything that makes it reachable from other hosts is a vulnerability.
- Vulnerabilities in the Azahar emulator itself belong to the
  [Azahar project](https://github.com/azahar-emu/azahar).
