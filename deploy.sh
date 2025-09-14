#!/bin/bash

# Pingoo 部署脚本
# 支持本地开发、Docker部署和云服务部署

set -e

echo "🚀 Pingoo 部署脚本"
echo "================================"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查命令是否存在
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# 打印信息
info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# 检查环境
check_env() {
    info "检查环境..."

    if ! command_exists go; then
        error "Go 未安装，请先安装 Go 1.21+"
    fi

    if ! command_exists docker; then
        warn "Docker 未安装，将跳过容器化部署"
    fi

    if ! command_exists docker-compose; then
        warn "Docker Compose 未安装，将跳过容器化部署"
    fi

    info "环境检查完成"
}

# 本地开发部署
deploy_local() {
    info "开始本地开发部署..."

    # 检查配置文件
    if [ ! -f .env ]; then
        info "创建 .env 文件..."
        cp .env.example .env
        warn "请编辑 .env 文件配置数据库连接"
        read -p "按回车键继续..."
    fi

    # 安装依赖
    info "安装 Go 依赖..."
    go mod tidy

    # 运行数据库迁移
    info "运行数据库迁移..."
    go run main.go migrate

    # 启动服务
    info "启动 Pingoo 服务..."
    go run main.go
}

# Docker部署
deploy_docker() {
    info "开始 Docker 部署..."

    # 构建镜像
    info "构建 Docker 镜像..."
    docker build -t pingoo:latest .

    # 检查配置文件
    if [ ! -f .env ]; then
        info "创建 .env 文件..."
        cp .env.example .env
        warn "请编辑 .env 文件配置数据库连接"
    fi

    # 启动服务
    info "启动 Docker 容器..."
    docker-compose up -d

    info "Docker 容器已启动"
    info "访问地址: http://localhost:8080"
    info "查看日志: docker-compose logs -f"
}

# 生产环境部署
deploy_production() {
    info "开始生产环境部署..."

    # 检查必需的环境变量
    if [ -z "$DATABASE_URL" ]; then
        error "请设置 DATABASE_URL 环境变量"
    fi

    if [ -z "$JWT_SECRET" ]; then
        error "请设置 JWT_SECRET 环境变量"
    fi

    # 构建生产镜像
    info "构建生产镜像..."
    docker build -t pingoo:production .

    # 运行迁移
    info "运行数据库迁移..."
    docker run --rm \
        -e DATABASE_URL="$DATABASE_URL" \
        pingoo:production migrate

    # 启动服务
    info "启动生产服务..."
    docker run -d \
        --name pingoo-prod \
        --restart unless-stopped \
        -p 8080:8080 \
        -e DATABASE_URL="$DATABASE_URL" \
        -e JWT_SECRET="$JWT_SECRET" \
        -e GIN_MODE=release \
        pingoo:production

    info "生产环境部署完成"
}

# 显示使用帮助
show_help() {
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  local      本地开发部署"
    echo "  docker     Docker容器化部署"
    echo "  production 生产环境部署"
    echo "  stop       停止服务"
    echo "  logs       查看日志"
    echo "  help       显示帮助信息"
    echo ""
    echo "示例:"
    echo "  $0 local      # 本地开发"
    echo "  $0 docker     # Docker部署"
    echo "  $0 production # 生产部署"
}

# 停止服务
stop_services() {
    info "停止服务..."

    if command_exists docker-compose; then
        docker-compose down
    fi

    # 停止本地进程
    pkill -f "go run main.go" || true

    info "服务已停止"
}

# 查看日志
show_logs() {
    info "查看日志..."

    if [ -f "docker-compose.yml" ] && command_exists docker-compose; then
        docker-compose logs -f
    else
        error "未找到Docker Compose配置"
    fi
}

# 主程序
main() {
    case "${1:-help}" in
        local)
            check_env
            deploy_local
            ;;
        docker)
            check_env
            deploy_docker
            ;;
        production)
            check_env
            deploy_production
            ;;
        stop)
            stop_services
            ;;
        logs)
            show_logs
            ;;
        help|*)
            show_help
            ;;
    esac
}

# 执行主程序
main "$@"