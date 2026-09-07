---
name: senior-frontend
description: Build, review, and optimize React, Next.js, TypeScript, and Tailwind frontend features in an existing codebase. Use when frontend implementation, component design, accessibility, state management, performance, or UI review is part of the task.
metadata:
  short-description: Implement and review production frontend features
---

# Senior Frontend

Use this skill for decisions and implementation in frontend code. Work with the
project's existing conventions first; do not introduce a new framework, state
library, styling system, or package manager without a concrete need.

## Workflow

1. Inspect the relevant app, package manifest, routing, component patterns,
   styling tokens, and test setup before editing.
2. Reuse existing components and primitives. Keep changes local to the feature
   and follow the repository's architecture.
3. Treat accessibility, loading, empty, error, and responsive states as part
   of the feature rather than polish added later.
4. Prefer server-rendered or static output in Next.js where appropriate; add
   client components only for browser interactivity or client-only APIs.
5. Validate with the narrowest relevant checks, then run the project's lint,
   typecheck, tests, and build when the change warrants them.

Do not install dependencies, rewrite lockfiles, or create deployment
configuration unless the user explicitly asks for it. Do not claim a script or
check succeeded without running it.

## Included helpers

The scripts are optional accelerators, not substitutes for understanding the
application. Inspect `--help` and the script behavior before using them. Run
them from the repository root with paths relative to this skill:

```sh
python3 .claude/skills/senior-frontend/scripts/component_generator.py --help
python3 .claude/skills/senior-frontend/scripts/frontend_scaffolder.py --help
python3 .claude/skills/senior-frontend/scripts/bundle_analyzer.py <target-path> --help
```

Use a helper only when its output matches the repository's conventions. Review
and adapt generated output before keeping it.

## Technical guidance

- Keep components cohesive and props explicit; avoid premature abstractions.
- Use stable keys, semantic HTML, keyboard support, visible focus, and labels
  for interactive controls.
- Preserve useful state across loading and error transitions and avoid hiding
  failures behind empty UI.
- Measure bundle or rendering problems before optimizing; remove unnecessary
  client boundaries and dependencies before adding caching or memoization.
- Validate user-controlled data at the boundary and never expose secrets in
  client code, logs, or rendered markup.
- Match existing design tokens and responsive breakpoints. If the feature
  changes visual language, use `$ui-design-system` as a companion skill.

## References

Read only the relevant reference when the task needs deeper guidance:

- [React patterns](references/react_patterns.md) for component composition and
  state patterns.
- [Next.js optimization](references/nextjs_optimization_guide.md) for routing,
  rendering, and performance work.
- [Frontend best practices](references/frontend_best_practices.md) for broader
  quality, security, and maintainability concerns.
