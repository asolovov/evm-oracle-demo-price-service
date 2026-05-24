# evm-oracle-demo-price-service Makefile.
#
# Build, codegen, migration, and lint targets. The deployment story is
# `docker run <image>` with env vars — see Dockerfile + docker-compose.yml.

APP            := price-service
APP_ENTRY_POINT := cmd/evm-oracle-demo-price-service.go
BUILD_OUT_DIR  := ./bin

# Pinned codegen tool versions (architecture rule 9 — never @latest).
BUF_VERSION              := v1.55.0
PROTOC_GEN_GO_VERSION    := v1.36.0
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1
GOLANG_MIGRATE_VERSION   := v4.18.1
GOLANGCI_LINT_VERSION    := v1.63.4

# Go build variables.
GOOS   := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

# Version embedding (best-effort; tolerate missing tags / detached HEAD).
GITVER_PKG := github.com/asolovov/evm-oracle-demo-price-service/pkg/version
TAG        := $(shell git describe --abbrev=0 --tags 2>/dev/null || true)
COMMIT     := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BRANCH     := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
REMOTE     := $(shell git config --get remote.origin.url 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ%Z')
RELEASE    := $(if $(TAG),$(TAG),$(COMMIT))

LDFLAGS := -w -s
LDFLAGS += -X $(GITVER_PKG).ServiceName=$(APP)
LDFLAGS += -X $(GITVER_PKG).CommitTag=$(TAG)
LDFLAGS += -X $(GITVER_PKG).CommitSHA=$(COMMIT)
LDFLAGS += -X $(GITVER_PKG).CommitBranch=$(BRANCH)
LDFLAGS += -X $(GITVER_PKG).OriginURL=$(REMOTE)
LDFLAGS += -X $(GITVER_PKG).BuildDate=$(BUILD_DATE)
LDFLAGS += -X $(GITVER_PKG).Release=$(RELEASE)

# Migration configuration.
MIGRATIONS_DIR := ./migrations
DATABASE_HOST     ?= localhost
DATABASE_PORT     ?= 5432
DATABASE_USER     ?= price_user
DATABASE_PASSWORD ?= price_pass
DATABASE_NAME     ?= evm_price
DATABASE_SSL_MODE ?= disable
DATABASE_URL := postgres://$(DATABASE_USER):$(DATABASE_PASSWORD)@$(DATABASE_HOST):$(DATABASE_PORT)/$(DATABASE_NAME)?sslmode=$(DATABASE_SSL_MODE)

# Proto configuration.
PROTO_DIR := ./protocols

.PHONY: all
all: tidy generate build test ## Tidy modules, generate proto stubs, build, and test.

.PHONY: help
help: ## Show available targets.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## Run go mod tidy.
	go mod tidy

.PHONY: update
update: ## Update all dependencies.
	go get -u ./...

# -------- Codegen --------

.PHONY: tools
tools: buf-install protoc-install migrate-install lint-install ## Install all pinned dev tools.

.PHONY: buf-install
buf-install: ## Install buf at the pinned version.
	@which buf >/dev/null && buf --version 2>/dev/null | grep -q '$(BUF_VERSION:v%=%)' \
		|| (echo "Installing buf $(BUF_VERSION)..." && go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION))

.PHONY: protoc-install
protoc-install: ## Install pinned protoc-gen-go and protoc-gen-go-grpc.
	@echo "Installing protoc-gen-go $(PROTOC_GEN_GO_VERSION)..."
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@echo "Installing protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)..."
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

.PHONY: generate
generate: buf-install protoc-install ## Generate Go stubs into ./internal/genproto/ (gitignored).
	@echo "Generating proto stubs into ./internal/genproto/..."
	@mkdir -p internal/genproto
	@buf generate --template buf.gen.yaml
	@echo "Proto generation complete."

.PHONY: generate-clean
generate-clean: ## Remove the generated proto tree.
	@rm -rf internal/genproto

# -------- Build / Test --------

.PHONY: build
build: generate ## Build the application binary into $(BUILD_OUT_DIR).
	@mkdir -p $(BUILD_OUT_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="$(LDFLAGS)" -o $(BUILD_OUT_DIR)/$(APP) $(APP_ENTRY_POINT)

.PHONY: run
run: ## Run the application locally with race detection.
	go run -race $(APP_ENTRY_POINT) serve

.PHONY: test
test: generate ## Run unit tests.
	go test ./...

.PHONY: test-integration
test-integration: generate ## Run integration tests (requires docker).
	go test -tags=integration -count=1 ./...

.PHONY: test-coverage
test-coverage: generate ## Run tests with coverage report.
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: clean
clean: generate-clean ## Remove build artefacts and generated code.
	@rm -rf $(BUILD_OUT_DIR)

# -------- E2E (task 05.1) --------
#
# /e2e/ is gitignored — it's a local-only harness. The targets below
# reference paths inside /e2e/ that only exist on a developer's machine.
# Authoritative spec lives at projects/evm-oracle-demo/tasks/05.1-price-service-e2e.md
# in the mimir vault; re-derive the directory contents from there on a
# fresh clone.

.PHONY: e2e-up
e2e-up: ## Start the E2E docker-compose stack.
	@docker compose --env-file .env.e2e -f e2e/docker-compose.e2e.yml up -d --build

.PHONY: e2e-down
e2e-down: ## Tear down the E2E docker-compose stack (drops volumes).
	@docker compose --env-file .env.e2e -f e2e/docker-compose.e2e.yml down -v

.PHONY: e2e-logs
e2e-logs: ## Tail the price-service container logs.
	@docker compose --env-file .env.e2e -f e2e/docker-compose.e2e.yml logs -f price-service

.PHONY: test-e2e
test-e2e: generate ## Run the full E2E suite (requires docker + .env.e2e).
	go test -tags=e2e -count=1 -timeout 15m ./e2e/...

# -------- Lint --------

.PHONY: lint
lint: generate ## Run golangci-lint.
	golangci-lint run ./...

.PHONY: lint-install
lint-install: ## Install golangci-lint at the pinned version.
	@which golangci-lint >/dev/null \
		|| (echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..." \
		&& go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION))

# -------- Migrations --------

.PHONY: migrate-install
migrate-install: ## Install golang-migrate at the pinned version.
	@which migrate >/dev/null \
		|| (echo "Installing golang-migrate $(GOLANG_MIGRATE_VERSION)..." \
		&& go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(GOLANG_MIGRATE_VERSION))

.PHONY: migrate-create
migrate-create: ## Create migration files. Usage: make migrate-create NAME=add_thing.
ifndef NAME
	@echo "Error: NAME parameter is required. Usage: make migrate-create NAME=add_thing"
	@exit 1
endif
	@migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

.PHONY: migrate-up
migrate-up: ## Apply all up migrations.
	@migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back the most recent migration.
	@migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

.PHONY: migrate-version
migrate-version: ## Print current migration version.
	@migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" version

# -------- Docker Compose --------

.PHONY: compose-up
compose-up: ## Start local Postgres + service via docker-compose.
	docker-compose up -d

.PHONY: compose-down
compose-down: ## Stop the local docker-compose stack.
	docker-compose down
