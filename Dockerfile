FROM golang:1.26-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/ktms-admin ./cmd/ktms-admin

FROM alpine:3.21
# CA certificates are needed to reach Google for sign-in.
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 appuser
COPY --from=builder /out/ktms-admin /ktms-admin
USER appuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/ktms-admin"]
