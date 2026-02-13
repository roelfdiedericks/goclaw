.PHONY: build run debug trace clean install test lint audit install-lint-tools skills-update skills-check

BINARY := goclaw
VERSION := 0.0.1

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
