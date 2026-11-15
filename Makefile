MODULES := mock/dict mock/spi

.PHONY: test e2e

test: ## unit and HTTP tests of both mocks (no database needed)
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && go test -race ./...) || exit 1; done

e2e: ## the closing test: both mocks on PostgreSQL (needs Docker), see scripts/e2e.sh
	./scripts/e2e.sh
