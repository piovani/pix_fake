DATABASE_URL ?= postgres://dict:dict@localhost:5432/dict?sslmode=disable

.PHONY: run test test-integration lint db-up db-down

run: db-up ## sobe o mock em :8080
	DICT_DATABASE_URL=$(DATABASE_URL) go run ./cmd/dict

test: ## testes unitários (sem banco)
	go test -race ./...

test-integration: db-up ## todos os testes, incluindo o repository no Postgres
	DICT_TEST_DATABASE_URL=$(DATABASE_URL) go test -race -count=1 ./...

lint:
	gofmt -l . && go vet ./...

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down
