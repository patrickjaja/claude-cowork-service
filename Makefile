VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY ?= cowork-svc-linux
GO ?= go
BUN ?= bun
CGO_ENABLED ?= 0
GOFLAGS ?= -trimpath -buildmode=pie
LDFLAGS := -X main.version=$(VERSION)
PREFIX ?= /usr
DESTDIR ?=
SRT_RUNTIME_DIR ?= sandbox-runtime
SRT_DIR ?= srt
UNAME_M ?= $(shell uname -m)
SRT_ARCH := $(if $(filter x86_64 amd64,$(UNAME_M)),amd64,$(if $(filter aarch64 arm64,$(UNAME_M)),arm64,))
SRT_BINARY := $(SRT_DIR)/srt-linux-$(SRT_ARCH)

all: build

build:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

build-arm64:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o cowork-svc-linux-arm64 .

build-srt:
	$(BUN) install --cwd $(SRT_RUNTIME_DIR)
	$(BUN) run --cwd $(SRT_RUNTIME_DIR) build:executables
	mkdir -p $(SRT_DIR)
	cp $(SRT_RUNTIME_DIR)/dist/srt-linux-amd64 $(SRT_DIR)/
	cp $(SRT_RUNTIME_DIR)/dist/srt-linux-arm64 $(SRT_DIR)/

extract-cowork-svc:
	bash scripts/extract-cowork-svc.sh

clean:
	rm -f $(BINARY) cowork-svc-linux-arm64
	rm -f cowork-svc.exe .cowork-svc-version

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	test -n "$(SRT_ARCH)" || { echo "Unsupported architecture for srt: $(UNAME_M)" >&2; exit 1; }
	test -f "$(SRT_BINARY)" || { echo "Missing $(SRT_BINARY); run: make build-srt" >&2; exit 1; }
	install -Dm755 "$(SRT_BINARY)" $(DESTDIR)$(PREFIX)/bin/srt-cowork
	install -Dm644 claude-cowork.service $(DESTDIR)$(PREFIX)/lib/systemd/user/claude-cowork.service

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	rm -f $(DESTDIR)$(PREFIX)/bin/srt-cowork
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/user/claude-cowork.service

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

.PHONY: all build build-arm64 build-srt clean install uninstall lint test extract-cowork-svc
