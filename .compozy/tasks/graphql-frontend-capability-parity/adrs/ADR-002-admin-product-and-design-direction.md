# ADR-002: Admin Product Boundary and Open Design Direction

## Status

Accepted

## Date

2026-09-08

## Context

The current Admin is a generic section-and-query shell and does not provide complete resource lifecycles. The user explicitly required Open Design to establish a mature market reference because the current Admin experience is visually and functionally poor. Admin and Dashboard serve different jobs and should not share one shell.

## Decision

Adopt the Open Design market reference as the mandatory Admin baseline.

Admin is a desktop-first B2B console for tenant configuration, identity, access, participation, inspection configuration, governance, and audit. Its canonical interaction model is:

`grouped navigation -> persistent tenant and scope -> searchable/filterable collection -> resource detail -> history/audit`

The navigation groups are:

1. Administrative overview.
2. Organization.
3. Identity and access.
4. Participation.
5. Inspection configuration.
6. Governance.

Use the “Evidence Trail” visual direction with the density of “Precise Operations.” Admin, Dashboard, and Capture share tokens, foundational controls, terminology, and accessibility standards, but retain distinct information architectures and interaction models.

Admin is fully supported on desktop and tablet. Mobile supports consultation and simple actions without requiring complex administrative workflows. Dashboard is responsive across desktop, tablet, and mobile. Capture remains mobile-first.

The interface ships in Brazilian Portuguese and is structured for future localization. Tenant timezone governs user-facing dates; UTC remains available in technical and audit details.

## Alternatives Considered

### Alternative 1: Reuse the Dashboard shell

- **Description**: Add administrative links and forms to the operational Dashboard shell.
- **Pros**: Less shell work.
- **Cons**: Mixes high-impact governance with high-frequency operations and recreates the current generic experience.
- **Why rejected**: Product responsibilities and user rhythms are materially different.

### Alternative 2: Treat Open Design as optional inspiration

- **Description**: Allow implementation teams to choose another information architecture.
- **Pros**: More implementation freedom.
- **Cons**: Reopens an explicitly resolved product decision and risks another weak generic Admin.
- **Why rejected**: The user selected the reference as mandatory.

### Alternative 3: Use the same visual architecture for all frontends

- **Description**: Apply one shell and navigation pattern to Admin, Dashboard, and Capture.
- **Pros**: Superficial visual consistency.
- **Cons**: Poor fit for operational queues and guided mobile capture.
- **Why rejected**: The products share a system, not the same experience.

## Consequences

### Positive

- Administrative scope, consequences, and history remain visible.
- Dense collections support efficient management without falling back to raw JSON.
- The design has a concrete, market-informed acceptance baseline.
- Shared primitives reduce inconsistency without erasing product boundaries.

### Negative

- Admin requires a substantial redesign rather than incremental styling.
- A shared design system must support multiple experience patterns.
- Mobile Admin capability is intentionally less comprehensive than desktop/tablet.

### Risks

- The Open Design reference could be reduced to colors and typography; mitigate by testing information architecture and interaction requirements, not screenshots alone.
- Dense tables may harm accessibility; mitigate with semantic tables, keyboard operation, visible focus, responsive record views, and 200% zoom checks.

## Implementation Notes

- Collections require search, filters, pagination, explicit empty/loading/error states, and contextual actions.
- Details require copyable IDs, status, scope, related objects, history, and optimistic concurrency conflict handling.
- Long forms and sensitive policies use full pages rather than complex modals.
- Do not use cards as the primary representation of administrative collections.

## References

- Open Design: `referencia-mercado-admin-inspection.md`
- [Carbon filtering pattern](https://carbondesignsystem.com/patterns/filtering/)
- [Carbon data table usage](https://carbondesignsystem.com/components/data-table/usage/)
- [Microsoft Entra admin center](https://learn.microsoft.com/en-us/entra/fundamentals/entra-admin-center)
- [GitHub organization audit log](https://docs.github.com/en/organizations/keeping-your-organization-secure/managing-security-settings-for-your-organization/reviewing-the-audit-log-for-your-organization)

