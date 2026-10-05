FROM golang:1.26.5-alpine AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.sum go.work go.work.sum ./
RUN go mod download
COPY libs ./libs
COPY services/inspection ./services/inspection
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-api ./services/inspection/cmd/inspection-api && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-migrate ./services/inspection/cmd/inspection-migrate && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-prompt-seed ./services/inspection/cmd/inspection-prompt-seed && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-seed ./services/inspection/cmd/inspection-seed && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-worker ./services/inspection/cmd/inspection-worker && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-scheduler ./services/inspection/cmd/inspection-scheduler && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-schema-check ./services/inspection/cmd/inspection-schema-check && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -o /out/inspection-keycloak-check ./services/inspection/cmd/inspection-keycloak-check

FROM alpine:3.22 AS runtime
# tzdata is required for server-side validation of tenant IANA timezones; CA
# certificates are required when production OIDC/provider endpoints use HTTPS.
RUN apk add --no-cache ca-certificates tzdata && addgroup -S inspection && adduser -S inspection -G inspection
USER inspection

FROM runtime AS prebuilt
COPY --chown=inspection:inspection .build/oci-bin/ /usr/local/bin/

FROM runtime AS local
COPY --from=build /out /usr/local/bin
