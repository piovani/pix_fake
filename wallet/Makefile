.PHONY: up

up: ## start the wallet's PostgreSQL on :5434 (needs Docker); the wallet's services join it once the stack is chosen
	docker compose up -d --wait
	@echo "wallet database: postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable"
