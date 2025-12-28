# ---------- builder ----------
FROM golang:alpine AS builder
LABEL stage=gobuilder

# 安装必要工具
RUN apk update --no-cache && apk add --no-cache tzdata

WORKDIR /build

# 先复制依赖文件，利用 Docker 层缓存
COPY go.mod go.sum ./

# 使用 Go 模块缓存加速（需要 BuildKit）
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 复制源代码
COPY . .

# 静态编译，去符号，使用缓存加速编译
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/main main.go

# ---------- final ----------
FROM scratch
COPY --from=builder /usr/share/zoneinfo/Asia/Shanghai /usr/share/zoneinfo/Asia/Shanghai
ENV TZ=Asia/Shanghai
WORKDIR /
COPY --from=builder /app/main /main
CMD ["/main"]