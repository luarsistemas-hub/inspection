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

`video-scope.json` handles `VIDEO_SCOPE` before the generic finding response. It
reads the frame IDs from each request and returns one deterministic, generic
observation per frame. The labels are synthetic QA data; they do not describe
the recording. A static response cannot satisfy scope validation because each
batch has its own frame IDs, and every requested frame must be accounted for.
The response includes `environmentCandidate` only when the request schema
requires it; captures with a fixed environment omit that field.

`video-match.json` answers `VIDEO_MATCH` with no proposed pairs, which keeps
comparative QA conservative instead of claiming that two views match. The
comparison workflow can then report missing comparable evidence.
