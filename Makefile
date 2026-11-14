.PHONY: run dev build test test-db demo migrate docker

# 本地运行（读取当前目录的 .env）
run:
	go run ./cmd/pingoo

# 写入 6 个演示网站的模拟数据
demo:
	go run ./cmd/pingoo demo

migrate:
	go run ./cmd/pingoo migrate

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/pingoo ./cmd/pingoo

test:
	go vet ./... && go test ./...

docker:
	docker build -t pingoo:latest .

# 连数据库的集成测试：在 .env 配置的数据库里建临时 schema（pingoo_test_xxx），跑完就删，不碰现有数据
test-db:
	PINGOO_TEST_DB=1 go test -count=1 ./...
