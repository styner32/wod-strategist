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

.PHONY: run run-worker migrate-create get-ip update-image-api update-image-worker test-upload

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

update-image-api:
	docker build --platform linux/amd64 -t $(IMAGE_REPO)/api -f Dockerfile_api --build-arg GIT_COMMIT=$(shell git rev-parse --short HEAD) .
	docker push $(IMAGE_REPO)/api:latest

update-image-worker:
	docker build --platform linux/amd64 -t $(IMAGE_REPO)/worker -f Dockerfile_worker --build-arg GIT_COMMIT=$(shell git rev-parse --short HEAD) .
	docker push $(IMAGE_REPO)/worker:latest

deploy-api:
	gcloud run deploy $(API_SERVICE_NAME) --image $(IMAGE_REPO)/api:latest --region $(REGION) --project $(PROJECT_ID)

deploy-worker:
	gcloud beta run worker-pools deploy $(WORKER_SERVICE_NAME) --image $(IMAGE_REPO)/worker:latest --region $(REGION) --project $(PROJECT_ID)

test-upload:
	curl -X POST http://localhost:8088/api/v1/upload -F "session_id=workout-session-001" -F "file=@./tmp/test_wod.mp4"
