# Avaliação do layout: checklist e laudo descritivo

Data: 11/11/2026. Código avaliado: `main`, commit `5d0bbace`.

## Parecer

A entrega cobre dados e controles importantes da jornada, mas a apresentação do laudo e da comparação ainda está parcial em relação ao PRD. Foram identificadas **cinco pendências dentro do escopo: duas de alta prioridade e três de média**. As tasks concluídas e os testes existentes não demonstram a conclusão da composição visual.

Há também uma diferença de escopo entre o estudo/protótipo e o PRD: várias telas do protótipo foram explicitamente adiadas pelo ADR-001 e pelos Non-Goals do PRD. Elas precisam de outra entrega se continuarem desejadas; não são omissões desta implementação.

Esta avaliação é de apresentação e aderência aos requisitos de interface. Não é uma certificação de todas as regras do backend nem uma conferência da versão implantada em produção. Nenhum código funcional foi alterado.

## Referências e precedência

- [Estudo, especialmente §§4, 6, 9 e 12](../../../estudo-checklist-laudo-flashvistoria.md).
- [Protótipo e seus limites](../../../prototypes/checklist-laudo/README.md).
- [PRD](../../../../.compozy/tasks/checklist-laudo-descritivo/_prd.md), [TechSpec](../../../../.compozy/tasks/checklist-laudo-descritivo/_techspec.md), [histórias](../../../../.compozy/tasks/checklist-laudo-descritivo/_user_stories.md), [contrato de testes](../../../../.compozy/tasks/checklist-laudo-descritivo/_tests.md) e tasks 6/7.
- ADR-001 delimita o escopo; ADR-009 preserva emissão automática mesmo com lacunas. O bloqueio de publicação demonstrado no protótipo foi superado por essa decisão e não deve ser copiado para o produto.

O protótipo orienta a leitura visual; o PRD e as decisões posteriores definem a entrega. Não há requisito de reprodução pixel a pixel nem autorização para copiar textos de demonstração como conclusões de laudos reais.

## Pendências comprovadas

| Prioridade | Pendência | Evidência e consequência |
| --- | --- | --- |
| Alta | Comparação do cliente sem fotos pareadas | `report-visual.tsx:155–164`: pares são parágrafos, sem imagens ou links. No navegador, a seção apresentou **0 imagens e 0 links**, mesmo informando duas evidências disponíveis. US-012 AC-1 exige ver os dois lados junto ao resultado. |
| Alta | Galeria PDF pode apresentar pares diferentes dos registrados | `render_pdf/maroto.go:276–308` imprime os pares textuais; `addEvidenceRows:654–679` organiza fotos por papel e posição. No PDF real de QA, o sofá declara **E002/E007**, mas a galeria mostra **E002/E006** lado a lado. A ordem das evidências não pode substituir o pareamento semântico. |
| Média | PDF separa descrição e primeira evidência do ambiente | `maroto.go:191–228`: texto dos ambientes primeiro; galeria de evidências depois. A proteção de título/primeira foto da galeria não protege o início da seção descritiva. PRD F5 e US-007 EC-2 pedem esse conjunto legível. |
| Média | Ausência dos grupos editoriais | Tela interna/cliente, HTML e PDF percorrem componentes numa lista linear. A TechSpec pede grupos por tipo e a task 7.2 os declara entregues. Falta hierarquia por famílias, preservando os componentes e o denominador. |
| Média | Identificação e síntese inicial incompletas | Cabeçalhos não compõem finalidade, datas declarada/emissão e perfil aplicado; a síntese de ambientes, cobertura, gravidade e registros pendentes está dispersa ou ausente. O perfil existe no snapshot, mas isso não significa que apareça no laudo. |

Os cinco achados estão detalhados em [reviews-001](../../../../.compozy/tasks/checklist-laudo-descritivo/reviews-001/), nos arquivos `issue_001.md` a `issue_005.md`. São recomendações de correção; a implementação não foi modificada.

Recomendação da rodada: **Needs fixes before merge**. Como a feature já foi publicada, isso se traduz em priorizar uma correção dos dois achados de alta severidade e fechar a apresentação contratada. Não se recomenda tratar a entrega visual como concluída.

## O que foi entregue e deve ser preservado

- Componentes descritivos, limitações, extensão avaliada e revisão aparecem mesmo sem achados.
- Cobertura tem denominador e distingue avaliação completa, parcial e lacunas.
- Há estados de processamento e seções próprias para chaves/medidores.
- A comparação interna tem componentes para exibir pares; os seis resultados e seus filtros existem na comparação do cliente.
- O filtro “Alterado (1)” foi acionado no navegador: mostrou um resultado e manteve **1 de 6 itens**.
- Existe histórico de versões e referência estável das evidências.
- O PDF já tem mecanismos de legenda/foto, título de requisito/primeira imagem e índice/anexo de evidências. O problema é aplicá-los também à composição descritiva e aos pares v3.

Essas observações confirmam a existência da apresentação no código e nas fixtures; não certificam processamento, autorização ou imutabilidade com uma stack real nesta sessão.

## Leitura das oito telas do protótipo

| Tela | Situação nesta feature |
| --- | --- |
| Laudo descritivo | Incluído e parcialmente apresentado. Precisa fechar identificação, síntese, grupos e composição das evidências. |
| Comparação | Incluída, com estados/filtros; faltam pares visuais no cliente e no PDF v3. |
| Checklist do participante | Incluído no Capture. Código implementa estados por componente/vista, justificativas e registros complementares. O formulário é linear e monta todos os ambientes abaixo da captura (`page.tsx:431`); uma composição por ambiente ativo é uma melhoria de UX a avaliar, não uma exigência de copiar o shell do protótipo. Não houve reprodução autenticada do Capture nesta sessão. |
| Ajuda na captura | Orientações contextuais de recaptura existem. O PRD pede ajuda no ponto da ação, não uma nova central de ajuda ou necessariamente uma página idêntica ao protótipo. |
| Preferências da IA | Adiada: estilo, extensão do texto, formato e prévia genérica são Non-Goals. O perfil de bens está incluído e é outra configuração. |
| Modelo de laudo | Marca, layouts configuráveis, termos e prévia completa de PDF foram adiados. A legibilidade do layout descritivo continua obrigatória nesta entrega. |
| Início | A reformulação de onboarding, próximos passos e configuração guiada foi adiada. |
| Ficha do imóvel | O novo workspace de perfil/histórico foi adiado. Usar vínculos e inventários existentes não equivale a entregar essa nova tela. |

Os textos de 7–11 px e a ausência de modo escuro são limitações documentadas do próprio protótipo. A implementação deve seguir o design system, inclusive o mínimo de 12 px; copiar essas limitações não é critério de aceite.

## Evidências no navegador

MCP utilizado: `agent-browser`. A tentativa inicial em `http://localhost:3002` retornou `net::ERR_CONNECTION_REFUSED`: não havia stack local ativa.

Para observar a interface, foi iniciado somente o Dashboard local e um proxy descartável em `http://127.0.0.1:8766`, com respostas fictícias derivadas de `descriptive-report-v3.spec.ts` e dos goldens internos/cliente. O proxy substituiu autenticação e GraphQL exclusivamente nesse cenário local. Também permitiu `unsafe-eval` somente na resposta do proxy para executar o bundle de desenvolvimento Next; nenhum header do produto foi alterado. As imagens são marcadores fictícios, não fotos de imóveis. Portanto, estas capturas demonstram composição de componentes reais, e não dados ou funcionamento de produção.

| URL / fluxo | Resultado | Captura |
| --- | --- | --- |
| `http://127.0.0.1:8766/reports?inspectionId=inspection-catalog` · perfil fictício de gestor · abrir laudo | Histórico precede a abertura; cabeçalho compacto e checklist em lista de cartões. | [Abertura](laudo-interno-desktop.png), [checklist](checklist-interno-desktop.png) |
| Mesma URL · perfil fictício de cliente · “Abrir resultado publicado” · comparação | Referências disponíveis em texto; nenhuma foto/link na seção de comparação. Filtro conserva o total. | [Comparação do cliente](comparacao-cliente-desktop.png) |
| `http://127.0.0.1:8765/#laudo` | Protótipo apresenta síntese, identificação e hierarquia editorial que ajudam a entender a diferença visual. | [Protótipo do laudo](prototipo-laudo-desktop.png) |
| `http://127.0.0.1:8765/#comparacao` | Referência visual da apresentação dos pares. | [Protótipo da comparação](prototipo-comparacao-desktop.png) |

Viewport observado no Dashboard: **1280 × 577**. As screenshots do modal mostram a área rolável visível; não representam uma exportação de todo o conteúdo do modal. A inspeção visual desta sessão não cobre celulares físicos, Admin autenticado, Capture autenticado nem a versão implantada.

## Evidências do renderizador PDF real

Foram gerados documentos com fixtures existentes e imagens sintéticas, usando `MarotoRenderer` real através de um teste temporário via `go test -overlay`. O código funcional e os testes permanentes não foram modificados. A skill `pdf` orientou a renderização/inspeção das páginas; esses arquivos registram defeitos observados, não são modelos finais aprovados de laudo.

| Cenário | Arquivos | Observação |
| --- | --- | --- |
| Comparação, 3 páginas | [PDF](comparacao-renderer-real.pdf), [conclusões na página 1](comparacao-pagina-1.png), [galeria na página 2](comparacao-pagina-2.png), [texto extraído](comparacao-renderer-real.txt) | Pares canônicos preservados; somente a ordem dos registros atuais foi invertida. A conclusão do sofá diz E002/E007, enquanto a segunda linha da galeria mostra E002/E006. Demonstra que a galeria depende da posição e não do par registrado. |
| Descritivo longo, 4 páginas | [PDF](laudo-longo-renderer-real.pdf), [catálogo na página 1](laudo-longo-pagina-1.png), [fotos na página 2](laudo-longo-pagina-2.png) | 14 componentes e 12 fotos; título/descrição começam na primeira página e fotos apenas na segunda. |
| Descritivo curto, 2 páginas | [PDF](laudo-curto-renderer-real.pdf), [página 1](laudo-curto-pagina-1.png), [página 2](laudo-curto-pagina-2.png) | Fixture curta usada como apoio de inspeção. Não se conclui que todo laudo com uma foto terá duas páginas. |

Parte dos metadados herdados das fixtures é simplificada e serve apenas ao ensaio do layout; não constitui evidência de erro funcional nos dados de uma vistoria real.

## Por que os testes existentes não encerram o layout

O contrato E2E-008 pede evidências pareadas, mas `descriptive-report-v3.spec.ts:283` verifica o texto `comparisonGolden.pair`, sem exigir duas fotos ou navegação para elas. O teste pode passar com a lacuna da comparação do cliente.

A suíte unitária de apresentação foi executada nesta avaliação e passou: **24 testes**. Isso é evidência de compatibilidade com as assertions atuais, não de aderência visual ao PRD. O relatório de execução anterior na task 7 também registra cenários autenticados não executados e comparação baseada em fixtures; esses limites devem continuar explícitos.

Verificações desta sessão:

- `npm run lint` em `apps/dashboard`: exit 0.
- `npm run test -- tests/unit/report-visual.test.ts`: exit 0, `Test Files 1 passed (1)`, `Tests 24 passed (24)`.
- `make lint` na raiz: exit 2, `No rule to make target 'lint'`; o comando válido do Dashboard acima foi usado.
- Teste temporário com renderer real: exit 0, `PASS: TestAuditLayoutExport`, com PDFs curto/longo/comparação de 2/4/3 páginas. O overlay substitui o arquivo somente para a compilação do teste; não altera o teste permanente.
- Integridade da rodada: cinco frontmatters YAML válidos e consistentes, dois achados `high` e três `medium`; caminhos e linhas de origem conferidos. Os 22 links do relatório resolveram para arquivos/diretórios existentes antes desta anotação.
- Não foi executada uma nova suíte E2E completa nem o pipeline Go completo: esta avaliação não modifica código funcional nem afirma que a feature inteira passou pela validação.

[Log de verificação](verificacao.txt). O overlay e o proxy são temporários; os PDFs, imagens e textos extraídos ficam preservados neste diretório. Os servidores locais usados para a inspeção foram encerrados ao terminar.

## Sequência recomendada

1. Fechar identificação, síntese e grupos editoriais do laudo sobre o snapshot/projeções autorizadas.
2. Renderizar os pares no cliente e no PDF v3, com indisponibilidade por lado e contexto do item.
3. Compor PDF por ambiente: título, início da observação e primeira imagem; demais imagens em anexo legível.
4. Validar tela interna/cliente e PDFs curto/longo com fixtures coerentes, desktop e 390 px, incluindo inspeção visual da paginação. Os testes devem verificar imagens e associação ao item, além de texto e contagens.

## Resultado após a correção

Os cinco itens da rodada foram corrigidos. A comparação do cliente agora associa as duas colunas ao par registrado e identifica a indisponibilidade de cada lado. O PDF monta a galeria a partir dos pares canônicos, sem depender da ordem de recebimento. A primeira evidência do ambiente permanece junto ao título e à observação; os componentes em tela, HTML e PDF aparecem em grupos editoriais estáveis. A síntese distingue as datas e explicita perfil não informado.

As capturas finais do Dashboard foram feitas no app local por E2E com autenticação e GraphQL simulados, pois o callback OIDC da conta de QA local retornou erro. Não foram usados dados de produção; as fotos são fixtures sintéticas.

| Tela ou documento | Evidência final |
| --- | --- |
| Laudo interno e identificação/síntese | [Tela inicial](laudo-interno-final.png) |
| Checklist interno, grupos e evidências | [Checklist](checklist-interno-final.png) |
| Comparação do cliente, pares e indisponibilidade | [Laudo completo](comparacao-cliente-final.png) |
| PDF de comparação, conclusões e pares da galeria | [Página 1](comparacao-final-pagina-1.png), [página 2](comparacao-final-pagina-2.png), [PDF completo](comparacao-renderer-real.pdf) |
| PDF descritivo longo, título/observação/foto na mesma página | [Página 1](laudo-longo-final-pagina-1.png), [PDF completo](laudo-longo-renderer-real.pdf) |
| PDF descritivo curto | [Página 1](laudo-curto-final-pagina-1.png), [PDF completo](laudo-curto-renderer-real.pdf) |

Validação final: `go test ./...`, `go vet ./...`, `go build ./...`; `npx vitest run tests/unit/report-visual.test.ts`, `npx tsc --noEmit`, `npm run lint`; E2E-004/005/008 em Chromium desktop e mobile; e `TestAuditLayoutExport` via `go test -overlay` com o renderer real. Todos passaram. Os PDFs produzidos têm 2 páginas (curto), 4 (longo) e 5 (comparação).

O gate completo `./scripts/verify.sh` não avançou além da primeira etapa: `design-system migration error` aponta várias linhas de evidência de acessibilidade não testadas ou ausentes no inventário de `docs/design-system/`. Esse gate não executou as suítes; os comandos de verificação listados acima foram rodados separadamente para a área alterada.

Preferências da IA, marca/modelo configurável, nova home e ficha do imóvel devem entrar em outra definição de produto se ainda forem desejadas.
