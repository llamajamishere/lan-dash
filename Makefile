# Convenience wrapper around build.sh / install.sh and the dev workflow.
#
#   make build    # frontend + static Go binary -> bin/lan-dash
#   make install  # sudo ./install.sh (build first)
#   make dev      # backend on :8080 with mock data + Vite dev server on :5173
#   make test     # Go unit tests + TypeScript type-check

SHELL := /bin/bash
DEV_DIR := $(CURDIR)/dev-data
GO ?= go

.PHONY: build install uninstall dev dev-backend dev-frontend test vet clean

build:
	./build.sh

install: build
	sudo ./install.sh

uninstall:
	sudo ./install.sh --uninstall

# Runs the backend with mock collectors and demo devices (DB + key under
# ./dev-data), and Vite with hot reload proxying /api to the backend.
# Open http://localhost:5173. Ctrl-C stops both.
dev:
	@mkdir -p $(DEV_DIR)
	@cd frontend && [ -d node_modules ] || npm install
	@trap 'kill 0' EXIT INT TERM; \
	  (cd backend && $(GO) run ./cmd/landash -listen 127.0.0.1:8080 -db $(DEV_DIR)/landash.db \
	     -key $(DEV_DIR)/master.key -init-key -mock -seed-demo -log-level debug) & \
	  (cd frontend && npm run dev) & \
	  wait

dev-backend:
	@mkdir -p $(DEV_DIR)
	cd backend && $(GO) run ./cmd/landash -listen 127.0.0.1:8080 -db $(DEV_DIR)/landash.db \
	  -key $(DEV_DIR)/master.key -init-key -mock -seed-demo -log-level debug

dev-frontend:
	cd frontend && npm run dev

test:
	cd backend && $(GO) test ./...
	cd frontend && npx tsc -b

vet:
	cd backend && $(GO) vet ./...

clean:
	rm -rf bin frontend/dist
	find backend/dist -mindepth 1 ! -name .placeholder -exec rm -rf {} +
