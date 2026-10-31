# Grok owner asset CLI

`cognate grok-assets` manages the authenticated owner's encrypted server assets.
Saving, copying, deleting or ordering an asset is **not native application**.
All native targets remain `NativeUnavailable`; qualification is not enabled.
This slice supports bounded file/terminal operations on Linux. Non-Linux cross-
compilation does not establish runtime support. No real keyring or user HOME is
needed or initialized.

## Session and commands

This command is added on main after `cli-v0.1.0-rc.1`; that preview archive is
unchanged. On Linux, the account wrapper can supply a private session without
exporting a token:

```sh
cognate auth run --server https://cognate.example -- grok-assets list
cognate auth run --server https://cognate.example -- grok-assets create --draft-file ./grok.json --key-action replace --key-file ./grok.key --apply
```

Account input does not approve a mutation. The same terminal must still show
the full Grok review and receive the exact key-slot and final review phrases.
Read-only session checks at preparation/submission do not replay a mutation.
The account wrapper attempts Logout once after its child finishes; an
unconfirmed Logout cannot roll back a committed Grok asset.

Every data command needs `--server ORIGIN --token-stdin`. Supply the current
owner session through bounded non-terminal stdin, never argv, environment,
share token, cookie or a review answer. HTTPS requires a trusted CA. Explicit
plaintext HTTP is accepted only for literal loopback IP origins. Redirects and
environment proxies are disabled. Help does not authenticate.

| Command | Additional flags |
| --- | --- |
| `grok-assets list` | none |
| `grok-assets get` / `read` | `--id ID` |
| `grok-assets create` | `--draft-file JSON` |
| `grok-assets update` | `--id ID --expected-revision N --draft-file JSON` |
| `grok-assets copy` | `--id SOURCE --expected-source-revision N --name NAME` |
| `grok-assets delete` | `--id ID --expected-revision N` |
| `grok-assets order get` | none |
| `grok-assets order replace` | `--expected-order-revision N --ids-file JSON` |

Create/update additionally accept `--key-action keep|replace|clear` and
`--key-file PRIVATE` **only** with replace. Keep is update-only. Omitted action
means no new key on create and retain existing key on update. Clear requires
explicit confirmation; native transition with an existing key requires Clear
in the same asset CAS. Native login cannot accept a replacement API key. Copy
always omits the source key, even if the source retains one. Credential
readiness is not proof that a referenced environment value exists.

Positive asset/source revisions are required. Order revision must be explicitly
supplied and may be zero for an absent partition. The IDs file is an ordered
JSON array of unique IDs (maximum250), and must be the full current membership.
`[]` is valid only for an actually empty partition, not a request to delete
existing membership. There is no force, automatic rebase, retry or resume.

## Strict private input

Draft files use canonical `configuration.v1.Draft` ProtoJSON, Grok arm only:

```json
{"name":"Official","grokbuildProvider":{"mode":"GROK_BUILD_MODE_NATIVE_LOGIN"}}
```

```json
{"name":"Custom","grokbuildProvider":{"mode":"GROK_BUILD_MODE_CUSTOM_MODEL","model":{"profile":"my-profile","upstreamModel":"x-ai/model","endpoint":"https://example.invalid/v1","name":"Model title","apiBackend":"responses","contextWindow":"500000","envKey":"GROK_API_KEY"}}}
```

Custom requires all model fields except `envKey`; native forbids a model. The
context integer uses a canonical decimal string in1..9007199254740991. Optional
env presence is preserved; null is not absence. Unknown/duplicate fields,
aliases, other provider arms, numeric enum spellings, null and trailing input
are rejected. Server metadata names are normalized by its existing policy;
review shows requested and expected normalized names. Other literals, including
key whitespace, are not silently trimmed.

Draft/order files are bounded64KiB regular owned single-link files, not group/
world-writable, with no symlink in any component or `..`. Key files require
mode0400 or0600 and contain raw1..8192 UTF8 bytes, no NUL/CR/LF: do not append a
newline. FIFOs/devices/sockets/hardlinks/changed snapshots are refused. Files
are snapshot-read once before review; approval does not reread mutable paths.
Checked Close failures invalidate input. Buffer clearing is not a promise of
physical secure erase or preemption of an uninterruptible filesystem syscall.

## Review and submit

All mutations default to **local review only**. No Preview RPC exists, and new
drafts have **NOT been server-validated**. Server quota, reference protection,
ownership and CAS remain authoritative at submit. Default preview sends no
replacement key and performs no mutation; reads/authentication are allowed.

`--apply` requires a real controlling TTY separate from session stdin. The same
complete stdout review is displayed again there: origin/owner/local review ID,
source identity/revision, old/new typed fields and optional presence, key action
and predicted presence/readiness, and complete old/new ordered IDs as relevant.
No key value/hash/length is displayed. Future schemas remain metadata-readable;
unknown preserved extras cannot be rewritten through this typed command.

For Replace enter exactly `replace grokbuild.api-key REVIEW_ID`; for Clear enter
exactly `clear grokbuild.api-key REVIEW_ID`. Then enter the displayed final
`grok ACTION REVIEW_ID` (`order-replace` for ordering). Each ID is fresh and
local, not a receipt/idempotency key. No stdin yes, generic yes, old ID or partial
output constitutes consent. Complete review, exact phrases and successful TTY
Close precede the single Submit. Reviews expire after60s within an outer120s
command budget; the deadline is not renewed. No DB/network lock is held while
the user reviews. Cancellation and owned I/O workers are drained.

Success reports server owner-asset state only, never “applied”. List/get/read
are explicit user-requested nonsecret views, not safe diagnostic log payloads.
CLI errors are finite/sanitized and do not print raw server/parser errors,
session tokens or key contents.

## Unknown outcomes

A lost/truncated/untrusted mutation response, uncertain cancellation or failed
final output/Close can mean the write committed. The command reports **Unknown;
do not replay**, never rollback or success. It submits at most once and does
not automatically issue another mutation. Use `list`, `get`, `read` or
`order get` for read-only manual reconciliation. These reads do not establish a
unique outcome for a lost create/copy response, and no Status/receipt/journal
has been invented. The client blocks further writes during its lifetime;
durable cross-process duplicate prevention is not claimed. Manually launching
the same mutation again can create a duplicate. Diagnose remote state first.
