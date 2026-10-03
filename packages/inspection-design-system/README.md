# @inspection/design-system

Product-neutral visual primitives for Inspection Admin, Dashboard, Capture, and Onboarding. Use the [common interaction guidance](../../docs/design-system/usage.md) for composition boundaries and responsive examples.

## Install

Consumers use the checked-out, compiled package through the existing local `file:` dependency. Before a clean app build, prepare the package from the repository root:

```sh
node scripts/prepare-design-system.mjs
```

```tsx
import { Alert, Button, Container, PageHeader, ProductIdentity, Stack, ThemeProvider, ThemeScript, ThemeSelector } from "@inspection/design-system";
import "@inspection/design-system/styles.css";
```

Each app wraps its tree in `ThemeProvider` and runs `ThemeScript` in the document head before paint. `ThemeSelector` shows a moon in light mode and a sun in dark mode; activating it switches between the two. The theme follows the operating system by default until the user makes an explicit choice. `useTheme()` exposes the saved preference and resolved theme. The shared key is `inspection.theme`. System changes and same-origin storage events are followed automatically.

Use the semantic light and dark tokens instead of page-local color values. `brand` is for primary actions and selection, `on-brand` is its foreground, surface roles distinguish the canvas from cards and navigation, and success, warning and danger remain separate state roles. The branded page header retains cobalt and white text in both themes. Literata, Source Sans 3 and IBM Plex Mono are bundled with their font licenses.

## Public exports

- `@inspection/design-system`: visual, form, choice, dialog, collection, navigation, step, feedback, recovery, and confirmation primitives, plus their public prop types.
- `@inspection/design-system/styles.css`: opt-in reset, brand, typography, color, spacing, focus, elevation, layout, and reduced-motion tokens.

The package also exports `PageHeader`, `ThemeProvider`, `ThemeScript`, `ThemeSelector`, `useTheme` and `MobileNavigation`. Buttons keep `primary` and `secondary` and add `tonal`, `text` and `danger`; icons default to a fixed 20 px box and accept an explicit size.

No auth, GraphQL, routing, product pages, authorization decisions, or domain rules are exported.

## Accessibility and responsiveness

Controls have a 44 px minimum touch target, visible `:focus-visible` treatment, native keyboard behavior, and disabled semantics. Alerts and statuses pair text with icons, so state is never color-only. `Dialog` and `Combobox` encapsulate React Aria interactions; `Dialog` accepts `isDismissable={false}` while a request is pending. Motion is disabled under `prefers-reduced-motion: reduce`.

Containers, stacks, and inline groups use logical, fluid dimensions and wrapping to avoid horizontal overflow at 320, 360, 768, 1024, and 1440 px. Product applications own their page shells, navigation structure, authorization, routes, and data.

## Versioning and publishing

The local package boundary is verified by building, testing and inspecting the packed artifact:

```sh
npm ci
npm run lint
npm test
npm run build
npm run pack:check
```

The supported `prebuild` scripts and `scripts/dev.sh` compare a content signature and rebuild stale `dist/` output; concurrent frontend starts share a build lock. Applications never import package source or React Aria directly. The package ships compiled styles, fonts, font licenses and package metadata.
