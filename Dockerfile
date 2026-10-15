FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
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

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/antigravity2api /usr/local/bin/antigravity2api
WORKDIR /data
ENV LISTEN=0.0.0.0:8017
EXPOSE 8017
CMD ["antigravity2api", "serve"]
