VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY ?= cowork-svc-linux
GO ?= go
CGO_ENABLED ?= 0
GOFLAGS ?= -trimpath -buildmode=pie
LDFLAGS ?= -X main.version=$(VERSION)
PREFIX ?= /usr
DESTDIR ?=

all: build

build:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

clean:
	rm -f $(BINARY)

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -Dm644 dist/claude-cowork.service $(DESTDIR)$(PREFIX)/lib/systemd/user/claude-cowork.service

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/user/claude-cowork.service

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

.PHONY: all build clean install uninstall lint test
