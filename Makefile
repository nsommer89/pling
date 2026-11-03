# pling: run `make help` for the targets.
GO        ?= go
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)

.PHONY: help build check fmt test race run-server run-client release snapshot docker clean

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build: ## build bin/pling and bin/plingd
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/pling ./cmd/plingd

check: ## gofmt, go vet and all tests (what CI runs)
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) test ./...

fmt: ## format all Go files
	gofmt -w .

test: ## run the tests
	$(GO) test ./...

race: ## run the tests with the race detector (needs cgo)
	$(GO) test -race ./...

run-server: build ## run plingd on :8080 with ./users.yaml (signed host localhost:8080)
	PLING_HOST=$${PLING_HOST:-localhost:8080} PLING_USERS=$${PLING_USERS:-./users.yaml} bin/plingd -v

run-client: build ## run the client against the local server
	bin/pling --server ws://localhost:8080/ws

release: ## build and publish a release with GoReleaser (needs a tag and GITHUB_TOKEN)
	goreleaser release --clean

snapshot: ## build release archives into dist/ without publishing
	goreleaser release --snapshot --clean

docker: ## build the plingd container image
	docker build -t plingd:local .

clean: ## remove build output (bin/ and dist/)
	rm -rf bin dist
