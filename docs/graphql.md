# GraphQL, autenticação e primeiro acesso

Endpoint: `POST http://localhost:8080/graphql`. Em local, `GET` também é permitido. Use `Authorization: Bearer <token>`, `X-Correlation-ID` opcional e `clientMutationId`/`Idempotency-Key` nas operações que exigem idempotência.

Após login no Keycloak local (`admin` / `admin`), a mutation abaixo provisiona tenant, unidade inicial e membership `TENANT_ADMIN`:

```graphql
mutation Bootstrap($input: CreateTenantInput!) {
  createTenant(input: $input) {
    tenant { id name status version }
    userErrors { field message code }
    clientMutationId
  }
}
```

```sh
curl -s http://localhost:8080/graphql -H 'content-type: application/json' -H "authorization: Bearer $TOKEN" --data '{"query":"mutation($input:CreateTenantInput!){createTenant(input:$input){tenant{id name status version} userErrors{field message code} clientMutationId}}","variables":{"input":{"name":"Minha operação","businessUnitCode":"MATRIZ","businessUnitName":"Matriz","clientMutationId":"bootstrap-001"}}}'
```

Consultas de conexão aceitam `first` e `after`, retornando `nodes` e `pageInfo`. Erros de autenticação/autorização aparecem como `UNAUTHENTICATED`/`FORBIDDEN`; conflitos usam `CONFLICT`; detalhes internos não são expostos. O schema completo está em [`services/inspection/schema.graphqls`](../services/inspection/schema.graphqls).
