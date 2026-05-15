.PHONY: help up down migrate-up migrate-down migrate-new server client install-tools

include .env
export

DB_URL=postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable

help:
	@echo "Targets:"
	@echo "  up            起本地依赖 (Postgres/Redis/MinIO/NATS)"
	@echo "  down          停本地依赖"
	@echo "  migrate-up    跑迁移到最新版本"
	@echo "  migrate-down  回滚一个版本"
	@echo "  migrate-new N=name  创建新迁移文件"
	@echo "  server        启动 Go API 服务"
	@echo "  client        启动 Electron 客户端 (开发模式)"

up:
	docker compose up -d

down:
	docker compose down

migrate-up:
	migrate -path migrations -database "$(DB_URL)" up

migrate-down:
	migrate -path migrations -database "$(DB_URL)" down 1

migrate-new:
	@test -n "$(N)" || (echo "Usage: make migrate-new N=migration_name"; exit 1)
	migrate create -ext sql -dir migrations -seq $(N)

server:
	cd server && go run ./cmd/api

client:
	cd client && npm run dev
