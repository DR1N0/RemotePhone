GREEN := \033[32m
BLUE := \033[34m
YELLOW := \033[33m
NC := \033[0m
.DEFAULT_GOAL := help

BUILD_DIR=.bin


##@ General

.PHONY: help
help: ## Display this help message
	@echo "$(BLUE)Welcome to MAKO$(NC)"
	@echo ""
	@echo "$(GREEN)Usage:$(NC)"
	@echo "  make <target>"
	@echo ""
	@awk 'BEGIN {FS = ":.*##"; printf "$(GREEN)Available targets:$(NC)\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  $(BLUE)%-20s$(NC) %s\n", $$1, $$2 } \
		/^##@/ { printf "\n$(YELLOW)%s$(NC)\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)

.PHONY: vendor
vendor: ## Update and tidy dependencies
	@echo "Updating dependencies..."
	@cd apps/server && go mod tidy
	@cd apps/server && go mod vendor
	@echo "Dependencies updated."

##@ Build and Run

.PHONY: run-controlplane
run-controlplane: ## Run the Go control plane (it spawns per-instance workers)
	@echo "Running control plane..."
	@cd apps/server && go run ./cmd/mako-controlplane

.PHONY: build-controlplane
build-controlplane: ## Build the Go control plane
	@echo "Building control plane..."
	@mkdir -p $(BUILD_DIR)
	@cd apps/server && go build -o ../../$(BUILD_DIR)/mako-controlplane ./cmd/mako-controlplane

.PHONY: build-worker
build-worker: ## Build the worker application
	@echo "Building worker..."
	@mkdir -p $(BUILD_DIR)
	@cd apps/server && go build -o ../../$(BUILD_DIR)/mako-worker ./cmd/mako-worker

.PHONY: run-worker
run-worker: ## Run the worker application
	@if [ ! -f $(BUILD_DIR)/mako-worker ]; then \
		$(MAKE) build-worker; \
	fi
	@echo "Running worker..."
	@cd $(BUILD_DIR) && ./mako-worker

.PHONY: run
run: run-controlplane ## Run the control plane (workers spawn per instance)

.PHONY: run-client
run-client: ## Run the Flutter Android client on a connected device/emulator
	@echo "Running client..."
	@cd apps/client && flutter run

.PHONY: build-admin
build-admin: ## Build admin frontend to apps/admin/dist
	cd apps/admin && npm run build

.PHONY: run-admin
run-admin: ## Run admin frontend dev server with API proxy to :3000
	cd apps/admin && npm run dev

.PHONY: build
build: build-controlplane build-worker ## Build the Go server binaries