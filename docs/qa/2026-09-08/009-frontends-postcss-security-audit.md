# [P1] Atualizar a árvore do Next.js/PostCSS para eliminar falha de segurança nos frontends

## Contexto

A verificação oficial para no `npm audit --audit-level=high` de `apps/admin`.
A confirmação isolada em `apps/dashboard` e `apps/capture` apresenta a mesma
falha. Os três produtos usam Next.js `15.5.25`, cuja árvore instala uma versão
vulnerável de `postcss`. O audit reporta XSS na serialização de CSS e variantes
de leitura de arquivos/path traversal por `sourceMappingURL` controlado.

## Reprodução

1. Entre em qualquer um de `apps/admin`, `apps/dashboard` ou `apps/capture`.
2. Execute `npm ci`.
3. Execute `npm audit --audit-level=high`.

## Resultado atual

O comando termina com código `1`, uma vulnerabilidade alta e uma moderada. A
correção automática sugerida pelo npm exige `--force` e migração para Next.js
`16.3.4`, portanto não deve ser aplicada sem validar compatibilidade.

## Resultado esperado

O audit não deve encontrar vulnerabilidade alta, e a atualização não pode
regredir build, SSR, autenticação nem os fluxos E2E do Admin.

## Critérios de aceite

- Atualizar Next.js/PostCSS para versões sem os advisories reportados.
- Revisar as mudanças incompatíveis da versão escolhida, sem usar
  `npm audit fix --force` cegamente.
- `npm audit --audit-level=high` retorna código `0` nos três frontends.
- Codegen, lint, testes unitários, build e E2E continuam passando.
