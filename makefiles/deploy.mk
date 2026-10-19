# Container image + compose lifecycle. See deployments/docker/README.md
# for volumes, env vars and the postgres profile.
DOCKER_DIR := deployments/docker
IMAGE ?= file-explorer:dev
APP_PORT ?= 8080
# multi-arch publish knobs (make docker-push IMAGE=<registry>/gvn-explorer:latest)
PLATFORMS ?= linux/amd64,linux/arm64
BUILDER ?= gvn-builder

docker-build:
	docker build -f $(DOCKER_DIR)/Dockerfile -t $(IMAGE) .
.PHONY: docker-build docker-push docker-up docker-down docker-logs docker-test

# Publish one tag containing every architecture in $(PLATFORMS). Needs a
# namespaced IMAGE (a registry/repo path) and buildx with the
# docker-container driver — created here on first use. Log in to the
# registry first (e.g. docker login ghcr.io or docker login).
docker-push:
	@case "$(IMAGE)" in */*) ;; *) echo "docker-push: IMAGE '$(IMAGE)' needs a registry prefix, e.g. make docker-push IMAGE=ghcr.io/gssmahadevan/gvn-explorer:latest"; exit 1 ;; esac
	docker buildx inspect $(BUILDER) >/dev/null 2>&1 || docker buildx create --name $(BUILDER) --driver docker-container --bootstrap
	docker buildx build --builder $(BUILDER) --platform $(PLATFORMS) -f $(DOCKER_DIR)/Dockerfile -t $(IMAGE) --push .

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
