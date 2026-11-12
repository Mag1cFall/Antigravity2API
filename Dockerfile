FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
COPY internal/i18n/messages/ /src/internal/i18n/messages/
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/antigravity2api ./cmd/antigravity2api

FROM debian:trixie-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata     && rm -rf /var/lib/apt/lists/*
WORKDIR /data
ENV LISTEN=0.0.0.0:8017
EXPOSE 8017
CMD ["antigravity2api", "serve"]

FROM runtime AS release
ARG TARGETARCH
COPY release/linux-$TARGETARCH/antigravity2api /usr/local/bin/antigravity2api

FROM runtime
COPY --from=build /out/antigravity2api /usr/local/bin/antigravity2api
