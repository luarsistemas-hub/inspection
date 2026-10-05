FROM node:22-alpine AS design-system
WORKDIR /workspace/packages/inspection-design-system
COPY packages/inspection-design-system/package.json packages/inspection-design-system/package-lock.json ./
RUN npm ci
COPY packages/inspection-design-system ./
COPY scripts/prepare-design-system.mjs /workspace/scripts/prepare-design-system.mjs
RUN node ../../scripts/prepare-design-system.mjs

FROM design-system AS design-system-runtime
RUN npm prune --omit=dev

FROM node:22-alpine AS build
WORKDIR /workspace/apps/dashboard
COPY apps/dashboard/package.json apps/dashboard/package-lock.json ./
COPY --from=design-system /workspace/packages/inspection-design-system /workspace/packages/inspection-design-system
RUN npm ci
ARG NEXT_PUBLIC_INSPECTION_API_URL
ARG NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ARG NEXT_PUBLIC_OIDC_TOKEN_URL
ARG NEXT_PUBLIC_OIDC_CLIENT_ID
ARG NEXT_PUBLIC_ADMIN_URL
ARG NEXT_PUBLIC_STORAGE_URL
ENV NEXT_PUBLIC_INSPECTION_API_URL=$NEXT_PUBLIC_INSPECTION_API_URL
ENV NEXT_PUBLIC_OIDC_AUTHORIZE_URL=$NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ENV NEXT_PUBLIC_OIDC_TOKEN_URL=$NEXT_PUBLIC_OIDC_TOKEN_URL
ENV NEXT_PUBLIC_OIDC_CLIENT_ID=$NEXT_PUBLIC_OIDC_CLIENT_ID
ENV NEXT_PUBLIC_ADMIN_URL=$NEXT_PUBLIC_ADMIN_URL
ENV NEXT_PUBLIC_STORAGE_URL=$NEXT_PUBLIC_STORAGE_URL
COPY apps/dashboard ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
COPY scripts/prepare-design-system.mjs ../../scripts/prepare-design-system.mjs
RUN npm run build && npm prune --omit=dev && rm -rf node_modules/@inspection/design-system && mkdir -p node_modules/@inspection/design-system && cp /workspace/packages/inspection-design-system/package.json node_modules/@inspection/design-system/ && cp -R /workspace/packages/inspection-design-system/dist node_modules/@inspection/design-system/

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3002
COPY --from=build /workspace/apps/dashboard/.next/standalone ./
COPY --from=build /workspace/apps/dashboard/.next/static ./.next/static
COPY --from=build /workspace/apps/dashboard/node_modules/@inspection/design-system ./node_modules/@inspection/design-system
COPY --from=design-system-runtime /workspace/packages/inspection-design-system/node_modules ./node_modules
EXPOSE 3002
CMD ["node", "server.js"]
