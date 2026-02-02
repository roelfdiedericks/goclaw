.PHONY: build run debug trace clean install test

BINARY := goclaw
VERSION := 0.0.1

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
	./$(BINARY) gateway -d --tui


debug: build
	./$(BINARY) -d gateway

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
