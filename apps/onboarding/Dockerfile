FROM node:22-alpine AS design-system
WORKDIR /workspace/packages/inspection-design-system
COPY packages/inspection-design-system/package.json packages/inspection-design-system/package-lock.json ./
RUN npm ci
COPY packages/inspection-design-system ./
RUN npm run build

FROM design-system AS design-system-runtime
RUN npm prune --omit=dev

FROM node:22-alpine AS build
WORKDIR /workspace/apps/onboarding
COPY apps/onboarding/package.json apps/onboarding/package-lock.json ./
COPY --from=design-system /workspace/packages/inspection-design-system /workspace/packages/inspection-design-system
RUN npm ci
COPY apps/onboarding ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
COPY scripts/prepare-design-system.mjs ../../scripts/prepare-design-system.mjs
RUN npm run build && npm prune --omit=dev && rm -rf node_modules/@inspection/design-system && mkdir -p node_modules/@inspection/design-system && cp /workspace/packages/inspection-design-system/package.json node_modules/@inspection/design-system/ && cp -R /workspace/packages/inspection-design-system/dist node_modules/@inspection/design-system/

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3004
COPY --from=build /workspace/apps/onboarding/.next/standalone ./
COPY --from=build /workspace/apps/onboarding/.next/static ./.next/static
COPY --from=build /workspace/apps/onboarding/node_modules/@inspection/design-system ./node_modules/@inspection/design-system
COPY --from=design-system-runtime /workspace/packages/inspection-design-system/node_modules ./node_modules
EXPOSE 3004
CMD ["node", "server.js"]
