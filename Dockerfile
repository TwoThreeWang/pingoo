# ---------- builder ----------
FROM golang:1.21-alpine AS builder
LABEL stage=gobuilder
RUN apk update --no-cache && apk add --no-cache tzdata

WORKDIR /build
# 复制 go.mod/go.sum 以利用缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 静态编译，去符号
ENV CGO_ENABLED=0
RUN go build -trimpath -p $(nproc) -ldflags="-s -w" -o /app/main main.go

# ---------- final ----------
FROM scratch
COPY --from=builder /usr/share/zoneinfo/Asia/Shanghai /usr/share/zoneinfo/Asia/Shanghai
ENV TZ Asia/Shanghai
WORKDIR /
COPY --from=builder /app/main /main
CMD ["/main"]