# @inspection/design-system

Private, product-neutral visual primitives for Inspection Admin, Dashboard, and Capture.

## Install

Consumers install an exact published version from the private registry; they do not use a workspace link or import this repository's source:

```sh
npm install @inspection/design-system@0.1.0 --save-exact
```

The registry credential belongs only to dependency installation and must never be added to browser configuration, source, or images.

```tsx
import { Alert, Button, Container, ProductIdentity, Stack } from "@inspection/design-system";
import "@inspection/design-system/styles.css";
```

## Public exports

- `@inspection/design-system`: `Alert`, `Button`, `Card`, `Combobox`, `Container`, `Dialog`, `Field`, `Icon`, `Inline`, `Input`, `Motion`, `Navigation`, `ProductIdentity`, `Select`, `Stack`, `Status`, and `Textarea`, plus their public prop types.
- `@inspection/design-system/styles.css`: opt-in reset, brand, typography, color, spacing, focus, elevation, layout, and reduced-motion tokens.

No auth, GraphQL, routing, product pages, authorization decisions, or domain rules are exported.

## Accessibility and responsiveness

Controls have a 44 px minimum touch target, visible `:focus-visible` treatment, native keyboard behavior, and disabled semantics. Alerts and statuses pair text with icons, so state is never color-only. `Dialog` focuses itself when opened and closes on Escape. Motion is disabled under `prefers-reduced-motion: reduce`.

Containers, stacks, and inline groups use logical, fluid dimensions and wrapping to avoid horizontal overflow at 320, 360, 768, and 1440 px. Product applications own their page shells, navigation structure, authorization, routes, and data.

## Versioning and publishing

Releases follow semantic versioning and are immutable: patch releases fix compatible behavior, minor releases add backward-compatible exports, and major releases may change public contracts. Publish only a new version from a tagged commit to the private registry:

```sh
npm ci
npm run lint
npm test
npm run build
npm run pack:check
npm publish
```

`npm publish` is configured with restricted access; it does not publish credentials or application source because `files` permits only `dist/`.
