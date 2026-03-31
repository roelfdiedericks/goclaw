.PHONY: build embtest embtest-xla embtest-ort embtest-xla-deps-check embtest-ort-deps-check run debug trace clean install test sandbox-test sandbox-test-short sandbox-test-ci lint audit supply-chain-check install-lint-tools skills-update skills-check changelog release-check release release-monitor re-release deps deps-check metadata

SHELL := /bin/bash
UNAME_S := $(shell uname -s)

ifeq ($(UNAME_S),Darwin)
ifeq ($(MAKE_VERSION),3.81)
$(error modern GNU Make is required on macOS. Install it with 'brew install make' and use 'gmake ...', or add '$(shell brew --prefix make 2>/dev/null)/libexec/gnubin' to your PATH)
endif
endif

BINARY := goclaw
RELEASE_TOOL := go run ./cmd/releasetool

VERSION := $(shell $(RELEASE_TOOL) current --field version 2>/dev/null)
CHANNEL := $(shell $(RELEASE_TOOL) current --field channel 2>/dev/null)
CHANGELOG_DATE := $(shell $(RELEASE_TOOL) current --field date 2>/dev/null)
RELEASE_TAG := $(shell $(RELEASE_TOOL) current --field release-tag 2>/dev/null)

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

# Run dependency checks before build by default.
# Use SKIP_DEPS_CHECK=1 to bypass explicitly (for controlled CI/dev scenarios).
BUILD_DEPS_CHECK := deps-check
ifeq ($(SKIP_DEPS_CHECK),1)
BUILD_DEPS_CHECK :=
endif

build: $(BUILD_DEPS_CHECK)
	go build -o $(BINARY) ./cmd/goclaw

embtest:
	go build -o embtest ./cmd/embtest

# =============================================================================
# embtest backend builds
# =============================================================================

# Default embtest build: Hugot + GoMLX simplego backend (portable CPU baseline)

embtest-xla:
	go build -tags XLA -o embtest-xla ./cmd/embtest

embtest-ort:
	go build -tags ORT -o embtest-ort ./cmd/embtest

# =============================================================================
# embtest backend dependency checks
# =============================================================================

# XLA build/runtime notes:
# - Requires cgo-enabled build environment
# - Requires PJRT/XLA runtime plugin installed on the host
# - Requires the rust tokenizer static library (libtokenizers.a) at build time
# - go-xla searches PJRT plugins via PJRT_PLUGIN_LIBRARY_PATH (":"-separated on Unix)
# - Common plugin filenames include pjrt-plugin-*.so, pjrt_plugin_*.so, pjrt_c_api_*_plugin.so
# - TPU setups may expose libtpu.so instead of a pjrt_plugin_* file
embtest-xla-deps-check:
	@if [ -z "$$CGO_ENABLED" ]; then \
		echo "NOTE: CGO_ENABLED not explicitly set; go build usually defaults to cgo=1 on supported hosts."; \
	fi
	@found=""; \
	check_dir() { \
		dir="$$1"; \
		[ -d "$$dir" ] || return 1; \
		compgen -G "$$dir/pjrt-plugin-*.so" >/dev/null || \
		compgen -G "$$dir/pjrt_plugin_*.so" >/dev/null || \
		compgen -G "$$dir/pjrt_c_api_*_plugin.so" >/dev/null || \
		[ -f "$$dir/libtpu.so" ]; \
	}; \
	if [ -n "$$PJRT_PLUGIN_LIBRARY_PATH" ]; then \
		IFS=':' read -r -a pjrt_paths <<< "$$PJRT_PLUGIN_LIBRARY_PATH"; \
		for dir in "$${pjrt_paths[@]}"; do \
			if check_dir "$$dir"; then \
				found="$$dir"; \
				break; \
			fi; \
		done; \
		if [ -n "$$found" ]; then \
			echo "OK: found XLA/PJRT plugin in PJRT_PLUGIN_LIBRARY_PATH at $$found"; \
			exit 0; \
		fi; \
		echo "FAIL: PJRT_PLUGIN_LIBRARY_PATH is set, but no plugin files were found in: $$PJRT_PLUGIN_LIBRARY_PATH"; \
		echo "Expected patterns: pjrt-plugin-*.so, pjrt_plugin_*.so, pjrt_c_api_*_plugin.so, or libtpu.so"; \
		exit 1; \
	fi; \
	for dir in /usr/local/lib/go-xla /usr/lib/go-xla /usr/local/lib /usr/lib; do \
		if check_dir "$$dir"; then \
			found="$$dir"; \
			break; \
		fi; \
	done; \
	if [ -n "$$found" ]; then \
		echo "OK: found XLA/PJRT plugin files in $$found"; \
	else \
		echo "FAIL: XLA runtime plugin not found in standard locations."; \
		echo "Checked: /usr/local/lib/go-xla, /usr/lib/go-xla, /usr/local/lib, /usr/lib"; \
		echo "Set PJRT_PLUGIN_LIBRARY_PATH to the plugin directory or install one, e.g.:"; \
		echo "  GOPROXY=direct go run github.com/gomlx/go-xla/cmd/pjrt_installer@latest -plugin=linux -version=<VERSION> -path=/usr/local/lib/go-xla"; \
		exit 1; \
	fi; \
	if [ -f "/usr/lib/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/lib"; \
	elif [ -f "/usr/local/lib/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/local/lib"; \
	elif [ -f "/usr/lib/x86_64-linux-gnu/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/lib/x86_64-linux-gnu"; \
	elif [ -f "/usr/lib/aarch64-linux-gnu/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/lib/aarch64-linux-gnu"; \
	else \
		echo "FAIL: libtokenizers.a not found in standard library paths."; \
		echo "There is usually no Debian package for Hugot's tokenizer static library."; \
		echo "Get it from Hugot release assets or build it from https://github.com/daulet/tokenizers"; \
		echo "Checked: /usr/lib, /usr/local/lib, /usr/lib/x86_64-linux-gnu, /usr/lib/aarch64-linux-gnu"; \
		exit 1; \
	fi

# ORT build/runtime notes:
# - Requires cgo-enabled build environment
# - Requires ONNX Runtime shared library on the host
# - Hugot expects an unversioned libonnxruntime shared library in the directory you pass
# - Common Linux paths include multiarch directories such as /usr/lib/x86_64-linux-gnu
# - Requires the rust tokenizer static library (libtokenizers.a) at build time
embtest-ort-deps-check:
	@if [ -z "$$CGO_ENABLED" ]; then \
		echo "NOTE: CGO_ENABLED not explicitly set; go build usually defaults to cgo=1 on supported hosts."; \
	fi
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		if [ -f "/usr/local/lib/libonnxruntime.dylib" ] || [ -f "/opt/homebrew/lib/libonnxruntime.dylib" ]; then \
			echo "OK: found libonnxruntime.dylib"; \
		else \
			echo "FAIL: libonnxruntime.dylib not found in /usr/local/lib or /opt/homebrew/lib"; \
			echo "Install ONNX Runtime and/or pass a custom path at runtime if needed."; \
			exit 1; \
		fi; \
	else \
		if [ -f "/usr/lib/libonnxruntime.so" ]; then \
			echo "OK: found libonnxruntime.so in /usr/lib"; \
		elif [ -f "/usr/local/lib/libonnxruntime.so" ]; then \
			echo "OK: found libonnxruntime.so in /usr/local/lib"; \
		elif [ -f "/usr/lib/x86_64-linux-gnu/libonnxruntime.so" ]; then \
			echo "OK: found libonnxruntime.so in /usr/lib/x86_64-linux-gnu"; \
		elif [ -f "/usr/lib/aarch64-linux-gnu/libonnxruntime.so" ]; then \
			echo "OK: found libonnxruntime.so in /usr/lib/aarch64-linux-gnu"; \
		else \
			echo "FAIL: libonnxruntime.so not found in standard Linux library paths."; \
			echo "Checked: /usr/lib, /usr/local/lib, /usr/lib/x86_64-linux-gnu, /usr/lib/aarch64-linux-gnu"; \
			echo "On Debian/Ubuntu, install: libonnxruntime-dev"; \
			echo "Or pass a custom directory at runtime with: -ort-lib-dir /path/to/libdir"; \
			exit 1; \
		fi; \
	fi
	@if [ -f "/usr/lib/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/lib"; \
	elif [ -f "/usr/local/lib/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/local/lib"; \
	elif [ -f "/usr/lib/x86_64-linux-gnu/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/lib/x86_64-linux-gnu"; \
	elif [ -f "/usr/lib/aarch64-linux-gnu/libtokenizers.a" ]; then \
		echo "OK: found libtokenizers.a in /usr/lib/aarch64-linux-gnu"; \
	else \
		echo "FAIL: libtokenizers.a not found in standard library paths."; \
		echo "There is usually no Debian package for Hugot's tokenizer static library."; \
		echo "Get it from Hugot release assets or build it from https://github.com/daulet/tokenizers"; \
		echo "Checked: /usr/lib, /usr/local/lib, /usr/lib/x86_64-linux-gnu, /usr/lib/aarch64-linux-gnu"; \
		exit 1; \
	fi

metadata:
	go run ./cmd/metamerge --format

test:
	go test -v -vet=off ./...

sandbox-test:
	go test -v -vet=off ./internal/sandbox/... ./internal/tools/jq

sandbox-test-short:
	go test -v -vet=off ./internal/sandbox -run 'Parity|Validate|ResolvePolicy'

sandbox-test-ci:
	go test -vet=off ./internal/sandbox/... ./internal/tools/jq

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
GITLEAKS := $(shell which gitleaks 2>/dev/null)
MIN_DEP_AGE_DAYS ?= 7
SUPPLY_CHAIN_FAIL_OPEN ?= 0
SUPPLY_CHAIN_FAIL_OPEN_FLAG := $(if $(filter 1 true yes TRUE YES,$(SUPPLY_CHAIN_FAIL_OPEN)),--allow-unresolved-metadata,)

install-lint-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install github.com/zricethezav/gitleaks/v8@latest

lint:
ifndef GOLANGCI_LINT
	@echo "Installing golangci-lint..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
endif
	golangci-lint run ./...

supply-chain-check:
	@echo "Checking dependency ages (min $(MIN_DEP_AGE_DAYS) days)..."
	@go run ./cmd/depscan --min-age-days=$(MIN_DEP_AGE_DAYS) $(SUPPLY_CHAIN_FAIL_OPEN_FLAG)

audit: lint
	@$(MAKE) supply-chain-check
ifndef GOVULNCHECK
	@echo "Installing govulncheck..."
	@go install golang.org/x/vuln/cmd/govulncheck@latest
endif
	govulncheck ./...
ifndef GITLEAKS
	@echo "Installing gitleaks..."
	@go install github.com/zricethezav/gitleaks/v8@latest
endif
	gitleaks detect --source . --no-git --config .gitleaks.toml -v

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
	@current_ver="$$( $(RELEASE_TOOL) current --field version )"; \
	current_chan="$$( $(RELEASE_TOOL) current --field channel )"; \
	next_ver="$$( $(RELEASE_TOOL) next-version )"; \
	today=$$(date +%Y-%m-%d); \
	echo "Current: [$$current_ver] $$current_chan"; \
	echo "New:     [$$next_ver] $$current_chan - $$today"; \
	echo ""; \
	$(RELEASE_TOOL) changelog new-entry; \
	echo "Opening editor..."; \
	$${EDITOR:-vim} +12 CHANGELOG.md; \
	echo ""; \
	new_ver="$$( $(RELEASE_TOOL) current --field version )"; \
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
	@$(RELEASE_TOOL) validate --release >/dev/null
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
	@echo "Tag:     $(RELEASE_TAG)"
	@echo ""

# Create and push release tag
release: release-check
	@read -p "Create and push $(RELEASE_TAG)? [y/N] " confirm; \
	if [ "$$confirm" != "y" ] && [ "$$confirm" != "Y" ]; then \
		echo "Aborted."; exit 1; \
	fi
	@git tag -a $(RELEASE_TAG) -m "Release $(VERSION) ($(CHANNEL))"
	@git push origin $(RELEASE_TAG)
	@echo ""
	@echo "✓ Tagged $(RELEASE_TAG)"
	@echo "GitHub Actions will build and publish."
	@echo ""
	@echo "Run 'make release-monitor' to watch progress."

# Monitor GitHub Actions release workflow
release-monitor:
	@if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then \
		$(RELEASE_TOOL) monitor $(if $(RUN),--run "$(RUN)",); \
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
re-release: audit
	@version="$$( $(RELEASE_TOOL) current --field release-tag 2>/dev/null )"; \
	echo "=== Re-release $$version ==="; \
	if [ -z "$$version" ]; then \
		echo "Failed to resolve latest changelog release tag."; \
		exit 1; \
	fi; \
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
