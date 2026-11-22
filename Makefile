MODULES := mock/dict mock/spi e2e wallet
DICT_URL ?= http://localhost:8080
SPI_URL  ?= http://localhost:8081
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.10.0

.PHONY: up test test-integration lint staticcheck vuln tidy proto-check e2e coverage setup down reset logs status check

up: setup ## start everything: both mocks (make setup) and the wallet's own database
	$(MAKE) -C wallet up

test: ## unit and HTTP tests of both mocks and of the wallet (no database needed)
	@for m in mock/dict mock/spi wallet; do echo "==> $$m"; (cd $$m && go test -race ./...) || exit 1; done

test-integration: ## all tests of both mocks and the wallet, including those on PostgreSQL (needs Docker; stop make setup first, the mocks share ports with it)
	@for m in mock/dict mock/spi wallet; do echo "==> $$m"; (cd $$m && $(MAKE) test-integration) || exit 1; done

lint: ## gofmt and go vet on every module
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }; go vet ./...) || exit 1; done

staticcheck: ## staticcheck on every module
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && go run $(STATICCHECK) ./...) || exit 1; done

vuln: ## govulncheck on every module: fails only on a vulnerability the code actually calls
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && go run $(GOVULNCHECK) ./...) || exit 1; done

tidy: ## fail if a go.mod or go.sum is not what go mod tidy makes
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && go mod tidy -diff) || exit 1; done

proto-check: ## fail if wallet/gen is not what the .proto files generate (needs protoc)
	$(MAKE) -C wallet proto-check

e2e: ## the closing test of the mocks and the wallet's end-to-end tests, on their own PostgreSQL (needs Docker; stop make setup first), see scripts/e2e.sh
	./scripts/e2e.sh

coverage: ## coverage of the wallet: end to end and unit tests, each and merged; report in .coverage/ (needs what e2e needs), see scripts/coverage.sh
	./scripts/coverage.sh

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
