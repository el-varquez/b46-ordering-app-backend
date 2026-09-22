SHELL := /bin/sh

COMPOSE_DATABASE_URL := postgres://b46:b46_local_only@localhost:5433/b46_ordering?sslmode=disable
STORE_COMPOSE_DATABASE_URL := postgres://b46_adapter_test@localhost:5434/b46_store_test?sslmode=disable
-include .env
.EXPORT_ALL_VARIABLES:
DATABASE_URL ?= $(COMPOSE_DATABASE_URL)
STORE_DATABASE_URL ?= $(STORE_COMPOSE_DATABASE_URL)
MIGRATIONS_DIR := services/ordering/migrations

.DEFAULT_GOAL := help

.PHONY: help setup db-up store-db-up db-down db-reset store-db-reset migrate migrate-status adapter-migrate adapter-run bootstrap-admin run fmt vet test test-race test-integration test-adapter-integration architecture contracts migrations-check check

help: ## Show the supported developer commands.
	@node scripts/show-help.mjs

setup: ## Restore Go dependencies.
	go -C services/ordering mod download
	go -C services/inventory-adapter mod download

db-up: ## Start the local PostgreSQL container.
	docker compose up -d --wait postgres

store-db-up: ## Start the disposable POS-compatible PostgreSQL container.
	docker compose up -d --wait store-postgres

db-down: ## Stop local services without deleting data.
	docker compose down

db-reset: ## Delete the disposable local database volume, recreate it, and migrate.
	docker compose stop postgres
	docker compose rm -f postgres
	docker volume rm b46-ordering_b46-postgres-data
	docker compose up -d --wait postgres
	$(MAKE) migrate DATABASE_URL=$(COMPOSE_DATABASE_URL)

store-db-reset: ## Delete only the disposable POS-compatible database volume and recreate it.
	docker compose stop store-postgres
	docker compose rm -f store-postgres
	docker volume rm b46-ordering_b46-store-postgres-data
	docker compose up -d --wait store-postgres
	$(MAKE) adapter-migrate STORE_DATABASE_URL=$(STORE_COMPOSE_DATABASE_URL)

migrate: ## Apply all Ordering database migrations.
	go -C services/ordering run ./cmd/migrate -dir migrations up

migrate-status: ## Show Ordering migration status.
	go -C services/ordering run ./cmd/migrate -dir migrations status

adapter-migrate: ## Apply inventory-adapter receipts to the disposable store database.
	go -C services/inventory-adapter run ./cmd/migrate -dir migrations up

adapter-run: ## Start the inventory adapter; set STORE_DATABASE_URL and service credentials first.
	go -C services/inventory-adapter run ./cmd/adapter

bootstrap-admin: ## Create the initial Admin from B46_BOOTSTRAP_ADMIN_* environment variables.
	go -C services/ordering run ./cmd/bootstrap-admin

run: ## Start the Ordering API with the current environment.
	go -C services/ordering run ./cmd/api

fmt: ## Format all Go source.
	go -C services/ordering fmt ./...
	go -C services/inventory-adapter fmt ./...

vet: ## Run Go static analysis.
	go -C services/ordering vet ./...
	go -C services/inventory-adapter vet ./...

test: ## Run Go tests once.
	go -C services/ordering test -count=1 ./...
	go -C services/inventory-adapter test -count=1 ./...

test-race: ## Run Go tests with the race detector.
	go -C services/ordering test -race -count=1 ./...
	go -C services/inventory-adapter test -race -count=1 ./...

test-integration: ## Run integration tests against TEST_DATABASE_URL.
	go -C services/ordering test -tags=integration -count=1 ./...

test-adapter-integration: ## Run adapter integration tests against STORE_TEST_DATABASE_URL.
	go -C services/inventory-adapter test -tags=integration -count=1 ./...

architecture: ## Check Clean Architecture dependency rules.
	node scripts/check-architecture.mjs

contracts: ## Validate OpenAPI and versioned contract examples.
	node scripts/check-contracts.mjs

migrations-check: ## Validate migration naming and append-only structure.
	node scripts/check-migrations.mjs

check: ## Run every completed backend gate through Phase 5.
	node scripts/check-phase5.mjs
