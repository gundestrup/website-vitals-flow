.PHONY: check build test coverage-ci clean

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')

build:
	go build -ldflags "-s -w -X github.com/gundestrup/website-vitals-flow/internal/cli.version=$(VERSION)" -o bin/wvf ./cmd/wvf

test:
	go test -race ./...

vet:
	gofmt -l $(GOFILES) | grep . && exit 1 || true
	go vet ./...

jscheck:
	@test ! -d internal/server/static || find internal/server/static -name '*.js' -exec node --check {} \;

uiqa:
	@sh scripts/uiqa.sh

# webcheck: real linters on first-party assets — biome (js/css lint),
# html-validate (html semantics); vendored files excluded by config.
# Pages are .tmpl now — html-validate runs on any standalone .html left.
webcheck:
	@test ! -d node_modules/@biomejs || ./node_modules/.bin/biome lint internal/server/static/app.js internal/server/static/style.css
	@test ! -d node_modules/html-validate || sh -c 'set -- internal/server/static/*.html; [ ! -e "$$1" ] || ./node_modules/.bin/html-validate "$$@"'
	@echo "webcheck ok"

# swiftbar plugin lifecycle: states + install/uninstall
sbtest:
	@sh scripts/swiftbar/test.sh

mdcheck:
	@test ! -d node_modules/markdownlint-cli2 || ./node_modules/.bin/markdownlint-cli2 "*.md" "docs/**/*.md" > /dev/null && echo "mdcheck ok"

yamlcheck:
	@for f in .golangci.yml .github/workflows/ci.yml; do \
	  [ -f "$$f" ] && node -e "require('yaml').parse(require('fs').readFileSync('$$f','utf8'))" || exit 1; \
	done && echo "yamlcheck ok"

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed — skipped"; exit 0; }
	@golangci-lint run ./...

check: vet test lint build jscheck uiqa webcheck mdcheck yamlcheck sbtest
	@echo "check ok"

# smoke: load the dashboard headless, fail on console errors —
# catches runtime binding bugs node --check can't see
smoke: build
	@echo "listen: '127.0.0.1:8421'" > /tmp/wvf-smoke.yml ; 	(./bin/wvf serve --config /tmp/wvf-smoke.yml >/tmp/wvf-smoke.log 2>&1 &) ; 	sleep 2 ; 	go run ./scripts/smoke http://127.0.0.1:8421 ; rc=$$? ; 	pkill -f "serve --config /tmp/wvf-smoke.yml" ; exit $$rc

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
