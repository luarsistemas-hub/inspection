# Estudo: inventário do imóvel como linha de base da vistoria

| Item | Valor |
| --- | --- |
| Situação | Estudo de produto e proposta de evolução. Nada deste documento foi implementado. |
| Data | 18 de outubro de 2026. |
| Código consultado | Checkout baseado em `b15f8967`, incluindo alterações locais ainda não commitadas. Análise por leitura de código; fluxos não executados. |
| Relacionado | [Estudo de vistoria por vídeo](estudo-vistoria-video.md), [runbook de vídeo](runbooks/video.md). |

## Sumário executivo

**Tese.** O Inspection entrega três capacidades sobre a mesma evidência: **catalogação** (o que existe no imóvel), **avaliação atual** (em que condição está) e **comparação histórica** (o que mudou em relação a uma base). Foto e vídeo são formas de coletar essa evidência; no vídeo, a evidência analisada são os frames extraídos. Uma mesma vistoria pode entregar as três capacidades.

**Lacunas principais.** (1) A vistoria hoje produz *findings* (ocorrências), não um **inventário estruturado**: itens em condição normal não ficam registrados. (2) A origem é um pacote de mídias, e a comparação é imagem × imagem. (3) Origem e vistoria atual precisam ter a **mesma modalidade**: foto de origem × vídeo atual está bloqueado.

**Proposta.** Tornar o **inventário do imóvel** uma entidade de primeira classe: ambientes, itens e atributos, cada um apoiado em evidências e com cobertura explícita (esperado, observado, avaliável). O inventário passa a ser a origem. Ele **só se torna base ativa depois de uma revisão humana**, em que o revisor decide item a item o que fica catalogado, corrige atributos e adiciona itens que a IA não encontrou. A promoção para base é **explícita e tem finalidade** (entrada de locação, entrega de obra, pós-reforma).

**Prompts.** Catalogar e comparar viram tarefas separadas, cada uma com prompt e schema próprios. O prompt de comparação recebe o catálogo de origem aprovado, recortado por ambiente, junto com as imagens de origem e as atuais (§5).

**Por que a revisão é obrigatória.** Um erro na base contamina todas as comparações futuras daquele imóvel. A revisão acontece uma vez por versão da base, não por vistoria comparativa; o laudo comparativo continua automático, como decidido no estudo de vídeo.

**Prioridade comercial.** Permitir comparação entre modalidades (§6), para aproveitar o acervo fotográfico e adotar vídeo aos poucos, sem refazer a origem.

## 1. Modelo conceitual

### 1.1 Três capacidades

| Capacidade | Pergunta | Precisa de origem? |
| --- | --- | --- |
| Catalogação | O que existe no imóvel e quais características conseguimos observar? | Não |
| Avaliação atual | Qual é a condição observável dos itens e ambientes? | Não |
| Comparação histórica | O que mudou em relação a um estado anterior escolhido? | Sim |

Duas consequências:

- **"Sem alteração" não significa "sem problemas".** Um dano preexistente continua sendo dano. O laudo apresenta a condição atual **e** as mudanças, separadamente.
- **Sem origem, não há "novo".** Uma vistoria sem base pode identificar problemas, mas não afirmar que surgiram depois de algo.

### 1.2 Checklist, catálogo e laudo

| Artefato | Função |
| --- | --- |
| **Checklist** (modelo de vistoria) | Roteiro do que deve ser observado; define o **escopo esperado**. |
| **Catálogo** (inventário) | Registro do que foi identificado, com evidências e cobertura. |
| **Laudo** | Condições, diferenças e limitações encontradas. |

### 1.3 Eixos

| Eixo | Opções |
| --- | --- |
| Modalidade de captura | Foto; vídeo, que gera frames. Afeta aquisição, processamento e rastreabilidade. |
| Modo de análise | Sem referência (catalogação + avaliação atual); contra a origem (+ comparação histórica). |
| Linha de base (origem) | Versões revisadas, ativadas com finalidade explícita. |

**Objetivo:** a possibilidade de comparar deve depender de **correspondência, qualidade e cobertura**, não da modalidade. Hoje isso **ainda não vale**: a comparação exige a mesma modalidade na origem e na vistoria atual (§2, §6).

O produto é cobrado por **resultado**: cadastrar o imóvel, comparar com a base ou acompanhar ao longo do tempo. A modalidade é uma conveniência de captura.

O vídeo original tem valor próprio (revisar contexto, conferir a sequência, extrair novos detalhes depois), mas a base de comparação são os frames, com identidade, horário de apresentação e hash próprios, que apontam exatamente qual evidência sustenta cada observação.

## 2. Situação atual no código

### 2.1 O que já existe

| Capacidade | Evidência |
| --- | --- |
| Modalidade exclusiva por modelo | `TemplateVersion.isVideo` e `mediaKind` em [`schema.graphqls`](../services/inspection/schema.graphqls). |
| Modos expostos | `CaptureComparisonMode { CHECKLIST_ONLY FIXED_ORIGIN }`. |
| Fotos sem origem e foto × foto | `CHECKLIST_ONLY` e `FIXED_ORIGIN` com origem fotográfica. |
| Vídeo sem origem | Preparação, extração e seleção de frames por nitidez ([`prepare_video`](../services/inspection/internal/features/media/prepare_video/setup.go)), com ambiente, componente, vista e visibilidade. |
| Vídeo × vídeo | Correspondência entre frames antes da análise ([`gateway_matcher.go`](../services/inspection/internal/features/analysis/prepare_video_comparisons/gateway_matcher.go)), com decisão `SAME_VIEW`, `NOT_COMPARABLE` ou `UNCERTAIN`. |
| Vistoria vira origem | `promoteInspectionPhotos` (fotos selecionadas) e `promoteInspectionVideo` (escopo e evidências preparadas). `OriginVersion` é versionada, com `mediaKind`, `limitedCoverage` e `scopeManifestDigest`, e a referência fica fixada por vistoria. |
| Seleção humana na promoção de fotos | `OriginPromotion` expõe `eligibleMedia` e `selectedMediaIds`. É o precedente da revisão proposta aqui, mas no nível da mídia, não do item. |
| Frames rastreáveis | Evidências com `frameId`, `timestampMs`, `ptsMicros` e `sourceMediaId`. |
| Retenção do vídeo | `media.video_policies`: padrão de **30 dias** para a fonte de origem e **5 dias** para a fonte atual, configurável por cliente; legal hold por fonte. |

### 2.2 Lacunas

| Lacuna | Evidência | Consequência |
| --- | --- | --- |
| Não há catálogo | O schema de saída em [`definition.go`](../services/inspection/internal/features/analysis/prompt/definition.go) só tem `noRelevantChange` e `findings`. | Itens sem ocorrência não são registrados; não existe "Cozinha → bancada, 2 portas, estado bom". |
| Origem é um pacote de mídias | A promoção copia mídias; a análise fotográfica recebe `origin:<mediaId>`. | A comparação é por imagem, não por item. Não há "item ausente" estruturado. |
| Modalidades diferentes bloqueadas | [`resolve_reference/setup.go`](../services/inspection/internal/features/origins/resolve_reference/setup.go) rejeita com "reference modality must match the inspection modality". | Foto de origem × vídeo atual e frames de origem × fotos atuais não funcionam. O acervo fotográfico não serve de base para vídeo. |
| Identidade de ambiente e componente não persiste | No vídeo, ambiente, componente e vista existem, mas presos à preparação daquela evidência. Nas fotos, não existem. | Não há identidade estável do imóvel ao longo do tempo; dois quartos parecidos podem ser confundidos. |
| Captura de vídeo contínua | [`capture/core/core.go`](../services/inspection/internal/features/capture/core/core.go): requisito único `video:main`, "Grave um único vídeo contínuo da vistoria". | Difere da recomendação de clipes por ambiente do estudo de vídeo; cobertura e repetição de trecho ruim ficam mais difíceis. |
| Vídeo não liberado em produção | [Runbook de vídeo](runbooks/video.md): faltam aparelhos físicos, corpus com avaliação semântica e capacidade na VM. | Implementado não é disponível. |
| Possível falha em `FIXED_ORIGIN` fotográfico | A promoção de fotos grava só o derivado `DISPLAY` ([`process.go`](../services/inspection/internal/features/origins/promote_inspection/process.go)); `build_request` busca `ANALYSIS` da origem ([`setup.go`](../services/inspection/internal/features/analysis/build_request/setup.go)). | Constatado por leitura, sem execução. Confirmar com teste antes de qualquer piloto. |

## 3. Inventário estruturado

```text
Imóvel (ativo)
└── Versão do inventário (rascunho → em revisão → ativa → substituída) + finalidade
    └── Ambiente (identidade estável: "cozinha", "banheiro-2")
        └── Item / componente (identidade estável no ativo)
            ├── atributos: tipo, material, cor, quantidade, estado (escala fixa) — cada um pode ser "desconhecido"
            ├── observação: observado | parcialmente observado | não observado | não avaliável
            ├── limitação: "interior não mostrado", "obstruído por móvel"...
            ├── evidências: foto ou frame + timestamp + recorte opcional
            ├── origem: IA | manual
            └── decisão de revisão: aceito | editado | rejeitado | adicionado
```

Exemplo de item:

| Campo | Exemplo |
| --- | --- |
| Ambiente | Cozinha |
| Item | Armário inferior da pia |
| Característica observada | Duas portas brancas |
| Condição | Desgaste visível na borda inferior |
| Evidência | Frame do vídeo em 01:32 |
| Limitação | Interior não mostrado |
| Correspondência histórica | Mesmo item da origem, quando sustentado |

Regras:

- **Itens em condição normal também são catalogados.** Uma lista de achados vazia não preserva o inventário.
- **Não observado ≠ ausente; ausência de achados ≠ inspeção completa.**
- **Não verificável fica "desconhecido".** Não deduzir material, medidas ou funcionamento só pela aparência.
- **Imagem não prova funcionamento.** Abrir janela, acionar torneira ou ouvir um equipamento depende de sequência e, talvez, áudio. Esses testes precisam de regras próprias e ficam fora do escopo inicial; o laudo deve deixar a limitação explícita.
- **A base pode conter danos.** Uma origem com armário riscado documenta que o risco já existia; não significa estado ideal.
- **Escala de estado fechada** (por exemplo: novo, bom, regular, ruim), para que a comparação estruturada seja determinística.
- **Identidade estável.** O mesmo item mantém o ID entre versões; a descrição livre não é chave.

### 3.1 Escopo e cobertura

"Catalogar tudo" não é uma promessa verificável: um vídeo de cinco minutos pode omitir um banheiro inteiro mesmo com todos os frames nítidos. A promessa passa a ser **catalogar o que foi mostrado com qualidade suficiente e dizer o que faltou**.

| Informação | Exemplo |
| --- | --- |
| Escopo esperado | O imóvel possui dois banheiros. |
| Escopo observado | O vídeo mostrou um banheiro. |
| Escopo avaliável | Nesse banheiro, foi possível avaliar pia e piso, mas não o box. |

Origem do escopo esperado:

- **Primeira catalogação:** modelo de vistoria (ambientes e itens relevantes) e atributos do ativo (por exemplo, quantidade de quartos e banheiros), confirmados na revisão (§4).
- **Vistorias seguintes:** a base ativa.

Saída esperada no laudo, **somente quando o escopo esperado estiver definido**:

> Foram avaliados 18 dos 24 componentes previstos. Quatro não aparecem e dois precisam de nova captura.

Sem escopo esperado, o sistema informa o que encontrou, mas não afirma cobertura do imóvel inteiro.

## 4. Revisão humana e promoção da base

### 4.1 Fluxo

```text
captura (foto ou vídeo) → IA sugere inventário e cobertura → revisão humana item a item
→ (opcional) ciência/contestação da contraparte → promoção com finalidade → versão ativa da base
```

Enquanto não houver revisão, o inventário fica como **rascunho** e não é usado como origem. Uma comparação feita contra base não revisada, se permitida, deve aparecer marcada como "base provisória" no laudo.

### 4.2 Ações do revisor

| Ação | Efeito | Observação |
| --- | --- | --- |
| **Aceitar** | O item sugerido entra no inventário como está. | Aceite em lote por ambiente para itens de alta confiança. |
| **Editar** | Corrige atributos (material, quantidade, estado, ambiente). | Guarda o valor sugerido e o corrigido. |
| **Rejeitar** | O item não fica catalogado. | Não é apagado: fica registrado como sugestão rejeitada, com motivo opcional (não existe, não é relevante, duplicado, objeto pessoal). |
| **Adicionar** | Inclui um item que a IA não encontrou. | Deve apontar para uma evidência (foto/frame, ou região dele). Item sem evidência é permitido apenas como "declarado", e não pode ser comparado visualmente no futuro. |
| **Mesclar/dividir** | Corrige duplicidade entre frames ou itens agrupados. | Especialmente importante em vídeo, onde o mesmo item aparece em vários frames. |
| **Confirmar escopo esperado** | Confirma ambientes e itens que deveriam existir (§3.1). | Ambiente esperado e não observado vira pendência de captura, não item ausente. |

### 4.3 Promoção explícita com finalidade

A vistoria **não** vira base automaticamente. Promover toda vistoria normalizaria a deterioração: surge um dano, a vistoria vira referência e a próxima comparação deixa de destacá-lo.

- A promoção registra a **finalidade**: entrada de locação, entrega de obra, estado após reforma, atualização de inventário.
- A comparação usa a base **escolhida** para aquela finalidade (por exemplo, saída × entrada), não necessariamente a mais recente.
- Uma origem nova não altera o significado de laudos anteriores, que continuam apontando para a versão usada. O versionamento e a referência fixada por vistoria já existentes são preservados.

### 4.4 Efeitos nas comparações futuras

- **Somente itens aceitos, editados ou adicionados** compõem a base.
- Itens **rejeitados** não geram achado de "ausência" depois. Um item visível na vistoria atual que não está na base aparece como **"fora do inventário"**, não como dano ou acréscimo indevido.
- Os `attentionItems` do prompt passam a ser derivados dos itens aceitos da base, em vez de seleção solta por mídia.
- A comparação visual (imagem × imagem) continua existindo para danos finos (trinca, mancha), usando as evidências ligadas a cada item.

### 4.5 Quem revisa

- **Operador do tenant** (imobiliária, administradora, vistoriador): responsável pela revisão, pela finalidade e pela ativação.
- **Contraparte** (por exemplo, o inquilino na vistoria de entrada): opcionalmente toma ciência e pode contestar itens dentro de um prazo. Contestação gera nova versão, sem apagar a anterior.

Esse fluxo reproduz a dinâmica da vistoria de entrada de locação e aumenta o valor probatório da base.

### 4.6 Auditoria e versionamento

- A versão ativa é imutável. Qualquer edição posterior gera nova versão.
- Registrar por item: sugestão original da IA, decisão, autor, data, motivo e evidência. Isso alimenta a trilha de auditoria (`audit`) e a contestação.

### 4.7 Experiência de revisão

- Revisão organizada por ambiente, com a evidência (foto ou frame com timestamp) ao lado de cada item.
- Ordenar primeiro os itens de **menor confiança** e os sem evidência adequada; permitir aceite em lote do restante.
- O custo humano é o tempo de revisão: medir e usar como métrica de produto.

### 4.8 Métricas da catalogação

As decisões do revisor são rótulos gratuitos para avaliar a IA:

- **taxa de aceite sem edição** → precisão;
- **itens adicionados manualmente** → recall perdido;
- **itens editados por atributo** → onde o modelo erra (material, quantidade, estado);
- **tempo de revisão por ambiente**.

Esses dados podem entrar no avaliador existente em [`scripts/analysis-eval.mjs`](../scripts/analysis-eval.mjs) como casos rotulados.

### 4.9 Relação com o estudo de vídeo

O estudo de vídeo decidiu que **o laudo é automático, sem fila de revisão por vistoria**. Isso continua valendo. A revisão proposta aqui ocorre **na criação de cada versão da base**, normalmente uma vez por contrato ou ciclo do imóvel, e protege todas as comparações seguintes.

## 5. Prompts e contratos de análise

### 5.1 Situação atual

Existe **um único prompt** por `analysisType` (`REAL_ESTATE`), com **um único schema de saída** de `findings` ([`definition.go`](../services/inspection/internal/features/analysis/prompt/definition.go)). O modo é inferido pela presença de evidências `ORIGIN`: sem elas, o prompt descreve condições atuais; com elas, compara. `AnalysisPromptSnapshot` é indexado por `analysis_type` e `canonical_digest` ([`resolve_prompt`](../services/inspection/internal/features/analysis/resolve_prompt/setup.go)).

O embrião do catálogo já existe: os `attentionItems` da referência ([`build_request`](../services/inspection/internal/features/analysis/build_request/setup.go)), uma lista de textos por foto de origem que o prompt manda avaliar individualmente.

### 5.2 Duas tarefas, dois prompts

| | Prompt de catalogação | Prompt de comparação |
| --- | --- | --- |
| Capacidades | Catalogação + avaliação atual | Avaliação atual + comparação histórica |
| Pergunta | O que existe aqui e em que estado? | O que mudou em relação à base escolhida? |
| Usado em | Vistoria sem origem e criação de nova versão da base | Vistoria com origem ativa (`FIXED_ORIGIN`) |
| Entrada | Escopo esperado do ambiente + imagens/frames atuais | Itens aprovados da base para o ambiente + evidências de origem ligadas a eles + imagens/frames atuais |
| Saída | Itens sugeridos, cobertura + `findings` de condição atual | Status por `itemId`, cobertura + `findings` de dano + itens fora do inventário |
| Destino | Rascunho do inventário → revisão humana (§4) | Laudo automático |

Não há uma terceira tarefa: a vistoria sem origem cataloga e avalia a condição atual na mesma chamada.

Os dois prompts são **agnósticos à modalidade**: recebem imagens com `evidenceId`, sejam fotos ou frames. A diferença entre modalidades fica antes, na preparação e na correspondência (§6).

### 5.3 Prompt de catalogação

Saída por item sugerido:

- `environmentKey` e `itemType` (vocabulário controlado, com valor livre como fallback);
- atributos: material, cor, quantidade, `condition` em escala fechada, cada um podendo ser `UNKNOWN`;
- `observation` (`OBSERVED`, `PARTIAL`, `NOT_OBSERVED`, `NOT_ASSESSABLE`), qualidade da evidência e limitação;
- `evidenceIds` obrigatórios para itens observados, com recorte opcional;
- `confidence`, usado para ordenar a revisão (§4.7).

Saída de cobertura: itens e ambientes do escopo esperado que não apareceram ou não puderam ser avaliados, com a orientação de captura.

Regras:

- Não catalogar o que não está visível; não inferir quantidade, material, medida ou funcionamento além do que aparece.
- Registrar itens sem ocorrência; a ausência de dano não é motivo para omitir o item.
- Em vídeo, frames do mesmo ambiente vêm juntos e o modelo deduplica o mesmo item visto em vários frames, listando todas as evidências no mesmo item.
- Objetos pessoais e cotidianos ficam fora por padrão, seguindo as exclusões que o prompt atual já aplica, salvo quando o modelo de vistoria os incluir no escopo.
- `findings` de condição atual continuam no mesmo schema de hoje, para não quebrar laudo, triagem e dashboard.

### 5.4 Prompt de comparação

Entrada:

- **Catálogo de origem recortado por ambiente**: somente itens aceitos, editados ou adicionados na versão ativa, cada um com `itemId`, atributos e as evidências de origem ligadas a ele. Itens rejeitados não são enviados.
- **Imagens de origem**: o texto do catálogo não substitui a imagem. "Bancada de granito, estado bom" não permite detectar uma trinca nova; o catálogo diz *o que verificar*, e a imagem mostra *como estava*.
- **Imagens/frames atuais** do mesmo ambiente, já pareados quando houver correspondência (§6).

Saída por item da base:

| Status | Significado | Exigência |
| --- | --- | --- |
| `PRESENT_UNCHANGED` | Presente, sem alteração relevante observada | `evidenceIds` atuais |
| `CONDITION_CHANGED` | Presente, estado diferente da base | Evidência nas duas datas e estado observado |
| `MISSING` | Ausente com cobertura suficiente do local | Evidência atual do local onde o item deveria estar |
| `NOT_OBSERVED` | Não foi possível ver o item | Motivo (enquadramento, obstrução, qualidade) |

Além disso:

- **Itens fora do inventário**: objetos relevantes visíveis que não estão na base. Não são dano nem acréscimo indevido; servem de insumo para a próxima versão do inventário.
- **`findings`** localizados, referenciando o `itemId` quando houver, separando condição atual (inclusive preexistente) de mudança em relação à base.

### 5.5 Cuidados

- **Catálogo como dado, não como instrução.** O catálogo vai no prompt do usuário, não no system prompt, que continua estável e versionado por digest. Itens adicionados manualmente contêm texto digitado por pessoas: a regra que hoje manda ignorar instruções em imagens e metadados se estende aos campos do catálogo.
- **Viés de confirmação.** Uma lista de itens esperados induz o modelo a "confirmar" presença. `PRESENT_UNCHANGED` exige evidência atual, e `NOT_OBSERVED` é resposta legítima, nunca convertida em presença nem em ausência.
- **Recorte por ambiente.** Enviar só o ambiente em análise reduz tokens e evita que o modelo associe um item a um ambiente errado. A identidade estável de ambiente (§3) é pré-requisito.
- **Correspondência antes da comparação.** Comparar o segundo 20 de um vídeo com o segundo 20 de outro não garante o mesmo componente. A comparação só ocorre sobre pares sustentados, como "frente do mesmo armário da cozinha, com cobertura suficiente para avaliar a porta inferior".
- **`attentionItems` evolui para itens da base.** Em vez de textos soltos por foto, passam a ser os itens aprovados com `itemId`, mantendo a regra de avaliação individual.

### 5.6 Versionamento e auditoria

- Cada tarefa tem system prompt e schema de saída próprios. A chave do snapshot passa a incluir a tarefa (`analysis_type`, tarefa, `canonical_digest`); a análise atual continua válida como versão legada.
- O snapshot de entrada da comparação registra a versão do inventário usada, além de IDs e hashes das evidências, como já é feito com a referência.
- Reprocessar com outro prompt gera nova versão do laudo ou do rascunho, mantendo a anterior auditável.

### 5.7 Ciclo da base

```text
catalogação → revisão humana → promoção (finalidade: entrada) → base v1 ativa
→ comparação (saída × entrada) → itens fora do inventário + mudanças de estado
→ rascunho da base v2 → revisão humana → promoção (finalidade: nova entrada) → base v2 ativa
```

A vistoria comparativa não altera a base diretamente: ela propõe um rascunho da próxima versão, que passa pela mesma revisão humana e por uma promoção explícita.

## 6. Comparação entre modalidades

Matriz desejada:

| Origem | Captura atual | Base da comparação | Hoje |
| --- | --- | --- | --- |
| Fotos | Fotos | Imagens correspondentes | Disponível |
| Vídeo | Vídeo | Frames × frames, com correspondência | Implementado, vídeo ainda não liberado em produção |
| Fotos | Vídeo | Fotos × frames | **Bloqueado** |
| Vídeo | Fotos | Frames × fotos | **Bloqueado** |

**Por que priorizar.** Aproveita o acervo fotográfico existente e permite adotar vídeo gradualmente, sem refazer a origem só para mudar a forma de captura. O estudo de vídeo já tratava foto × vídeo como primeira entrega.

**O que exige.** Não basta retirar a validação de `resolve_reference`. O fluxo de comparação por vídeo espera estruturas de frames e preparação da origem (`VideoPreparationID`, `scopeManifestDigest`), e o fluxo fotográfico espera pares `origin:<mediaId>`. É preciso:

- tratar fotos de origem como evidências com a mesma identidade de ambiente e componente que os frames;
- rodar a correspondência (`SAME_VIEW`, `NOT_COMPARABLE`, `UNCERTAIN`) entre fotos e frames;
- emitir lacuna de cobertura quando não houver par, em vez de comparar imagens não correspondentes.

A modalidade continua exclusiva **dentro** de cada vistoria; captura mista (vídeo geral + fotos de detalhe) pode ser avaliada depois.

## 7. Captura e retenção do vídeo

### 7.1 Captura guiada por ambiente

Hoje o requisito orienta **um único vídeo contínuo**. Hipótese a testar: captura por ambiente, com instruções curtas (visão geral, paredes, piso, teto e componentes relevantes), facilita orientação, acompanhamento de cobertura e repetição de um trecho ruim, ao custo de mais passos. Decidir por piloto, medindo conclusão, tempo e recapturas.

### 7.2 Retenção do original

Padrões atuais: **30 dias** para a fonte de origem e **5 dias** para a fonte atual, configuráveis por cliente. Depois que o original é excluído, os frames preservados continuam úteis, mas não é possível buscar um detalhe que não foi extraído nem reprocessar a captura.

Antes de fechar a oferta comercial:

- alinhar os prazos ao tempo real de consulta, contestação e complemento (por exemplo, a contestação de uma vistoria de saída pode passar de 5 dias);
- informar ao cliente por quanto tempo o vídeo fica disponível, quais evidências permanecem depois e até quando é possível reprocessar;
- considerar retenção estendida da base ativa e dos vídeos ligados a uma contestação aberta (legal hold já existe).

## 8. Estratégia de produto

O ativo estratégico é o **histórico do imóvel**: inventário revisado, estado e provas datadas, versionados ao longo do tempo. Vistoria avulsa é commodity; histórico confiável, **evidências localizáveis** e **cobertura compreensível** são difíceis de copiar e ganham valor a cada vistoria.

| Oferta | Valor para o cliente | Cobrança a testar |
| --- | --- | --- |
| Cadastro inicial do imóvel | Organizar ambientes, componentes, condições e evidências; estabelecer a base revisada | Por vistoria, ticket maior |
| Vistoria comparativa | Identificar mudanças em relação à base escolhida | Por vistoria ou pacote |
| Acompanhamento recorrente | Conservação, intervenções e pendências ao longo do tempo (`schedules`) | Assinatura por carteira de imóveis |
| Revisão por vistoriador | Revisão da base ou do laudo feita pela operação | Adicional por vistoria |

Sequência comercial: o cadastro inicial gera valor imediato; as comparações reutilizam esse trabalho; o acompanhamento dá continuidade ao relacionamento. Público inicial: clientes com imóveis sob gestão e necessidade recorrente de vistoria.

Minutos de vídeo, armazenamento e reprocessamentos podem funcionar como limites operacionais transparentes. **Não fechar preços antes de medir o custo completo de uma vistoria concluída**, incluindo suporte e recaptura.

Segmentos, do mais aderente ao menos aderente ao que já existe: imobiliárias e administradoras (entrada e saída de locação), temporada (inventário a cada troca de hóspede), construtoras (entrega de chaves, com `projects`/`stages`), seguradoras (base antes da apólice, comparação no sinistro) e facilities/condomínios (manutenção recorrente).

### 8.1 Métricas de produto

- Percentual de vistorias concluídas sem ajuda.
- Tempo entre convite e laudo útil.
- Taxa e motivo de recaptura.
- Cobertura dos componentes do escopo esperado.
- Alterações reais que o sistema deixou de identificar.
- Alertas incorretos e correspondências erradas.
- Custo total por vistoria concluída.
- Tempo de revisão da base e taxa de aceite sem edição (§4.8).
- Uso recorrente pelos clientes.

## 9. Riscos

- **Base contaminada.** Mitigada pela revisão obrigatória e pelo versionamento.
- **Deterioração normalizada.** Mitigada pela promoção explícita com finalidade e pela escolha da base de comparação.
- **"Sem alteração" errado** no laudo automático. Manter "inconclusivo / não comparável" visível e medir recall por categoria antes de liberar.
- **Cobertura superestimada.** Só exibir "X de Y avaliados" com escopo esperado definido.
- **Correspondência errada** entre ambientes parecidos. Exigir pares sustentados e identidade estável.
- **Revisão virar gargalo.** Mitigar com aceite em lote, ordenação por confiança e prazo com ativação explícita.
- **Itens manuais sem evidência.** Permitidos só como "declarados" e fora da comparação visual.
- **Funcionamento inferido por imagem.** Fora do escopo; limitação explícita no laudo.
- **Retenção curta do original.** Pode expirar antes de contestações; alinhar à oferta (§7.2).
- **LGPD em vídeo.** Áudio desligado por padrão; screening de pessoas; aceite de processamento que hoje cita somente fotos.

## 10. Ordem sugerida

1. Confirmar ou corrigir a possível falha `DISPLAY`/`ANALYSIS` em `FIXED_ORIGIN` fotográfico.
2. Definir o contrato do catálogo e da cobertura: identidade estável de ambiente e componente no ativo, escopo esperado e estados de observação (§3).
3. Validar a qualidade do fluxo atual de vídeo e fechar os gates do runbook.
4. Prompt de catalogação com saída estruturada de inventário, sem quebrar `findings` (§5.3).
5. Revisão humana, promoção com finalidade e ativação da versão do inventário, com auditoria (§4).
6. Comparação foto de origem × vídeo atual (§6).
7. Piloto de captura guiada por ambiente × vídeo contínuo (§7.1) e revisão dos prazos de retenção (§7.2).
8. Prompt de comparação com o catálogo de origem por ambiente (§5.4).
9. Ciência e contestação da contraparte.
10. Acompanhamento recorrente sobre o histórico acumulado.
