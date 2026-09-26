# syntax=docker/dockerfile:1
# Multi-stage build: web UI → Go binary (web embedded) → minimal runtime image.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
ARG TARGETOS TARGETARCH TARGETVARIANT VERSION=dev
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/web/dist/ ./internal/server/webui/
RUN GOARM=${TARGETVARIANT#v} CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ferry ./cmd/ferry

FROM alpine:3.20
LABEL org.opencontainers.image.source="https://github.com/anand34577/ferry" \
      org.opencontainers.image.description="Ferry - self-hosted file sharing" \
      org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 ferry && mkdir -p /data && chown ferry /data
COPY --from=backend /out/ferry /usr/local/bin/ferry
USER ferry
ENV FERRY_DATA_DIR=/data FERRY_ADDR=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["ferry", "healthcheck"]
ENTRYPOINT ["ferry"]
CMD ["serve"]
