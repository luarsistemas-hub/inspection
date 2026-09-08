# [P1] Remover fingerprint do Admin e tornar o security smoke compatível com o Capture

## Evidência

`./scripts/security-smoke.sh` confirma corretamente `UNAUTHENTICATED` no
GraphQL, mas falha ao verificar os produtos. O Admin responde
`X-Powered-By: Next.js`, revelando tecnologia e contrariando a política do
próprio smoke. O Capture responde 404 na raiz (a aplicação só possui
`/capture/[linkToken]`), então `curl -fsSI "$product_url/"` também encerra o
script antes de verificar seus headers.

## Resultado esperado

Nenhum frontend deve expor `X-Powered-By`, e os smokes devem usar endpoints
existentes para validar headers sem transformar um 404 funcional em falha de
infraestrutura de teste.

## Critérios de aceite

- Remover `X-Powered-By` do Admin e manter a asserção no smoke.
- Selecionar uma rota estável existente para o Capture ou disponibilizar
  endpoint de health/readiness.
- `./scripts/security-smoke.sh` passa integralmente.
- A resposta GraphQL sem autenticação continua retornando `UNAUTHENTICATED`.
