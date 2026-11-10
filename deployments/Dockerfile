# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:22-alpine AS ui-builder
ARG VERSION=v2.1.0
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /src
# Build React frontend
COPY frontend/package*.json ./frontend/
RUN cd frontend && npm install
COPY frontend/ ./frontend/
RUN cd frontend && VITE_APP_VERSION=${VERSION} VITE_BUILD_COMMIT=${GIT_COMMIT} VITE_BUILD_TIME=${BUILD_TIME} npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETARCH
ARG VERSION=v2.1.0
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /src

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=ui-builder /src/web/index.html ./web/index.html
COPY --from=ui-builder /src/web/assets/ ./web/assets/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X 'xiaoya-media-server/internal/version.Version=${VERSION}' -X 'xiaoya-media-server/internal/version.GitCommit=${GIT_COMMIT}' -X 'xiaoya-media-server/internal/version.BuildTime=${BUILD_TIME}'" -o /out/xiaoya-media-server ./cmd/server

FROM alpine:3.21
LABEL org.opencontainers.image.title="xiaoya-media-server" \
      org.opencontainers.image.description="Unified Emby-compatible media server, Xiaoya SQLite read-only discovery, Python-compatible STRM engine, and AList/TMDB console"

RUN apk add --no-cache \
      ca-certificates \
      tzdata \
      ffmpeg \
      postgresql16 \
      postgresql16-client \
      postgresql16-contrib \
      su-exec \
      wget \
      font-wqy-zenhei \
    && mkdir -p /data /media/strm /xiaoya-db /etc/xiaoya-media-server /run/postgresql

ENV TZ=Asia/Shanghai \
    DATA_DIR=/data \
    MEDIA_ROOTS=/media \
    STRM_OUTPUT_ROOT=/media/strm \
    XIAOYA_STRM_PATH=/xiaoya-db/strm.txt \
    LISTEN=0.0.0.0:8096 \
    POSTGRES_HOST=127.0.0.1 \
    POSTGRES_PORT=5432

COPY --from=builder /out/xiaoya-media-server /usr/local/bin/xiaoya-media-server
COPY configs/config.example.yaml /etc/xiaoya-media-server/config.yaml
COPY deployments/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

WORKDIR /data
EXPOSE 8096

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8096/api/system/health || exit 1

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/usr/local/bin/xiaoya-media-server", "-config", "/etc/xiaoya-media-server/config.yaml"]
