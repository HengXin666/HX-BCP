# Security Policy

## Reporting a vulnerability

Please do not publish working exploits, subscription credentials, session
cookies, database files, or server details in a public issue. Use GitHub's
private vulnerability reporting / Security Advisory flow for this repository.
Include the affected version, deployment shape, reproduction steps, expected
impact, and any relevant redacted logs.

No release should be described as free of all vulnerabilities. Security fixes
are prioritized by exploitability, privilege gained, exposed secrets, and
impact on the independent Mihomo data plane.

## Deployment boundary

- The management API binds to loopback by default. Publish it only through an
  authenticated private network, VPN, or hardened reverse proxy with TLS.
- The Mihomo External Controller uses a private Unix Socket in production and
  must not be exposed publicly.
- Public proxy Listeners and the management endpoint are separate. WebSocket
  paths are routing identifiers, not authentication. Direct HTTP/SOCKS/Mixed
  residential listeners bypass Cloudflare/WAF and must use authentication,
  firewall restrictions, and an explicit public bind.
- `/sub/` share tokens disclose client proxy credentials. `/ctl/` control
  tokens can rotate residential exits and spend provider quota. Treat both as
  passwords, serve them only over HTTPS, and rotate them independently after
  suspected disclosure.
- Keep `/var/lib/hx-pxy`, the master key, SQLite database, runtime
  configuration, subscription snapshots, and disaster backups readable only
  by the service account and administrators.
- Apply OS, Mihomo, and HX-Pxy security updates together. The project's
  MIT license does not change the licenses or security policies of bundled
  third-party components.

## Browser terminal threat model

The optional browser terminal is a local PTY Shell on the HX-Pxy server;
it is not an SSH client or an SSH jump host. Enabling it grants the logged-in
administrator command execution. A local development session runs as the
control-plane account; the production systemd helper creates a root PTY. Treat
production management-panel administrator access as root-equivalent while the
feature is on.

The terminal is enabled by default, can be disabled with
`HX_PXY_TERMINAL=0`, and requires all of the following:

- a configured, authenticated administrator session;
- a configured TOTP second factor and a recent step-up verification;
- same-origin WebSocket acceptance;
- a maximum of two concurrent sessions;
- no idle disconnect and a two-hour absolute session limit;
- periodic session revalidation, so logout, username changes, password changes,
  and expiry terminate existing sessions;
- bounded WebSocket frames and PTY window dimensions;
- a minimized Shell environment that excludes control-plane environment
  variables; command history is persisted by default (bash `~/.bash_history`,
  zsh `~/.zsh_history`) for the single administrator and can be restored to a
  no-history lockdown with `HX_PXY_TERMINAL_HISTORY=0`;
- structured open/close audit records without command contents or secrets.

Predictive local echo is enabled only when the server reads both `ECHO` and
`ICANON` from the PTY. Safe printable input may be batched for about 12ms; Enter,
control characters, passwords and raw/full-screen input flush immediately and
disable prediction as appropriate. A control input suspends subsequent
prediction until the server synchronizes the PTY mode again. This covers silent
password reads such as `read -s` while reducing weak-network typing latency.

In production the terminal helper creates a root PTY after validating the
control-plane peer through Unix-socket permissions and `SO_PEERCRED`. Therefore
successful terminal authentication grants root command execution, not merely
the `hx-pxy` service account. The same helper accepts one separate,
argument-free update frame and can only schedule the root-owned installer's
fixed `upgrade` action; browser input cannot supply a command or target version.
The terminal file manager's list/stat/download/upload/mkdir/remove operations
are executed by the same helper over the same validated socket, so the panel
sees the same root filesystem view as the shell (the sandboxed control plane
cannot read `/home` or `/root` under `ProtectHome=true`). Each operation runs
in its own connection with a 30s timeout, a 256 MiB upload cap, a 5000-entry
listing cap, and re-validated absolute paths.

Residual risk remains: an authorized administrator can intentionally damage
application or system data, execute resource-intensive commands, or start processes that
outlive the browser session. Keep the terminal disabled when it is not needed,
and prefer ordinary SSH with OS-level keys, MFA/bastion policy, and a dedicated
administrative account for routine server administration.
