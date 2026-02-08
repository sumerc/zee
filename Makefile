.PHONY: build build-linux-amd64 build-linux-arm64 test test-integration benchmark integration-test clean release

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags="-X main.version=$(VERSION)" -o zee

build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -s -w" -o zee-linux-amd64

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="-X main.version=$(VERSION) -s -w" -o zee-linux-arm64

test:
	go test -v ./encoder/

integration-test:
	@test -n "$(WAV)" || (echo "Usage: make integration-test WAV=file.wav" && exit 1)
	@if [ -f .env ]; then export $$(grep -v '^#' .env | xargs); fi; \
	test -n "$$GROQ_API_KEY" || (echo "Error: GROQ_API_KEY not set (create .env or export it)" && exit 1); \
	go run test/integration_test.go $(WAV)

benchmark: build
	@test -n "$(WAV)" || (echo "Usage: make benchmark WAV=file.wav [RUNS=5]" && exit 1)
	@if [ -f .env ]; then export $$(grep -v '^#' .env | xargs); fi; \
	./zee -benchmark $(WAV) -runs $(or $(RUNS),3)

test-integration:
	@tmp=$$(mktemp -d) && \
	go build -o "$$tmp/zee-test-bin" . && \
	ZEE_TEST_BIN="$$tmp/zee-test-bin" go test -tags integration -v -timeout 120s -count=1 ./test/ ; \
	status=$$? ; rm -rf "$$tmp" ; exit $$status

clean:
	rm -f zee

release:
	@test -n "$(V)" || (echo "Usage: make release V=0.1.0" && exit 1)
	git tag v$(V)
	git push origin v$(V)
