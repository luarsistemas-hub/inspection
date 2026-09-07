# Frontend e captura

`apps/web` é Next.js 15 com React 19. O dashboard administra tenant, unidades, acessos, participantes, catálogos, ativos, agendas, projetos, relatórios, triagem, notificações, uso e retenção. A captura fica em `/capture/[linkToken]`, usa IndexedDB para rascunhos e upload multipart para URLs assinadas.

O login usa Authorization Code + PKCE S256. O verifier fica associado ao `state`; o callback troca o código no Keycloak e mantém o access token em memória. A API valida issuer, audience, JWKS e membership. A captura usa cookie `inspection_external` e `X-CSRF-Token`.

O service worker cacheia somente o shell público. CSP, `frame-ancestors`, `X-Frame-Options` e `nosniff` são aplicados em `middleware.ts`/`next.config.ts`. O storage permanece privado; o browser recebe apenas URLs presigned.

No host, variáveis `NEXT_PUBLIC_*` apontam para localhost. No Docker elas são argumentos de build públicos; nomes como `inspection-api` e `keycloak` ficam somente na rede interna.
