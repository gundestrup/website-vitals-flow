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
