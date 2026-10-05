ARG NODE_IMAGE=node:22-alpine
FROM ${NODE_IMAGE} AS design-system
WORKDIR /workspace/packages/inspection-design-system
COPY packages/inspection-design-system/package.json packages/inspection-design-system/package-lock.json ./
RUN npm ci
COPY packages/inspection-design-system ./
COPY scripts/prepare-design-system.mjs /workspace/scripts/prepare-design-system.mjs
RUN node ../../scripts/prepare-design-system.mjs

FROM design-system AS design-system-runtime
RUN npm prune --omit=dev

FROM ${NODE_IMAGE} AS dependencies
WORKDIR /workspace/apps/admin
COPY apps/admin/package.json apps/admin/package-lock.json ./
COPY --from=design-system /workspace/packages/inspection-design-system /workspace/packages/inspection-design-system
COPY packages/inspection-address /workspace/packages/inspection-address
RUN npm ci

FROM dependencies AS build
ARG NEXT_PUBLIC_INSPECTION_API_URL
ARG NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ARG NEXT_PUBLIC_OIDC_TOKEN_URL
ARG NEXT_PUBLIC_OIDC_CLIENT_ID
ARG NEXT_PUBLIC_DASHBOARD_URL
ARG NEXT_PUBLIC_CAPTURE_URL
ARG STAGE=production
ENV NEXT_PUBLIC_INSPECTION_API_URL=$NEXT_PUBLIC_INSPECTION_API_URL
ENV NEXT_PUBLIC_OIDC_AUTHORIZE_URL=$NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ENV NEXT_PUBLIC_OIDC_TOKEN_URL=$NEXT_PUBLIC_OIDC_TOKEN_URL
ENV NEXT_PUBLIC_OIDC_CLIENT_ID=$NEXT_PUBLIC_OIDC_CLIENT_ID
ENV NEXT_PUBLIC_DASHBOARD_URL=$NEXT_PUBLIC_DASHBOARD_URL
ENV NEXT_PUBLIC_CAPTURE_URL=$NEXT_PUBLIC_CAPTURE_URL
ENV STAGE=$STAGE
COPY apps/admin ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
COPY scripts/prepare-design-system.mjs ../../scripts/prepare-design-system.mjs
RUN npm run build && npm prune --omit=dev && rm -rf node_modules/@inspection/design-system && mkdir -p node_modules/@inspection/design-system && cp /workspace/packages/inspection-design-system/package.json node_modules/@inspection/design-system/ && cp -R /workspace/packages/inspection-design-system/dist node_modules/@inspection/design-system/

FROM ${NODE_IMAGE}
WORKDIR /app
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3000
COPY --from=build /workspace/apps/admin/.next/standalone ./
COPY --from=build /workspace/apps/admin/.next/static ./.next/static
COPY --from=build /workspace/apps/admin/node_modules/@inspection/design-system ./node_modules/@inspection/design-system
COPY --from=build /workspace/packages/inspection-address ./node_modules/@inspection/address
COPY --from=design-system-runtime /workspace/packages/inspection-design-system/node_modules ./node_modules
COPY --from=build /workspace/apps/admin/public ./public
EXPOSE 3000
CMD ["node", "server.js"]
