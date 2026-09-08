FROM node:22-alpine AS design-system
WORKDIR /workspace/packages/inspection-design-system
COPY packages/inspection-design-system/package.json packages/inspection-design-system/package-lock.json ./
RUN npm ci
COPY packages/inspection-design-system ./
RUN npm run build

FROM node:22-alpine AS build
WORKDIR /workspace/apps/capture
COPY apps/capture/package.json apps/capture/package-lock.json ./
COPY --from=design-system /workspace/packages/inspection-design-system /workspace/packages/inspection-design-system
RUN npm ci
COPY apps/capture ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
RUN npm run build && npm prune --omit=dev

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3003
COPY --from=build /workspace/apps/capture/.next/standalone ./
COPY --from=build /workspace/apps/capture/.next/static ./.next/static
COPY --from=build /workspace/apps/capture/public ./public
EXPOSE 3003
CMD ["node", "server.js"]
