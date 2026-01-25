DATABASE_URL ?= postgresql://sunjinlee@localhost:5432/wod_dev?sslmode=disable
TEST_DATABASE_URL ?= postgresql://sunjinlee@localhost:5432/wod_test?sslmode=disable
MIGRATIONS_DIR ?= internal/db/migrations
CMD_DIR ?= cmd

.PHONY: run run-worker migrate-create get-ip test-upload

run:
	go run ./cmd/server/main.go

run-worker:
	go run ./cmd/worker/main.go

migrate-create: ## Create new migration files. Usage: make migrate-create NAME=your_desc
ifndef NAME
	$(error NAME is not set)
endif
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq ${NAME}

get-ip:
	ipconfig getifaddr en0

test-upload:
	curl -X POST http://localhost:8088/api/v1/upload -F "session_id=workout-session-001" -F "file=@./tmp/test_wod.mp4"