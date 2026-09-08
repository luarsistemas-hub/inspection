# Rodada de QA dos frontends — 2026-09-08

Ambiente: stack Docker local, tenant `Minha operação`, usuário Keycloak `admin`,
Admin em `:3000`, Dashboard em `:3002` e Capture em `:3003`.

## Resultado

| Produto | Fluxo | Resultado |
| --- | --- | --- |
| Admin | Login e gate de tenant | Passou |
| Admin | Organização / Consultar | Falhou com `internal error (INTERNAL)` |
| Admin | Acessos / Consultar | Falhou com `internal error (INTERNAL)` |
| Admin | Catálogos / Consultar | Passou vazio e com participante semeado |
| Admin | Ativos / Consultar | Passou vazio e com ativo semeado |
| Admin | Governança / Consultar | Passou com política padrão `MANUAL`, versão `0` |
| Admin | Auditoria / Consultar | Falhou com `internal error (INTERNAL)` |
| Dashboard | Login, resumo, filtro e triagem | Passou vazio e com três cenários semeados |
| Dashboard | Abrir inspeção | URL muda, mas nenhum detalhe é renderizado |
| Dashboard | Nova ação / Publicar relatório | Apenas mensagens instrutivas; nenhuma operação é executada |
| Dashboard | Notificações | Contador permanece zero e a rota mostra prioridades genéricas |
| Capture | Convite, OTP e consentimento | Passou |
| Capture | Upload de imagem | Upload chega a `SCREENED`, mas metadados falham por corrida assíncrona e a UI fica sem retomada |
| Capture | Impossibilidade | Justificativa é salva, mas o envio é bloqueado por não haver draft de mídia |
| Sessão | Expiração durante uso | UI exibe erro cru de parse JSON (`Unexpected token`) |
| Dependências | `npm audit --audit-level=high` nos três frontends | Falhou por vulnerabilidades conhecidas do `postcss` na árvore do Next.js 15.5.25 |
| Capture | Suíte Playwright | Não inicia: readiness aponta para `/`, rota inexistente que responde 404 |
| Smoke | PWA shell | Script procura `inspection-static-v1`, mas o SW publicado usa `inspection-capture-shell-v1` |
| Security smoke | Headers dos produtos | Admin expõe `X-Powered-By: Next.js`; script também assume raiz 2xx no Capture |

Os logs anexados originalmente continham inicialização e health checks, sem a
requisição GraphQL que falhou. A reprodução também mostrou que erros GraphQL
internos não são registrados com causa/correlation ID nos logs da API.

## Issues preparadas

- [001-admin-unused-graphql-variable.md](001-admin-unused-graphql-variable.md)
- [002-auth-response-json-parsing.md](002-auth-response-json-parsing.md)
- [003-dashboard-operational-placeholders.md](003-dashboard-operational-placeholders.md)
- [004-dashboard-notifications-unimplemented.md](004-dashboard-notifications-unimplemented.md)
- [005-capture-upload-race.md](005-capture-upload-race.md)
- [006-capture-impossibility-submit.md](006-capture-impossibility-submit.md)
- [007-graphql-internal-observability.md](007-graphql-internal-observability.md)
- [008-authenticated-e2e-coverage.md](008-authenticated-e2e-coverage.md)
- [009-frontends-postcss-security-audit.md](009-frontends-postcss-security-audit.md)
- [010-capture-playwright-readiness.md](010-capture-playwright-readiness.md)
- [011-smoke-service-worker-assertion.md](011-smoke-service-worker-assertion.md)
- [012-security-smoke-hardening.md](012-security-smoke-hardening.md)

Issues publicadas no GitHub: [#1](https://github.com/luarsistemas-hub/inspection/issues/1), [#2](https://github.com/luarsistemas-hub/inspection/issues/2), [#3](https://github.com/luarsistemas-hub/inspection/issues/3), [#4](https://github.com/luarsistemas-hub/inspection/issues/4), [#5](https://github.com/luarsistemas-hub/inspection/issues/5), [#6](https://github.com/luarsistemas-hub/inspection/issues/6), [#7](https://github.com/luarsistemas-hub/inspection/issues/7), [#8](https://github.com/luarsistemas-hub/inspection/issues/8), [#9](https://github.com/luarsistemas-hub/inspection/issues/9), [#10](https://github.com/luarsistemas-hub/inspection/issues/10), [#11](https://github.com/luarsistemas-hub/inspection/issues/11) e [#12](https://github.com/luarsistemas-hub/inspection/issues/12).

## Revalidação após as correções

Os problemas da tabela acima são o baseline da rodada; a revalidação foi executada em 2026-09-08 contra a stack Docker reconstruída.

| Escopo | Evidência | Resultado |
| --- | --- | --- |
| Admin autenticado | `INSPECTION_E2E_AUTH=true npm run test:e2e` | 14 testes passaram; Organização, Acessos e Auditoria consultaram a API sem erros GraphQL |
| Dashboard autenticado | `INSPECTION_E2E_AUTH=true npm run test:e2e` | 4 testes passaram; triagem, notificações semeadas e leitura passaram |
| Capture autenticado | `INSPECTION_E2E_AUTH=true npm run test:e2e` | 4 testes passaram em Android/WebKit; OTP Mailpit, consentimento, impossibilidade sem mídia e envio passaram |
| Seed | `./scripts/local.sh seed` executado duas vezes | Bootstrap local automático via Keycloak/API e cenários novos por execução |
| Smoke, segurança e carga | `smoke.sh`, `security-smoke.sh`, `load-smoke.sh` | Passaram; carga: 100 requisições, concorrência 20, p95 4 ms |
| Dependências | `npm audit --audit-level=high` nos três frontends | 0 vulnerabilidades reportadas |

As fixtures autenticadas observam `console.error`, `pageerror` e respostas GraphQL
com `errors`. O modo é opt-in porque depende da stack local, Keycloak e Mailpit:

```sh
INSPECTION_E2E_AUTH=true npm run test:e2e
```
