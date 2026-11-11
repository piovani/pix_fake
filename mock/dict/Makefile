DATABASE_URL ?= postgres://dict:dict@localhost:5432/dict?sslmode=disable

.PHONY: run seed test test-integration lint db-up db-down

run: db-up ## start the mock on :8080
	DICT_DATABASE_URL=$(DATABASE_URL) go run ./cmd/dict

seed: db-up ## load the test keys into the database (idempotent)
	DICT_DATABASE_URL=$(DATABASE_URL) go run ./cmd/seed

test: ## unit tests (no database needed)
	go test -race ./...

test-integration: db-up ## all tests, including the Postgres repository and end-to-end tests
	DICT_TEST_DATABASE_URL=$(DATABASE_URL) go test -race -count=1 ./...

lint:
	gofmt -l . && go vet ./...

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down
