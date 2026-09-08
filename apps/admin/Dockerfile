FROM node:22-alpine AS design-system
WORKDIR /workspace/packages/inspection-design-system
COPY packages/inspection-design-system/package.json packages/inspection-design-system/package-lock.json ./
RUN npm ci
COPY packages/inspection-design-system ./
RUN npm run build

FROM node:22-alpine AS dependencies
WORKDIR /workspace/apps/admin
COPY apps/admin/package.json apps/admin/package-lock.json ./
COPY --from=design-system /workspace/packages/inspection-design-system /workspace/packages/inspection-design-system
RUN npm ci

FROM dependencies AS build
ARG NEXT_PUBLIC_INSPECTION_API_URL
ARG NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ARG NEXT_PUBLIC_OIDC_TOKEN_URL
ARG NEXT_PUBLIC_OIDC_CLIENT_ID
ENV NEXT_PUBLIC_INSPECTION_API_URL=$NEXT_PUBLIC_INSPECTION_API_URL
ENV NEXT_PUBLIC_OIDC_AUTHORIZE_URL=$NEXT_PUBLIC_OIDC_AUTHORIZE_URL
ENV NEXT_PUBLIC_OIDC_TOKEN_URL=$NEXT_PUBLIC_OIDC_TOKEN_URL
ENV NEXT_PUBLIC_OIDC_CLIENT_ID=$NEXT_PUBLIC_OIDC_CLIENT_ID
COPY apps/admin ./
COPY services/inspection/schema.graphqls ../../services/inspection/schema.graphqls
RUN npm run build && npm prune --omit=dev

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /workspace/apps/admin/package.json ./
COPY --from=build /workspace/apps/admin/node_modules ./node_modules
COPY --from=build /workspace/packages/inspection-design-system/package.json /packages/inspection-design-system/package.json
COPY --from=build /workspace/packages/inspection-design-system/dist /packages/inspection-design-system/dist
COPY --from=build /workspace/apps/admin/.next ./.next
COPY --from=build /workspace/apps/admin/public ./public
EXPOSE 3000
CMD ["npm", "run", "start"]
