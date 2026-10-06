# Plano de melhoria do deploy em produção

Data da avaliação: 5 de outubro de 2026  
Revisão de referência: `96f7e6ed973e0d1711dd6226f9ea5b7749cdafd7`  
Status: proposta; melhorias ainda não implementadas por este documento.

## Objetivo

Tornar o fluxo de publicação previsível e recuperável, corrigindo problemas de migrations, rollback, estado, concorrência e proteção das credenciais. Preservar as partes que já funcionam: imagens por digest, plano identificável por hash, recibo da composição, WireGuard, verificação estrita da identidade SSH e execução supervisionada.

O deploy atual funcionou, mas isso não comprova os caminhos de recuperação. A prioridade é estabilizar a arquitetura existente antes de ampliar sua complexidade.

## Escopo e evidências da avaliação

A avaliação incluiu leitura do código, histórico Git, consultas à API do GitHub e consultas somente de leitura à VM. Não foram executados testes de falha, rollback ou restauração durante a avaliação.

Estado observado em 5 de outubro de 2026:

- Deployment: `deploy-37358083515-1`.
- Commit publicado: `96f7e6ed973e0d1711dd6226f9ea5b7749cdafd7`.
- 12 serviços em execução: 11 com healthcheck saudável e Caddy em execução sem healthcheck.
- Nenhum bloqueio de deployment ativo.
- [Workflow de publicação concluído com sucesso](https://github.com/luarsistemas-hub/inspection/actions/runs/37358083515).
- Environment `production` sem revisores obrigatórios ou política de branches.
- API da `main` retornou ausência de branch protection; a consulta de rulesets não listou regras.
- Releases consultadas com `immutable: false`.
- Uma cópia temporária de configuração em `/etc/inspection` e outra em `/run`, sem leitura de seus conteúdos.

Essas observações representam aquele momento. Reconsultar as configurações antes de implementar mudanças.

## Prioridades

| Prioridade | Significado |
| --- | --- |
| P1 | Corrigir antes de confiar em migrations, recuperação e publicações frequentes. |
| P2 | Corrigir na sequência para reduzir falhas operacionais e divergências. |
| P3 | Melhorar manutenção, documentação e eficiência após estabilizar os caminhos críticos. |

## Fase 1 — Integridade do deploy e recuperação

### DEP-01 — Calcular migrations e alvos contra produção

**Prioridade:** P1.

**Problema:** o build compara o código com o último candidato construído. Esse candidato pode nunca ter sido publicado. A decisão de executar migrations e os alvos sugeridos podem ignorar mudanças que ainda não chegaram à produção. Selecionar `all` não corrige a decisão de migration.

**Cenário:** produção está em A; B adiciona uma migration e é construído sem publicação; C altera outro arquivo. C pode registrar `databaseChange: none` ao comparar com B, embora publicar C sobre A ainda exija a migration.

**Ações:**

- [ ] Separar seleção de imagens para build da seleção de mudanças para publicação.
- [ ] Calcular o plano de publicação a partir da composição ativa e da versão real do schema.
- [ ] Registrar a origem e a compatibilidade dos componentes de uma composição mista.
- [ ] Recusar planos cuja base ativa tenha mudado desde o planejamento.
- [ ] Definir tratamento explícito para compatibilidade desconhecida, sem convertê-la implicitamente em ausência de migration.

**Critérios de conclusão:**

- Construir candidatos intermediários sem publicá-los não faz uma migration necessária desaparecer do plano.
- A opção `changed` inclui mudanças pendentes em relação à produção.
- O plano apresenta a base ativa, o schema observado e as operações necessárias.

**Referências:** `.github/workflows/build-images.yml`, `.github/workflows/publish.yml`, `deploy/oci/planner.py`.

### DEP-02 — Verificar compatibilidade antes de rollback

**Prioridade:** P1.

**Problema:** o executor antigo fazia schema-check antes de parar serviços no rollback. O fluxo novo define `runMigration: false` e executa schema-check somente dentro do bloco de migration. Além disso, combina imagens antigas com o bundle da `main` atual.

**Ações:**

- [ ] Registrar a versão de configuração e a compatibilidade de schema da composição.
- [ ] Verificar as imagens de destino contra o banco atual antes de interromper serviços.
- [ ] Usar uma configuração compatível com a composição de destino, explicitamente identificada no plano.
- [ ] Validar compatibilidade entre componentes em rollback seletivo.
- [ ] Preservar a regra de não executar downgrade automático do banco.

**Critérios de conclusão:**

- Rollback incompatível é recusado antes de parar qualquer serviço.
- O plano identifica imagens, configuração e limites de compatibilidade de destino.
- Um rollback compatível restaura os componentes selecionados e produz um novo recibo verificável.

**Referências:** `deploy/oci/planner.py`, `deploy/oci/host/apply-plan.sh`, `deploy/oci/host/deploy-release.sh`, `.github/workflows/publish.yml`.

### DEP-03 — Respeitar bloqueios de recuperação

**Prioridade:** P1.

**Problema:** o executor cria `deployment-blocked`, mas não verifica bloqueios anteriores na entrada. Uma publicação somente de `operations`, sem migration ou smoke, pode promover o recibo e remover um bloqueio anterior sem resolver sua causa.

**Ações:**

- [ ] Recusar publicação normal quando existir recuperação pendente.
- [ ] Definir uma operação explícita de recuperação, com evidências adequadas à fase que falhou.
- [ ] Guardar e restaurar o estado anterior do bloqueio.
- [ ] Após recuperação automática bem-sucedida, remover apenas o bloqueio criado pela operação recuperada.
- [ ] Registrar causa, fase, deployment e resultado da recuperação.

**Critérios de conclusão:**

- Um plano somente de `operations` não consegue apagar um bloqueio de migration.
- Falha após início de migration exige recuperação validada.
- Recuperação automática bem-sucedida não deixa um bloqueio residual indevido.

**Referência:** `deploy/oci/host/apply-plan.sh`.

### DEP-04 — Corrigir status e persistir o resultado da execução

**Prioridade:** P1.

**Problema:** o classificador considera `Result=success` e `ExecMainStatus=0` suficientes para sucesso. Em uma unidade `Type=oneshot`, esses valores podem existir enquanto o serviço ainda está em `activating`. A conferência posterior do recibo no workflow é uma proteção adicional, mas não corrige o status informado.

**Ações:**

- [ ] Interpretar corretamente os estados de uma unidade oneshot, incluindo `activating`, `active/exited`, falha e unidade ausente.
- [ ] Confirmar término antes de informar sucesso.
- [ ] Conferir identidade e hash do recibo correspondente.
- [ ] Persistir o resultado por deployment, independentemente da existência da unidade transitória.
- [ ] Expor fase, timestamps e diagnóstico sanitizado para o workflow e a CLI.

**Critérios de conclusão:**

- Job aguardando lock ou executando nunca informa sucesso.
- Unidade inexistente nunca informa sucesso.
- Resultado final continua consultável após reboot.
- CLI e Actions interpretam os mesmos estados.

**Referências:** `deploy/oci/host/deployment-status.sh`, `deploy/oci/host/run-deployment.sh`, `.github/workflows/publish.yml`.

### DEP-05 — Tornar promoção e recuperação consistentes

**Prioridade:** P1.

**Problema:** `compose.env`, recibo e symlink são promovidos em passos separados. Uma falha intermediária pode deixar metadados divergentes dos serviços efetivamente executados. O segundo `trap EXIT` também substitui a limpeza inicial, deixando cópias temporárias de segredos.

**Ações:**

- [ ] Agrupar o estado de uma composição em diretório versionado e promover um ponteiro atômico, quando aplicável.
- [ ] Registrar transições antes e depois de mudanças externas, como recriação dos containers.
- [ ] Definir recuperação para cada ponto de interrupção, incluindo a fase de promoção.
- [ ] Unificar cleanup e recuperação em um único handler de saída.
- [ ] Limpar temporários sensíveis em sucesso e falha, preservando somente evidências sanitizadas.
- [ ] Remover temporários antigos após confirmar que não pertencem a uma operação ativa.

**Critérios de conclusão:**

- Falhas entre etapas de promoção deixam um estado detectável e recuperável.
- Recibo, configuração e composição efetiva são reconciliados antes de declarar sucesso.
- Não permanecem cópias temporárias de segredos ao término da operação.

**Referência:** `deploy/oci/host/apply-plan.sh`.

## Fase 2 — Proteções e confiança

### DEP-06 — Aplicar proteções reais no GitHub

**Prioridade:** P1.

**Problema:** proteções previstas no README não estavam configuradas. O build manual verifica ancestralidade na `main`, mas não exige CI aprovada para o SHA selecionado.

**Ações:**

- [ ] Proteger a `main` e exigir os checks definidos para publicação.
- [ ] Restringir o Environment `production` à origem autorizada.
- [ ] Configurar revisão de publicação conforme a política do projeto, incluindo a decisão sobre autoaprovação.
- [ ] Verificar CI aprovada para o SHA exato também no caminho manual.
- [ ] Habilitar imutabilidade das releases e conferir seu efeito sobre assets e tags.
- [ ] Documentar exceções operacionais e como ficam registradas.

**Critérios de conclusão:**

- SHA sem a CI exigida não gera publicação normal, inclusive por dispatch manual.
- As proteções consultadas pela API correspondem à política documentada.
- Assets usados para planejar um deploy aprovado não podem ser substituídos silenciosamente.

**Referências:** `.github/workflows/build-images.yml`, `.github/workflows/publish.yml`, `deploy/oci/readme.md`.

### DEP-07 — Definir a fronteira de confiança do executor privilegiado

**Prioridade:** P1.

**Problema:** a conta SSH limita comandos, mas pode enviar scripts que serão instalados e executados como root. O cliente fornece tanto o bundle quanto o hash do plano; isso verifica integridade interna, mas não comprova aprovação por uma autoridade independente.

**Ações:**

- [ ] Tratar a credencial de deploy como credencial administrativa enquanto esse modelo permanecer.
- [ ] Escolher entre executor privilegiado estável com planos declarativos ou bundles assinados por identidade confiável independente do cliente SSH.
- [ ] Verificar origem e autorização antes de instalar ou executar scripts do bundle.
- [ ] Restringir atualizações do próprio executor a um fluxo explícito.
- [ ] Documentar rotação e revogação das credenciais.
- [ ] Confirmar a rotação da chave privada WireGuard compartilhada na conversa; substituir se ainda estiver ativa.

**Critérios de conclusão:**

- Posse isolada da chave SSH não permite instalar scripts arbitrários como root sem a autorização adicional definida.
- A documentação descreve com precisão os privilégios efetivos da conta.
- Nenhuma chave privada é incluída em código, relatórios ou logs.

**Referências:** `deploy/oci/host/stage-and-apply.sh`, `deploy/oci/host/install-host-tools.sh`, `deploy/oci/host/setup-deploy-user.sh`.

## Fase 3 — Contrato operacional único

### DEP-08 — Unificar protocolo da CLI e do GitHub Actions

**Prioridade:** P2.

**Problema:** o workflow foi corrigido para enviar `bundle.tgz`, mas `ops.sh apply` ainda envia `source-bundle.tgz`, incompatível com o receptor. Há duplicação de empacotamento, polling e tratamento de erros.

**Ações:**

- [ ] Compartilhar a definição e a implementação do formato de upload.
- [ ] Padronizar os nomes dos arquivos internos do pacote.
- [ ] Aplicar a mesma validação de recibo na CLI e no workflow.
- [ ] Definir versão do protocolo e mensagem explícita para incompatibilidade.

**Critérios de conclusão:** os dois clientes conseguem aplicar o mesmo plano e bundle em ambiente de teste e conferem o mesmo recibo final.

**Referências:** `deploy/oci/ops.sh`, `.github/workflows/publish.yml`, `deploy/oci/host/stage-and-apply.sh`.

### DEP-09 — Fixar a versão do executor por operação

**Prioridade:** P2.

**Problema:** o staging troca ferramentas globais e libera o lock antes de iniciar a unidade. Existe uma janela em que outro pedido pode trocar o symlink global antes de o primeiro job adquirir o lock.

**Ações:**

- [ ] Executar os scripts pelo caminho imutável da versão aprovada.
- [ ] Remover dependência do symlink global durante uma operação.
- [ ] Definir comportamento de fila ou rejeição para pedidos concorrentes.
- [ ] Incluir publicações pela CLI no mesmo mecanismo de serialização.

**Critérios de conclusão:** dois pedidos concorrentes não misturam versões de scripts ou configuração, e cada resultado identifica o executor utilizado.

**Referências:** `deploy/oci/host/stage-and-apply.sh`, `deploy/oci/host/run-deployment.sh`, `deploy/oci/host/install-host-tools.sh`.

### DEP-10 — Identificar estado ativo e tornar sincronização idempotente

**Prioridade:** P2.

**Problemas:**

- O fluxo ordena releases por `createdAt`, que representa a data do commit, não da publicação.
- Rebuilds do mesmo SHA podem empatar; a listagem limitada também não é uma fonte robusta de identidade do estado ativo.
- A sincronização tenta criar novamente `state-<deployment-id>` quando esse registro já existe.
- Uma publicação pode terminar na VM e falhar antes de registrar seu recibo no GitHub.

**Ações:**

- [ ] Usar a identidade explícita do estado ativo, conciliada com o recibo da VM.
- [ ] Quando necessária, usar cronologia de publicação com desempate e paginação.
- [ ] Ao sincronizar um registro existente, comparar conteúdo: concluir se igual e rejeitar divergência.
- [ ] Permitir recuperar o registro de um deploy concluído sem reaplicar os containers.
- [ ] Registrar referências de configuração e componentes suficientes para reconstruir a composição.

**Critérios de conclusão:** repetição da sincronização é segura; rebuilds do mesmo SHA não alteram a escolha da base; falha de registro no GitHub é recuperável sem novo deploy.

**Referências:** `.github/workflows/publish.yml`, `.github/workflows/build-images.yml`, `.github/workflows/sync-production-state.yml`, `deploy/oci/host/export-state.sh`.

### DEP-11 — Limitar esperas e melhorar diagnóstico

**Prioridade:** P2.

**Ações:**

- [ ] Definir `ConnectTimeout`, keepalive e limites por chamada SSH.
- [ ] Definir timeout total dos jobs e tratamento de resposta vazia ou inválida no polling.
- [ ] Distinguir falha do túnel, conexão SSH, autenticação, rejeição do pacote e execução remota.
- [ ] Publicar evidências sanitizadas mesmo quando o job falhar.
- [ ] Orientar consulta e reconciliação após perda de conexão antes de tentar novamente.

**Critérios de conclusão:** cada fase possui espera limitada e uma falha aponta o deployment e o diagnóstico necessário, sem expor segredos.

**Referências:** `.github/workflows/publish.yml`, `.github/workflows/sync-production-state.yml`, `deploy/oci/ops.sh`.

### DEP-12 — Separar publicação do executor e manutenção de infraestrutura

**Prioridade:** P2.

**Problemas:** mudanças somente no executor ou planner podem não gerar candidato automático. Os digests ativos de PostgreSQL, RabbitMQ, Dragonfly e Caddy são preservados pelo planner, portanto alterar o lock não atualiza esses serviços pelo fluxo normal.

**Ações:**

- [ ] Versionar o bundle de deploy sem exigir reconstrução desnecessária das imagens da aplicação.
- [ ] Oferecer caminho explícito para promover correções do executor.
- [ ] Criar procedimento de manutenção de infraestrutura com compatibilidade, backup e janela de manutenção.
- [ ] Definir como alterações de Compose e Caddy serão aplicadas e registradas.
- [ ] Evitar que o procedimento de infraestrutura dependa do executor legado.

**Critérios de conclusão:** correção apenas de deploy chega à VM pelo fluxo documentado; atualização de infraestrutura tem um plano específico e não ocorre acidentalmente em publicação de aplicação.

**Referências:** `deploy/oci/components.json`, `deploy/oci/planner.py`, `deploy/oci/base-images.lock`, `deploy/oci/compose.yaml`.

## Fase 4 — Verificação, continuidade e operação

### DEP-13 — Cobrir o executor novo com testes de falha

**Prioridade:** P1 para cobertura dos itens críticos; execução incremental junto às fases anteriores.

**Problema:** os testes de recuperação existentes exercitam principalmente `deploy-release.sh`, o executor antigo. A validação `bash -n arquivo1 arquivo2 ...` verifica somente o primeiro script.

**Ações:**

- [ ] Executar `bash -n` individualmente para cada script.
- [ ] Cobrir o protocolo entre cliente, staging, supervisor, executor e status.
- [ ] Manter os cenários isolados de produção, com serviços simulados ou ambiente descartável.
- [ ] Adicionar uma integração com systemd real em ambiente de teste para o ciclo de vida das unidades.

**Matriz mínima de cenários:**

| Cenário | Resultado esperado |
| --- | --- |
| Candidato intermediário não publicado contém migration | Plano final ainda exige a migration. |
| Rollback incompatível com schema | Rejeição antes da parada dos serviços. |
| Bloqueio anterior e plano somente de operations | Publicação normal recusada. |
| Job aguardando lock | Status em execução ou espera; nunca sucesso. |
| Unidade inexistente | Status desconhecido ou ausente; nunca sucesso. |
| Bundle com nome, conteúdo ou hash inválido | Rejeição antes de ativar ferramentas. |
| Dois pedidos concorrentes | Serialização ou rejeição sem mistura de versões. |
| Falha durante migration | Bloqueio mantido e recuperação explícita exigida. |
| Falha de smoke sem migration | Recuperação coerente dos serviços e metadados anteriores. |
| Falha entre passos da promoção | Estado recuperável sem recibo enganoso. |
| Conexão interrompida | Consulta posterior encontra o resultado correto. |
| Repetição da sincronização | Sucesso idempotente ou divergência explicitamente rejeitada. |
| Encerramento em sucesso ou falha | Temporários sensíveis removidos. |

**Referências:** `deploy/oci/tests/`, `.github/workflows/ci.yml`.

### DEP-14 — Reorganizar E2E e smoke funcional

**Prioridade:** P2.

A retirada dos E2E longos do bloqueio principal foi autorizada. O smoke atual verifica saúde e HTTP, mas não comprova fluxos autenticados do produto. Os E2E do design system permanecem na CI.

**Ações:**

- [ ] Manter a CI principal curta e determinística.
- [ ] Criar smoke funcional pequeno em ambiente de teste para login, autorização e uma jornada essencial.
- [ ] Executar jornadas completas em workflow manual ou agendado.
- [ ] Incluir convite e upload autenticado na cobertura adequada.
- [ ] Preservar o Playwright existente e separar falhas de ambiente de regressões funcionais.
- [ ] Atualizar a documentação para refletir os checks realmente obrigatórios.

**Critérios de conclusão:** existe evidência funcional além de HTTP saudável, e a suíte longa pode ser executada sem bloquear indiscriminadamente toda publicação.

### DEP-15 — Definir e ensaiar backup e restauração

**Prioridade:** P1 antes de depender do ambiente para dados cuja perda não seja aceitável.

**Contexto:** o README declara uma VM sem failover e sem backup. Não foi confirmada a existência de solução externa. Rollback de imagens não recupera banco ou documentos perdidos.

**Ações:**

- [ ] Inventariar eventual backup externo existente.
- [ ] Definir perda de dados e tempo de indisponibilidade aceitáveis.
- [ ] Definir backup independente do PostgreSQL e estratégia de recuperação dos objetos.
- [ ] Definir retenção, proteção contra exclusão, acesso e custos.
- [ ] Ensaiar restauração em ambiente separado e registrar duração e resultado.
- [ ] Documentar recuperação da VM, configuração e credenciais necessárias.

**Critérios de conclusão:** uma restauração ensaiada recupera dados e serviço dentro das metas definidas. Snapshots no mesmo volume não contam como proteção contra perda desse volume.

**Referência:** `deploy/oci/readme.md`.

### DEP-16 — Reduzir e documentar indisponibilidade

**Prioridade:** P2.

**Problema:** o executor para todos os serviços selecionados antes de recriá-los, mesmo sem migration.

**Ações:**

- [ ] Medir e documentar a janela de indisponibilidade por tipo de publicação.
- [ ] Reduzir a seleção de serviços interrompidos ao conjunto necessário.
- [ ] Avaliar atualizações compatíveis sem parada coordenada quando possível.
- [ ] Caso haja requisito de continuidade, planejar réplicas ou troca entre duas versões, considerando recursos e custos.

**Critérios de conclusão:** o plano informa o impacto esperado e existe uma meta de indisponibilidade verificável. Não declarar deploy sem interrupção enquanto a arquitetura não oferecer isso.

**Referência:** `deploy/oci/host/apply-plan.sh`.

## Fase 5 — Manutenção e encerramento da transição

### DEP-17 — Revisar alterações adjacentes e documentação

**Prioridade:** P2 para compatibilidade e proteção funcional; P3 para limpeza.

- [ ] Validar compatibilidade S3 do RustFS local com as operações usadas pelo produto.
- [ ] Documentar que o volume RustFS novo não migra automaticamente os objetos do antigo MinIO; definir migração ou reset coerente com o banco local.
- [ ] Revisar separadamente a mudança de limite de envio OTP, de 5/hora para 20/10 minutos. Registrar justificativa funcional e impacto de abuso; não tratá-la como requisito de deploy.
- [ ] Remover do versionamento o executável gerado `inspection-worker` e o `.pyc`, e ajustar o ignore.
- [ ] Atualizar o README sobre CI, E2E, proteções, conta SSH, rollback e comandos disponíveis.
- [ ] Manter a solução localizada do campo `tags` no formulário OCI, preservando o default Terraform e documentando como fornecer valores personalizados.
- [ ] Desativar o workflow e comandos legados de publicação após ensaiar recuperação no fluxo novo.

**Critérios de conclusão:** existe um caminho oficial de publicação e recuperação; documentação e configuração real estão alinhadas; mudanças funcionais adjacentes têm justificativa própria.

**Referências:** `deploy/docker-compose.yml`, `services/inspection/internal/platform/ratelimit/limiter.go`, `deploy/oci/runtime/schema.yaml`, `.github/workflows/oci-images.yml`, `deploy/oci/readme.md`.

## Correções que devem ser preservadas

| Correção ou mecanismo | Diretriz |
| --- | --- |
| Buildx para gerar attestations | Manter. |
| Inputs em `RUNNER_TEMP` antes do checkout | Manter. |
| Nome correto do bundle no workflow | Manter e aplicar também à CLI. |
| Supervisão pelo systemd | Manter, corrigindo status e persistência. |
| Imagens por digest e plano por hash | Manter, complementando com verificação de origem. |
| Recibo da composição aplicada | Manter e enriquecer para recuperação. |
| WireGuard e host key SSH estrita | Manter, com diagnóstico e rotação. |
| Default Terraform para tags | Manter a correção localizada do formulário. |

## Sequência recomendada de execução

1. Resolver DEP-01 a DEP-05, adicionando a cobertura correspondente de DEP-13.
2. Aplicar DEP-06 e definir a fronteira de confiança de DEP-07.
3. Unificar clientes, concorrência, estado e diagnóstico: DEP-08 a DEP-12.
4. Ensaiar rollback e recuperação em ambiente de teste; concluir DEP-13.
5. Implementar e ensaiar backup conforme a criticidade dos dados: DEP-15.
6. Completar smoke funcional e metas de indisponibilidade: DEP-14 e DEP-16.
7. Atualizar documentação, revisar mudanças adjacentes e encerrar o legado: DEP-17.

Configurações de proteção e planejamento de backup podem avançar junto às correções de código. A desativação do caminho legado depende de recuperação demonstrada no caminho novo.

## Conclusão do plano

Considerar o fluxo estabilizado quando:

- [ ] Migrations são determinadas pelo estado real de produção.
- [ ] Rollback incompatível falha antes de afetar serviços.
- [ ] Status e recibo só confirmam uma operação efetivamente concluída.
- [ ] Falhas e interrupções deixam um estado recuperável e auditável.
- [ ] CLI e Actions seguem o mesmo protocolo.
- [ ] Proteções reais do GitHub correspondem à política documentada.
- [ ] Testes exercitam o executor novo e seus caminhos de falha.
- [ ] Backup e restauração atendem à criticidade acordada para os dados.
- [ ] Existe um único procedimento oficial, documentado e ensaiado.

## Fontes complementares

- [GitHub REST API — semântica de `created_at` nas releases](https://docs.github.com/en/rest/releases/releases#get-the-latest-release).
- [systemd — ciclo de vida e configuração de serviços](https://github.com/systemd/systemd/blob/main/man/systemd.service.xml).
