.PHONY: up proto

up: ## start the wallet's PostgreSQL on :5434 (needs Docker); the wallet's services join it later
	docker compose up -d --wait
	@echo "wallet database: postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable"

proto: ## generate the Go code of the gRPC contract (api/proto) into gen/ (needs protoc, protoc-gen-go and protoc-gen-go-grpc)
	protoc -I api/proto \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		$$(find api/proto -name '*.proto')
