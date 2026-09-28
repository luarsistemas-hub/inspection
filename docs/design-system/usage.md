# Inspection design-system usage

Use the shared package for recurring presentation and interaction mechanics.
Applications keep route structure, authorization, GraphQL calls, versions,
filters, cursors, media policy, upload persistence and domain labels.

## Common patterns

- Use `Field` with a visible label and place the actual input or select inside
  it. Server and field errors stay associated with that control.
- Use `Combobox` for searchable choices. Keep the visible label, option value,
  empty result and required error associated with the actual input.
- Use `Button` for actions and links for navigation. A pending button represents
  one request; the feature owns the in-flight guard and the mutation inputs.
- Use `Dialog` and `Confirmation` for consequential actions. Name the target,
  scope, consequence and required reason. Keep dismissal disabled while a
  committed request is pending.
- Use `Recovery` with an explicit `kind` for loading, empty, error, unavailable
  or denied states. Do not present an unknown or pending domain result as
  success.
- Use `DataTable`, `Pagination` and `Steps` for their semantic purpose. Keep
  records, ordering, cursors, permissions and stage transitions in the owning
  feature.
- Use `Status` and `Alert` with text as well as color. Repeated row status
  values are not live announcements; a feature may add one explicit live
  region for a meaningful asynchronous change.

## Responsive examples

Dialogs must remain usable with long Portuguese consequences at 320 CSS
pixels. Content scrolls inside the dialog and both cancel and confirm remain
reachable by keyboard. A table may switch to labelled cards when the domain
layout permits it; the table header and cell relationship remains available to
assistive technology when a table is retained. Test 320, 360, 768 and 1440 CSS
pixel widths, 200% text and 400% browser zoom.

Synthetic example:

```tsx
<Dialog isOpen title="Cancelar vistoria" onClose={close}>
  <Confirmation
    target="Apartamento 101"
    scope="Unidade Norte"
    consequence="A vistoria deixará de aparecer como trabalho ativo."
    confirmLabel="Cancelar vistoria"
    onCancel={close}
    onConfirm={cancelInspection}
  />
</Dialog>
```

The example contains no credentials, invitation token, signed URL or personal
evidence. Replace the target, scope and consequence with the feature's
authorized data at runtime.

## Domain boundaries and exceptions

Capture owns camera/gallery/location decisions, draft keys, multipart transfer,
media processing and finalization. Onboarding owns definition-driven steps,
server-confirmed progression and its page-local unsent `File` objects.
Dashboard owns list/board/agenda and report/media composition; Admin owns
tenant, access, governance and audit context. These are documented composition
exceptions, not alternate meanings for shared controls.

Dashboard's `confirmation-dialog.tsx` and `form-dialog.tsx` compose the shared
`Dialog` and keep feature state local. They do not import React Aria or replace
the shared modal mechanics. Native table, board, agenda and media markup may
remain when it expresses the domain structure.

See [migration-inventory.json](./migration-inventory.json) for every current
page and [accessibility-evidence.md](./accessibility-evidence.md) for the
execution record. The machine-readable [evidence contract](./accessibility-evidence.json)
keeps all 55 criteria present; it is unresolved until the listed manual
environments have been used.
