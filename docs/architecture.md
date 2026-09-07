# Arquitetura

O backend segue Vertical Slice Architecture: cada `internal/features/<domínio>/<operação>` possui entrada `Setup`, fluxo de aplicação, regras, persistência e testes. `internal/platform` contém preocupações transversais; `cmd` é o composition root.

```mermaid
flowchart LR
 B[Next.js/PWA] -->|OIDC PKCE| K[Keycloak]
 B -->|GraphQL| A[inspection-api]
 A --> M[Mediator]
 M --> D[(PostgreSQL + RLS)]
 M --> O[(Outbox)]
 O --> W[inspection-worker]
 W --> R[RabbitMQ]
 W --> S[(MinIO privado)]
 W --> L[LiteLLM]
 W --> P[Gotenberg]
 T[inspection-scheduler] --> D
 T --> M
```

A API registra GraphQL e `/healthz`, `/readyz`, `/metrics`. O worker executa dispatcher e consumer em background. O scheduler materializa agendas, lembretes e retenção a cada minuto. Contexto carrega tenant, principal, correlação e idempotência até as transações.
