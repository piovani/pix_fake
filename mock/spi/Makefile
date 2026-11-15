DATABASE_URL ?= postgres://spi:spi@localhost:5433/spi?sslmode=disable

.PHONY: run test test-integration lint db-up db-down

run: db-up ## start the mock on :8081
	SPI_DATABASE_URL=$(DATABASE_URL) go run ./cmd/spi

test: ## unit tests (no database needed)
	go test -race ./...

test-integration: db-up ## all tests, including the Postgres repository and end-to-end tests
	SPI_TEST_DATABASE_URL=$(DATABASE_URL) go test -race -count=1 ./...

lint:
	gofmt -l . && go vet ./...

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down
