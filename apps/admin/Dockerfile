FROM node:22-alpine AS dependencies
WORKDIR /workspace/apps/admin
COPY apps/admin/package.json apps/admin/package-lock.json .npmrc ./
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
RUN npm run build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /workspace/apps/admin/package.json /workspace/apps/admin/package-lock.json ./
RUN npm ci --omit=dev
COPY --from=build /workspace/apps/admin/.next ./.next
COPY --from=build /workspace/apps/admin/public ./public
EXPOSE 3000
CMD ["npm", "run", "start"]
