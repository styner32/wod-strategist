DATABASE_URL ?= postgresql://sunjinlee@localhost:5432/wod_dev?sslmode=disable
TEST_DATABASE_URL ?= postgresql://sunjinlee@localhost:5432/wod_test?sslmode=disable
MIGRATIONS_DIR ?= internal/db/migrations
CMD_DIR ?= cmd

.PHONY: run migrate-create

run:
	go run ./cmd

migrate-create: ## Create new migration files. Usage: make migrate-create NAME=your_desc
ifndef NAME
	$(error NAME is not set)
endif
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq ${NAME}