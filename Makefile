.PHONY: check build test coverage-ci clean

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')

build:
	go build -o bin/wvf ./cmd/wvf

test:
	go test -race ./...

vet:
	gofmt -l $(GOFILES) | grep . && exit 1 || true
	go vet ./...

jscheck:
	@test ! -d internal/server/static || find internal/server/static -name '*.js' -exec node --check {} \;

check: vet test build jscheck
	@echo "check ok"

coverage-ci:
	go test -race -coverpkg=./... -coverprofile=coverage.out ./...
	go test -json ./... > test-report.json || true

clean:
	rm -rf bin coverage.out test-report.json

PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

release: check
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  out=dist/wvf-$(VERSION)-$$os-$$arch; \
	  [ "$$os" = windows ] && out=$$out.exe; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w" -o $$out ./cmd/wvf; \
	  echo built $$out; \
	done
	@cd dist && shasum -a 256 wvf-* > SHA256SUMS && echo "sha256sums in dist/"
