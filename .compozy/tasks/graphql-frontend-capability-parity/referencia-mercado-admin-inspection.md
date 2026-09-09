# Referência de mercado · Frontend Admin da plataforma Inspection

**Status:** referência obrigatória para o PRD do Frontend Admin  
**Escopo:** console B2B desktop-first, separado do Dashboard operacional  
**Produto:** Inspection  
**Idioma da interface:** pt-BR

## 1. Recomendação executiva

O Frontend Admin deve ser um **console de configuração, identidade e governança do tenant**, não uma segunda versão do Dashboard.

A arquitetura recomendada é:

> **navegação lateral agrupada → contexto persistente de tenant e escopo → página de coleção com busca, filtros e ações → detalhe do objeto → histórico/auditoria.**

O Admin deve responder principalmente:

- Quem pode acessar o quê?
- Em qual tenant, unidade ou segmento esse acesso vale?
- Como participantes, templates, perfis, assets e políticas estão configurados?
- Qual foi a origem de uma decisão e quem alterou a configuração?
- O que pode ser alterado com segurança, e qual será o impacto?

O Dashboard responde outra pergunta:

- O que está acontecendo agora na operação e quais inspeções precisam de atenção?

Essa separação evita que governança, configuração e operação concorram pela mesma hierarquia visual.

## 2. Fronteira obrigatória: Admin versus Dashboard

| Dimensão | Frontend Admin | Dashboard |
|---|---|---|
| Objetivo | Configurar, conceder, restringir e auditar | Acompanhar, triar e revisar inspeções |
| Usuário principal | Administrador do tenant, administrador de acesso, governança | Gestor operacional, revisor, analista |
| Ritmo | Menos frequente, alto impacto, exige confirmação | Frequente, orientado a fila e próxima ação |
| Unidade principal | Tenant, unidade, usuário, papel, política e recurso | Inspeção, evidência, ativo, projeto e evento |
| Navegação | Taxonomia estável por domínio administrativo | Filas e objetos operacionais |
| Densidade | Alta, comparativa, orientada a tabela | Média-alta, com contexto documental |
| Ação | Criar, editar, convidar, atribuir, publicar, arquivar | Revisar, solicitar evidência, classificar, acompanhar |
| Evidência | Quem alterou configuração, quando e com qual escopo | O que aconteceu na inspeção e quais fatos sustentam a decisão |
| Estado de sucesso | Configuração aplicada e auditada | Inspeção recebida, revisada ou encaminhada |

O Admin compartilha com o Dashboard os tokens, a terminologia e a **Trilha de Evidências**, mas não compartilha o mesmo shell visual. O Admin usa a densidade da **Operação Precisa**; o Dashboard usa a Trilha de Evidências para contexto e rastreabilidade.

## 3. O que os consoles empresariais fazem bem

Esta referência compara padrões, não recomenda copiar interfaces ou marcas específicas.

### Microsoft Entra admin center — modelo de produto e governança

O Entra organiza áreas administrativas por produto na navegação lateral e oferece busca global para encontrar configurações, usuários, grupos, aplicações e papéis. Também separa gestão de identidade, governança de acesso, revisões de acesso e logs.

**Padrão aproveitado:** a navegação deve agrupar por domínio administrativo, e não apresentar todos os recursos do tenant em um único nível. A busca global é importante quando a taxonomia cresce.

**Aplicação no Inspection:** “Identidade e acesso”, “Configuração de inspeção” e “Governança” devem ser grupos explícitos, com acesso efetivo e auditoria conectados à mesma linguagem.

### Stripe Dashboard — modelo de settings e papéis

O Stripe separa navegação de operação, configurações pessoais, configurações da conta e configurações de produto. A gestão de equipe e segurança fica vinculada a papéis, e as operações podem ser relacionadas a recursos específicos.

**Padrão aproveitado:** configuração pessoal não deve se misturar com configuração do tenant; papéis precisam aparecer no contexto da ação; o link para voltar à operação deve existir sem transformar o Admin em Dashboard.

**Aplicação no Inspection:** “Minha conta” e “Configuração do tenant” devem ser contextos distintos. Um usuário pode administrar convites sem poder alterar retenção ou políticas de publicação.

### GitHub Organization settings — modelo de auditoria consultável

O GitHub trata a organização como unidade de administração, expõe um audit log com ator, alvo, ação e momento, permite filtros por categoria e oferece exportação.

**Padrão aproveitado:** auditoria é um produto consultável, não uma lista decorativa. Cada evento deve explicar quem fez o quê, sobre qual objeto, em qual escopo e com qual resultado.

**Aplicação no Inspection:** auditoria precisa de busca por identificador, filtros por ator, recurso, ação, resultado e intervalo de tempo, além de detalhe do evento e exportação conforme permissão.

### Atlassian Administration — modelo de administração organizacional

O Atlassian diferencia administração da organização, administração de produto e atividade registrada em audit logs. A plataforma também relaciona governança a políticas, aplicações e ciclo de vida.

**Padrão aproveitado:** o usuário deve saber se está alterando o tenant, uma unidade ou uma configuração de produto. O mesmo recurso pode ter administrações diferentes, e a interface precisa mostrar essa fronteira.

**Aplicação no Inspection:** o contexto de tenant deve aparecer sempre no topo; páginas com escopo menor devem mostrar a unidade, segmento ou projeto selecionado antes de permitir mutações.

### Síntese de mercado

Os melhores padrões convergem em cinco decisões:

1. A navegação lateral agrupa domínios e pode ser pesquisada.
2. O contexto organizacional é persistente e visível.
3. Coleções começam com busca, filtros e tabela; o detalhe vem depois.
4. Papéis, escopos e acesso efetivo são parte da informação principal.
5. Auditoria, exportação e estados de erro são funções de primeira classe.

## 4. Arquitetura de informação recomendada

### Shell global

**Barra superior:**

- identidade “Inspection Admin”;
- seletor de tenant, quando o usuário possui mais de um tenant;
- escopo atual, como “Tenant”, “Unidade Norte” ou “Segmento: Operações”;
- busca global por pessoa, unidade, asset, template ou identificador;
- link “Abrir Dashboard” como saída contextual;
- ajuda, notificações administrativas e menu da conta.

**Lateral persistente desktop:**

```text
Visão administrativa
  Resumo do tenant

Organização
  Tenant e unidades
  Origens

Identidade e acesso
  Usuários
  Convites
  Papéis e permissões
  Acesso efetivo

Participação
  Participantes
  Segmentos

Configuração de inspeção
  Templates
  Perfis de análise
  Assets

Governança
  Políticas de publicação
  Notificações
  Retenção
  Auditoria
```

“Resumo do tenant” não é um dashboard de KPIs. É uma página de contexto administrativo com saúde de configuração: convites pendentes, permissões sem revisão, políticas incompletas, retenção configurada e mudanças recentes.

### Por que esta taxonomia

- **Organização** define onde a operação acontece.
- **Identidade e acesso** define quem pode agir.
- **Participação** define quem participa da coleta e como é segmentado.
- **Configuração de inspeção** define o que será coletado e analisado.
- **Governança** define como publicar, notificar, reter e provar alterações.

O agrupamento reduz a lista plana atual e cria uma ordem de dependência: primeiro o escopo, depois o acesso, depois a configuração, por fim a governança.

## 5. Padrões de tela obrigatórios

### 5.1 Página de coleção

Usar para usuários, convites, participantes, unidades, segmentos, templates, perfis, assets, origens e auditoria.

Estrutura:

1. Breadcrumb e título do recurso.
2. Descrição curta da responsabilidade da página.
3. Contexto do tenant e escopo atual.
4. Ação primária única, por exemplo “Convidar usuário” ou “Criar template”.
5. Busca textual persistente.
6. Filtros por estado, escopo, papel, origem, data ou versão.
7. Contagem da consulta e indicação de atualização.
8. Ações em massa somente quando forem seguras e compreensíveis.
9. Tabela densa com cabeçalhos persistentes e colunas ordenáveis.
10. Paginação ou carregamento incremental explícito.

Colunas devem priorizar identificação e decisão:

```text
identificador → nome/contexto → escopo → estado → última alteração → responsável → ações
```

Não esconder o escopo em um tooltip. Não usar cartões como representação principal de coleções administrativas.

### 5.2 Página de detalhe

Usar para tenant, unidade, usuário, participante, template, perfil, asset, origem e evento de auditoria.

Estrutura:

- cabeçalho com nome, ID copiável, estado e escopo;
- ação principal e menu de ações secundárias;
- resumo de fatos principais;
- abas ou seções: “Visão geral”, “Acesso”, “Relacionados”, “Histórico”;
- histórico com autor, horário, versão e motivo;
- links para objetos relacionados sem perder o contexto;
- aviso claro quando o objeto estiver arquivado, desativado ou fora do escopo atual.

O detalhe deve responder rapidamente “o que é”, “onde vale”, “quem pode alterar” e “o que mudou”.

### 5.3 Formulário de criação ou edição

Usar uma coluna de leitura de aproximadamente 640–720 px, mesmo em desktop amplo.

Estrutura:

- título e consequência da configuração;
- campos agrupados por decisão, não por modelo de dados;
- rótulo persistente, ajuda contextual e erro próximo ao campo;
- valores atuais e versão do objeto quando houver concorrência;
- prévia do escopo afetado;
- barra de ações inferior ou superior com “Cancelar” e uma única ação primária;
- alerta de alterações não salvas;
- confirmação somente para mudanças de alto impacto.

Não usar modal para formulários longos, edição de permissões, retenção ou políticas de publicação.

### 5.4 Papéis, permissões e acesso efetivo

Esta é a tela mais importante do Admin.

Mostrar o acesso em duas camadas:

```text
Acesso concedido
  direto
  por papel
  por unidade
  por segmento
  por convite

Acesso efetivo
  recurso
  ação
  permitido / negado
  origem da permissão
  escopo
  validade
```

Cada permissão deve ter uma explicação legível. O administrador não deve precisar inferir a combinação de papéis para descobrir por que alguém consegue editar um template.

Recomendações:

- mostrar concessões diretas separadas das herdadas;
- sinalizar permissões conflitantes ou redundantes;
- oferecer comparação entre dois usuários ou papéis;
- impedir atribuição fora do escopo do administrador atual;
- registrar toda alteração de acesso em auditoria;
- orientar para menor privilégio sem bloquear exceções justificadas.

### 5.5 Convites

Tabela recomendada:

```text
destinatário → papel → escopo → enviado em → expira em → estado → ações
```

Estados mínimos: pendente, aceito, expirado, revogado e falha de entrega.

Reenvio, revogação e alteração de escopo devem ser ações distintas. O reenvio não deve parecer uma nova concessão de acesso.

### 5.6 Políticas de publicação e retenção

Estas telas exigem linguagem de consequência:

- qual dado é afetado;
- a partir de quando a política vale;
- se afeta dados novos, históricos ou ambos;
- quem pode publicar ou alterar;
- qual prazo de retenção foi configurado;
- o que acontece com dados expirados;
- como a mudança será auditada.

O formulário deve mostrar um resumo “Antes de salvar” com escopo, impacto e versão. Para uma ação irreversível, usar confirmação explícita com o nome do objeto; não usar confirmação genérica “Tem certeza?”.

### 5.7 Auditoria

Tabela mínima:

```text
data/hora → ator → ação → recurso → escopo → resultado → ID do evento
```

Filtros:

- intervalo de data e hora;
- ator;
- tipo de ação;
- recurso;
- unidade ou tenant;
- sucesso, falha ou negado;
- origem da ação, quando disponível.

O detalhe do evento deve conter o estado anterior e o novo estado quando seguro, motivo, versão, identificador de correlação e relação com o objeto alterado.

Exportação deve respeitar o filtro atual, a permissão do usuário e a política de retenção. A interface deve informar quando o resultado é parcial ou quando a exportação está sendo preparada.

## 6. Estados de interface obrigatórios

### Vazio inicial

Mensagem específica do recurso, por exemplo:

> “Nenhum usuário foi configurado neste tenant.”

A ação primária aparece somente para quem tem permissão. O estado vazio não deve parecer uma falha nem mostrar métricas zeradas como se fossem dados carregados.

### Busca sem resultado

> “Nenhum participante corresponde aos filtros atuais.”

Manter os filtros visíveis e oferecer “Limpar filtros”. Não substituir a consulta silenciosamente.

### Loading

- reservar a altura da tabela;
- anunciar “Carregando usuários…” em `role="status"`;
- não inventar números nem usar shimmer como única explicação;
- manter tenant e escopo visíveis.

### Erro recuperável

> “Não foi possível carregar os papéis. Seus filtros foram preservados. Tente novamente.”

O botão de nova tentativa deve ficar próximo da mensagem. Não apagar dados já carregados sem explicar se estão desatualizados.

### Conflito de versão

> “Esta configuração mudou desde que você abriu a página. Revise os valores atuais antes de salvar.”

Mostrar diferenças quando possível. Nunca sobrescrever silenciosamente uma alteração concorrente.

### Sem acesso

> “Você não tem permissão para acessar este recurso neste escopo.”

Não expor existência de dados, IDs sensíveis ou ações disponíveis para outro papel.

### Mutação em andamento

- manter a largura do botão;
- trocar o rótulo para “Salvando…” ou “Revogando convite…”;
- impedir duplicação;
- preservar os dados do formulário;
- anunciar o resultado em região de status.

### Ação destrutiva

Exibir alvo, escopo, consequência, reversibilidade, motivo exigido e ação final. A confirmação deve ser proporcional ao risco:

- arquivar: confirmação simples com resumo;
- revogar acesso: resumo do usuário, papel e escopo;
- excluir ou encerrar retenção: digitação do identificador e motivo, quando irreversível.

## 7. Densidade e composição visual

O Admin deve preservar a direção **Trilha de Evidências + Operação Precisa**:

- tabelas e filas compactas nas coleções;
- contexto documental no detalhe;
- divisórias e espaçamento para hierarquia, não cartões em excesso;
- uma cor de ação por tela;
- estados semânticos acompanhados de texto e ícone quando necessário;
- sem aparência hospitalar, sem gradiente decorativo e sem dashboard genérico de KPIs.

### Tokens obrigatórios

```css
:root {
  --bg: oklch(0.975 0.012 180);
  --surface: oklch(1 0 0);
  --fg: oklch(0.24 0.035 205);
  --muted: oklch(0.52 0.035 205);
  --border: oklch(0.87 0.025 190);
  --accent: oklch(0.52 0.09 185);
  --font-display: 'Literata', 'Charter', Georgia, serif;
  --font-body: 'Source Sans 3', system-ui, sans-serif;
  --font-mono: 'IBM Plex Mono', ui-monospace, monospace;
}
```

Uso:

- Literata em títulos de página e seções de contexto;
- Source Sans 3 em navegação, tabelas, formulários e mensagens;
- IBM Plex Mono em IDs, datas, versões, coordenadas e referências de auditoria;
- teal apenas para ação, seleção e conexão da Trilha de Evidências;
- `--border` somente como divisória decorativa, nunca como único indicador de foco, erro ou seleção.

### Geometria

- lateral de referência: 240 px;
- área de trabalho flexível com 24–32 px de margem;
- grid de 4 px, com ritmo recorrente de 8, 16, 24 e 32 px;
- alvos interativos de pelo menos 44 × 44 px;
- tabela com linha mínima de 48 px, expansível;
- formulário limitado a 65ch ou aproximadamente 720 px;
- sem scroll horizontal em 320 px; em telas pequenas, tabela vira registro empilhado com rótulos de campo;
- navegação lateral vira barra horizontal ou menu compacto abaixo de 920 px.

## 8. Acessibilidade e segurança de uso

Critérios obrigatórios para o PRD:

- HTML nativo: botão para ação, link para navegação, `details`/`summary` para complemento;
- ordem de foco igual à ordem de leitura;
- foco visível com anel de 3 px e contraste suficiente;
- nenhum estado depende apenas de cor;
- campos têm rótulo persistente;
- instruções e erros associados por `aria-describedby`;
- `aria-invalid` somente quando houver erro;
- `role="status"` para carregamento e conclusão;
- `role="alert"` para erro que exige atenção imediata;
- diálogos com nome, Escape, fechar acessível e retorno de foco;
- sem `tabindex` positivo;
- tabelas têm `caption`, cabeçalhos de coluna e associação semântica preservada no mobile;
- tenant, escopo e permissão atual são anunciados de modo compreensível;
- ações de alto impacto mostram consequência antes da confirmação;
- dados pessoais só aparecem no contexto permitido pelo perfil.

## 9. Critérios de aceite do PRD

### Arquitetura

- [ ] Admin e Dashboard têm shells e navegações distintas.
- [ ] A lateral segue os cinco grupos recomendados.
- [ ] O tenant e o escopo atual permanecem visíveis em todas as páginas administrativas.
- [ ] Existe saída contextual para o Dashboard sem misturar as duas hierarquias.

### Coleções

- [ ] Cada recurso principal tem busca, filtros, paginação e estado vazio específico.
- [ ] Ações em massa aparecem somente quando a seleção e a consequência são claras.
- [ ] A tabela mostra estado, escopo, última alteração e responsável.
- [ ] O detalhe pode ser aberto sem perder filtros ou contexto.

### Acesso

- [ ] Papéis, concessões diretas e herdadas são distinguíveis.
- [ ] O acesso efetivo explica a origem de cada permissão.
- [ ] Usuário sem permissão não vê dados fora do escopo.
- [ ] Mudanças de acesso geram evento de auditoria.

### Formulários e mutações

- [ ] Formulários preservam rascunho e informam alterações não salvas.
- [ ] Conflitos de versão não sobrescrevem dados silenciosamente.
- [ ] Ações destrutivas mostram alvo, escopo e consequência.
- [ ] Submissão duplicada é impedida durante loading.

### Auditoria

- [ ] Evento contém ator, ação, recurso, escopo, momento e resultado.
- [ ] Auditoria tem filtros e detalhe consultável.
- [ ] Exportação respeita filtros, permissão e retenção.
- [ ] Falhas e eventos negados também podem ser distinguidos.

### Responsividade e acessibilidade

- [ ] O Admin é utilizável em desktop de 1280 px ou mais.
- [ ] A experiência não cria scroll horizontal em 320 px.
- [ ] A navegação é operável por teclado.
- [ ] Foco, erro, seleção, loading e disabled têm contraste e texto suficientes.
- [ ] O layout funciona com zoom de 200%.

## 10. Decisões que não devem ser reabertas sem evidência

- Não transformar o Admin em uma segunda tela de métricas operacionais.
- Não usar navegação plana com todos os módulos no mesmo nível.
- Não esconder escopo de tenant, unidade ou segmento dentro de menus secundários.
- Não representar listas administrativas apenas com cards.
- Não tratar auditoria como uma timeline ornamental sem filtros.
- Não usar uma confirmação genérica para ações destrutivas.
- Não usar “acesso efetivo” como um cálculo invisível; a origem da permissão precisa ser explicada.

## 11. Fontes de mercado consultadas

- [Microsoft Entra admin center — visão geral e navegação por áreas](https://learn.microsoft.com/en-us/entra/fundamentals/entra-admin-center)
- [Microsoft Entra RBAC — atribuições, revisões de acesso e audit logs](https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/custom-overview)
- [Stripe Dashboard — navegação, configurações e equipe](https://docs.stripe.com/dashboard/basics)
- [Stripe — papéis de usuário e separação de permissões](https://docs.stripe.com/get-started/account/teams/roles?locale=pt-BR)
- [GitHub — revisão, filtros e exportação do audit log de organização](https://docs.github.com/en/organizations/keeping-your-organization-secure/managing-security-settings-for-your-organization/reviewing-the-audit-log-for-your-organization)
- [Atlassian Administration — atividade e audit log organizacional](https://support.atlassian.com/organization-administration/docs/understand-atlassian-access/)
- [Nielsen Norman Group — padrões de busca e filtros em portais empresariais](https://www.nngroup.com/reports/intranet-portals-experiences-real-life-projects/)

As fontes servem para identificar padrões recorrentes de consoles empresariais. A recomendação para Inspection é uma síntese própria, subordinada ao `brand-spec.md` e à necessidade de separar configuração/governança da operação de inspeções.
