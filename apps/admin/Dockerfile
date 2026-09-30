FROM node:22-alpine AS design-system
WORKDIR /workspace/packages/inspection-design-system
COPY packages/inspection-design-system/package.json packages/inspection-design-system/package-lock.json ./
RUN npm ci
COPY packages/inspection-design-system ./
RUN npm run build

FROM design-system AS design-system-runtime
RUN npm prune --omit=dev

FROM node:22-alpine AS dependencies
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
ARG STAGE=production
ENV NEXT_PUBLIC_INSPECTION_API_URL=$NEXT_PUBLIC_INSPECTION_API_URL
ENV NEXT_PUBLIC_OIDC_AUTHORIZE_URL=$NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ENV NEXT_PUBLIC_OIDC_TOKEN_URL=$NEXT_PUBLIC_OIDC_TOKEN_URL
ENV NEXT_PUBLIC_OIDC_CLIENT_ID=$NEXT_PUBLIC_OIDC_CLIENT_ID
ENV STAGE=$STAGE
COPY apps/admin ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
COPY scripts/prepare-design-system.mjs ../../scripts/prepare-design-system.mjs
RUN npm run build && npm prune --omit=dev && rm -rf node_modules/@inspection/design-system && mkdir -p node_modules/@inspection/design-system && cp /workspace/packages/inspection-design-system/package.json node_modules/@inspection/design-system/ && cp -R /workspace/packages/inspection-design-system/dist node_modules/@inspection/design-system/

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /workspace/apps/admin/package.json ./
COPY --from=build /workspace/apps/admin/node_modules ./node_modules
COPY --from=build /workspace/packages/inspection-address ./node_modules/@inspection/address
COPY --from=design-system-runtime /workspace/packages/inspection-design-system/node_modules ./node_modules
COPY --from=build /workspace/apps/admin/.next ./.next
COPY --from=build /workspace/apps/admin/public ./public
EXPOSE 3000
CMD ["npm", "run", "start"]
