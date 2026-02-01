.PHONY: build run debug trace clean install

BINARY := goclaw
VERSION := 0.0.1

build:
	go build -o $(BINARY) ./cmd/goclaw

run: build
	./$(BINARY) gateway

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
