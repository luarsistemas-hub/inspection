# ADR-005: Customer Publication, Evidence, and Communication Policy

## Status

Accepted

## Date

2026-09-08

## Context

Customer portfolio, timeline, report, and evidence queries exist but are incomplete. Customer viewers must receive useful published results without exposure to internal notes, preliminary analysis, raw sensitive metadata, or unapproved evidence. Report publications can be invalidated, and the communication subsystem supports email, SMS, and WhatsApp.

## Decision

Make customer visibility policy-driven with a restrictive default. `CUSTOMER_VIEWER` can see only published reports and evidence explicitly shared by the tenant publication policy. Internal notes, preliminary results, and sensitive metadata remain excluded by default.

Invalidating a report publication immediately removes it from customer access, retains internal history, and notifies affected recipients. No previous publication becomes visible automatically; access remains unavailable until a new version is published.

Support email, SMS, and WhatsApp as complete communication channels. Only active, verified, and selected destinations may receive invitations or external notifications. Record outcome, retry state, and partial failure per channel. User preferences may disable optional external channels but may not suppress mandatory in-product notices.

## Alternatives Considered

### Alternative 1: Report-only customer access

- **Description**: Never expose individual evidence.
- **Pros**: Simplest privacy boundary.
- **Cons**: Prevents policy-approved evidentiary transparency.
- **Why rejected**: Configurable evidence sharing was selected.

### Alternative 2: Expose all evidence after completion

- **Description**: Completion automatically unlocks every evidence item.
- **Pros**: Maximum transparency.
- **Cons**: Exposes sensitive and non-published material.
- **Why rejected**: The chosen default is restrictive and publication-driven.

### Alternative 3: Fall back to an older report after invalidation

- **Description**: Show the last previously valid publication.
- **Pros**: Avoids an empty customer state.
- **Cons**: Can resurface stale or incorrect conclusions.
- **Why rejected**: Immediate withdrawal without fallback was selected.

### Alternative 4: Email-only communication

- **Description**: Complete only the email channel.
- **Pros**: Smaller provider surface.
- **Cons**: Does not satisfy the accepted multi-channel scope.
- **Why rejected**: Email, SMS, and WhatsApp were all selected.

## Consequences

### Positive

- Customer access has a clear publication boundary.
- Invalid information is withdrawn predictably.
- Communication failures are visible and recoverable by channel.
- The customer Dashboard can be completed against explicit privacy rules.

### Negative

- Publication policy must describe evidence-level visibility.
- Customers may temporarily have no report after invalidation.
- Three delivery providers and verification paths must be operated.

### Risks

- Customer projections may expose internal fields; mitigate with explicit allowlists and customer-safe view models.
- Notification retries may duplicate delivery; mitigate with idempotency and per-destination attempt records.

## Implementation Notes

- Customer downloads use authorized, time-limited access and obey the active publication policy.
- The customer UI must explain unavailable, invalidated, and not-yet-published states without leaking internal reasons.
- Channel preference forms must show human-readable verified destinations rather than raw IDs.

## References

- `services/inspection/internal/features/reports/customer`
- `services/inspection/internal/platform/notifications`
- `services/inspection/schema.graphqls`

