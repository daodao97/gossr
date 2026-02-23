SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
NPM ?= npm
DEV_SERVER_URL ?= http://127.0.0.1:3333

.PHONY: help init-dist web-install web-dev web-build dev run test fmt tidy clean

help: ## 显示可用命令
	@awk 'BEGIN {FS = ":.*## "; print "\nAvailable commands:"} /^[a-zA-Z_-]+:.*## / {printf "  %-12s %s\n", $$1, $$2} END {print ""}' $(MAKEFILE_LIST)

init-dist: ## 初始化 embed 所需的 dist 占位目录
	@mkdir -p web/dist/client web/dist/server
	@touch web/dist/client/.keep web/dist/server/.keep

web-install: ## 安装前端依赖
	$(NPM) --prefix web install

web-dev: ## 启动前端开发服务（127.0.0.1:3333）
	$(NPM) --prefix web run dev

web-build: ## 构建前端产物（dist/client + dist/server）
	$(NPM) --prefix web run build

dev: ## Go 开发模式（需先执行 make web-dev）
	DEV_MODE=1 DEV_SERVER_URL=$(DEV_SERVER_URL) $(GO) run -tags nov8 .

run: web-build ## Go 生产模式运行（自动先执行 web-build）
	$(GO) run -tags nov8 .

test: ## 运行 Go 测试（goja 路径）
	$(GO) test -tags nov8 ./...

fmt: ## 格式化 Go 代码
	$(GO) fmt ./...

tidy: ## 整理 Go 依赖
	$(GO) mod tidy

clean: ## 清理前端构建产物并恢复 embed 占位目录
	rm -rf web/dist
	$(MAKE) init-dist
