.PHONY: all quality-gate test test-race lint build clean inspect live-inspect

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
