# @inspection/design-system

Product-neutral visual primitives for Inspection Admin, Dashboard, Capture, and Onboarding. Use the [common interaction guidance](../../docs/design-system/usage.md) for composition boundaries and responsive examples.

## Install

Consumers use the checked-out, compiled package through the existing local `file:` dependency. Before a clean app build, prepare the package from the repository root:

```sh
npm --prefix packages/inspection-design-system ci
npm --prefix packages/inspection-design-system run build
```

```tsx
import { Alert, Button, Container, ProductIdentity, Stack } from "@inspection/design-system";
import "@inspection/design-system/styles.css";
```

## Public exports

- `@inspection/design-system`: visual, form, choice, dialog, collection, navigation, step, feedback, recovery, and confirmation primitives, plus their public prop types.
- `@inspection/design-system/styles.css`: opt-in reset, brand, typography, color, spacing, focus, elevation, layout, and reduced-motion tokens.

No auth, GraphQL, routing, product pages, authorization decisions, or domain rules are exported.

## Accessibility and responsiveness

Controls have a 44 px minimum touch target, visible `:focus-visible` treatment, native keyboard behavior, and disabled semantics. Alerts and statuses pair text with icons, so state is never color-only. `Dialog` and `Combobox` encapsulate React Aria interactions; `Dialog` accepts `isDismissable={false}` while a request is pending. Motion is disabled under `prefers-reduced-motion: reduce`.

Containers, stacks, and inline groups use logical, fluid dimensions and wrapping to avoid horizontal overflow at 320, 360, 768, and 1440 px. Product applications own their page shells, navigation structure, authorization, routes, and data.

## Versioning and publishing

The local package boundary is verified by building, testing and inspecting the packed artifact:

```sh
npm ci
npm run lint
npm test
npm run build
npm run pack:check
```

The supported `prebuild` scripts and `scripts/dev.sh` prepare missing `dist/` automatically. Applications never import package source or React Aria directly. The package ships only `dist/` and package metadata.
