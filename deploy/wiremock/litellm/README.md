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

`transcription.json` handles audio requests to `POST /v1/chat/completions` for
the dedicated aliases `gemini-2.5-flash-lite`, `gemini-3.5-flash-lite`, and
`gemini-2.0-flash-lite`. It also requires `metadata.comparison_mode` to be
`TRANSCRIPTION` and an `input_audio` item, so visual and catalog requests do not
match. The response contains the literal PT-BR transcript
`cinco pratos fundos brancos na cozinha`, token usage, and the
`X-LiteLLM-Response-Cost` header parsed by the adapter.

### Injetar respostas de QA em runtime

Os arquivos `fixtures/transcription-transient-failure.json.example`,
`fixtures/transcription-no-speech.json.example` e
`fixtures/transcription-missing-cost-header.json.example` são exemplos JSON
para testes. A extensão `.json.example` impede que WireMock os carregue no
startup. Cada exemplo tem `persistent: false`, prioridade 1, e pode ser
registrado durante o teste assim:

```sh
curl -sS -X POST http://localhost:18080/__admin/mappings \
  -H 'Content-Type: application/json' \
  --data-binary @deploy/wiremock/litellm/fixtures/transcription-transient-failure.json.example
```

Para simular uma resposta válida sem fala, use
`@deploy/wiremock/litellm/fixtures/transcription-no-speech.json.example`. Ela devolve
`{"transcript":"","noSpeech":true}`. Para testar a contabilização quando o
gateway não reporta custo, use
`@deploy/wiremock/litellm/fixtures/transcription-missing-cost-header.json.example`; a
resposta segue válida, mas não contém `X-LiteLLM-Response-Cost`. Os três
fixtures casam somente com os aliases de transcrição e requests que contêm
áudio. Após o teste, remova um mapping temporário com
`DELETE /__admin/mappings/{id}`, usando o `id` retornado ao criá-lo.
