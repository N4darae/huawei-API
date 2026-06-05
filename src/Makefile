SHELL := /bin/bash
GO ?= go
NPM ?= npm

BIN_DIR := bin
BINARY := $(BIN_DIR)/dongled
CMD := ./cmd/dongled

OPENAPI := api/openapi.yaml
SCHEMA_TS := web/src/api/schema.d.ts
EVENTS_TS := web/src/api/events.ts
EMBED_DIST := internal/webui/dist

GENERATED := $(SCHEMA_TS) $(EVENTS_TS)

CROSS_GOARCH := amd64

.DEFAULT_GOAL := build
.PHONY: build run test test-race vet fmt fmt-check lint gen gen-api gen-events \
        build-linux build-windows build-darwin cross-build \
        vet-linux vet-windows vet-darwin cross-vet \
        check web web-install web-dev web-reset tidy clean help

build:
	$(GO) build -o $(BINARY) $(CMD)

build-linux:
	GOOS=linux GOARCH=$(CROSS_GOARCH) $(GO) build ./...

build-windows:
	GOOS=windows GOARCH=$(CROSS_GOARCH) $(GO) build ./...

build-darwin:
	GOOS=darwin GOARCH=$(CROSS_GOARCH) $(GO) build ./...

cross-build: build-linux build-windows build-darwin

vet-linux:
	GOOS=linux GOARCH=$(CROSS_GOARCH) $(GO) vet ./...

vet-windows:
	GOOS=windows GOARCH=$(CROSS_GOARCH) $(GO) vet ./...

vet-darwin:
	GOOS=darwin GOARCH=$(CROSS_GOARCH) $(GO) vet ./...

cross-vet: vet-linux vet-windows vet-darwin

run:
	$(GO) run $(CMD) serve

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

fmt-check:
	@out="$$(gofmt -l . )"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint: fmt-check vet
	@bash -c 'set -e; \
	  if grep -rn --include=*.go --include=*.sh --include=*.nft --include=*.tmpl "flush ruleset" . ; then \
	    echo "flush ruleset is forbidden everywhere"; exit 1; fi; \
	  if grep -rln --include=*.go -e "nft -f" -e "nft add rule" . | grep -v "^./internal/fw/" | grep -v "^./deploy/" ; then \
	    echo "only internal/fw and deploy may drive nft"; exit 1; fi; \
	  if grep -rn --include=*.conf --include=*.sh "tcp_retries2" deploy 2>/dev/null; then \
	    echo "tcp_retries2 must stay out of the installer"; exit 1; fi; \
	  if grep -rn --include=*.conf "net.ipv4.conf.all.rp_filter\|all.arp_ignore" deploy 2>/dev/null; then \
	    echo "sysctl drop-ins may only use the default. prefix"; exit 1; fi'

gen: gen-api gen-events

gen-api:
	$(GO) run ./tools/gen/openapi -in $(OPENAPI) -out $(SCHEMA_TS)

gen-events:
	$(GO) run ./tools/gen/events -out $(EVENTS_TS)

check: lint gen test cross-build cross-vet
	@git diff --exit-code -- $(GENERATED) || { echo "generated files are stale: commit the output of make gen"; exit 1; }

web-install:
	cd web && $(NPM) install

web:
	rm -rf $(EMBED_DIST)/assets
	cd web && $(NPM) run build

web-dev:
	cd web && $(NPM) run dev

web-reset:
	rm -rf $(EMBED_DIST)/assets
	git checkout -- $(EMBED_DIST)/index.html

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR) $(EMBED_DIST)/assets web/dist web/node_modules/.vite

help:
	@grep -E '^[a-z][a-z-]*:' $(MAKEFILE_LIST) | cut -d: -f1 | sort -u

-include Makefile.netns
