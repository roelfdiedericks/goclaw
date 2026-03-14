.PHONY: build run debug trace clean install test lint audit install-lint-tools skills-update skills-check changelog release-check release release-monitor re-release deps deps-check metadata

SHELL := /bin/bash
UNAME_S := $(shell uname -s)

ifeq ($(UNAME_S),Darwin)
ifeq ($(MAKE_VERSION),3.81)
$(error modern GNU Make is required on macOS. Install it with 'brew install make' and use 'gmake ...', or add '$(shell brew --prefix make 2>/dev/null)/libexec/gnubin' to your PATH)
endif
endif

BINARY := goclaw

# Version info from CHANGELOG.md (format: ## [VERSION] CHANNEL - DATE)
CHANGELOG_LINE := $(shell rg -m1 '^## \[[0-9]' CHANGELOG.md 2>/dev/null)
VERSION := $(word 2,$(subst ], ,$(subst [, ,$(CHANGELOG_LINE))))
CHANNEL := $(word 3,$(CHANGELOG_LINE))
CHANGELOG_DATE := $(word 5,$(CHANGELOG_LINE))

# Compute git tag (stable = vX.Y.Z, beta/rc = vX.Y.Z-channel.N)
TAG = $(shell sh -c 'if [ "$(CHANNEL)" = "stable" ]; then echo "v$(VERSION)"; else n=1; while git rev-parse "v$(VERSION)-$(CHANNEL).$$n" >/dev/null 2>&1; do n=`expr $$n + 1`; done; echo "v$(VERSION)-$(CHANNEL).$$n"; fi')

# Skills sync from upstream OpenClaw
OPENCLAW_REPO := https://github.com/openclaw/openclaw.git
SKILLS_TMP := .skills-upstream

# Platform-specific build configuration for local whisper.cpp support.
CPU_COUNT := $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)
BREW := $(shell command -v brew 2>/dev/null)
LIBOMP_PREFIX := $(shell if [ -n "$(BREW)" ]; then brew --prefix libomp 2>/dev/null || true; fi)

# CGO flags for SQLite FTS5 support (required for memory search)
# and Whisper.cpp STT support (run 'make deps' first on developer machines)
WHISPER_LIB := $(HOME)/.goclaw/lib/whisper
SQLITE_CGO_CFLAGS := -DSQLITE_ENABLE_FTS5
WHISPER_LIB_PATHS := -L$(WHISPER_LIB)

ifeq ($(UNAME_S),Darwin)
WHISPER_CMAKE_ARGS := -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF -DGGML_OPENMP=ON
ifneq ($(LIBOMP_PREFIX),)
WHISPER_CMAKE_ARGS += -DCMAKE_PREFIX_PATH=$(LIBOMP_PREFIX) -DOpenMP_ROOT=$(LIBOMP_PREFIX)
WHISPER_PLATFORM_LIBS := -Wl,-no_warn_duplicate_libraries $(LIBOMP_PREFIX)/lib/libomp.a
endif
else
WHISPER_CMAKE_ARGS := -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF
WHISPER_PLATFORM_LIBS := $(WHISPER_LIB_PATHS) -lm -lstdc++ -fopenmp -lpthread
endif

export CGO_CFLAGS := $(SQLITE_CGO_CFLAGS) -I$(WHISPER_LIB)
export CGO_LDFLAGS := $(WHISPER_LIB_PATHS) $(WHISPER_PLATFORM_LIBS)
export C_INCLUDE_PATH := $(WHISPER_LIB)
export LIBRARY_PATH := $(WHISPER_LIB)

build:
	go build -o $(BINARY) ./cmd/goclaw

metadata:
	go run ./cmd/metamerge --format

test:
	go test -v -vet=off ./...

run: build
	./$(BINARY) gateway

tui: build
	./$(BINARY) gateway -d -i


debug: build
	./$(BINARY) -d gateway --dev

trace: build
	./$(BINARY) -t gateway

menuconfig: build
	./$(BINARY) setup edit


clean:
	rm -f $(BINARY)

install: build
	cp $(BINARY) ~/bin/$(BINARY)

# =============================================================================
# Dependencies (run once per machine)
# =============================================================================

WHISPER_VERSION := 1.8.3

# Build whisper.cpp from source for STT support (static libraries)
deps:
	@command -v cmake >/dev/null 2>&1 || { \
		if [ "$(UNAME_S)" = "Darwin" ]; then \
			if [ -z "$(BREW)" ]; then \
				echo "FAIL: Homebrew not found. Install Homebrew from https://brew.sh first."; \
				exit 1; \
			fi; \
			echo "Installing cmake via Homebrew..."; \
			brew install cmake; \
		else \
			echo "FAIL: cmake not found. Install cmake using your package manager."; \
			exit 1; \
		fi; \
	}
ifeq ($(UNAME_S),Darwin)
	@if [ -z "$(BREW)" ]; then \
		echo "FAIL: Homebrew not found. Install Homebrew from https://brew.sh first."; \
		exit 1; \
	fi
	@echo "Checking Homebrew dependencies..."
	@brew list cmake >/dev/null 2>&1 || brew install cmake
	@brew list libomp >/dev/null 2>&1 || brew install libomp
endif
	@echo "Building whisper.cpp $(WHISPER_VERSION) (static)..."
	@mkdir -p "$(WHISPER_LIB)"
	@if [ ! -f "$(WHISPER_LIB)/libwhisper.a" ]; then \
		echo "Cloning whisper.cpp..."; \
		rm -rf /tmp/whisper.cpp; \
		git clone --depth 1 -b v$(WHISPER_VERSION) https://github.com/ggerganov/whisper.cpp /tmp/whisper.cpp; \
		echo "Building static libraries (CPU-only)..."; \
		cd /tmp/whisper.cpp && cmake -B build $(WHISPER_CMAKE_ARGS) && cmake --build build -j$(CPU_COUNT); \
		echo "Installing to $(WHISPER_LIB)..."; \
		cp /tmp/whisper.cpp/build/src/libwhisper.a "$(WHISPER_LIB)/"; \
		cp /tmp/whisper.cpp/build/ggml/src/libggml*.a "$(WHISPER_LIB)/"; \
		if [ "$(UNAME_S)" = "Darwin" ]; then \
			cp /tmp/whisper.cpp/build/ggml/src/ggml-metal/libggml-metal.a "$(WHISPER_LIB)/"; \
			cp /tmp/whisper.cpp/build/ggml/src/ggml-blas/libggml-blas.a "$(WHISPER_LIB)/"; \
		fi; \
		cp /tmp/whisper.cpp/include/whisper.h "$(WHISPER_LIB)/"; \
		cp /tmp/whisper.cpp/ggml/include/*.h "$(WHISPER_LIB)/"; \
		rm -rf /tmp/whisper.cpp; \
		echo "whisper.cpp installed to $(WHISPER_LIB)"; \
	else \
		echo "whisper.cpp already installed"; \
	fi
	@echo ""
	@echo "Next: Download a Whisper model to ~/.goclaw/stt/whisper/"
	@echo "  Tiny English (39MB):  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin"
	@echo "  Base English (142MB): https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"
	@echo "  Small English (466MB): https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin"

deps-check:
	@command -v cmake >/dev/null 2>&1 || { \
		echo "FAIL: cmake not found."; \
		exit 1; \
	}
ifeq ($(UNAME_S),Darwin)
	@if [ -z "$(BREW)" ]; then \
		echo "FAIL: Homebrew not found. Install Homebrew from https://brew.sh first."; \
		exit 1; \
	fi
	@brew list cmake >/dev/null 2>&1 || { \
		echo "FAIL: cmake not found in Homebrew. Run: make deps"; \
		exit 1; \
	}
	@brew list libomp >/dev/null 2>&1 || { \
		echo "FAIL: libomp not found in Homebrew. Run: make deps"; \
		exit 1; \
	}
	@echo "OK: Homebrew dependencies installed"
endif
	@if [ -f "$(WHISPER_LIB)/libwhisper.a" ] && [ -f "$(WHISPER_LIB)/libggml.a" ] && [ -f "$(WHISPER_LIB)/libggml-base.a" ] && [ -f "$(WHISPER_LIB)/libggml-cpu.a" ] && [ -f "$(WHISPER_LIB)/whisper.h" ]; then \
		echo "OK: whisper.cpp installed at $(WHISPER_LIB)"; \
	else \
		echo "FAIL: whisper.cpp not found. Run: make deps"; \
		exit 1; \
	fi
ifeq ($(UNAME_S),Darwin)
	@if [ -f "$(WHISPER_LIB)/libggml-metal.a" ] && [ -f "$(WHISPER_LIB)/libggml-blas.a" ]; then \
		echo "OK: macOS ggml backends installed"; \
	else \
		echo "FAIL: macOS ggml backend libs not found. Run: make deps"; \
		exit 1; \
	fi
endif

# Daemon shortcuts
start: build
	./$(BINARY) start

stop:
	./$(BINARY) stop

status:
	./$(BINARY) status

# Code quality and security
GOLANGCI_LINT := $(shell which golangci-lint 2>/dev/null)
GOVULNCHECK := $(shell which govulncheck 2>/dev/null)

install-lint-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

lint:
ifndef GOLANGCI_LINT
	@echo "Installing golangci-lint..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
endif
	golangci-lint run ./...

audit: lint
ifndef GOVULNCHECK
	@echo "Installing govulncheck..."
	@go install golang.org/x/vuln/cmd/govulncheck@latest
endif
	govulncheck ./...

# Update embedded skills catalog from upstream OpenClaw repo
skills-update:
	@echo "Fetching skills from upstream..."
	@rm -rf $(SKILLS_TMP)
	@mkdir -p $(SKILLS_TMP)
	@cd $(SKILLS_TMP) && git init --quiet
	@cd $(SKILLS_TMP) && git sparse-checkout init --cone
	@cd $(SKILLS_TMP) && git sparse-checkout set skills
	@cd $(SKILLS_TMP) && git remote add origin $(OPENCLAW_REPO)
	@cd $(SKILLS_TMP) && git fetch --quiet --depth 1 origin main
	@cd $(SKILLS_TMP) && git checkout --quiet main
	@rm -rf internal/skills/catalog
	@mv $(SKILLS_TMP)/skills internal/skills/catalog
	@rm -rf $(SKILLS_TMP)
	@echo "Skills catalog updated from upstream (internal/skills/catalog/)"

# Check for differences without modifying local skills
skills-check:
	@echo "Checking for upstream changes..."
	@rm -rf $(SKILLS_TMP)
	@mkdir -p $(SKILLS_TMP)
	@cd $(SKILLS_TMP) && git init --quiet
	@cd $(SKILLS_TMP) && git sparse-checkout init --cone
	@cd $(SKILLS_TMP) && git sparse-checkout set skills
	@cd $(SKILLS_TMP) && git remote add origin $(OPENCLAW_REPO)
	@cd $(SKILLS_TMP) && git fetch --quiet --depth 1 origin main
	@cd $(SKILLS_TMP) && git checkout --quiet main
	@diff -rq internal/skills/catalog $(SKILLS_TMP)/skills 2>/dev/null || echo "Differences found"
	@rm -rf $(SKILLS_TMP)

# =============================================================================
# Release Management
# =============================================================================

# Create new changelog entry (auto-increments patch version, keeps channel)
# After editing, prompts to commit and push
changelog:
	@current_ver=$$(grep -m1 '^## \[[0-9]' CHANGELOG.md | sed 's/## \[\([^]]*\)\].*/\1/'); \
	current_chan=$$(grep -m1 '^## \[[0-9]' CHANGELOG.md | sed 's/.*\] \([a-z]*\) -.*/\1/'); \
	if [ -z "$$current_ver" ]; then current_ver="0.0.0"; fi; \
	if [ -z "$$current_chan" ]; then current_chan="beta"; fi; \
	major=$$(echo $$current_ver | cut -d. -f1); \
	minor=$$(echo $$current_ver | cut -d. -f2); \
	patch=$$(echo $$current_ver | cut -d. -f3); \
	new_ver="$$major.$$minor.$$((patch+1))"; \
	today=$$(date +%Y-%m-%d); \
	echo "Current: [$$current_ver] $$current_chan"; \
	echo "New:     [$$new_ver] $$current_chan - $$today"; \
	echo ""; \
	sed -i '/^## \[Unreleased\]/a\\n## ['"$$new_ver"'] '"$$current_chan"' - '"$$today"'\n\n- ' CHANGELOG.md; \
	echo "Opening editor..."; \
	$${EDITOR:-vim} +11 CHANGELOG.md; \
	echo ""; \
	new_ver=$$(grep -m1 '^## \[[0-9]' CHANGELOG.md | sed 's/## \[\([^]]*\)\].*/\1/'); \
	read -p "Commit and push release $$new_ver? [y/N] " confirm; \
	if [ "$$confirm" = "y" ] || [ "$$confirm" = "Y" ]; then \
		git add CHANGELOG.md; \
		git commit -m "Release $$new_ver"; \
		git push; \
		echo ""; \
		echo "Done! Now run: make release"; \
	else \
		echo ""; \
		echo "Changelog updated but not committed."; \
		echo "To commit manually: git add CHANGELOG.md && git commit -m 'Release $$new_ver' && git push"; \
	fi

# Pre-release validation
release-check: lint audit
	@echo "=== Release Check ==="
	@# Must be on master branch
	@branch=$$(git branch --show-current); \
	if [ "$$branch" != "master" ]; then \
		echo "ERROR: Must be on master branch (currently on $$branch)"; exit 1; \
	fi
	@# No uncommitted changes to tracked files (ignores untracked)
	@if [ -n "$$(git status --porcelain -uno)" ]; then \
		echo "ERROR: Uncommitted changes to tracked files. Commit first."; \
		git status --short -uno; exit 1; \
	fi
	@# Up to date with remote
	@git fetch origin master --quiet 2>/dev/null || true; \
	if [ "$$(git rev-parse HEAD)" != "$$(git rev-parse origin/master 2>/dev/null || echo 'no-remote')" ]; then \
		echo "WARNING: Not synced with origin/master (or no remote). Continuing..."; \
	fi
	@# CHANGELOG has valid entry
	@test -n "$(VERSION)" || (echo "ERROR: No version in CHANGELOG" && exit 1)
	@test -n "$(CHANNEL)" || (echo "ERROR: No channel in CHANGELOG" && exit 1)
	@# CHANGELOG date is today
	@today=$$(date +%Y-%m-%d); \
	if [ "$(CHANGELOG_DATE)" != "$$today" ]; then \
		echo "ERROR: CHANGELOG date is $(CHANGELOG_DATE), not today ($$today)"; \
		echo "Did you forget to run 'make changelog'?"; exit 1; \
	fi
	@# Tag doesn't already exist (for stable)
	@if [ "$(CHANNEL)" = "stable" ] && git rev-parse "v$(VERSION)" >/dev/null 2>&1; then \
		echo "ERROR: Tag v$(VERSION) already exists"; exit 1; \
	fi
	@echo ""
	@echo "Version: $(VERSION)"
	@echo "Channel: $(CHANNEL)"
	@echo "Tag:     $(TAG)"
	@echo ""

# Create and push release tag
release: release-check
	@read -p "Create and push $(TAG)? [y/N] " confirm; \
	if [ "$$confirm" != "y" ] && [ "$$confirm" != "Y" ]; then \
		echo "Aborted."; exit 1; \
	fi
	@git tag -a $(TAG) -m "Release $(VERSION) ($(CHANNEL))"
	@git push origin $(TAG)
	@echo ""
	@echo "✓ Tagged $(TAG)"
	@echo "GitHub Actions will build and publish."
	@echo ""
	@echo "Run 'make release-monitor' to watch progress."

# Monitor GitHub Actions release workflow
release-monitor:
	@if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then \
		echo "Watching GitHub Actions (Ctrl+C to stop)..."; \
		gh run watch; \
	else \
		echo "Opening GitHub Actions in browser..."; \
		url="https://github.com/roelfdiedericks/goclaw/actions"; \
		if command -v xdg-open >/dev/null 2>&1; then xdg-open "$$url"; \
		elif command -v open >/dev/null 2>&1; then open "$$url"; \
		else echo "Visit: $$url"; fi; \
	fi

# Re-release: delete existing tag and recreate on HEAD
# Use when a release failed and you need to retry with the same version
# Use FORCE=1 to skip confirmation prompt
re-release:
	@version=$(call get_tag); \
	echo "=== Re-release $$version ==="; \
	if [ "$(FORCE)" != "1" ]; then \
		read -p "Delete and recreate tag $$version? [y/N] " confirm; \
		if [ "$$confirm" != "y" ] && [ "$$confirm" != "Y" ]; then \
			echo "Aborted."; exit 1; \
		fi; \
	fi; \
	echo "Deleting remote tag..."; \
	git push origin --delete $$version 2>/dev/null || true; \
	echo "Deleting local tag..."; \
	git tag -d $$version 2>/dev/null || true; \
	echo "Creating tag on HEAD..."; \
	git tag -a $$version -m "Release $(VERSION) ($(CHANNEL))"; \
	echo "Pushing tag..."; \
	git push origin $$version; \
	echo ""; \
	echo "Done! Run: make release-monitor"
