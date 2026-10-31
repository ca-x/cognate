# Grok Build owner client library

`internal/grokclient` implements owner-authenticated configuration asset
management through the existing nine ConfigurationService RPCs. This is a library
checkpoint, **not a registered CLI command, GUI editor or native application**.

- List/get/read, create/update/copy/delete and complete order replacement use the
  current authenticated owner. Revision CAS is never silently refreshed.
- `Prepare` snapshots typed input and reads current state. Its review is local,
  explicitly **not server validation**; no mutation or replacement key is sent.
- `Submit` consumes one operation. Lost or invalid mutation acknowledgements
  return Unknown and block further writes on that Client, without automatic
  replay. Reads remain available for manual reconciliation. This is not durable
  cross-process idempotency or proof that an ambiguous create was rolled back.
- Key actions are Keep/Replace/Clear; copy never inherits a key. Switching to
  native login with a retained key requires explicit Clear in the same CAS.
  Unknown preserved payload fields remain read-only for rewrite/copy.
- Linux file input is bounded, owned, regular and nofollow, with checked Close;
  keys require mode 0400 or 0600. Non-Linux file loading returns Unsupported.
- HTTPS verifies normal CA trust. Only literal loopback can use explicit HTTP.
  Transport pins origin/procedures and bounds wire data, deadlines and workers;
  proxy, redirects, cookies, caller traces and arbitrary transports are disabled.
- Opaque carriers redact by default; explicit summaries show only reviewed
  nonsecret fields. Close owned Input, View, Operation and Client resources.
  Clearing buffers is not a promise to physically erase all Go/protobuf copies.

Actual Linux TLS/App/encrypted SQLite tests cover persistence, CAS, credentials,
owner isolation, order, cancellation and committed-response loss. Cross-builds
are compile evidence only. CLI terminal consent, Web/MyGo integration, import,
local TOML application and the complete agent matrix remain separate work.
