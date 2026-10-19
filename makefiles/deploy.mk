# Container image + compose lifecycle. See deployments/docker/README.md
# for volumes, env vars and the postgres profile.
DOCKER_DIR := deployments/docker
IMAGE ?= file-explorer:dev
APP_PORT ?= 8080

docker-build:
	docker build -f $(DOCKER_DIR)/Dockerfile -t $(IMAGE) .
.PHONY: docker-build docker-up docker-down docker-logs docker-test

docker-up:
	docker compose -f $(DOCKER_DIR)/docker-compose.yml up -d --build

docker-down:
	docker compose -f $(DOCKER_DIR)/docker-compose.yml down

docker-logs:
	docker compose -f $(DOCKER_DIR)/docker-compose.yml logs -f app

# Point the curl suite at the running compose stack (defaults in the
# script match the compose ADMIN env: system:admin:admin).
docker-test:
	BASE_URL=http://localhost:$(APP_PORT) ./scripts/test_files.sh
