.PHONY: all quality-gate test test-race test-regression bench lint build extension-pack clean inspect live-inspect

BIN := bin/easyprofilectl

all: quality-gate

vet:
	@echo "🔍 Running go vet..."
	go vet ./...

fmt-check:
	@echo "🔍 Checking go fmt..."
	@test -z "$$(gofmt -s -l cmd internal)" || (echo "Unformatted files:" && gofmt -s -l cmd internal && exit 1)

test:
	@echo "🧪 Running unit tests (hermetic)..."
	EASYPROFILE_TEST_MODE=1 go test -v ./...

test-race:
	@echo "🧪 Running unit tests with race detector (hermetic)..."
	EASYPROFILE_TEST_MODE=1 go test -v -race ./...

build:
	@echo "🔨 Building easyprofilectl..."
	@mkdir -p bin
	go build -o $(BIN) ./cmd/easyprofilectl

inspect: build
	@$(BIN) inspect --pw-dump-file test/fixtures/pw-dump-live-2026-10-18.json --offline

live-inspect: build
	@$(BIN) inspect

quality-gate:
	@./scripts/quality-gate.sh

clean:
	@rm -rf bin/

extension-pack:
	@mkdir -p dist
	gnome-extensions pack --force --out-dir=dist --extra-source=dbus.js gnome-extension/easyprofile@gnome-shell

bench:
	EASYPROFILE_TEST_MODE=1 go test -run '^$$' -bench . -benchmem ./internal/inspect

test-regression: test-race

lint: fmt-check vet
	EASYPROFILE_TEST_MODE=1 /home/toru/go/bin/staticcheck ./...
