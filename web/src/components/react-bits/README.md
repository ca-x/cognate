# React Bits effects in Cognate

Reference: DavidHDev/react-bits, commit
`4dc7b5b7772e1217c8fb8d2a7aa074302180e5b4` (2026-11-06).

- `spotlight-card.tsx` / `effects.css`: a deliberately reduced adaptation of
  `src/ts-default/Components/SpotlightCard/SpotlightCard.tsx` and its CSS.
  Retains local pointer coordinates and a radial light layer. No ambient
  animation, window-level pointer listeners, RAF loop, flare, noise or canvas.
- `fade-content.tsx` / `effects.css`: CSS-only adaptation of the mount-fade
  and stagger idea from `src/ts-default/Animations/FadeContent/FadeContent.tsx`.
  Not the upstream GSAP/ScrollTrigger implementation or its full API.

Only the login story, public directory introduction and GitHub connection
surface use these treatments. The form, list results and high-frequency
search input do not animate. Existing React/Radix/TanStack components,
theme tokens and input-mode handling remain authoritative.

Motion is finite or event-driven. Reduced motion disables it immediately;
keyboard input cancels entrance motion and hides pointer lighting; touch and
coarse pointers get the static surface. Light layers are aria-hidden and never
capture input. No external assets, runtime dependencies or CDN are required.

The complete upstream license is `web/public/assets/react-bits-LICENSE.txt`:
MIT + Commons Clause, not an unrestricted MIT grant. It is copied into Vite
`dist/assets` and embedded into the server, available without authentication at
`/assets/react-bits-LICENSE.txt`. SHA256:
`4bdd1fa59a6b7d5954bc3b213dbc34e455c635a94bbcecf9293553797d0fad02`.
These adaptations ship only as part of Cognate, not as a separate component
library. The project root license does not replace this third-party notice.
