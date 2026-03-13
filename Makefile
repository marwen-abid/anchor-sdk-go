.PHONY: all check ci build test lint vet fmt tidy vuln \
        integration integration-basic integration-etherfuse \
        run-basic run-etherfuse clean

# ── Ports used by example servers ────────────────────────────────────────────
BASIC_PORT     ?= 8000
ETHERFUSE_PORT ?= 8001

# ── Primary targets ───────────────────────────────────────────────────────────

## check: lint + vet + tests (run after every big change)
check: lint vet test

## ci: everything in check + module hygiene + integration tests (run before push)
ci: check tidy integration

# ── Build ─────────────────────────────────────────────────────────────────────

build:
	go build ./...

# ── Tests ─────────────────────────────────────────────────────────────────────

test:
	go test -race ./...

# ── Linting & formatting ──────────────────────────────────────────────────────

lint:
	golangci-lint run ./...

vet:
	go vet ./...

fmt:
	gofumpt -l -w .

# ── Module hygiene ────────────────────────────────────────────────────────────

tidy:
	go mod tidy
	git diff --exit-code go.mod go.sum

vuln:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# ── Integration tests ─────────────────────────────────────────────────────────

integration: integration-basic integration-etherfuse

integration-basic:
	@echo "==> Starting basic-anchor on port $(BASIC_PORT)..."
	@go run ./examples/basic-anchor -port $(BASIC_PORT) &
	@echo "==> Waiting for server..."
	@for i in $$(seq 1 30); do \
		curl -sf http://localhost:$(BASIC_PORT)/.well-known/stellar.toml >/dev/null 2>&1 && \
			echo "==> Server ready" && break; \
		echo "    waiting... ($$i/30)"; \
		sleep 1; \
	done
	@echo "==> Running anchor-tests (SEP-1, SEP-10, SEP-24)..."
	-docker run --rm --network host stellar/anchor-tests:latest \
		--home-domain http://localhost:$(BASIC_PORT) --seps 1 10 24
	@kill $$(lsof -ti tcp:$(BASIC_PORT)) 2>/dev/null || true
	@echo "==> basic-anchor integration done"

integration-etherfuse:
	@if [ -z "$$ETHERFUSE_API_KEY" ] && [ ! -f examples/anchor-etherfuse/.env ]; then \
		echo "==> Skipping integration-etherfuse: ETHERFUSE_API_KEY not set"; \
		exit 0; \
	fi
	@echo "==> Starting anchor-etherfuse on port $(ETHERFUSE_PORT)..."
	@ANCHOR_PORT=$(ETHERFUSE_PORT) ANCHOR_DOMAIN=localhost:$(ETHERFUSE_PORT) \
		go run ./examples/anchor-etherfuse &
	@echo "==> Waiting for server..."
	@for i in $$(seq 1 30); do \
		curl -sf http://localhost:$(ETHERFUSE_PORT)/.well-known/stellar.toml >/dev/null 2>&1 && \
			echo "==> Server ready" && break; \
		echo "    waiting... ($$i/30)"; \
		sleep 1; \
	done
	@echo "==> Running anchor-tests (SEP-1, SEP-10, SEP-24)..."
	-docker run --rm --network host stellar/anchor-tests:latest \
		--home-domain http://localhost:$(ETHERFUSE_PORT) --seps 1 10 24
	@kill $$(lsof -ti tcp:$(ETHERFUSE_PORT)) 2>/dev/null || true
	@echo "==> anchor-etherfuse integration done"

# ── Run example servers (development) ────────────────────────────────────────

run-basic:
	go run ./examples/basic-anchor -port $(BASIC_PORT)

run-etherfuse:
	go run ./examples/anchor-etherfuse

# ── Housekeeping ──────────────────────────────────────────────────────────────

clean:
	rm -f coverage.out
