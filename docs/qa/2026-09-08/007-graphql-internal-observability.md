# API: erros GraphQL INTERNAL não registram causa e correlation ID

## Severidade

Média — aumenta significativamente o tempo de diagnóstico em produção.

## Evidência

O erro `internal error (INTERNAL)` foi reproduzido no Admin. Os logs recentes
da API não continham a operação, a causa original nem o correlation ID. O anexo
original também continha apenas inicialização e health checks.

## Esperado

Falhas internas devem continuar genéricas para o cliente, mas registrar no
servidor a causa, operação e correlation ID, sem dados sensíveis.

## Critérios de aceite

- Log estruturado no error presenter/recoverer para `INTERNAL`.
- Incluir operation name e correlation ID.
- Não registrar tokens, OTPs, payloads de imagem ou dados pessoais.
- Teste que confirme mensagem pública genérica e log interno correlacionável.
