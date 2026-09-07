---
name: ui-design-system
description: Design and evolve accessible, consistent UI systems for web applications. Use when defining or applying design tokens, component variants, responsive behavior, visual hierarchy, accessibility, or developer handoff documentation.
metadata:
  short-description: Create consistent accessible UI systems
---

# UI Design System

Use this skill to turn a visual requirement into reusable, implementable UI
decisions. Inspect the existing product before creating tokens: preserve its
brand, naming, spacing scale, component API, CSS strategy, and accessibility
conventions unless the user requests a redesign.

## Workflow

1. Inventory existing tokens, themes, primitives, typography, breakpoints, and
   component states.
2. Define semantic tokens before component-specific values. Keep light/dark or
   high-contrast differences in theme layers rather than duplicating components.
3. Specify component anatomy, variants, interaction states, responsive rules,
   content constraints, and accessibility requirements.
4. Implement the smallest reusable primitive that serves the feature. Avoid
   inventing a design-system abstraction for a one-off layout.
5. Verify contrast, focus visibility, keyboard behavior, reduced motion,
   overflow, touch targets, and narrow/large viewport behavior.

## Token helper

`design_token_generator.py` can produce a starting token set from a brand color:

```sh
python3 .claude/skills/ui-design-system/scripts/design_token_generator.py --help
```

Generated tokens are scaffolding. Review color contrast and semantic meaning,
then adapt the output to the application's existing token format before
committing it. Do not overwrite existing design tokens automatically.

## Design rules

- Prefer semantic names such as `surface`, `text-muted`, `border-subtle`, and
  `action-primary` over names tied to a specific hue or component.
- Keep spacing, type, radius, elevation, motion, and breakpoint values
  systematic; document intentional exceptions.
- Every interactive component needs default, hover, focus-visible, pressed,
  disabled, loading, and error behavior where applicable.
- Use semantic HTML and preserve a logical reading and focus order.
- Do not communicate meaning by color alone; provide text, icons with labels,
  or other non-color cues.
- Treat copy length, localization, zoom, reduced motion, and high contrast as
  design inputs, not edge cases.

When implementing the design in React or Next.js, use `$senior-frontend` for
component architecture, state, and code-level validation.
