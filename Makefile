.PHONY: build build-linux-amd64 build-linux-arm64 test test-integration benchmark bench-local bench-save clean bump-version release icns app parakeet-lib whisper-lib download-models manifest model-release

# --match 'v*' keeps model-release tags (models-vN) out of the app version.
VERSION ?= $(shell git describe --tags --match 'v*' --always --dirty 2>/dev/null || echo "dev")

# Local STT (Parakeet) is a darwin/arm64-only cgo feature. On that host we build
# the static parakeet.cpp + ggml archives first and stamp the macOS deploy
# target; everywhere else the no-cgo stub is compiled and these are no-ops.
MACOS_MIN     := 11.0
PARAKEET_DIR  := third_party/parakeet.cpp
PARAKEET_LIB  := $(PARAKEET_DIR)/build-release/libparakeet.a
# Whisper links the SAME ggml parakeet builds (one ggml in the process, and it is
# the patched one — see whisper-lib). GGML_PREFIX is where parakeet's build is
# installed so whisper's find_package(ggml) can resolve it; nothing leaves the
# repo, it is a plain copy into the gitignored build dir.
GGML_PREFIX   := $(CURDIR)/$(PARAKEET_DIR)/build-release/ggml-prefix
WHISPER_DIR   := third_party/whisper.cpp
WHISPER_LIB   := $(WHISPER_DIR)/build-release/src/libwhisper.a
HOST          := $(shell go env GOOS)/$(shell go env GOARCH)
ifeq ($(HOST),darwin/arm64)
CGO_ENV := MACOSX_DEPLOYMENT_TARGET=$(MACOS_MIN) CGO_CFLAGS=-mmacosx-version-min=$(MACOS_MIN) CGO_LDFLAGS=-mmacosx-version-min=$(MACOS_MIN)
endif

build: whisper-lib download-models
	$(CGO_ENV) go build -ldflags="-X main.version=$(VERSION)" -o zee

# The dev model folder `localmodels download` writes to (cmd/localmodels keeps
# the same layout). Test binaries run from a temp dir, so their default lookup
# can't find it — bench-local points ZEE_MODELS_DIR here.
MODELS_VERSION  := $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' localmodel/localmodel.go)
# Absolute: `go test` runs the binary with cwd set to the package dir.
MODELS_DEV_DIR  := $(CURDIR)/models/local/$(MODELS_VERSION)

# Fetch the mandatory (PreFetch) local models into the dev folder from the
# pinned models-<Version> GitHub release. Reuses the localmodel registry +
# downloader (single source of truth) and is a per-file no-op when present.
download-models:
	go run ./cmd/localmodels download

# Regenerate localmodel/manifest.txt (the bash-readable projection of the
# registry that install.sh reads) from localmodel.go. Commit the result;
# TestManifestUpToDate fails if it drifts.
manifest:
	go run ./cmd/localmodels manifest > localmodel/manifest.txt
	@echo "==> localmodel/manifest.txt regenerated — commit it"

# Configure once (submodule init + cmake, which auto-applies the in-tree ggml
# patches), then always `cmake --build` so source changes recompile incrementally
# and relink — a no-op when nothing changed. After a submodule bump, delete
# build-release to force a reconfigure (re-applies the patch to the new ggml).
parakeet-lib:
	@if [ "$(HOST)" != "darwin/arm64" ]; then exit 0; fi; \
	if [ ! -f $(PARAKEET_DIR)/CMakeLists.txt ]; then \
	  echo "==> initializing parakeet.cpp submodule (first checkout)"; \
	  git submodule update --init --recursive $(PARAKEET_DIR); \
	fi; \
	if [ ! -d $(PARAKEET_DIR)/build-release ]; then \
	  echo "==> configuring parakeet.cpp (one-time)"; \
	  cmake -S $(PARAKEET_DIR) -B $(PARAKEET_DIR)/build-release \
	    -DBUILD_SHARED_LIBS=OFF -DPARAKEET_SHARED=OFF -DPARAKEET_BUILD_CLI=OFF \
	    -DPARAKEET_GGML_METAL=ON -DGGML_NATIVE=OFF \
	    -DCMAKE_OSX_DEPLOYMENT_TARGET=$(MACOS_MIN) \
	    -DCMAKE_INSTALL_PREFIX=$(GGML_PREFIX) \
	    -DCMAKE_C_FLAGS="-mcpu=apple-m1" -DCMAKE_CXX_FLAGS="-mcpu=apple-m1"; \
	fi && \
	cmake --build $(PARAKEET_DIR)/build-release -j && \
	cmake --install $(PARAKEET_DIR)/build-release >/dev/null

# Build libwhisper.a against the ggml parakeet just installed, instead of the
# copy whisper vendors. Two copies would mean duplicate ggml symbols at the cgo
# link AND — worse — whisper compiling against unpatched headers while linking
# parakeet's patched archives, which is silent memory corruption rather than an
# error. -DWHISPER_USE_SYSTEM_GGML=ON + the prefix is what prevents both.
whisper-lib: parakeet-lib
	@if [ "$(HOST)" != "darwin/arm64" ]; then exit 0; fi; \
	if [ ! -f $(WHISPER_DIR)/CMakeLists.txt ]; then \
	  echo "==> initializing whisper.cpp submodule (first checkout)"; \
	  git submodule update --init --recursive $(WHISPER_DIR); \
	fi; \
	if [ ! -d $(WHISPER_DIR)/build-release ]; then \
	  echo "==> configuring whisper.cpp (one-time)"; \
	  cmake -S $(WHISPER_DIR) -B $(WHISPER_DIR)/build-release \
	    -DWHISPER_USE_SYSTEM_GGML=ON -DCMAKE_PREFIX_PATH=$(GGML_PREFIX) \
	    -DBUILD_SHARED_LIBS=OFF -DGGML_METAL=ON \
	    -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=OFF \
	    -DWHISPER_BUILD_SERVER=OFF \
	    -DCMAKE_OSX_DEPLOYMENT_TARGET=$(MACOS_MIN) \
	    -DCMAKE_C_FLAGS="-mcpu=apple-m1" -DCMAKE_CXX_FLAGS="-mcpu=apple-m1"; \
	fi && \
	cmake --build $(WHISPER_DIR)/build-release -j

build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -s -w" -o zee-linux-amd64

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="-X main.version=$(VERSION) -s -w" -o zee-linux-arm64

test: whisper-lib
	$(CGO_ENV) go test -race -v ./...

benchmark: build
	@test -n "$(WAV)" || (echo "Usage: make benchmark WAV=file.wav [RUNS=5]" && exit 1)
	@if [ -f .env ]; then export $$(grep -v '^#' .env | xargs); fi; \
	./zee -benchmark $(WAV) -runs $(or $(RUNS),3)

# Isolated local-inference benchmark (no capture/encode/network): each clip x
# each downloaded model, reporting ns/op plus a realtime factor (xRT). WAV is a
# file or a directory of clips (16 kHz mono 16-bit), default test/data/short.wav
# — e.g. WAV="$$HOME/Library/Application Support/zee/samples" to benchmark your
# own saved recordings. Pipe to a file and compare runs with benchstat.
bench-local: whisper-lib download-models
	ZEE_BENCH_WAV="$(WAV)" ZEE_MODELS_DIR="$(MODELS_DEV_DIR)" $(CGO_ENV) go test ./internal/localbench \
		-run '^$$' -bench BenchmarkTranscribe -benchtime $(or $(RUNS),3)x -v

# Append a bench-local run to BENCH_FILE as a labelled per-machine baseline
# block, so results from several machines accumulate in one comparable file.
# Same WAV=/RUNS= options as bench-local; one invocation = one block.
BENCH_FILE ?= benchmark.txt
bench-save: whisper-lib download-models
	@test -f "$(BENCH_FILE)" || { \
	  echo "# zee — local (Parakeet + Whisper) inference baselines"; \
	  echo "#"; \
	  echo "# One block per machine+corpus, appended by: make bench-save [WAV=...] [RUNS=n]"; \
	  echo "# Isolated local inference (model load + Transcribe) — no capture, encode or network."; \
	  echo "#"; \
	  echo "# xRT = realtime factor: audio seconds processed per wall second (higher is faster)."; \
	  echo "# Compare two runs with: benchstat old.txt $(BENCH_FILE)"; \
	} > "$(BENCH_FILE)"
	@{ \
	  echo ""; \
	  echo "################################################################################"; \
	  if [ "$$(uname)" = Darwin ]; then \
	    echo "## MACHINE:  $$(sysctl -n machdep.cpu.brand_string), $$(sysctl -n hw.ncpu) cores, $$(($$(sysctl -n hw.memsize)/1073741824)) GB RAM, macOS $$(sw_vers -productVersion)"; \
	  else \
	    echo "## MACHINE:  $$(uname -sm)"; \
	  fi; \
	  echo "## CORPUS:   $(or $(WAV),test/data/short.wav)"; \
	  echo "## RUNS:     $(or $(RUNS),3)"; \
	  echo "## COMMIT:   $$(git rev-parse --short HEAD 2>/dev/null)$$(git diff --quiet 2>/dev/null || echo ' (dirty)')"; \
	  echo "## MODELS:   $(MODELS_VERSION)"; \
	  echo "## DATE:     $$(date '+%Y-%m-%dT%H:%M:%S%z')"; \
	  echo "################################################################################"; \
	  echo ""; \
	} >> "$(BENCH_FILE)"
	@ZEE_BENCH_WAV="$(WAV)" ZEE_MODELS_DIR="$(MODELS_DEV_DIR)" $(CGO_ENV) go test ./internal/localbench \
		-run '^$$' -bench BenchmarkTranscribe -benchtime $(or $(RUNS),3)x -v 2>&1 \
		| grep -vE 'duplicate librar|Backend using device' >> "$(BENCH_FILE)"
	@echo "appended a baseline block to $(BENCH_FILE)"

test-integration: whisper-lib
	@tmp=$$(mktemp -d) && \
	$(CGO_ENV) go build -o "$$tmp/zee-test-bin" . && \
	ZEE_TEST_BIN="$$tmp/zee-test-bin" $(CGO_ENV) go test -race -tags integration -v -timeout 600s -count=1 ./test/ ; \
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

# Publish the offline Parakeet GGUF models as an immutable, never-"latest"
# GitHub release. Prereq: each .gguf's entry (Filename + SHA256) already exists
# in localmodel.go. Copy the ggufs into a folder, then:
#   make model-release MODELS_DIR=./out MODELS_TAG=models-v2
# It regenerates the manifest, verifies the local ggufs against the registry's
# SHA256s, and uploads them with --latest=false so the app-release "latest"
# pointer is never hijacked. install.sh reads localmodel/manifest.txt (from main)
# for filenames + hashes + prefetch flags — nothing is hardcoded there. Adopting
# the models is a separate commit: localmodel.go + the regenerated manifest.txt
# (+ Version / install.sh MODELS_TAG if the tag changed).
model-release: manifest
	@test -n "$(MODELS_TAG)" || (echo "usage: make model-release MODELS_DIR=./dir MODELS_TAG=models-vN" && exit 1)
	@test -d "$(MODELS_DIR)" || (echo "ERROR: MODELS_DIR '$(MODELS_DIR)' not found" && exit 1)
	@ls "$(MODELS_DIR)"/*.gguf "$(MODELS_DIR)"/*.bin >/dev/null 2>&1 || (echo "ERROR: no .gguf/.bin files in $(MODELS_DIR)" && exit 1)
	@case "$(MODELS_TAG)" in models-*) ;; *) echo "ERROR: MODELS_TAG must start with 'models-'" && exit 1;; esac
	@echo "==> verifying $(MODELS_DIR) model files against the localmodel registry..."
	@cd "$(MODELS_DIR)" && for f in *.gguf *.bin; do \
	  [ -f "$$f" ] || continue; \
	  want=$$(awk -v f="$$f" '$$1==f {print $$2}' "$(CURDIR)/localmodel/manifest.txt"); \
	  test -n "$$want" || { echo "ERROR: $$f is not in localmodel.go — add its entry first" && exit 1; }; \
	  got=$$(shasum -a 256 "$$f" | awk '{print $$1}'); \
	  test "$$want" = "$$got" || { echo "ERROR: $$f sha mismatch (registry $$want, file $$got)" && exit 1; }; \
	  echo "  ok $$f"; \
	done
	gh release create "$(MODELS_TAG)" --repo sumerc/zee --latest=false \
	  --title "Local STT $(MODELS_TAG)" \
	  --notes "Model files for zee local STT: NVIDIA NeMo Parakeet GGUF (CC-BY-4.0) and OpenAI Whisper ggml (MIT)." \
	  $$(ls "$(MODELS_DIR)"/*.gguf "$(MODELS_DIR)"/*.bin 2>/dev/null)
	@echo "==> published $(MODELS_TAG) (not marked latest)."
	@echo "==> commit: localmodel.go + localmodel/manifest.txt (+ install.sh MODELS_TAG if bumped)."

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
