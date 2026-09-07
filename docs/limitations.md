# Limitações conhecidas

- O código ativo está em `services/inspection`; `internal/platform/graphql` na raiz é residual e não é importado pelos executáveis.
- LiteLLM, Twilio e algumas respostas de provider são WireMock locais. Eles validam contratos, não comportamento ou disponibilidade de fornecedores reais.
- O worker e o scheduler não expõem uma API de negócio; seus endpoints operacionais são úteis apenas para liveness/readiness.
- `readyz` não verifica RabbitMQ, Dragonfly, MinIO, Mailpit, LiteLLM ou Gotenberg.
- Testes de integração e Playwright precisam de infraestrutura local e fixtures; o harness externo não é substituído pelos testes unitários.
- Não existe manifesto de deploy de produção neste repositório. Segredos e credenciais do Compose são somente para desenvolvimento.
