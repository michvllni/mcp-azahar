# Security policy

## Reporting a vulnerability
Please report vulnerabilities privately through
[GitHub security advisories](https://github.com/michvllni/mcp-azahar/security/advisories/new),
not as a public issue.

## Scope notes
- Azahar's RPC server is unauthenticated and, as of Azahar 2127, listens on
  UDP port 45987 on all interfaces; Azahar has no setting to restrict it,
  so a running session is reachable from the network (known limitation,
  docs/DECISIONS.md D9: use a firewall or an isolated container). Anything
  in mcp-azahar that makes it reachable beyond that is a vulnerability.
- Vulnerabilities in the Azahar emulator itself belong to the
  [Azahar project](https://github.com/azahar-emu/azahar).
