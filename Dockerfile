# ---------- 构建 ----------
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache tzdata ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/pingoo ./cmd/pingoo

# ---------- 运行 ----------
# 模板和静态文件已经打包进二进制，只需要 .env
FROM scratch
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/pingoo /pingoo
WORKDIR /
# 不用 root 运行（65534 是 nobody）。程序只需要监听 5004 和读挂载进来的 .env / 密钥文件
USER 65534:65534
EXPOSE 5004
ENTRYPOINT ["/pingoo"]
