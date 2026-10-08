FROM oven/bun:1 AS web-build

WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

FROM golang:1.27-alpine AS go-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist

ARG VERSION=dev
ENV CGO_ENABLED=0
RUN go build -tags embedui -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/panel ./cmd/panel

FROM alpine:3.23

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 panel \
    && adduser -S -D -H -u 65532 -G panel panel \
    && mkdir -p /data \
    && chown panel:panel /data

COPY --from=go-build --chown=panel:panel /out/panel /panel
COPY --chown=panel:panel LICENSE NOTICE /

ENV PANEL_LISTEN=0.0.0.0:8787 \
    PANEL_DATA_DIR=/data

USER panel
EXPOSE 8787
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8787/healthz"]

ENTRYPOINT ["/panel"]
CMD ["serve"]
