# User-managed MCP

Cognate exposes `/mcp` through the official `github.com/modelcontextprotocol/go-sdk` v1.8.0 Streamable HTTP transport. The App mounts this handler independently of Connect's smaller request-body guard. MCP is authenticated, stateless, POST-only, and returns JSON rather than opening persistent SSE streams. The SDK implements protocol negotiation, JSON-RPC, tool schemas, and client/server transport; Cognate does not implement a second protocol stack.

## Credentials and local-session management

The Connect service is `skillhub.mcp.v1.MCPManagementService`, defined in `proto/skillhub/mcp/v1/mcp.proto`. Its Go client is under `gen/skillhub/mcp/v1/mcpv1connect`; its TypeScript service descriptor is in `web/src/gen/skillhub/mcp/v1/mcp_pb.ts`.

Every management RPC requires the existing local Web session in `Authorization: Bearer …`. An MCP credential, GitHub credential, or Pack share token cannot substitute for that session. Owner identity comes exclusively from the authenticated session, never a request field.

| RPC | Input | Result |
| --- | --- | --- |
| `CreateCredential` | `spec` | Metadata and one-time `token` |
| `ListCredentials` | Empty | Own metadata only |
| `GetCredential` | `id` | Own metadata only |
| `UpdateCredential` | `id`, `expected_revision`, `spec` | Updated metadata |
| `RotateCredential` | `id`, `expected_revision` | Updated metadata and one-time replacement `token` |
| `RevokeCredential` | `id`, `expected_revision` | Revoked metadata; logical deletion |
| `ListAudit` | `credential_id` | Own bounded audit metadata |

`CredentialSpec` contains a nonempty name of at most 128 UTF-8 bytes, a nonempty list of distinct supported scopes, and an explicit future `expires_at_unix` no more than 365 days away. There is no implicit unlimited expiration. Credential metadata contains opaque `id`, name, scopes, creation/expiration/revocation Unix timestamps, and `revision`. A zero revocation timestamp means no explicit revocation; expiration must still be checked. TypeScript protobuf timestamps and revisions are `bigint`.

Create and rotate return the raw `smcp1_` credential once. These are random 256-bit secrets. Only the SHA-256 digest and authorization metadata are persisted; the digest is marked sensitive in the ent schema. Credential wrappers redact string/Go formatting, text/JSON serialization, and structured logging. A raw token is deliberately obtainable only through the create/rotate return object. Neither metadata lookup nor audit lookup can recover it.

Rotation atomically replaces the digest using revision CAS, invalidating the old secret without changing the public ID. Concurrent rotations with the same revision cannot both succeed. Update and revoke also use CAS; revoked records cannot be reactivated. Expired records remain unusable until their owner explicitly changes the expiration through the local-session API. Rotation alone does not extend expiration. Scope changes apply on the next authorization check, even when a client already connected earlier.

Management status codes are `unauthenticated` for missing/wrong sessions, `invalid_argument` for invalid metadata, `not_found` for missing or foreign credential IDs, `aborted` for stale revisions/revoked mutation attempts, and `resource_exhausted` for capacity limits. Foreign lists are empty. A stale revision should trigger metadata refresh before asking the user to retry.

For the Web UI, show the newly created/rotated secret only in temporary component state, with an explicit copy/dismiss flow. Do not put it into local/session storage, IndexedDB, a query cache, URLs, analytics, exception telemetry, or logs. Clearing the one-time display must drop its in-memory reference. Rotation and revocation should refresh metadata. Secret-bearing responses use `Cache-Control: no-store`.

## Web management

Sign in with a local Web session and open **MCP** at `/mcp-settings`. The page lists only the current user's credential metadata and offers creation, metadata edits, rotation, revocation, and credential-scoped audit history. Choose the least required scopes and an explicit expiration. A revision conflict refreshes metadata and asks for a new explicit action rather than silently retrying a write.

Creation and rotation open a one-time dialog. Copy the value into the intended MCP client's secure configuration before dismissing it; it cannot be recovered later. The page drops this display on dismissal, navigation, logout, or a changed owner/client, and ignores delayed responses from an invalidated action. A failed or unsupported clipboard operation reports the failure without claiming that the value was copied.

This page uses the shared light/dark/system theme and simplified Chinese/English preferences. Those preferences persist in the browser; session and MCP credentials do not. The AI client's endpoint is `/mcp`, not this management page or the Connect management service.

## Tool scopes

Scopes are independent: a write grant does not grant list/get operations, and one domain's grant does not grant another domain's operations.

| Tool | Required scope | Operation |
| --- | --- | --- |
| `catalog_list` | `catalog:read` | Page through the owner's collected repositories |
| `catalog_get` | `catalog:read` | Read one collected repository and its skill IDs |
| `catalog_collect` | `catalog:write` | Collect one canonical public GitHub `owner/repository` through the existing library |
| `packs_list` | `packs:read` | Page through the owner's Packs |
| `packs_get` | `packs:read` | Read one Pack |
| `packs_create` | `packs:write` | Create from the owner's collected skill IDs |
| `packs_update` | `packs:write` | Replace a Pack using `expected_revision` CAS |
| `packs_delete` | `packs:write` | Delete a Pack using `expected_revision` CAS |

All eight tools have SDK-inferred input/output JSON schemas and typed handlers. Tool discovery describes their explicit scope requirements. The four accepted scope strings are exactly `catalog:read`, `catalog:write`, `packs:read`, and `packs:write`; empty, duplicate, unknown, wildcard, publishing, and category scopes are rejected.

`catalog_collect` accepts the lowercase canonical `owner/repository`, not a full URL, filesystem path, or command. Optional branches pass the existing repository reference validation. Collection uses the existing bounded, verified library/cache pipeline; no database transaction spans its network wait. This is not a general URL-fetch capability. Pack selections use `{skill_id, slug?}`. Create takes `{name, description?, items}`; update takes `{id, expected_revision, draft}`; delete takes `{id, expected_revision}`. Get takes `{id}`. List takes optional `{offset, limit}`; defaults are offset 0/limit 50, maximum limit 100 and offset 1000. `next_offset: 0` indicates no next page. Skill outputs exclude filesystem paths and archive internals.

Queries and writes reuse owner-scoped state/library contracts, including Pack selection validation, slug conflict handling, and revision CAS. Tools cannot supply an owner override, read share secrets or GitHub credentials, manage MCP credentials, run JS/commands, read server paths, or publish repositories. Publishing/category tools deliberately wait for separate backend authorization design; `catalog:write` and `packs:write` must not be treated as publishing permission when that backend arrives.

## HTTP and lifecycle bounds

- Send MCP credentials only in one `Authorization: Bearer …` header. No cookie, URL parameter, or MCP session ID is an authentication substitute. All query strings on `/mcp` are rejected.
- Every HTTP request checks its Bearer; every tool handler checks it again against current stored digest, scopes, owner, creation time, expiration, and revocation. No credential authority is cached in an SDK connection/session. Revocation/rotation/expiry take effect for subsequent calls on an already-connected client. The official client may close its session after a 401; reconnect with the replacement credential after rotation.
- The default endpoint permits only loopback hosts, including the SDK's localhost guard. With `PublicOrigin`, configure an exact `http[s]://host[:port]` origin without a trailing slash/path/query/fragment. Host and any supplied Origin must match. That explicit allowlist replaces the SDK's localhost-only guard so a reverse proxy can preserve its public Host while connecting to a loopback listener. Forwarded headers are not trusted. Production remote deployments should use HTTPS, with the proxy preserving the configured host and never logging authorization headers.
- Requests are at most 64 KiB, including chunked bodies. Typed tool output is checked against a conservative 256 KiB serialized budget before the SDK duplicates structured/text content. The complete SDK response is capped at 1 MiB; oversize responses fail rather than silently truncating data. Clients can reduce list page size.
- The default is eight concurrent HTTP requests and eight concurrent tool operations, with immediate rejection at capacity rather than an unbounded waiting queue. `MaxConcurrent` can be set from 1 to 64. Each request/tool has a 20-second default lifetime; `RequestTimeout` must be positive and at most one minute. Read/write deadlines and cancellation also bound slow request/response I/O.
- Management RPCs accept at most 4 KiB, send at most 1 MiB, and cap concurrent calls at eight with a 20-second context. They use the ordinary App HTTP shutdown/drain lifecycle.
- An owner can retain at most 100 credential records, including revoked records. Each credential retains the newest 1000 audit entries. There is no credential purging endpoint or background GC in this increment.
- Audit rows contain only credential ID, allowlisted action, optional validated opaque resource ID, `success`/`denied`/`failure`, and timestamp. No argument objects, labels, branches, URLs, secret values, request headers, or raw backend errors are stored. Invalid/unknown Bearers are not persisted as audit identities. A failure after an already-committed business change may return a sanitized storage error if the separate audit append fails; refresh metadata/domain state before retrying a write.
- Authorization is checked at request/tool boundaries. Revocation is not a rollback mechanism for an already-authorized, in-flight business transaction. Only endpoint shutdown cancels all tracked in-flight work.

App integration uses `mcpaccess.NewService(store)` and `mcpserver.New(mcpserver.Options{Access: access, Store: store, Library: libraryService, PublicOrigin: publicOrigin})`, then passes `MCPAccess` and `MCP` to `httpapi.Options`. The endpoint borrows the Store and Library; it does not close them. `endpoint.Close(ctx)` stops admission, cancels in-flight request/tool contexts, and waits for both. It is safe to call repeatedly or concurrently. It returns the caller's context error if a dependency ignores cancellation; a later Close can continue waiting. App shutdown must drain the HTTP server and close the endpoint with a bounded context before closing Library, Pack service, or Store. Main owns this App wiring.

## SDK sources and generation

Verified on October 28, 2026:

- Official release: https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0 (published September 14, 2026).
- Official release API: https://api.github.com/repos/modelcontextprotocol/go-sdk/releases/latest.
- Pinned source/module: https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/go.mod.
- SDK HTTP transport/options: https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/mcp/streamable.go.
- Typed `AddTool` and server options: https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/mcp/server.go.
- Official guide/client examples: https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/README.md.

Existing dependency versions remain pinned. New indirect dependencies are the SDK's required JSON Schema/encoding/URI/time modules. Build and generation use the installed Go 1.27.3 toolchain and `CGO_ENABLED=0`.

Generate only this service when working alongside other agents:

```sh
export PATH="$(mise where go@1.27.3)/bin:$PATH" CGO_ENABLED=0
buf generate --path proto/skillhub/mcp/v1
buf generate --template buf.gen.web.yaml --path proto/skillhub/mcp/v1
buf lint --path proto/skillhub/mcp/v1
```

Server ent generation is owned by the public backend agent after the explicit MCP schema freeze; do not regenerate it concurrently.
