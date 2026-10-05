ARG GO_IMAGE
ARG RUNTIME_IMAGE
FROM ${GO_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH=arm64
ARG COMPONENT
WORKDIR /workspace
COPY go.mod go.sum go.work go.work.sum ./
RUN go mod download
COPY libs ./libs
COPY services/inspection ./services/inspection
RUN set -eu; \
    case "$COMPONENT" in \
      api) commands="api" ;; \
      worker) commands="worker" ;; \
      scheduler) commands="scheduler" ;; \
      operations) commands="migrate prompt-seed seed schema-check keycloak-check" ;; \
      *) echo "Unknown Go image component: $COMPONENT" >&2; exit 2 ;; \
    esac; \
    mkdir -p /out; \
    for command in $commands; do \
      GOOS="$TARGETOS" GOARCH="$TARGETARCH" CGO_ENABLED=0 go build -trimpath -o "/out/inspection-$command" "./services/inspection/cmd/inspection-$command"; \
    done

FROM ${RUNTIME_IMAGE} AS runtime
RUN apk add --no-cache ca-certificates tzdata && addgroup -S inspection && adduser -S inspection -G inspection
COPY --from=build --chown=inspection:inspection /out/ /usr/local/bin/
USER inspection
