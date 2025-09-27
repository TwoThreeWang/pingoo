#!/bin/bash
cd "$(dirname "$0")"
echo '开始拉取最新代码'
git pull origin main
echo '打包镜像'
DOCKER_BUILDKIT=1 docker build -t pingoo:latest .
echo '启动容器'
docker-compose down
docker-compose up --force-recreate -d --remove-orphans