# Agent Note: TOTP encryption context survives the product rename

Status: implemented

## Problem

An enabled authenticator stored by HX-ProxyGroup uses `hx-proxygroup/admin-totp/v1`
as AES-GCM associated data. Renaming that string to `hx-pxy/admin-totp/v1`
does not rename stored ciphertext. Correct codes then fail before TOTP validation,
and the terminal unlock API returns a generic internal error.

## Decision

New setups retain the current context. Secret loading attempts the current context,
then exactly the deployed legacy context with the same master key. Both attempts
require successful GCM authentication. Verification does not rewrite or delete the
record; enable, disable and terminal step-up share this reader.

## Alternatives considered

- **Do nothing / reuse only the new context:** simplest reader, but existing
  authenticators remain unusable even when the administrator enters a valid code.
- **Reset every authenticator:** avoids compatibility code, but discards a security
  binding and forces manual recovery for an unrelated branding change.
- **Re-encrypt on read:** eventually unifies stored contexts, but adds a write and
  concurrent setup/disable race to verification. Two bounded authenticated reads
  preserve both deployed formats without a destructive migration.

## Consequences

The legacy context remains a storage contract, not a display name. A missing or
incorrect master key and corrupted data still fail closed; this is not recovery
from lost keys. No TOTP code, key or plaintext is logged.

## Testing

`internal/auth/totp_upgrade_test.go` covers both stored formats, enable/verify/disable,
invalid codes, unchanged ciphertext, context mismatch, corruption and wrong keys.
`internal/api/terminal_test.go` carries a legacy encrypted binding through the HTTP
verification endpoint into a real PTY WebSocket, retaining origin and revocation checks.
