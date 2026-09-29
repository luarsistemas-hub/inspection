# Layout responsivo — Inspection

Este guia registra a adaptação dos quatro frontends às classes de janela do Material 3. A composição responde à largura disponível; a mudança de classe não deve recriar a jornada nem descartar filtros ou rascunhos.

## Classes e regras

| Janela | Console | Conteúdo |
|---|---|---|
| Compacta, abaixo de 600 px | Barra inferior com quatro destinos e “Mais” | Uma coluna; coleções no formato de cartões/lista; formulários extensos em tela cheia |
| Média, 600–839 px | Trilho lateral com ícones e rótulos | Uma coluna útil |
| Expandida, 840–1199 px | Trilho lateral; “Mais” abre a lista de rotas secundárias | Duas colunas somente quando cada painel conservar largura útil |
| Grande, 1200 px ou mais | Navegação lateral persistente | Conteúdo usa a largura disponível com coleções e detalhes amplos |

O design system compartilha alvos de toque de 48 px, foco visível, cores semânticas e tipografia Literata/Source Sans 3. As barras inferiores consideram as safe areas. Tabelas de administração mantêm a tabela larga e oferecem uma lista semântica independente em janelas compactas; a apresentação inativa usa `display: none`, mantendo um único conjunto de controles acessível.

## Inventário de rotas

Rotas incluídas: 27, conforme os manifests de rotas dos quatro aplicativos. As rotas técnicas de página não encontrada foram excluídas.

| Aplicação | Rotas | Layout e estados cobertos |
|---|---|---|
| Admin (13) | `/`, `/overview`, `/organization`, `/access`, `/catalogs`, `/assets`, `/prompts`, `/governance`, `/audit`, `/llm-usage`, `/activate`, `/auth/callback`, `/tenants/[tenantId]` | Resumos e coleções; formulário; metadados de prompt com quebra segura; consumo de LLM; estados de identidade, permissão, erro e carregamento |
| Painel (10) | `/`, `/tenants/[tenantId]`, `/inspections`, `/schedules`, `/projects`, `/triage`, `/reports`, `/notifications`, `/portfolio`, `/auth/callback` | Início/contexto; lista, quadro e agenda; criação e edição; lista/detalhe de triagem; laudos e portfólio; notificações; estados de sessão |
| Capture (1) | `/capture/[linkToken]` | Acesso indisponível, OTP, consentimento, captura, revisão, envio e retomada do rascunho |
| Onboarding (3) | `/`, `/imobiliaria`, `/complete` | Identidade, OTP, etapas, fotos, revisão e acompanhamento da conclusão |

## Navegação

O Painel operacional usa Início, Agenda, Vistorias e Mais; Projetos, Triagem, Laudos e Notificações permanecem no menu secundário. O cliente vê Portfólio, Laudos e Notificações. O Admin destaca Visão geral, Organização e Acessos; as demais rotas administrativas autorizadas ficam agrupadas por finalidade no menu “Mais” e na navegação ampla. A capacidade e a autorização existentes continuam definindo quais destinos aparecem. O detalhe de uma vistoria substitui a lista no celular; o retorno preserva a busca e a visualização.

Capture e Onboarding não usam a navegação dos consoles. Capture apresenta os requisitos em uma lista vertical rolável no celular, sem depender de rolagem horizontal; o visualizador ampliado conserva o fechamento do diálogo no cabeçalho. Onboarding mostra a etapa atual em uma linha expansível e deixa a lista completa recolhida; antes do OTP, o progresso não ocupa a primeira tela. Triagem e Consumo de LLM recolhem filtros avançados, mantendo busca, período e ações principais disponíveis.

## Verificação e evidências

- `@inspection/design-system`: lint, 62 testes e build passaram.
- Painel, Admin, Capture e Onboarding: lint, testes unitários e builds de produção passaram.
- O E2E `E2E-064` verifica o menu autenticado nos tamanhos 412 × 968 e 320 × 568, separação/alvos de 48 px e ausência de overflow global. A execução local requer `INSPECTION_E2E_MOCKS=true` e o servidor com URL de retorno OAuth autorizada em `localhost:3002`.
- O E2E `E2E-066` verifica o detalhe móvel da vistoria, o retorno à lista com a busca preservada e a ausência de overflow em 412 px.
- O E2E `E2E-065` verifica menu e coleções do Admin em 320 px, os metadados de Prompts nos temas claro/escuro e a abertura do detalhe sem tabela duplicada.
- Safari/iOS e Chrome/Android físicos, VoiceOver/TalkBack, câmera, teclado virtual e PWA não estão disponíveis neste ambiente; esses casos continuam como validação manual de aceite.

## Referências

- [Material 3 — classes de janela](https://m3.material.io/foundations/layout/applying-layout/window-size-classes)
- [Material 3 — barra de navegação](https://m3.material.io/components/navigation-bar/overview)
- [Material 3 — trilho de navegação](https://m3.material.io/components/navigation-rail/overview)
- [Material 3 — contraste de cores](https://m3.material.io/foundations/designing/color-contrast)
