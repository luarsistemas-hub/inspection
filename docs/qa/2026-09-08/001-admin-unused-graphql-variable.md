# Admin: consultas de Organização, Acessos e Auditoria retornam INTERNAL

## Severidade

Alta — três das seis consultas principais do Admin não funcionam.

## Como reproduzir

1. Entrar em `http://localhost:3000` com `admin` / `admin`.
2. Abrir Organização, Acessos ou Auditoria.
3. Clicar em **Consultar**.

## Atual

A tela mostra `internal error (INTERNAL)`.

`AdminContent` sempre declara `$search:String`, mas só usa essa variável nas
queries `participants` e `assets`. As operações `businessUnits`, `memberships`
e `auditEvents` são rejeitadas pela validação GraphQL por variável não usada.

## Esperado

As três conexões devem ser consultadas com paginação e mostrar o resultado
vazio ou os registros do tenant, sem erro interno.

## Critérios de aceite

- Montar a assinatura de variáveis de acordo com cada operação.
- Cobrir as seis seções do Admin em teste autenticado contra o schema real.
- Erros de validação GraphQL do cliente não devem ser classificados como falha interna do servidor.
