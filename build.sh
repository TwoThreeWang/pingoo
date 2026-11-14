#!/bin/bash
# 任何一步失败就停：拉代码或打包没成功时，不要去重启正在跑的容器
set -e
cd "$(dirname "$0")"
echo '开始拉取最新代码'
git pull origin main
echo '打包镜像'
DOCKER_BUILDKIT=1 docker build -t pingoo:latest .
echo '启动容器'
# 不用先 down：up 会用新镜像直接替换旧容器，中断时间更短
docker-compose up --force-recreate -d --remove-orphans
