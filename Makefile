.PHONY: build test benchmark integration-test clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags="-X main.version=$(VERSION) -extldflags=-Wl,-no_warn_duplicate_libraries" -o ses9000

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
	./ses9000 -benchmark $(WAV) -runs $(or $(RUNS),3)

clean:
	rm -f ses9000
