FROM node:22-alpine AS build
WORKDIR /workspace/apps/capture
COPY apps/capture/package.json apps/capture/package-lock.json ./
RUN npm ci
COPY apps/capture ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
RUN npm run build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production PORT=3003
COPY --from=build /workspace/apps/capture/.next/standalone ./
COPY --from=build /workspace/apps/capture/.next/static ./.next/static
EXPOSE 3003
CMD ["node", "server.js"]
