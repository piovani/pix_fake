DATABASE_URL ?= postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable

.PHONY: up proto proto-check test-integration

up: ## start the wallet's PostgreSQL on :5434 and load the test accounts (needs Docker and Go)
	docker compose up -d --wait
	WALLET_DATABASE_URL=$(DATABASE_URL) go run ./cmd/seed
	@echo "wallet database: $(DATABASE_URL)"

PROTOC_GEN_GO      := v1.36.12
PROTOC_GEN_GO_GRPC := v1.6.2
BIN := $(CURDIR)/.bin

proto: ## generate the Go code of the gRPC contract (api/proto) into gen/ (needs protoc; the plugins are installed at pinned versions)
	GOBIN=$(BIN) go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO)
	GOBIN=$(BIN) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC)
	protoc -I api/proto \
		--plugin=protoc-gen-go=$(BIN)/protoc-gen-go --plugin=protoc-gen-go-grpc=$(BIN)/protoc-gen-go-grpc \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		$$(find api/proto -name '*.proto')

proto-check: proto ## fail if gen/ is not what the .proto files generate (version comments ignored)
	git diff --exit-code -I '^// .*protoc' -- gen/

test-integration: ## all tests, including the ones on PostgreSQL (needs Docker)
	docker compose up -d --wait
	WALLET_TEST_DATABASE_URL=$(DATABASE_URL) go test -race -count=1 ./...
