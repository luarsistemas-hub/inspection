FROM golang:1.26.5-alpine AS build

WORKDIR /workspace
COPY go.mod go.sum go.work go.work.sum ./
RUN go mod download
COPY libs ./libs
COPY services/inspection ./services/inspection
RUN CGO_ENABLED=0 go build -trimpath -o /out/inspection-api ./services/inspection/cmd/inspection-api && \
    CGO_ENABLED=0 go build -trimpath -o /out/inspection-migrate ./services/inspection/cmd/inspection-migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/inspection-seed ./services/inspection/cmd/inspection-seed && \
    CGO_ENABLED=0 go build -trimpath -o /out/inspection-worker ./services/inspection/cmd/inspection-worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/inspection-scheduler ./services/inspection/cmd/inspection-scheduler

FROM alpine:3.22
# tzdata is required for server-side validation of tenant IANA timezones; CA
# certificates are required when production OIDC/provider endpoints use HTTPS.
RUN apk add --no-cache ca-certificates tzdata && addgroup -S inspection && adduser -S inspection -G inspection
USER inspection
COPY --from=build /out /usr/local/bin
