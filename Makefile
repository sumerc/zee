.PHONY: build build-linux-amd64 build-linux-arm64 test test-integration benchmark integration-test clean bump-version release icns app

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags="-X main.version=$(VERSION)" -o zee

build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -s -w" -o zee-linux-amd64

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="-X main.version=$(VERSION) -s -w" -o zee-linux-arm64

test:
	go test -race -v ./...

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
	ZEE_TEST_BIN="$$tmp/zee-test-bin" go test -race -tags integration -v -timeout 120s -count=1 ./test/ ; \
	status=$$? ; rm -rf "$$tmp" ; exit $$status

icns:
	packaging/mkicns.sh packaging/appicon.png

app: build icns
	packaging/mkdmg.sh zee $(VERSION) Zee-$(VERSION).dmg

clean:
	rm -f zee Zee-*.dmg

bump-version:
	@branch=$$(git rev-parse --abbrev-ref HEAD); \
	if [ "$$branch" != "main" ]; then echo "ERROR: must be on main branch" && exit 1; fi; \
	ver="$(VER)"; \
	if [ -z "$$ver" ]; then echo "usage: make bump-version VER=0.3.7" && exit 1; fi; \
	latest=$$(git tag --sort=-v:refname | head -1); \
	claude -p "Look at the git log from tag $$latest to HEAD. Write a CHANGELOG.md entry for Zee version v$$ver in this exact format: ## v$$ver, blank line, then concise '- ' bullets only. No Added/Changed/Fixed headings. Skip merge commits and CI-only changes. Output ONLY the changelog entry, no code fences." > /tmp/zee-changelog-entry; \
	echo "" >> /tmp/zee-changelog-entry; \
	sed -i '' '/^## Unreleased/r /tmp/zee-changelog-entry' CHANGELOG.md; \
	rm -f /tmp/zee-changelog-entry; \
	echo "CHANGELOG.md updated — review and edit as needed"

release:
	@branch=$$(git rev-parse --abbrev-ref HEAD); \
	if [ "$$branch" != "main" ]; then echo "ERROR: must be on main branch" && exit 1; fi; \
	ver="$(VER)"; \
	if [ -z "$$ver" ]; then echo "usage: make release VER=0.3.7" && exit 1; fi; \
	grep -q "^## v$$ver$$" CHANGELOG.md || (echo "ERROR: v$$ver missing from CHANGELOG.md — run make bump-version first" && exit 1); \
	git diff --quiet || (echo "ERROR: working tree has uncommitted changes" && exit 1); \
	git diff --cached --quiet || (echo "ERROR: index has staged changes" && exit 1); \
	notes=$$(awk "/^## v$$ver$$/{found=1; next} /^## /{if(found) exit} found{print}" CHANGELOG.md | sed '/^$$/d'); \
	echo ""; \
	echo "v$$ver Release Notes:"; \
	echo ""; \
	echo "$$notes"; \
	echo ""; \
	read -p "create and push tag v$$ver? [y/N] " confirm; \
	case "$$confirm" in y|Y) ;; *) echo "aborted" && exit 1;; esac; \
	git tag -a "v$$ver" -m "v$$ver"; \
	git push origin "v$$ver"
