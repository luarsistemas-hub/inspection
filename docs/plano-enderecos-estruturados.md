# Plano de endereços estruturados no Inspection

Data: 29/09/2026. Estado: proposta de implementação, baseada no código atual. Nenhuma alteração funcional ou migration foi executada nesta investigação.

## 1. Resultado proposto

Substituir o endereço livre por um cadastro estruturado, reutilizado no onboarding e no Admin, com consulta de CEP, preenchimento manual e validação no servidor. Persistir os componentes do endereço e gerar uma representação textual consistente para listagens, convites, notificações e laudos.

Premissa de produto: Brasil na primeira versão, com `countryCode` explícito e contrato preparado para evolução internacional. Essa premissa foi apresentada para confirmação; suporte internacional completo não está incluído nesta primeira entrega.

O benefício imediato é obter dados que possam ser corrigidos e pesquisados por CEP, cidade, UF e logradouro, reduzir digitação e manter coerência entre cadastro e operação. Geocodificação e mapas são uma evolução separada: cadastrar um endereço completo não comprova que o imóvel existe nem determina coordenadas precisas.

## 2. Diagnóstico e abrangência real

Os caminhos abaixo são relativos à raiz `/Users/Payface/Documents/elvio/projetos/GoLang/inspection`. O inventário distingue captura de dados, persistência e consumo.

| Área | Evidência no código atual | Mudança planejada |
| --- | --- | --- |
| Onboarding, etapa Imóvel | `onboarding/real_estate_catalog/catalog.go` define `address` como `textarea`; `apps/onboarding/src/features/onboarding/onboarding-journey.tsx` renderiza campos dinâmicos | Usar o formulário composto e persistir objeto tipado no checkpoint |
| Validação do onboarding | `onboarding/coordinator/core.go:ValidateProperty` exige apenas texto preenchido e limite de tamanho | Validar o mesmo contrato do cadastro de imóveis |
| Conclusão do onboarding | `onboarding/complete/core.go` usa a string como endereço e como origem do nome do imóvel | Enviar componentes e derivar nome curto sem perder o endereço completo |
| Admin, `/assets` | `apps/admin/src/features/admin/admin-shell.tsx` envia `address` em `registerAsset` | Formulário compartilhado, endereço visível na listagem e ação de completar/corrigir endereço |
| Atualização de imóvel | `assets/update_asset/setup.go` existe; a interface administrativa examinada oferece cadastro, mas não editor de endereço | Expor correção pelo Admin, com controle de versão e preservação dos outros dados |
| API GraphQL | `services/inspection/schema.graphqls`: `Asset.address` e `AssetInput.address` são `String!` | Evolução aditiva com `addressDetails`; conservar saída textual compatível |
| Domínio de imóveis | `services/inspection/internal/features/assets/core/core.go` concentra registro, atualização, comparação de mudanças e busca | Normalização, validação, persistência atômica e comparação do objeto |
| Banco | `services/inspection/internal/platform/database/models.go:Asset` possui `address` de até 2.000 caracteres, coordenadas E6 e raio | Colunas tipadas na tabela atual, estado de preenchimento e texto legado preservado |
| Busca administrativa | `assets/core/core.go:List` pesquisa apenas `name` | Incluir endereço formatado e CEP normalizado; filtros explícitos por cidade/UF/CEP |
| Dashboard e triagem | `dashboard/operations.graphql`, `triage-journey.tsx` e `platform/graphql/resolvers/triage_review.go` | Manter texto consistente, cobrir busca e adicionar filtros geográficos onde úteis |
| Relatórios e PDFs | `reports/core/core.go`, `reports/list_reports/setup.go`, `cmd/inspection-worker/main.go` | Incluir componentes em novos snapshots e conservar leitura dos antigos |
| Notificações | Convites, lembretes, notificações internas e resolvers utilizam `Asset.Address` | Consumir projeção textual gerada a partir dos componentes |
| Capture | Não encontrei formulário de endereço; usa coordenadas congeladas na política da inspeção | Regenerar tipos e verificar ausência de regressão em GPS/geofence |
| QA, exemplos e builds | `development/seed_qa`, harness, testes Go, Vitest, Playwright e quatro apps com codegen | Atualizar dados novos; conservar cenários legados explícitos; incluir pacote compartilhado no build |

Os diretórios abreviados de features na tabela ficam em `services/inspection/internal/features/`.

Não foram encontrados cadastros postais próprios de participantes, usuários, tenants ou unidades de negócio. A etapa Imobiliária do onboarding recebe nome. Portanto, o plano padroniza todos os cadastros postais existentes e prepara reutilização futura, sem inventar novas exigências de endereço para essas entidades. Referências a endereço de e-mail, IP, SMTP e URLs têm outro significado e permanecem fora deste contrato.

Artefatos em `internal/` na raiz são legados; o contrato canônico continua sendo o schema do serviço. A imagem fornecida foi usada como referência visual, e a investigação foi estática, sem reprodução de UI em navegador ou acesso ao banco de um ambiente.

## 3. Padrão de cadastro e regras

A decomposição segue os elementos de endereçamento descritos pelos [Correios](https://www.correios.com.br/enviar/precisa-de-ajuda/guia-de-enderecamento/guia-de-enderecamento). As obrigatoriedades abaixo são decisões propostas para o produto, não uma alegação de que existe um formulário universal obrigatório.

| Campo de API | Rótulo | Regra proposta para cadastro completo |
| --- | --- | --- |
| `countryCode` | País | `BR` nesta versão; duas letras; não presumir país dos dados legados |
| `postalCode` | CEP | Obrigatório, string de oito dígitos; preservar zeros iniciais; exibir `00000-000` |
| `street` | Logradouro | Obrigatório, até 200 caracteres; inclui tipo e nome, como Rua, Avenida ou Estrada |
| `number` | Número | Texto de até 30 caracteres, permitindo `123A`, lote ou km; obrigatório quando não marcado sem número |
| `withoutNumber` | Sem número | Booleano; incompatível com número preenchido; não armazenar `0` ou `S/N` como número fictício |
| `complement` | Complemento | Opcional, até 200 caracteres; apartamento, bloco, casa ou sala |
| `district` | Bairro / distrito | Até 120 caracteres; solicitar quando aplicável, permitir ausência para endereços rurais ou sem bairro |
| `city` | Cidade / município | Obrigatório, até 120 caracteres; preservar acentos |
| `state` | UF | Obrigatório; uma das 27 UFs brasileiras |
| `municipalityCode` | Código IBGE | Opcional, sete dígitos quando informado; obtido do lookup, não exigido do usuário |
| `reference` | Ponto de referência | Opcional, até 300 caracteres; informação operacional separada do endereço postal |

Normalização no servidor: remover espaços nas extremidades, normalizar espaços repetidos, retirar apenas a máscara permitida do CEP e converter país/UF para maiúsculas. Não transformar todo o endereço em maiúsculas nem remover acentos do dado persistido. Entradas com letras no CEP devem falhar, não ser silenciosamente consertadas.

Para rascunhos administrativos, admitir campos ausentes com estado `INCOMPLETE`; validar formato dos campos presentes. O onboarding só confirma a etapa Imóvel com endereço completo. Um novo imóvel só pode ficar `ACTIVE` quando satisfizer as regras de endereço e as demais invariantes existentes; `ARCHIVED` mantém sua semântica atual. Imóveis legados ativos não devem ser desativados em massa.

Um CEP não encontrado no provedor não impede o cadastro manual com formato válido. Para endereço rural, aceitar logradouro do tipo estrada/localidade e sem número; usar o CEP aplicável ao local ou município. Quem não souber o CEP pode consultar a busca oficial ou manter rascunho administrativo; não preencher CEP fictício. A primeira versão não contempla imóvel sem qualquer CEP conhecido como cadastro novo completo.

O código IBGE deve ser limpo quando cidade/UF forem alteradas manualmente, salvo se novamente confirmado pelo lookup. Metadados de origem do provedor nunca significam endereço verificado. Atualizações relevantes de localização devem invalidar coordenadas antigas, conforme a seção de geolocalização.

## 4. Experiência de preenchimento

Fluxo comum a Admin e onboarding:

1. Mostrar CEP primeiro, com máscara e teclado numérico. Consultar após oito dígitos válidos, com debounce curto, ou por ação explícita de buscar.
2. Apresentar estado de consulta e preencher os dados retornados: logradouro, bairro, cidade, UF e código IBGE.
3. Solicitar número ou sem número; permitir complemento e ponto de referência.
4. Manter todos os campos editáveis e oferecer preenchimento manual em indisponibilidade ou CEP não encontrado.
5. Apresentar resumo formatado antes de salvar; erros junto ao campo e foco no primeiro erro.

Layout sugerido: CEP; logradouro; número/sem número; complemento; bairro; cidade/UF; referência opcional. Uma coluna em telas pequenas, agrupamentos em telas maiores. O país pode permanecer fixo com rótulo Brasil nesta versão.

Cancelar ou ignorar respostas de consultas antigas. Uma resposta tardia não pode substituir um CEP mais recente, campos editados ou um formulário já fechado. Ao mudar CEP, limpar apenas os valores ainda associados ao preenchimento automático anterior; sinalizar conflitos com campos manuais em vez de sobrescrevê-los silenciosamente. Nunca preencher apartamento/casa com o campo `complemento` do ViaCEP, que pode descrever um trecho postal, como lado par/ímpar.

Usar `fieldset`/`legend`, labels, `aria-describedby`, feedback de carregamento acessível e atributos de autocomplete adequados. Preservar foco visível, temas, navegação por teclado e alvos de toque do design system. O número é texto, mesmo se a maioria dos valores for numérica.

A consulta só ajuda a digitar: salvar e avançar continuam dependendo da confirmação do servidor. Falha de lookup não bloqueia o envio manual. A revisão do onboarding deve formatar o objeto; não pode omiti-lo por filtrar apenas strings ou exibir `[object Object]`.

## 5. Modelo de dados: estrutura no banco, pertencente ao imóvel

Recomendação: adicionar colunas tipadas em `assets.assets`. O código atual possui um endereço por imóvel e não demonstra necessidade de endereços compartilhados ou múltiplos endereços por entidade. Uma tabela global `addresses` com proprietário polimórfico adicionaria relacionamentos e riscos de isolamento sem benefício concreto agora.

Adicionar `address_country_code`, `address_postal_code`, `address_street`, `address_number`, `address_without_number`, `address_complement`, `address_district`, `address_city`, `address_state`, `address_municipality_code` e `address_reference`. Campos desconhecidos devem admitir `NULL` durante migração e rascunho. No objeto completo, `withoutNumber` é sempre booleano definido.

Adicionar também:

- `address_status`: `LEGACY`, `INCOMPLETE` ou `COMPLETE`; indica preenchimento, nunca verificação de existência.
- `legacy_address`: cópia preservada do texto anterior, para revisão e rastreabilidade da conversão.
- Manter `address` com o nome atual: projeção textual gerada pelo servidor, atualizada na mesma transação dos componentes. Para registros legados, continua contendo o texto existente.

Exemplo de projeção: `Rua das Flores, 123A, Apto 402 — Centro, Florianópolis/SC — CEP 88000-000`. É um exemplo de formatação, não uma afirmação de correspondência postal. Usar `s/n` na projeção quando necessário; omitir campos opcionais ausentes. Referência operacional fica fora de convites e títulos por padrão.

Componentes são a fonte de verdade para registros estruturados. Não permitir edição independente da projeção. Não usar `attributes` ou JSON de provedor como armazenamento canônico do endereço: os componentes precisam de tipos, constraints e filtros estáveis. JSON continua adequado para checkpoints e snapshots imutáveis.

Constraints devem ser condicionais: formato/limite dos campos presentes; completude quando `address_status = 'COMPLETE'`; número ou sem número mutuamente exclusivos. Formular os checks com `IS NOT NULL` quando necessário, pois um `CHECK` que resulta em `NULL` não rejeita a linha. Não aplicar `NOT NULL` global antes de tratar os registros antigos.

Criar índices compostos por tenant para os filtros entregues, por exemplo `(tenant_id, address_postal_code)` e `(tenant_id, address_state, address_city)`, além de acesso a pendências se o volume justificar. Busca por substring exige avaliação própria; um B-tree não otimiza automaticamente `ILIKE '%texto%'`. Não criar unicidade por CEP, texto ou coordenadas: unidades diferentes podem compartilhar esses valores.

Manter RLS e escopo de unidade existentes. Atualizações de componentes, projeção, status e `version` devem ser atômicas. Acrescentar auditoria de quem alterou e quais campos mudaram, sem copiar endereço completo para logs operacionais.

Se surgir um segundo proprietário real de endereço ou múltiplos endereços por imóvel, reavaliar tabela própria com vínculo explícito e isolamento por tenant. O contrato de campos pode ser reaproveitado sem antecipar essa generalização.

## 6. Backend e contrato GraphQL

Criar um objeto de valor puro em `services/inspection/internal/features/assets/address/` com normalização, validação, igualdade e formatação. Ele representa regras reutilizadas pelo cadastro e pelo onboarding; não é um slice com rota nem requer `Setup`. Evitar novo serviço global de repositories/controllers e não extrair para `libs/` enquanto só este serviço consumir a regra.

Aplicar esse objeto em `assets/core`, mantendo os entrypoints `register_asset`, `update_asset`, `get_asset` e `list_assets`. Testar o fluxo pelo mediator e pelos `Setup` existentes. Atualizar comparação de no-op para incluir todos os componentes, inclusive referência, e não apenas o texto formatado.

Contrato proposto:

```graphql
# Esboço aditivo; manter os demais campos atuais.
enum AddressStatus { LEGACY INCOMPLETE COMPLETE }

type PostalAddress {
  countryCode: String
  postalCode: String
  street: String
  number: String
  withoutNumber: Boolean
  complement: String
  district: String
  city: String
  state: String
  municipalityCode: String
  reference: String
}

input PostalAddressInput {
  countryCode: String
  postalCode: String
  street: String
  number: String
  withoutNumber: Boolean
  complement: String
  district: String
  city: String
  state: String
  municipalityCode: String
  reference: String
}

# Em Asset: conservar address: String!, adicionar:
# addressDetails: PostalAddress
# addressStatus: AddressStatus!
# Em AssetInput: adicionar addressDetails: PostalAddressInput
# e tornar a entrada legada address: String opcional na transição.
```

Campos internos opcionais permitem representar rascunhos; a completude é uma regra de negócio aplicada pelo servidor. O usuário não envia `addressStatus`, texto formatado ou alegações de validação por provedor.

Regras de compatibilidade:

- Novos clientes enviam somente `addressDetails`. Se as duas representações forem recebidas, exigir consistência após formatação ou retornar erro de entrada; não escolher silenciosamente uma delas.
- Durante a janela de compatibilidade, texto livre ainda pode criar/manter um registro `LEGACY` por cliente antigo autorizado pelo rollout.
- Em um imóvel já estruturado, um cliente antigo que reenvia o mesmo texto preserva componentes. Se tentar mudar só o texto, devolver erro de endereço estruturado obrigatório; jamais descartar os componentes.
- Em atualização, endereço omitido preserva o existente. Especificar e testar a distinção entre omissão e limpeza; não permitir apagar endereço de imóvel ativo por `null`. Um `addressDetails` presente substitui o conjunto completo de campos, com opcionais ausentes limpos.
- Após a migração dos clientes e expiração das sessões antigas, bloquear novos cadastros somente com texto. Conservar a saída `address` para leitores e histórico; remover a entrada legada apenas em mudança de contrato planejada.

Mapear erros para campos como `addressDetails.postalCode`, usando os códigos de `userErrors` existentes e mensagens de produto. Falhas internas continuam genéricas. Propagar `context.Context` até o banco e o lookup.

Adicionar filtros opcionais de CEP/UF/cidade às operações de listagem que compõem o escopo, mantendo paginação e autorização. A busca administrativa deve passar a encontrar endereço; a triagem já busca `a.address`. Relatórios devem pesquisar endereço do snapshot, sem trocar para o cadastro atual.

## 7. Consulta de CEP como slice independente

Criar `features/addresses/lookup_postal_code/`, com `setup.go` como entrada via mediator, input/output, política de acesso, interface pequena de provedor, adapter HTTP e testes próximos da operação. Registrar no composition root da API e projetar uma query GraphQL, por exemplo `lookupPostalCode(postalCode: String!, countryCode: String = "BR")`.

Retornar resultado tipado: `FOUND`, `NOT_FOUND` ou `UNAVAILABLE`, componentes conhecidos e `userErrors` para entrada inválida. A consulta não deve criar imóvel nem escrever checkpoints.

Provedor inicial sugerido: [ViaCEP](https://viacep.com.br/), substituível por adapter. Sua documentação exige oito dígitos, diferencia formato inválido de CEP inexistente e alerta contra validação massiva de bases. Usá-lo para consultas interativas; não executar backfill em massa por esse serviço.

Configuração proposta: timeout total curto, inicialmente 3 segundos; cache de sucesso com TTL, inicialmente 24 horas, e cache negativo curto, inicialmente 5 minutos; deduplicação de consultas concorrentes. Reaproveitar a infraestrutura de cache disponível quando apropriado. Não colocar chamadas externas dentro da transação de salvar endereço.

Permitir consulta com usuário interno autenticado ou sessão válida e verificada de onboarding, inclusive antes de existir tenant. Não abrir um proxy anônimo irrestrito. Encaixar o campo nas regras GraphQL de autenticação pública/onboarding e aplicar limite por usuário/sessão/IP confiável. Como ponto de partida, 30 consultas por minuto por usuário/sessão, com proteção agregada por IP; ajustar com métricas.

O cache de CEP contém apenas resposta postal pública, chaveada por país/CEP. Não armazenar número, complemento ou referência do usuário nesse cache. Logs e métricas devem registrar latência, provedor, resultado e volume, sem endereço completo ou tokens.

O adapter valida schema/tamanho da resposta e trata timeout, JSON inválido, `erro` como booleano ou string, dados parciais e cancelamento. Falha do cache também deve permitir preenchimento manual. O cliente nunca pode escolher uma URL arbitrária para consulta.

## 8. Reuso no frontend e integração com o monorepo

Criar `packages/inspection-address`, consumido por `file:` em Admin e onboarding, com um componente controlado `AddressForm`, resumo `AddressSummary`, tipos e validação de UX. A duplicação já existe em dois produtos, portanto há reutilização concreta.

Compor `Field`, `Input`, `Select`, feedback e layouts de `@inspection/design-system`. O design system declara que não exporta regras de domínio, GraphQL ou autenticação; mantê-lo neutro. O pacote de endereço recebe o callback de lookup e não conhece sessão, cookies, tenant ou cliente GraphQL.

As aplicações adaptam resultados e erros do GraphQL ao componente. Compartilhar casos de contrato entre Go e TypeScript para evitar regras divergentes; o backend permanece autoridade final. Não gerar regras de domínio por cópia manual dos artefatos GraphQL.

Além do código de interface, incluir manifest, lockfile gerado, build, testes e exports do pacote. Preparar a dependência antes dos consumidores em `scripts/dev.sh`, nos `prebuild`, em `scripts/verify.sh` e em `.github/workflows/ci.yml`. Ajustar Dockerfiles de Admin e onboarding, cópia do pacote compilado no runtime e instalação a partir de checkout limpo. Seguir o mecanismo local já adotado pelo design system sem transformar esta tarefa em refatoração geral de build.

Admin: consultar dados completos antes de editar, preservar template, atributos, assignments, coordenadas e políticas; usar `expectedVersion` e tratar conflito. A mutation existente recebe um input amplo, então não preencher campos desconhecidos com `{}` ou `[]`. Acrescentar ação “Completar endereço” para `LEGACY`/`INCOMPLETE`, indicação de pendência e filtros para localizar esses imóveis.

## 9. Onboarding: objeto, retomada e versionamento

O renderer atual aceita strings/string arrays; a revisão só exibe strings. O catálogo atual tem `schemaVersion = 1` e `version = 4`, e a sessão no banco não fixa versão do catálogo. O mapper devolve a definição global vigente. Logo, trocar apenas o `textarea` quebraria retomada e validação.

Planejar estes ajustes conjuntamente:

1. Introduzir campo composto `type: "address"`, com chave `addressDetails`. Incrementar o schema da definição para 2 e a versão de conteúdo seguinte, preservando o contrato 1 para sessões antigas.
2. Fixar schema/versão da definição na criação da sessão. Migrar sessões existentes para o contrato anterior e resolver `session.definition` por essa versão. Revisar também funções SQL que criam sessões para preencher os campos corretamente.
3. Entregar o frontend capaz de ler versões 1 e 2 antes de ativar o novo contrato. Usar a definição da sessão ao retomar; a query pública serve à jornada sem sessão. Navegadores antigos devem receber indicação para recarregar diante de schema não suportado.
4. Tipar `StepValues` para incluir objeto de endereço e tratar payload JSON como entrada não confiável. Validar tipos, campos permitidos e limite total antes de validar cada componente.
5. Atualizar `DynamicField`, `validateStep`, mapa de erros, revisão, gravação/leitura em `sessionStorage` e retorno de `completedSteps`. Versionar o envelope do rascunho; reconhecer formato antigo sem apagar os valores digitados.
6. Na retomada, usar o checkpoint confirmado como base e sobrepor apenas um rascunho compatível da mesma sessão/versão. Não deixar formulário vazio quando só existe valor no servidor.
7. Manter checkpoints antigos imutáveis. Sessões antigas podem terminar pelo adaptador legado até expirar; não exigir voltar a uma etapa que a máquina de estados proíbe editar. Sessões novas exigem componentes e não podem escolher a versão antiga para contornar a regra.
8. Atualizar `coordinator`, `session` e `complete` para carregar e validar conforme o contrato da sessão, inclusive retries de conclusão, digests e idempotência. Guardar o objeto no JSON do checkpoint e convertê-lo para o modelo de imóvel ao concluir.
9. Gerar nome curto do imóvel a partir de logradouro/número/complemento, dentro de 200 caracteres. O texto completo permanece em `address`; uma correção de endereço não deve sobrescrever nomes administrativos escolhidos pelo usuário.

O encerramento do suporte de escrita legado depende tanto da expiração das sessões quanto do processamento de submissões já aceitas e seus retries. Não reescrever digests, checkpoints ou pedidos antigos para forçar o novo formato.

## 10. Laudos, notificações, busca e geolocalização

Novos snapshots de relatório recebem `addressDetails` opcional e continuam contendo `context.asset.address`. Congelar ambos juntos no momento de geração. Leitura de snapshots antigos devolve os componentes como ausentes; não enriquecer relatório histórico consultando o imóvel atual.

Atualizar `reportcore.AssetContext`, montagem no worker e mappers GraphQL. Preservar bytes/digests de documentos já publicados, HTML armazenado e PDFs existentes. O novo campo deve ser omitido na serialização quando ausente, e a compatibilidade da canonicalização antiga precisa de teste explícito. Correções de cadastro não alteram laudos emitidos. Novas versões seguem o mecanismo de relatório existente.

Dashboard, listas de relatórios, visualização do cliente e triagem continuam mostrando a string formatada. Componentes ficam disponíveis quando necessários para detalhes/filtros. Diferenciar o endereço atual na triagem do endereço histórico no laudo, caso sejam diferentes.

Revisar consumidores de `Asset.Address`: `invitations/dispatch_capture_invitation`, `invitations/correct_responsible_email`, `origins/invite_capture`, `notifications/record_in_app`, `notifications/remind_deadlines`, catálogo de mensagens e resolvers de notificações/triagem. Como a coluna textual é mantida, muitos consumidores só precisam de teste de regressão. Mensagens já emitidas não são reescritas.

Preservar `latitudeE6`, `longitudeE6` e `geofenceMeters`. CEP não fornece posição precisa do imóvel. Mudança de país/CEP/logradouro/número/cidade/UF deve limpar coordenadas atuais quando não houver nova localização explicitamente confirmada; alteração só de referência não exige isso. Não mudar snapshots de geofence de inspeções em andamento. A política existente de ausência de coordenadas deve continuar explícita e testada.

Opcional posterior: autocomplete por logradouro e geocodificação com provider, precisão, horário da consulta, confirmação e invalidação. Coordenadas aproximadas de centro de cidade/CEP não devem automaticamente alimentar geofence. Antes dessa fase, escolher provedor e definir orçamento e regras de retenção; não é dependência para entregar cadastro estruturado.

## 11. O que aproveitar do Fortview

Referências examinadas em `/Users/Payface/Documents/code-base/fortview`:

- `apps/panel/app/components/AddressForm.tsx`: formulário que ainda envia endereço textual.
- `apps/panel/app/components/address-form/usePlacesAutocompleteAddress.ts`: autocomplete restrito ao Brasil, que busca `formattedAddress` e mantém string no estado.
- `packages/core/prisma/schema.prisma:Geocoding`: endereço original/formatado, componentes JSON, coordenadas, precisão e expiração de cache, com escopo de organização/projeto.
- `packages/core/src/2-application/usecases/geocoding/GeocodeAddressUseCase.ts`: consulta com cache e métricas.
- `packages/core/src/1-domain/address-normalizer.ts`: normalização textual simples.

Aproveitar preenchimento assistido e, numa futura geocodificação, metadados de precisão e cache com escopo adequado. O cache geográfico do Fortview não é um modelo canônico de cadastro postal. Não copiar Prisma, camadas globais, dependências MUI nem ajustes que removem foco visível de widgets. Converter uma string para minúsculas também não é estratégia suficiente de deduplicação de imóveis.

A documentação do [Google Geocoding](https://developers.google.com/maps/documentation/geocoding/guides-v3/requests-geocoding) distingue resultados parciais e tipos de localização. Isso reforça a separação proposta entre endereço declarado pelo usuário e localização inferida por um provedor.

## 12. Migração, ativação e recuperação

### Etapa A — Expandir banco e disponibilizar backend compatível

Adicionar colunas nullable e estado legado inicial; preencher `legacy_address` com o texto atual, sem tentar adivinhar componentes. Adicionar versão de definição às sessões e registrar migration no catálogo canônico `platform/database/migrations/planner.go`. Usar a próxima versão livre na implementação; não fixar número em configuração, derivar de `LatestVersion()`.

O migrador atual executa `AutoMigrate` antes das migrations versionadas. Por isso, tags GORM não podem introduzir obrigatoriedade incompatível com linhas antigas antes do backfill. Validar upgrade e banco vazio; não editar migrations já aplicadas. Backfill volumoso deve ser retomável e em lotes, com contagem e verificação, em vez de uma chamada externa por linha.

Publicar backend de compatibilidade em todos os escritores antes de permitir componentes. Conferir janela de compatibilidade de schema de API, worker e scheduler: uma mudança aditiva no banco não garante que um binário antigo aceite a nova versão. O baseline para recuperação deve ser uma versão compatível com o schema expandido.

### Etapa B — Ativar produtores e leitores novos

Entregar pacote compartilhado, frontends, consulta de CEP, relatórios compatíveis, seeds e tipos regenerados. Só então ativar nova definição para novas sessões e novas gravações estruturadas. Usar configuração explícita de rollout, com métricas agregadas de erros, resultados de lookup e uso de escrita legada.

### Etapa C — Completar cadastros existentes

Admin oferece pendências por estado/cidade/CEP e ação de completar endereço. Mostrar o texto original ao operador; preencher componentes somente com dados confirmados. Não extrair rua/número/cidade de frases por regex ou IA e promovê-los automaticamente a dado confiável.

Ao salvar, preservar `legacy_address`, gerar a projeção e mudar para `COMPLETE` na mesma transação. Registrar versão/ator. O total de convertidos mais pendentes deve reconciliar com o total avaliado. Migração gradual de cadastros não pode interromper inspeções existentes.

### Etapa D — Encerrar novas escritas livres

Exigir estrutura para todos os novos cadastros depois de migrar consumidores e drenar sessões/submissões antigas. Para imóvel legado existente, permitir operações não relacionadas sem perda de dados; exigir estrutura quando o endereço for efetivamente corrigido. Acompanhar pendências até a meta acordada de saneamento.

### Recuperação

Desativar lookup ou novo fluxo por configuração em caso de problema. Manter campos e dados estruturados. Não fazer rollback destrutivo nem liberar escritores antigos que alterem a string sem atualizar os componentes. Reverter para o backend de compatibilidade; novas gravações devem continuar mantendo invariantes. Testar explicitamente recuperação com registros estruturados já existentes.

## 13. Pacotes de trabalho e dependências

| Ordem | Entrega | Depende de | Evidência de aceite |
| --- | --- | --- | --- |
| 1 | Contrato de campos, regras e casos de referência; confirmar Brasil | — | Mesmos exemplos de endereço aceitos/rejeitados em Go e frontend |
| 2 | Objeto de valor e persistência de imóvel; migrations e versão de sessão | 1 | Banco vazio e upgrade funcionam; legado preservado; RLS mantida |
| 3 | Registro/atualização/listagem, GraphQL aditivo e política de compatibilidade | 2 | Novos e antigos clientes cobertos; concorrência e no-op corretos |
| 4 | Slice de lookup de CEP e configuração operacional | 1 | Timeout/not found/erro não bloqueiam cadastro manual; acesso limitado |
| 5 | Pacote frontend e integração de build/CI/Docker | 1, contrato de 3/4 | Componente acessível e build dos consumidores em checkout limpo |
| 6 | Onboarding composto, catálogo versionado, checkpoint e conclusão | 2–5 | Jornada nova completa e sessão antiga retomada sem perda |
| 7 | Admin: cadastro, correção, pendências e filtros | 3–5 | Correção preserva demais dados; versão concorrente é tratada |
| 8 | Relatórios, notificações, triagem, busca e coordenadas | 3 | Histórico permanece idêntico; novas saídas usam endereço correto |
| 9 | Seeds, fixtures, documentação e verificação integrada | 6–8 | Matriz abaixo executada com evidências; codegen reproduzível |
| 10 | Ativação gradual, saneamento e encerramento de escrita livre | 9 | Novos produtores estruturados; recuperação ensaiada; pendências contabilizadas |

Depois de estabilizado o contrato, lookup e componente podem ser desenvolvidos independentemente da persistência. A ativação em ambiente depende do conjunto pronto. Maps/geocodificação e internacionalização são entregas posteriores explícitas, sem bloquear as dez entregas acima.

## 14. Matriz mínima de validação

| Camada | Cenários obrigatórios |
| --- | --- |
| Domínio | CEP com zero inicial e máscara; letras/quantidade inválida; UF inválida; limites Unicode; número alfanumérico; número + sem número; opcionais ausentes; rural; igualdade após normalização; formatação sem separadores sobrando |
| Cadastro/API | Registrar completo; rascunho incompleto; tentativa de ativar incompleto; corrigir; omissão/null; input duplo inconsistente; atualização legada de estruturado; no-op; conflito de versão; dependência ausente; falha transacional sem escrita parcial |
| Banco | Instalação limpa; upgrade com imóveis ativos e sessões em andamento; execução repetida; constraints com NULL; RLS entre tenants e escopo de unidade; preservação do texto; contagem de backfill |
| Lookup | Sucesso; inexistente; formato inválido sem chamada externa; resposta parcial; JSON inválido; timeout; cancelamento; limites; autorização de onboarding; cache e cache indisponível; nenhuma coordenada inventada |
| Frontend | Digitar/colar CEP; troca rápida de CEP; resposta fora de ordem; edição manual; complemento preservado; sem número; erro por campo; teclado; leitor de tela; temas; viewport 320/360/768/1440; reabrir modal |
| Onboarding | Checkpoint e reload; revisão do objeto; rascunho local compatível; hidratação do servidor; sessão antiga em cada etapa; schema desconhecido; conclusão idempotente; submissão antiga em retry; ambos os modos de fotos de referência |
| Admin | Cadastro novo; completar legado; corrigir completo; buscar por CEP/logradouro; filtros; conflito de versão; preservar template, atributos, assignments e políticas |
| Consumidores | Laudo novo com componentes; histórico sem componentes; digest/HTML/PDF antigo inalterado; notificações com escaping; triagem e busca histórica; ausência de vazamento em logs |
| Capture | Mudança de endereço limpa coordenadas atuais quando necessário; snapshots de inspeções existentes mantidos; ausência de cadastro postal novo; GPS/geofence e offline sem regressão |

Preservar Go tests, Vitest e Playwright já existentes. Para novos slices, cobrir sucesso, entrada inválida, falha de dependência e validação de `Setup`. Usar PostgreSQL real nos testes de migration/RLS; esses comportamentos não ficam comprovados só pelos fakes SQLite. Provedores externos devem ser stubados nos testes determinísticos.

Verificação da futura implementação:

1. `gofmt` nos Go alterados; `go test ./...`, `go vet ./...` e `go build ./...` a partir da raiz.
2. Regenerar gqlgen a partir de `services/inspection/schema.graphqls`, no diretório do serviço, e executar codegen nos quatro apps. Verificar também `src/graphql/schema.ts`, além de `generated.ts`.
3. Executar lint, testes, build e validação de pacote do novo pacote de endereço; nos quatro apps, codegen:check, lint, testes, build e Playwright, conforme scripts existentes.
4. Executar testes reais de banco com as variáveis `INSPECTION_TEST_DATABASE_URL` e `INSPECTION_TEST_RUNTIME_DATABASE_URL` no ambiente de teste; conferir que não foram ignorados. Validar consumidores do harness conforme seu README.
5. Executar `./scripts/verify.sh` atualizado, com infraestrutura de QA disponível, como gate integrado. Registrar dependências indisponíveis como bloqueio de validação, nunca como sucesso.
6. Fazer QA visual local do onboarding/Admin e relatório. Usar `agent-browser` se disponível para investigação interativa, mantendo regressões no Playwright. Guardar URLs, fluxo, screenshots e logs sem segredos.

Critério final: nenhum cadastro postal novo de primeira parte depende de texto livre; todos os consumidores continuam operando; falhas de lookup permitem preenchimento manual; legado está preservado e tem caminho de correção; documentos históricos permanecem imutáveis; builds e testes cobrem as quatro aplicações.

## 15. Referências de implementação

- [Modelo atual do imóvel](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/services/inspection/internal/platform/database/models.go:378)
- [Contrato GraphQL](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/services/inspection/schema.graphqls:169)
- [Regras atuais de imóveis](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/services/inspection/internal/features/assets/core/core.go)
- [Catálogo do onboarding](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/services/inspection/internal/features/onboarding/real_estate_catalog/catalog.go:66)
- [Tipos e validação do formulário](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/apps/onboarding/src/features/onboarding/definition.ts)
- [Formulário e retomada](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/apps/onboarding/src/features/onboarding/onboarding-journey.tsx:185)
- [Cadastro administrativo](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/apps/admin/src/features/admin/admin-shell.tsx:225)
- [Limites do design system](/Users/Payface/Documents/elvio/projetos/GoLang/inspection/packages/inspection-design-system/README.md)
- [Autocomplete do Fortview](/Users/Payface/Documents/code-base/fortview/apps/panel/app/components/address-form/usePlacesAutocompleteAddress.ts)
- [Persistência de geocodificação no Fortview](/Users/Payface/Documents/code-base/fortview/packages/core/prisma/schema.prisma:81)
