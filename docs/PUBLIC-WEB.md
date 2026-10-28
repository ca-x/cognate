# Public catalog Web UI

Implemented on October 28, 2026 against the catalog contract in `proto/skillhub/catalog/v1/catalog.proto`. This document covers the catalog/categories Web work and the shared theme/localization follow-through. The catalog backend, anonymous CLI installation, MCP management page, and their integration have separate owners.

## Routes and boundaries

| Route | Access | Purpose |
| --- | --- | --- |
| `/explore` | Anonymous | Public search, category filtering, bounded skill/category pagination |
| `/explore/<skill-id>` | Anonymous | Public metadata, source, content hash, publication revision, copy-only installation command |
| `/categories` | Local Web session | Owner category creation, renaming, deletion, and counts |
| `/repositories` | Local Web session | Existing collection flow, repository publication controls, per-skill category selection and owner filtering |
| `/`, `/packs`, `/account` | Existing authentication gate | Existing sources, Packs/shares, and account flows |

The guest bypass requires both an exact public route match and the `/explore` or single-segment detail pathname. It does not exempt arbitrary paths beginning with `explore`. The server's SPA whitelist and its rejection of unknown, nested, and traversal paths are maintained separately in `web/embed.go`; adding a client route alone does not authorize a server fallback.

`PublicCatalogService` uses a separate Connect transport with no session interceptor and `credentials: "omit"`, including when a signed-in user visits Explore. `CategoryService` and `PublicationService` use the existing authenticated management transport. Empty Pack/share tokens do not become anonymous access. Catalog URLs carry only search text, category IDs, and skill IDs, never a newly introduced authentication or distribution token.

## Using the UI

### Anonymous discovery

Explore fetches actual `ListSkills` and public `ListCategories` RPCs. Loading, empty, validation, and RPC error states are distinct. Search text is checked using UTF-8 bytes, including search terms opened from a URL. Unclassified published skills remain discoverable.

Skills use 24-result pages with previous/next controls. Public category choices use bounded 64-category pages with explicit previous/more-category controls; moving between pages replaces the bounded option page instead of accumulating all owners' categories. A selected category remains represented when it is off the current page. Cursor histories stop at 100 pages, and repeated cursors disable forward requests. These are UI browsing bounds, not claims that the complete server catalog has been loaded.

Details show the source repository, branch, relative skill path, full SHA-256 hash, publication revision, and public categories. The install command is shell-quoted for a POSIX shell:

```sh
skillhub install-public --server 'https://your-skillhub.example' --skill 'opaque-skill-id'
```

The page only copies this command. It does not install, stream files, validate a local installation, or report a successful installation. The CLI must independently authorize current publication and verify downloaded content. An old page or copied command does not bypass a subsequent unpublication.

### Owner categories

`/categories` shows owner-only counts and revisions. Names must be nonempty after trimming and fit 80 UTF-8 bytes. The form displays the byte count rather than treating every character as one byte. Each account has at most 100 categories.

Renaming and deletion submit the revision captured when their dialog opens. A conflict is an error requiring a fresh read, not a successful save. The deletion confirmation explicitly states that only the category and its associations are removed, not skills, repositories, or installed files.

Every collected skill has a Radix-backed category dialog with mature checkbox controls and an eight-category selection limit. It changes management metadata only, not files, the content hash, or publication. The owner repository filter first loads assignments only when a category is selected. It can make one assignment request per collected skill; the existing scanner bounds a repository at 512 skills. This path has not been stress-tested at that maximum.

### Repository publication

The selected repository's publication state is read from `GetPublication`; collection is not publication. Enabling publication requires an explicit checkbox acknowledging that each collected skill's entire directory, including binary and hidden files, may be downloaded anonymously. Confirmation remains disabled until that acknowledgement is selected.

`SetPublication` sends the original revision and `confirm_full_contents: true` only for an acknowledged publication. The dialog closes and catalog queries invalidate only after an RPC success. Unpublication has a separate destructive confirmation explaining that already transmitted files cannot be recalled and private Packs are not deleted. There is a reload-status control for unknown or changed state. Missing, unimplemented, unauthorized, duplicate-name, conflict, capacity, transport, and data-loss failures are displayed without invented success or placeholder catalog records.

## Frozen protobuf contract

Package: `skillhub.catalog.v1`.

Go package: `github.com/ca-x/skillhub/gen/skillhub/catalog/v1;catalogv1`.

Generated artifacts:

- `gen/skillhub/catalog/v1/catalog.pb.go`
- `gen/skillhub/catalog/v1/catalogv1connect/catalog.connect.go`
- `web/src/gen/skillhub/catalog/v1/catalog_pb.ts`

| Service / RPC | Request | Response |
| --- | --- | --- |
| `CategoryService.ListCategories` | `ListCategoriesRequest` | `ListCategoriesResponse` |
| `CategoryService.CreateCategory` | `CreateCategoryRequest` | `CreateCategoryResponse` |
| `CategoryService.UpdateCategory` | `UpdateCategoryRequest` | `UpdateCategoryResponse` |
| `CategoryService.DeleteCategory` | `DeleteCategoryRequest` | `DeleteCategoryResponse` |
| `CategoryService.GetSkillCategories` | `GetSkillCategoriesRequest` | `GetSkillCategoriesResponse` |
| `CategoryService.SetSkillCategories` | `SetSkillCategoriesRequest` | `SetSkillCategoriesResponse` |
| `PublicationService.GetPublication` | `GetPublicationRequest` | `GetPublicationResponse` |
| `PublicationService.SetPublication` | `SetPublicationRequest` | `SetPublicationResponse` |
| `PublicCatalogService.ListSkills` | `ListSkillsRequest` | `ListSkillsResponse` |
| `PublicCatalogService.ListCategories` | `PublicCatalogServiceListCategoriesRequest` | `PublicCatalogServiceListCategoriesResponse` |
| `PublicCatalogService.GetSkill` | `GetSkillRequest` | `GetSkillResponse` |
| `PublicCatalogService.DownloadSkill` | `DownloadSkillRequest` | stream `skillhub.pack.v1.DownloadSkillResponse` |

Type/field summary:

- `Category`: `id`, `name`, `uint32 skill_count`, `int64 revision`.
- `Publication`: `repository_id`, `published`, `int64 revision`.
- `PublicCategory`: `id`, `name`, `uint32 skill_count`.
- `PublicSkill`: `id`, `slug`, `name`, `description`, `content_hash`, `repository_id`, `repository_name`, `path`, `branch`, `int64 publication_revision`, repeated `PublicCategory categories`.
- Owner category list is an empty request returning repeated categories. Create takes `name`; update takes `category_id`, `expected_revision`, and `name`; delete takes `category_id` and `expected_revision`. Create/update responses contain `category`; delete response is empty.
- Get assignments takes `skill_id`; set takes `skill_id` and repeated `category_ids`. Both return repeated categories.
- Get publication takes `repository_id`. Set takes `repository_id`, `expected_revision`, `published`, and `confirm_full_contents`. Both return `publication`.
- Public skill list takes `query`, `category_id`, `uint32 limit`, and `cursor`, returning repeated `skills` and `next_cursor`.
- Public category list takes `uint32 limit = 1` and `string cursor = 2`, returning repeated `PublicCategory categories = 1` and `string next_cursor = 2`.
- Public detail takes `skill_id` and returns `skill`. Public download takes `skill_id`, `content_hash`, and `int64 publication_revision`; it imports the existing Pack streaming response rather than defining another file stream.

IDs, hashes, names, and cursors are strings. Counts/limits are `uint32`; revisions are `int64` (TypeScript `bigint`). Catalog request/response types contain no token fields.

Server-enforced semantics agreed before freeze:

| Concern | Limit / invariant |
| --- | --- |
| Owner categories | At most 100 per owner; owner list remains unpaginated |
| Category name | At most 80 UTF-8 bytes |
| Skill assignments | At most 8 owner categories per skill |
| Owner category CAS | Initial revision 1; update/delete require expected revision |
| Publication | Initially unpublished, revision 1; enabling requires full-directory confirmation |
| Public skill pages | Default 24, maximum 100 |
| Public search | At most 128 UTF-8 bytes |
| Public category pages | Default 64, maximum 100 |
| Public category exposure | At least one published skill; public-only skill counts |

The only pre-freeze refinement to the brief's exact RPC shape is bounded public category pagination. `PublicCatalogService.ListCategories` is no longer an empty request/unbounded result. The requester explicitly added `limit`, `cursor`, and `next_cursor` so arbitrary owners cannot create an unbounded anonymous category response. The publication and owner category revision defaults are semantic rules, not nonzero proto3 field defaults.

Protocol SHA-256 at freeze: `2986b1c3753be21254fea02df90fddf6d7d92aa2ef53e669565c70f6cc33ef37`.

## Themes and localization

The design extends the existing quiet repository workspace: left-aligned source/identity information, restrained blue actions, readable technical metadata, and no decorative hero or fabricated content. Existing typography and semantic tokens are retained. Dark colors are token overrides, including input/background/foreground colors; Tailwind uses `@theme` variables rather than literal inline utility colors that defeat those overrides.

Theme choices are light, dark, and system, initially system. A small external-module app integration sets `data-theme`, responds to system color-scheme changes only in system mode, and uses the existing CSS tokens. There is no theme library or inline bootstrap script. The static document can use its light base colors before the external module runs; this implementation does not promise a pre-JavaScript flash-free dark boot.

The locale choices are `zh-CN` and `en`, implemented with i18next/react-i18next, not a custom translation engine. A valid saved preference wins. Otherwise browser English selects English, and other/unsupported locales select Simplified Chinese. English is the deliberate missing-resource fallback. At this task's freeze, the shared resource inventory has 400 entries, with Chinese natural-language keys and explicit English values; main's subsequent MCP navigation label brings the final shared audit to 401. The Chinese resource is derived from those same keys. `useTranslation` subscriptions update live components; changing locale does not key-remount forms or discard entered credentials. Document `lang` and title update with the locale.

Coverage includes login/registration, OIDC/device flow UI, account, GitHub sources, repository collection, Packs/shares, categories, publication and assignments, anonymous discovery/details, validation, RPC errors, empty/loading states, and accessibility labels. User-authored names, descriptions, repository content, hashes, opaque IDs, product names, and provider names are not automatically translated. Dates and displayed counts/revisions use `Intl.DateTimeFormat` and `Intl.NumberFormat`. The separately authored MCP management page uses its own localization namespace and requires its own review.

Only these preference keys are persisted:

- `skillhub.theme`: `system`, `light`, or `dark`.
- `skillhub.locale`: `zh-CN` or `en`.

Storage access is guarded for unavailable browser storage. Web sessions remain page-memory state; existing one-time Pack/share token handling remains component-local rather than a Query cache, preference, or URL feature.

### Mature components and CSP

Existing React 19, TanStack Query/Router, Lucide, shadcn-style Button/Input, and Radix Checkbox are reused. Added exact dependencies:

- `@radix-ui/react-dialog` `1.3.1`: Root/Trigger/Portal/Overlay/Content/Title/Description/Close, including controlled state and `asChild` composition.
- `@radix-ui/react-select` `2.4.1`: Root/Trigger/Value/Portal/Content/Viewport/Item/ItemText/ItemIndicator and scroll controls.
- `i18next` `26.5.11` and `react-i18next` `17.1.9`: `initReactI18next`, `useTranslation`, supported locales, fallback resources, and explicit initialization.

The thin component files compose official Radix primitives; they do not implement a custom focus trap, listbox, checkbox, dialog system, or combobox. Simple category/repository/expiry selections retain native browser controls. There is no added animation dependency. Existing reduced-motion and focus treatments remain in use.

Official APIs and React peer compatibility were checked before adding these versions. Reference pages: `https://www.radix-ui.com/primitives/docs/components/dialog`, `https://www.radix-ui.com/primitives/docs/components/select`, `https://react.i18next.com/latest/using-with-hooks`, and `https://www.i18next.com/overview/configuration-options`. i18next 26's supported configuration is used; the removed `interpolation.format` API is not used.

The real isolated App returned the unchanged policy during browser checks:

```text
default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'
```

No CSP weakening, `unsafe-inline`, `eval`, remote font, or theme bootstrap exception was added by this work. Radix selector/dialog operations and theme changes were exercised with that policy active.

## Generation and verification

Generate only this package; MCP protobuf files have another owner:

```sh
buf lint
buf lint --path proto/skillhub/catalog/v1
buf generate --path proto/skillhub/catalog/v1
buf generate --template buf.gen.web.yaml --path proto/skillhub/catalog/v1
go test ./gen/skillhub/catalog/v1/...
npm --prefix web run typecheck
npm --prefix web run build
npm --prefix web audit
```

For a non-writing generation check, add `--output /tmp/your-clean-generation-directory` to both generation commands and compare the three catalog-generated artifacts. The repository currently has no Web test runner or ESLint command; typechecking/building do not claim to be a new frontend test suite.

The detailed implementation/evidence report is `.superpowers/sdd/2026-10-28-client-subscriptions/public-ui-report.md`. Bounded real-browser checks covered a real isolated App/database and an actual public GitHub repository, not successful RPC response mocks. More-than-one-page catalog/category populations, maximal assignment count, concurrent browser CAS, external OIDC/GitHub authorization, and client installation were not independently exercised by this Web task.
