# IDaaS Akless Sidecar：本地 HTTP 凭据代理。
# 用法：make <target>。无参数时显示帮助。

BINARY   := bin/idaas-akless-sidecar
MAIN_PKG := ./cmd/idaas-akless-sidecar
GOFLAGS  := -trimpath
LDFLAGS  := -s -w
VERSION  ?= dev
REVISION ?= $(shell git describe --always --dirty --abbrev=12 2>/dev/null || echo unknown)
GOLANGCI_LINT_VERSION := v2.6.2
# Analyze against the project's minimum supported Go release line.
LINT_GO_TOOLCHAIN := go1.24.6
GOLANGCI_LINT := GOTOOLCHAIN=$(LINT_GO_TOOLCHAIN) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: help build run test check check-linux check-sdk-contracts vet fmt lint clean tidy docker

help: ## 显示可用 target
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## 编译二进制到 bin/
	go build $(GOFLAGS) -ldflags="$(LDFLAGS) -X main.version=$(VERSION) -X main.revision=$(REVISION)" -o $(BINARY) $(MAIN_PKG)

run: build ## 使用示例 JSON 运行（先填写配置；默认认证需 Linux 只读投射卷及 Pod 上下文）
	./$(BINARY) --config cloud_idaas.json.example

test: ## 运行 Go 单元测试
	go test ./...

check: ## 安装锁定的 Node 测试依赖并运行全部离线回归（不需要 Docker 或云账号）
	npm ci --prefix business-example/multicloud --ignore-scripts --no-audit --no-fund
	npm ci --prefix verification/native-uri --ignore-scripts --no-audit --no-fund
	go test -race ./...
	go vet ./...
	$(MAKE) lint
	python3 -m unittest discover -s deploy -p 'test_*.py'
	python3 -m unittest discover -s verification/native-uri -p run_test.py
	python3 -m unittest discover -s verification/projected-auth -p run_test.py
	python3 -m unittest discover -s verification/sdk-contracts -p '*_test.py'
	node --test business-example/*.test.js verification/native-uri/*.test.cjs deploy/*.test.js
	npm test --prefix business-example/multicloud

check-linux: ## 在本地 Docker 中验证非 root 只读投射卷（假 Token、无云调用）
	python3 verification/projected-auth/run.py

check-sdk-contracts: ## 验证 13 项离线 SDK 契约（先按验证文档安装锁定依赖）
	python3 verification/sdk-contracts/check_endpoint_responses.py --output /tmp/idaas-sdk-results.json

vet: ## go vet
	go vet ./...

fmt: ## gofmt（写回）
	gofmt -s -w cmd internal verification/sdk-contracts/go

lint: ## 使用固定版本 golangci-lint（首次运行需下载工具）
	$(GOLANGCI_LINT) run ./...

tidy: ## go mod tidy
	go mod tidy

docker: ## 本地 docker 构建（构建上下文 = 仓库根）
	docker build -f Dockerfile -t idaas-akless-sidecar:local .

clean: ## 清理产物
	rm -rf bin/
