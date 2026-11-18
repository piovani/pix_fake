DATABASE_URL ?= postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable

.PHONY: up proto test-integration

up: ## start the wallet's PostgreSQL on :5434 and load the test accounts (needs Docker and Go)
	docker compose up -d --wait
	WALLET_DATABASE_URL=$(DATABASE_URL) go run ./cmd/seed
	@echo "wallet database: $(DATABASE_URL)"

proto: ## generate the Go code of the gRPC contract (api/proto) into gen/ (needs protoc, protoc-gen-go and protoc-gen-go-grpc)
	protoc -I api/proto \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		$$(find api/proto -name '*.proto')

test: ## all tests, including the ones on PostgreSQL (needs Docker)
	docker compose up -d --wait
	WALLET_TEST_DATABASE_URL=$(DATABASE_URL) go test -race -count=1 ./...
