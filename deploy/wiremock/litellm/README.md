# LiteLLM WireMock fixtures

Inventory requests are routed by `metadata.comparison_mode` to the three fixtures
(priority 2, so the more specific priority-1 fixtures such as
`inventory-unknown-evidence.json` win when their body marker matches):

- `INVENTORY_CATALOG` returns a valid empty catalog.
- `INVENTORY_MATCH` returns no proposed evidence pairs.
- `INVENTORY_COMPARE` returns no model item claims; the executor fails closed for unpaired items.

The existing `failure.json` and `malformed.json` fixtures provide transport and
malformed response cases. `unknown-evidence.json` covers invented evidence IDs
for the legacy finding mode; inventory catalog output validation independently
discards evidence not present in its request.
