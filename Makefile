.PHONY: build run debug trace clean install test lint audit install-lint-tools skills-update skills-check changelog release-check release release-monitor re-release

BINARY := goclaw

# Version info from CHANGELOG.md (format: ## [VERSION] CHANNEL - DATE)
VERSION := $(shell grep -m1 '^## \[[0-9]' CHANGELOG.md 2>/dev/null | sed 's/## \[\([^]]*\)\].*/\1/' || echo "0.0.0")
CHANNEL := $(shell grep -m1 '^## \[[0-9]' CHANGELOG.md 2>/dev/null | sed 's/.*\] \([a-z]*\) -.*/\1/' || echo "dev")
CHANGELOG_DATE := $(shell grep -m1 '^## \[[0-9]' CHANGELOG.md 2>/dev/null | sed 's/.*- //' || echo "")

# Compute git tag (stable = vX.Y.Z, beta/rc = vX.Y.Z-channel.N)
define get_tag
$(shell \
  if [ "$(CHANNEL)" = "stable" ]; then \
    echo "v$(VERSION)"; \
  else \
    n=1; \
    while git rev-parse "v$(VERSION)-$(CHANNEL).$$n" >/dev/null 2>&1; do \
      n=$$((n+1)); \
    done; \
    echo "v$(VERSION)-$(CHANNEL).$$n"; \
  fi)
endef
TAG = $(call get_tag)

# Skills sync from upstream OpenClaw
OPENCLAW_REPO := https://github.com/openclaw/openclaw.git
SKILLS_TMP := .skills-upstream

# CGO flags for SQLite FTS5 support (required for memory search)
export CGO_CFLAGS := -DSQLITE_ENABLE_FTS5
export CGO_LDFLAGS := -lm

build:
	go build -o $(BINARY) ./cmd/goclaw

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

clean:
	rm -f $(BINARY)

install: build
	cp $(BINARY) ~/bin/$(BINARY)

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

# Update bundled skills from upstream OpenClaw repo
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
	@rm -rf skills
	@mv $(SKILLS_TMP)/skills skills
	@rm -rf $(SKILLS_TMP)
	@echo "Skills updated from upstream"

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
	@diff -rq skills $(SKILLS_TMP)/skills 2>/dev/null || echo "Differences found"
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
re-release:
	@version=$(call get_tag); \
	echo "=== Re-release $$version ==="; \
	read -p "Delete and recreate tag $$version? [y/N] " confirm; \
	if [ "$$confirm" != "y" ] && [ "$$confirm" != "Y" ]; then \
		echo "Aborted."; exit 1; \
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
