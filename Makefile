GREEN := \033[32m
BLUE := \033[34m
YELLOW := \033[33m
NC := \033[0m
.DEFAULT_GOAL := help

BINARY_NAME=mako-server
BUILD_DIR=.bin


##@ General

.PHONY: help
help: ## Display this help message
	@echo "$(BLUE)Welcome to MAKO SERVER$(NC)"
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
	@cd apps/worker && go mod tidy
	@cd apps/worker && go mod vendor
	@echo "Dependencies updated."

##@ Build and Run

.PHONY: run-frontend
run-frontend: ## Run the frontend application
	@echo "Running frontend..."
	@cd apps/controlplane && npm run dev

.PHONY: build-worker
build-worker: ## Build the worker application
	@echo "Building worker..."
	@mkdir -p $(BUILD_DIR)
	@cd apps/worker && go build -o ../../$(BUILD_DIR)/worker cmd/mako-worker/main.go

.PHONY: run-worker
run-worker: ## Run the worker application
	@if [ ! -f $(BUILD_DIR)/worker ]; then \
		$(MAKE) build-worker; \
	fi
	@echo "Running worker..."
	@cd $(BUILD_DIR) && ./worker

.PHONY: run
run: ## Run both frontend and worker applications
	@set -e; \
	trap 'echo "Stopping..."; kill -- -$$PGID 2>/dev/null' INT TERM EXIT; \
	PGID=$$PPID; \
	$(MAKE) run-worker & \
	$(MAKE) run-frontend & \
	wait