# NetOps Lab — 多阶段构建（Linux 运行时）
#
# 阶段 1：构建 React 前端 → internal/webui/dist（go:embed 的输入）
# 阶段 2：编译 Go 二进制（嵌入阶段 1 的 dist）
# 阶段 3：精简运行时镜像（仅二进制 + 数据目录）
#
# 用法：
#   docker build -t netops-lab:local .
#   docker run --rm -p 8080:8080 -v netops-data:/data netops-lab:local

# ---------- 阶段 1：前端 ----------
FROM node:24-bookworm-slim AS web

WORKDIR /src/web

# 先只拷贝清单，让依赖层可缓存（源码变更不触发 npm ci）
COPY web/package.json web/package-lock.json ./
RUN npm ci

# 拷贝前端源码并构建到 ../internal/webui/dist
COPY web/ ./
COPY internal/webui/dist/.gitkeep /src/internal/webui/dist/.gitkeep
RUN npm run build

# ---------- 阶段 2：Go 二进制 ----------
FROM golang:1.27-bookworm AS build

WORKDIR /src

# 依赖层缓存
COPY go.mod go.sum ./
RUN go mod download

# 拷贝源码 + 阶段 1 的前端产物
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist

# 纯 Go，无 CGO（modernc.org/sqlite 为纯 Go 实现）
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/netopslab ./cmd/netopslab

# ---------- 阶段 3：运行时 ----------
FROM debian:bookworm-slim

# ca-certificates：地图底图等出站 HTTPS 需要
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/netopslab /app/netopslab

# 数据库与上传文件目录（挂载卷以持久化）
RUN mkdir -p /data
VOLUME ["/data"]

EXPOSE 8080

# 默认以 serve 启动，数据库位于卷内
ENTRYPOINT ["/app/netopslab"]
CMD ["serve", "--addr", ":8080", "--db", "/data/netops.db", "--data-dir", "/data"]
