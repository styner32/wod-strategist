ENV_FILE ?= .env
ifneq (,$(wildcard $(ENV_FILE)))
  include $(ENV_FILE)
  export
endif

DATABASE_URL ?= postgresql://sunjinlee@localhost:5432/wod_dev?sslmode=disable
TEST_DATABASE_URL ?= postgresql://sunjinlee@localhost:5432/wod_test?sslmode=disable
MIGRATIONS_DIR ?= internal/db/migrations
IMAGE_REPO ?= $(REGION)-docker.pkg.dev/$(PROJECT_ID)/$(REPO_NAME)
CMD_DIR ?= cmd
TEST_DIR ?= ./...

GIT_SHA := $(shell git rev-parse --short HEAD)

.PHONY: run run-worker migrate-create migrate-up migrate-down get-ip update-image-api update-image-worker update-image-migrate deploy-api deploy-worker migrate-up-remote test-upload test-chunk-upload help test

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $1, $2}'

run: ## Run API server locally
	go run ./cmd/server/main.go

run-worker: ## Run worker locally
	go run ./cmd/worker/main.go

run-admin: ## Run admin CLI. Usage: make run-admin CMD="reparse-highlights --dry-run"
	go run ./cmd/admin/main.go $(CMD)

migrate-create: ## Create new migration files. Usage: make migrate-create NAME=your_desc
ifndef NAME
	$(error NAME is not set)
endif
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq ${NAME}

migrate-up: ## Run up migrations
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" -verbose up

migrate-down: ## Run down migration (step 1)
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" -verbose down 1

get-ip: ## Get local IP (macOS)
	ipconfig getifaddr en0

update-image-api: ## Build and push API image
	docker build --platform linux/amd64 -t $(IMAGE_REPO)/api:latest -t $(IMAGE_REPO)/api:$(GIT_SHA) -f Dockerfile_api --build-arg GIT_COMMIT=$(GIT_SHA) .
	docker push $(IMAGE_REPO)/api:latest
	docker push $(IMAGE_REPO)/api:$(GIT_SHA)

update-image-worker: ## Build and push Worker image
	docker build --platform linux/amd64 -t $(IMAGE_REPO)/worker:latest -t $(IMAGE_REPO)/worker:$(GIT_SHA) -f Dockerfile_worker --build-arg GIT_COMMIT=$(GIT_SHA) .
	docker push $(IMAGE_REPO)/worker:latest
	docker push $(IMAGE_REPO)/worker:$(GIT_SHA)

update-image-migrate: ## Build and push Migration image
	docker build --platform linux/amd64 -t $(IMAGE_REPO)/migrate:latest -t $(IMAGE_REPO)/migrate:$(GIT_SHA) -f Dockerfile_migrate .
	docker push $(IMAGE_REPO)/migrate:latest
	docker push $(IMAGE_REPO)/migrate:$(GIT_SHA)

deploy-api: ## Deploy API to Cloud Run
	gcloud run deploy $(API_SERVICE_NAME) --image $(IMAGE_REPO)/api:latest --region $(REGION) --project $(PROJECT_ID)

deploy-worker: ## Deploy Worker to Cloud Run Worker Pools
	gcloud beta run worker-pools deploy $(WORKER_SERVICE_NAME) --image $(IMAGE_REPO)/worker:latest --region $(REGION) --project $(PROJECT_ID)

migrate-up-remote: ## Run up migrations remotely
	gcloud run jobs execute $(MIGRATE_SERVICE_NAME) --region $(REGION) --project $(PROJECT_ID)

test-upload: ## Test video upload locally
	node --env-file=../.env ../scripts/test-upload.js

test-chunk-upload: ## Replay chunk upload from a saved video. Usage: make test-chunk-upload
	node --env-file=../.env \
		../scripts/test-chunk-upload.js

test: ## Run the tests
	@mkdir -p tmp/go-build tmp/go-tmp
	GOCACHE=$$(pwd)/tmp/go-build GOTMPDIR=$$(pwd)/tmp/go-tmp go test $$(TEST_DIR) -p 1 -v

migrate-test-up:
ifndef TEST_DATABASE_URL
	$(error TEST_DATABASE_URL is not set)
endif
	migrate -path=$(MIGRATIONS_DIR) -database "$(TEST_DATABASE_URL)" -verbose up

migrate-test-redo:
ifndef TEST_DATABASE_URL
	$(error TEST_DATABASE_URL is not set)
endif
	migrate -path=$(MIGRATIONS_DIR) -database "$(TEST_DATABASE_URL)" -verbose down 1
	migrate -path=$(MIGRATIONS_DIR) -database "$(TEST_DATABASE_URL)" -verbose up
