# cc-switch repository snapshot upload

The repository page accepts an explicitly selected cc-switch SQLite snapshot.
Choose the cc-switch source, choose the file, then click Upload and preview.
File selection alone does not transmit anything; candidates start unselected.
The CLI/MyGo do not execute moejs or source-specific plugins.

## Produce a consistent export

Do not upload the main file of a running WAL database or copy it without its
committed WAL state. Use SQLite's backup API to produce a consistent destination,
then use SQLite itself to put that destination in DELETE journal mode and close
it before upload. Only change the exported destination, not the original app's
database. A backup of a WAL source can retain WAL header flags even after backup
completion; merely calling the backup API does not establish the accepted file
format. Never fix this by editing header bytes or dropping a sidecar.

For users already using the SQLite shell, `.backup` followed by
`PRAGMA journal_mode=DELETE` on the export is one way to produce such a file.
The application uses the pure-Go SQLite driver; the shell is not a runtime or
build dependency. The consistency regression test creates a live WAL source,
backs it up through the actual SQLite API, changes only the exported database's
journal mode, and verifies that committed WAL repository rows survive preview.

The server's header/structure checks reject clearly incomplete or unsupported
files. They cannot prove that a manually copied file came from a transactionally
consistent capture; the export procedure remains essential.

## What is uploaded and returned

The entire selected database is transmitted to the trusted server. It may contain
provider credentials or other unrelated secrets: inspect the destination and
authorize the upload accordingly. Raw bytes stay in request/SQLite memory, are
not logged or written to a server staging directory, and are discarded at request
completion. Buffer clearing does not guarantee secure erasure of every copy.

The server reads only the bounded `skill_repos(owner,name,branch,enabled)` records
and passes those records through the existing server-only moejs transformer.
The response contains normalized repository/ref candidates, optional source
enabled state and an existing-record hint scoped to the authenticated owner.
The enabled hint is not an automatic selection or a change to the source app.
Unlike Magpie, cc-switch normalizes HEAD case-insensitively, following its existing
adapter contract. Malformed or conflicting normalized records fail closed.

Preview does not collect repositories, fetch archives, migrate local skills,
import provider configuration or execute any code from the database. Explicitly
selected repositories use the same sequential Collect/cache validation/private
default/idempotency and partial-failure/cancellation rules as Magpie. Collection
scans the whole public repository, not only source-installed references.

## Protocol and limits

`skillhub.import.v1.ImportService/PreviewCCSwitchSnapshot` accepts `snapshot_data`
bytes, never a server path, owner, SQL command, plugin module or download URL.

- Raw snapshot: nonempty, at most 16 MiB, SQLite header/page-count consistency,
  rollback-mode read/write format versions and whole-file page alignment.
- HTTP/Connect request: at most 24 MiB including ProtoJSON base64 overhead;
  identity encoding only. Response remains at most 2 MiB.
- Authentication and admission precede body decoding. Both import RPCs share a
  process maximum of two active requests and one per owner, with no wait queue.
- Snapshot request/read deadline: 15 seconds; helper: five seconds including the
  existing bounded moejs transformation. Cancellation and App.Close interrupt
  body reads and stalled response writes, then drain admitted work before
  library/store teardown. Response/error flushing has at most one additional
  second of grace; HTTP/1 transport clears the write deadline only after its
  final response flush, not prematurely when the business handler returns.
- The isolated SQLite memory connection uses query-only, trusted-schema off,
  in-memory temporary storage, limited cache, string/SQL/column/expression/attach
  limits, structural quick-check, an ordinary table and no generated selected
  columns. At most 1,000 repository rows are returned; fields/booleans are bounded
  and validated. SQLite/plugin bounds are not a hard process-memory or OS sandbox.
- Existing Magpie limits remain 1 MiB raw, 2 MiB wire and a five-second request;
  ordinary unrelated RPC limits are unchanged.

This is repository import, not completion of F6/M6. Skill-content migration,
client configuration selection/encryption/Pack attachments/automatic application,
complete third-party plugin manifests/host capabilities and other adapters remain
separate release requirements.
