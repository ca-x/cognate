# Cognate catalog and navigation polish

## Scope and reference

Improve the existing Web catalog and workspace navigation, using the user-named
`davila7/claude-code-templates` as an information-design reference. Inspect its
public Skills page, search, category controls and grid/list switch, not just its
README. Keep React/TanStack/Radix, the existing logos, system font and blue brand.
Do not change API contracts, publication/install authorization, import receipts,
credentials, native MyGo or release tags.

Reference inspected at `0604ce8bbf9464744a60ad610dc8179a90d336d4` and the live
`aitmpl.com/skills/` page. Borrow patterns, not source code, artwork, download
counts, popularity claims, sponsors or installation behavior.

## Design

- Content first: a compact introduction, prominent labeled search, six bounded
  category shortcuts, real per-page counts, and readable cards with a list option.
- Reuse light tokens: background `#f4f6f9`, foreground `#1b2839`, card `#ffffff`,
  primary `#235fc6`, muted foreground `#58697e`, border `#d7dfe9`; dark theme keeps
  its current semantic equivalents. No remote font or new animation dependency.
- Desktop keeps the sidebar. A Radix Dialog navigation drawer replaces the long
  mobile link wall; page lookup filters existing route metadata without reading
  private business data. It is explicitly navigation, not full-text skill search.
- Motion is feedback/spatial context only: shared 150ms press and 200ms entry
  tokens, transform/opacity, existing ease-out. Keyboard and reduced-motion stay
  immediate. No delayed dismissal or replayed entrances while filtering.

## Implementation and verification

1. Capture the fixed baseline Web bundle in the existing synthetic HTTP/browser
   fixture before editing. Record reference screenshots outside Git.
2. Reuse navigation metadata across desktop, mobile and a Radix page finder.
   Preserve current paths, labels, focus restoration, skip links and intent loading.
3. Reuse SearchInput for clear/IME/Escape behavior, add explicit category shortcuts
   and grid/list views, and update scoped catalog styles and bilingual text.
4. Add browser regressions for mobile navigation, same-page dismissal, shortcut
   guards, focus, route search, category/filter composition, view changes and
   no-extra-request behavior. Run existing catalog and affected workspace flows.
5. Inspect actual before/after images in both themes, English/Chinese, 320/375px,
   desktop, large text and reduced-motion. Run build and relevant state checks;
   document failures without hiding them, review, then commit and SSH-push.

## Review focus

- No private request is made merely by opening/filtering navigation.
- Another modal or composing input must not be hijacked by the shortcut.
- Escape clears a nonempty search first; a later Escape closes the dialog.
- Mobile navigation closes immediately, restores a sensible focus target, and
  does not survive a switch to desktop as a hidden modal.
- Category chips compose with the current draft; UTF-8 validation, cursor bounds,
  stale/error receipts and anonymous-only-public semantics remain intact.

The design-system lookup first matched documentation and then a generic P2P
marketplace, neither a suitable style authority for this tool. Keep existing
tokens instead. The verified UX keyboard/skip-link/sticky-navigation guidance and
React state guidance are applied only where they fit; no input delay is added for
a ten-route local filter. This is a source-only stage, not a release request.

## Outcome

Implemented and verified against the final production bundle. The public catalog
has 111 evidence entries; workspace navigation has 34 evidence entries and 29
layout records. The unchanged broad repository browser script fails at the same
snapshot-update action on the fixed baseline and this stage; it is not counted as
passing. Full scope, observed failures and screenshots are recorded in
`docs/CATALOG-NAVIGATION-VERIFICATION.md`. No Go/API/dependency changes or new tags.
