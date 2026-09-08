FROM node:22-alpine AS build
WORKDIR /workspace/apps/dashboard
COPY apps/dashboard/package.json apps/dashboard/package-lock.json ./
RUN npm ci
COPY apps/dashboard ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
RUN npm run build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production PORT=3002
COPY --from=build /workspace/apps/dashboard/.next/standalone ./
COPY --from=build /workspace/apps/dashboard/.next/static ./.next/static
EXPOSE 3002
CMD ["node", "server.js"]
