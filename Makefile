VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY ?= cowork-svc-linux
GO ?= go
CGO_ENABLED ?= 0
GOFLAGS ?= -trimpath -buildmode=pie
LDFLAGS := -X main.version=$(VERSION)
PREFIX ?= /usr
DESTDIR ?=

all: build

build:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

build-arm64:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o cowork-svc-linux-arm64 .

extract-cowork-svc:
	bash scripts/extract-cowork-svc.sh

clean:
	rm -f $(BINARY) cowork-svc-linux-arm64
	rm -f cowork-svc.exe .cowork-svc-version

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

.PHONY: all build build-arm64 clean install uninstall lint test extract-cowork-svc
