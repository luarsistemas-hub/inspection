---
status: completed
title: Present descriptive and comparison reports across audiences
type: frontend
complexity: high
---

# Task 7: Present descriptive and comparison reports across audiences

## Overview
Deliver the final report-reading journey in Dashboard and customer views, including all apparently sound components and explainable move-out states. This task closes cross-surface GraphQL contract generation and tests the user-visible copy against the pinned report and PDF.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST show catalog items and evidence even when classification is NORMAL and there are zero findings.
- 2. MUST label scope, coverage denominator, unreviewed AI, failure/no-consent, required-record gaps, and out-of-scope observations accurately.
- 3. MUST present comparison baseline/version, paired evidence, every outcome/filter count, and no-basis/uncertain states without blame or cost claims.
- 4. MUST keep customer fields within the backend allowlist and match PDF substantive content for the same audience/version.
- 5. MUST regenerate and verify all GraphQL clients/operation manifests after schema and query changes.
</requirements>

## Subtasks
- [x] 7.1 Replace false comparison/complete-coverage copy in current-state report view.
- [x] 7.2 Display grouped descriptive component rows, evidence, review/processing labels, and handover pending entries.
- [x] 7.3 Expose report version history and customer-safe reading states.
- [x] 7.4 Add comparison filters, paired evidence, accurate totals and no-basis explanations.
- [x] 7.5 Regenerate GraphQL contracts across apps and verify no stale generated diff.
- [x] 7.6 Run final Playwright journeys for analysis failure, sound catalog, versioning, baseline gate, and customer comparison.
- [x] 7.7 Add baseline selection and the "catalog and approve first" guidance to move-out creation, and show manager-visible refusals from scheduled creation.

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `apps/dashboard/src/features/dashboard/report-visual.tsx` — current hidden-evidence and false-summary behavior.
- `apps/dashboard/src/features/dashboard/reports-journey.tsx` — report navigation/version view.
- `apps/dashboard/src/features/dashboard/baselines-journey.tsx` — baseline and comparison navigation.
- `apps/dashboard/tests/unit/report-visual.test.ts` — existing component test pattern.

### Dependent Files
- `services/inspection/schema.graphqls` — typed v3 report fields.
- `services/inspection/internal/features/reports/customer/core/inventory.go` — customer projection contract.
- `scripts/generate-graphql-contracts.sh` — client and manifest regeneration.
- `apps/dashboard/src/graphql/generated.ts` — generated Dashboard types.

### Related ADRs
- [ADR-002](adrs/adr-002.md) — Binding decision for this slice.
- [ADR-004](adrs/adr-004.md) — Binding decision for this slice.
- [ADR-005](adrs/adr-005.md) — Binding decision for this slice.
- [ADR-008](adrs/adr-008.md) — Binding decision for this slice.
- [ADR-009](adrs/adr-009.md) — Binding decision for this slice.
- [ADR-010](adrs/adr-010.md) — Binding decision for this slice.

## Deliverables
- Completed present descriptive and comparison reports across audiences behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-100, UT-101, UT-102, UT-103, UT-104, UT-105, UT-106, UT-116 — unit/component rules.
- [x] IT-027 — integrated boundary behavior.
- [x] E2E-003, E2E-004, E2E-005, E2E-006, E2E-007, E2E-008 — end-to-end user journey.

## Verification report

### Resultado da rodada 6 — concluída

**Paridade contratual:** conferidos campo a campo os critérios de `_prd.md`, `_techspec.md`, `_tests.md` (UT-100…106, UT-116, IT-027, E2E-003…008), `_user_stories.md`, `task_06.md`, `task_07.md`, matriz de capacidades e ADRs 002/004/005/008/009/010 solicitados; também foram revisados os ADRs transversais 001/003/006/007 e o grafo `_tasks.md`. Os artefatos sustentam a projeção por audiência/allowlist, cobertura com denominador fixo, comparação separada, compatibilidade de snapshots antigos e o caminho v3. Não há diretórios `analysis/`, `handoffs/` ou `memory/` nesta feature.

#### Verificação final

- Dashboard (`apps/dashboard`): `npx tsc --noEmit` — **OK**; `npm run lint` — **OK**; `npm run test` — **OK**, Vitest reportou `Test Files 28 passed (28)` e `Tests 161 passed (161)`.
- Go: `go build ./...` — **OK**; `go vet ./...` — **OK**; `go test ./...` — **OK**. `gofmt -l` nos arquivos Go alterados não listou arquivos; `git diff --check` — **OK**.
- Integrações PostgreSQL, sequenciais, com `-tags=integration -p 1 -count=1 -timeout 40m` no banco descartável `inspection_cld_tmp`:

```text
ok   inspection/services/inspection/internal/features/reports/core                         0.434s
ok   inspection/services/inspection/internal/features/reports/customer/core                0.264s
ok   inspection/services/inspection/internal/features/reports/render_pdf                   3.396s
ok   inspection/services/inspection/internal/features/reports/handle_classified            2.415s
ok   inspection/services/inspection/internal/features/reports/generate_snapshot            8.993s
ok   inspection/services/inspection/internal/features/reports/list_reports                 1.264s
ok   inspection/services/inspection/internal/platform/graphql/resolvers                   415.851s
ok   inspection/services/inspection/internal/platform/graphqlcontract                      0.633s
```

- Codegen GraphQL foi executado duas vezes. Os hashes SHA-256 de `generated.go`, `models_gen.go`, `schema.resolvers.go`, `operation-manifest.json` e dos `schema.ts`/`generated.ts` dos quatro apps (admin, dashboard, capture e onboarding) foram idênticos entre execuções. Cada `npm run codegen` terminou com exit 0; `grep -c "not implemented" services/inspection/internal/platform/graphql/resolvers/schema.resolvers.go` retornou `0`. O teste `TestIT027AllFrontendManifestsAndGeneratedSchemasMatchCanonicalContract` também passou.

#### Playwright por arquivo e projeto

Com `cd apps/dashboard && set -a && . ./.env.example && set +a && npx playwright test` aplicado aos nove specs pedidos, Chromium, WebKit e Mobile, porta 3112 limpa e `INSPECTION_E2E_AUTH=false`, a execução terminou em `76 passed`, `38 skipped`, `0 failed` (`duration 93.014s`, sem flaky). Contagens `pass / skip / fail`:

| Spec | Chromium | WebKit | Mobile |
|---|---:|---:|---:|
| `authenticated-dashboard.spec.ts` | 0 / 3 / 0 | 0 / 3 / 0 | 0 / 3 / 0 |
| `baselines-journeys.spec.ts` | 10 / 0 / 0 | 10 / 0 / 0 | 10 / 0 / 0 |
| `baselines-review.spec.ts` | 2 / 1 / 0 | 2 / 1 / 0 | 3 / 0 / 0 |
| `create-operations.spec.ts` | 0 / 6 / 0 | 0 / 6 / 0 | 0 / 6 / 0 |
| `descriptive-report-v3.spec.ts` | 6 / 0 / 0 | 6 / 0 / 0 | 6 / 0 / 0 |
| `descriptive-scope.spec.ts` | 1 / 2 / 0 | 1 / 2 / 0 | 1 / 2 / 0 |
| `pending-review.spec.ts` | 3 / 0 / 0 | 3 / 0 / 0 | 3 / 0 / 0 |
| `recapture-cross-product.spec.ts` | 0 / 1 / 0 | 0 / 1 / 0 | 0 / 1 / 0 |
| `report-v2-triage.spec.ts` | 3 / 0 / 0 | 3 / 0 / 0 | 3 / 0 / 0 |
| **Total** | **25 / 13 / 0** | **25 / 13 / 0** | **26 / 12 / 0** |

Os 38 skips são cenários condicionados à autenticação/stack e seed local; `baselines-review` também contém um teste exclusivo para mobile. Portanto, os E2E dependentes de uma stack autenticada (`authenticated-dashboard`, `create-operations`, `recapture-cross-product` e cenários condicionais de `descriptive-scope`) não foram verificados nesta execução. Todos os E2E-003…008 passaram em cada projeto. Na primeira execução, os três E2E-003 e os três E2E-004 falharam por expectativas de fixture incompatíveis com o payload verdadeiro; as fixtures/assertions foram alinhadas ao formato real e passaram no rerun completo. Um caso não relacionado em `baselines-journeys.spec.ts` falhou transitoriamente no primeiro WebKit run e passou no rerun. Resultado final sem falhas.

#### Achados desta rodada e evidência

- **A1 — referência de evidência do cliente:** `CustomerEvidenceItem.reference` é calculado por `reportcore.EvidenceReference` após triagem do snapshot, sob a allowlist de projeção ao cliente. `CustomerChecklistView` associa mídia por essa referência; miniatura só é renderizada para item disponível. `TestTask07CustomerResolverUsesV3ProcessingAndTriagedLocalizedSnapshot` em `task07_customer_resolver_integration_test.go` comprova referência E001 e ausência de URL para evidência bloqueada. `E2E-005` em `descriptive-report-v3.spec.ts` usa o golden real do cliente, confirma a miniatura disponível e que não há requisição para a evidência retida/revogação; passou nos três browsers.
- **M1 — handover:** `report-visual.tsx` exibe apenas `payload.presentation` quando fornecida, sem rótulo/estado/declarador duplicados. `E2E-004` verifica a redação da apresentação do payload real (`Chaves · Pendente`) em Chromium, WebKit e Mobile.
- **M2 — histórico e correções:** ações de reemissão/pareamento só aparecem na versão mais recente; após mutação, o histórico refaz a consulta com polling assíncrono cancelável e limite. `UT-116` exercita `ReportVersionHistory` com labels vindos do backend; `E2E-006` modela a chegada assíncrona da versão 2 e verifica que a versão antiga não oferece ação de correção. Ambos passaram nos três projetos.
- **M3 — RBAC:** `reissueReport` exige autorização mutável para Manager/Employee, alinhada à matriz; Viewer e CustomerViewer recebem recusa segura em português. `TestIT060ReissueReportRefusesViewerAndAllowsEmployeeAuthorization` em `report_verification_test.go` cobre recusa e Employee; integração do resolver também passou no pacote `platform/graphql/resolvers`.
- **Consultas e isolamento:** `list_reports` agrega cobertura/revisão no SQL sem carregar a subárvore textual do checklist. `TestTask07ReportHistoryUsesRuntimeTenantRLSAndEmployeeScope` em `report_list_task07_integration_test.go` cobre RLS/tenant cruzado, escopo Employee e `inspectionId` inválido pelo resolver; `TestInspectionHistoryPostgresReturnsEveryVersionWithinTenant` cobre o histórico do adapter.
- **Vocabulário e estados:** removidos fallbacks de histórico divergentes, classificação/condição/processamento usam labels do backend, e os estados vazios de checklist/cômodo coincidem com a mensagem substantiva do PDF. Testes de componente foram atualizados; Vitest completo passou.
- **Contrato:** descrição de `Query.reports`, campo `CustomerEvidenceItem.reference` e linha de capacidades foram atualizados; contratos/manifests foram regenerados duas vezes sem diff stale. O caminho de verificação apresenta o digest retornado pelo backend em formato copiável; teste E2E do digest passou dentro de `descriptive-report-v3.spec.ts`.

#### Limites de verificação

- O Playwright foi executado com autenticação desabilitada; os 38 cenários listados como skips exigem autenticação/stack/seed e não foram exercitados. Não foram tratados como passes.
- O browser compara a interface com fixtures/golden compartilhado; a igualdade substantiva de HTML/PDF é verificada pelos testes Go que comparam HTML e texto extraído do PDF real (`TestTask07SharedComparisonGoldenMatchesHTMLAndExtractedCustomerPDF`).
- Não foram executados serviços autenticados de ponta a ponta nem validado o fluxo de cópia do digest contra um PDF aberto fora do teste de interface.
- Nenhum acesso ao banco `inspection`, alteração de senha, `docker compose down`, parity gate ou mutação em ambiente externo foi feito.

### Resultado da rodada 7 — fechamento

#### Achados corrigidos e evidência

- **Paginação de evidências do cliente:** `dashboard-shell.tsx` busca as páginas de `CustomerEvidence` até `hasNextPage=false`, com teto de 25 páginas (625 itens), estado de carregamento e mensagens seguras em português para falha ou teto atingido. `E2E-005` em `descriptive-report-v3.spec.ts` usa 27 itens no formato GraphQL realista, valida a miniatura da referência E026 na segunda página e confirma que E027, marcada WITHHELD e referenciada no checklist, não gera `<img>` nem requisição de mídia. Passou em Chromium, WebKit e Mobile.
- **Disponibilidade de mídia no checklist do cliente:** `report-visual.test.ts`, teste “never requests or renders customer checklist media unless availability is AVAILABLE”, cobre AVAILABLE, WITHHELD, UNAVAILABLE e REVOKED; só AVAILABLE gera imagem e URL. Remover a guarda por disponibilidade faz o teste falhar.
- **Polling do histórico:** `reports-journey.tsx` verifica o estado ativo após cada consulta e espera, evita atualizar detalhes depois do unmount e preserva `version=` da URL ao recarregar o laudo. As ações de reemissão e pareamento ficam desabilitadas enquanto uma versão está pendente. “report history polling stops updating details after the journey unmounts” e E2E-006 passaram nos três projetos; E2E-006 também mantém a versão 1 selecionada após a versão 2 chegar.
- **RBAC do pareamento:** `report_comparison_task05_test.go`, `TestUT075OnlyAuthorizedStaffCanReviseComparisonPairing`, fixa o produto Dashboard, confirma Viewer recusado com `Acesso negado.` e Employee autorizado até a validação de entrada.
- **Vocabulário e markup:** `report-visual.tsx` removeu o fallback de handover que mostrava “Registrado”; `report-visual.test.ts` verifica “Condição observada: Bom” no HTML renderizado.
- **Allowlist GraphQL:** `customer_schema_test.go`, `TestCustomerSchemaExposesOnlyAllowlistedFieldsIT220`, agora verifica os tipos CustomerChecklist, CustomerHandover e CustomerComparison, as allowlists exatas e a presença de `CustomerEvidenceItem.reference`; a denylist cobre IDs, URLs, `typeCode`, `key`, `outsideScope` e `attributeSources` nos tipos novos.

#### Verificação final desta rodada

- Dashboard: `npx tsc --noEmit` — **OK**; `npm run lint` — **OK**; `npm run test` — **OK**, Vitest: `Test Files 28 passed (28)`, `Tests 163 passed (163)`.
- Playwright, nove specs obrigatórios, Chromium/WebKit/Mobile, porta 3112 e `INSPECTION_E2E_AUTH=false`: **79 passed, 38 skipped, 0 failed**. Contagens por spec em formato `pass/skip/fail`:

| Spec | Chromium | WebKit | Mobile |
|---|---:|---:|---:|
| `authenticated-dashboard.spec.ts` | 0/3/0 | 0/3/0 | 0/3/0 |
| `baselines-journeys.spec.ts` | 10/0/0 | 10/0/0 | 10/0/0 |
| `baselines-review.spec.ts` | 2/1/0 | 2/1/0 | 3/0/0 |
| `create-operations.spec.ts` | 0/6/0 | 0/6/0 | 0/6/0 |
| `descriptive-report-v3.spec.ts` | 7/0/0 | 7/0/0 | 7/0/0 |
| `descriptive-scope.spec.ts` | 1/2/0 | 1/2/0 | 1/2/0 |
| `pending-review.spec.ts` | 3/0/0 | 3/0/0 | 3/0/0 |
| `recapture-cross-product.spec.ts` | 0/1/0 | 0/1/0 | 0/1/0 |
| `report-v2-triage.spec.ts` | 3/0/0 | 3/0/0 | 3/0/0 |
| **Total** | **26/13/0** | **26/13/0** | **27/12/0** |

- Go: `go build ./...` — **OK**; `go vet ./...` — **OK**; `go test ./...` — **OK**. Testes direcionados de RBAC e allowlist GraphQL — **OK**. `gofmt -l` nos dois arquivos Go alterados ficou vazio; `git diff --check` — **OK**.
- Integrações PostgreSQL sequenciais no banco descartável, com `-tags=integration -p 1 -count=1 -timeout 40m`:

```text
ok   inspection/services/inspection/internal/platform/graphql/resolvers                   416.172s
ok   inspection/services/inspection/internal/platform/graphqlcontract                        0.436s
ok   inspection/services/inspection/internal/features/reports/customer/core                 0.259s
ok   inspection/services/inspection/internal/features/reports/list_reports                  1.368s
```

- Schema e operações GraphQL não mudaram nesta rodada; não foi necessário executar codegen nem alterar a matriz de capacidades.

#### Não verificado

- Os 38 skips do Playwright dependem da stack e autenticação local com seed; foram contados como skips, nunca como passes. Não foi executada a jornada autenticada de ponta a ponta.
- Integrações de `reports/core` e `render_pdf` não foram repetidas porque nenhum arquivo desses pacotes foi alterado nesta rodada.
- Não foi executada geração GraphQL porque schema, operações e manifesto não foram alterados.
- Não houve acesso ao banco `inspection`, mudança de senha de role, `docker compose down`, parity gate ou escrita externa.

## Success Criteria
- Every assigned test case implemented and passing.
- The present descriptive and comparison reports across audiences behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
