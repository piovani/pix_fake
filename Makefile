MODULES := mock/dict mock/spi e2e wallet
DICT_URL ?= http://localhost:8080
SPI_URL  ?= http://localhost:8081

.PHONY: up test test-integration lint e2e setup down reset logs status check

up: setup ## start everything: both mocks (make setup) and the wallet's own database
	$(MAKE) -C wallet up

test: ## unit and HTTP tests of both mocks and of the wallet (no database needed)
	@for m in mock/dict mock/spi wallet; do echo "==> $$m"; (cd $$m && go test -race ./...) || exit 1; done

test-integration: ## all tests of both mocks, including those on PostgreSQL (needs Docker; stop make setup first, they share ports)
	@for m in mock/dict mock/spi; do echo "==> $$m"; (cd $$m && $(MAKE) test-integration) || exit 1; done

lint: ## gofmt and go vet on every module
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }; go vet ./...) || exit 1; done

e2e: ## the closing test on its own PostgreSQL (needs Docker), see scripts/e2e.sh
	./scripts/e2e.sh

setup: ## build and start PostgreSQL and both mocks, with the seed data loaded (needs Docker)
	docker compose up -d --build --wait
	@echo
	@echo "DICT mock  $(DICT_URL)"
	@echo "SPI mock   $(SPI_URL)"
	@echo "Control API at /mock on each (make reset, make check); logs with make logs."

down: ## stop the environment (the data stays in the Docker volumes)
	docker compose down

reset: ## put both mocks back to their seed data, through the control API
	curl -fsS -X POST $(DICT_URL)/mock/reset
	curl -fsS -X POST $(SPI_URL)/mock/reset
	@echo "both mocks are back to their seed data"

logs: ## follow the logs of the mocks
	docker compose logs -f --tail=100 dict spi

status: ## show what is running
	docker compose ps

check: ## run the closing test against the environment started by make setup
	cd e2e && DICT_URL=$(DICT_URL) SPI_URL=$(SPI_URL) go test -count=1 -v ./...
