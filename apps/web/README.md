# Aplicação web

Next.js 15/React 19 com dashboard administrativo e jornada de captura. O schema usado no codegen está em `../../services/inspection/schema.graphqls`.

```sh
npm ci
npm run dev
npm run codegen:check
npm run lint
npm run test
npm run build
npx playwright install chromium webkit
npm run test:e2e
```

Configure somente endpoints públicos em `apps/web/.env` ou exporte `NEXT_PUBLIC_*` de `.env.inspection`. Tokens não são persistidos no browser.
