# Documentação

Documentos baseados no código atual do Inspection. Stubs WireMock, Mailpit e Gotenberg são contratos locais determinísticos, não integrações de produção.

- [Arquitetura](architecture.md): processos, slices, mediator e integrações.
- [Funcionalidades](features.md): domínios e operações.
- [GraphQL](graphql.md): autenticação, primeiro tenant, paginação e erros.
- [Banco](database.md): schemas, RLS, roles e migrations.
- [Mensageria](messaging.md): eventos, outbox/inbox e worker.
- [Frontend](frontend.md): dashboard, captura, OIDC/PKCE e PWA.
- [Configuração](configuration.md): variáveis e diferenças host/Compose.
- [Desenvolvimento](development.md): codegen, testes e CI.
- [Operação](operations.md): checks e diagnóstico.
- [Observabilidade de LLM](llm-observability.md): eventos, métricas Prometheus, scrape e consultas.
- [Limitações](limitations.md): escopo dos stubs, artefatos residuais e lacunas conhecidas.
- [Uso do design system](design-system/usage.md): padrões comuns, limites de domínio e exceções de composição.
- [Inventário de migração](design-system/migration-inventory.json): rotas, estados, responsáveis e evidências da migração atual.
- [Evidência de acessibilidade](design-system/accessibility-evidence.md): registro executável de critérios WCAG e avaliações manuais.
- [Contrato machine-readable de acessibilidade](design-system/accessibility-evidence.json): 55 critérios e seus resultados atuais.
