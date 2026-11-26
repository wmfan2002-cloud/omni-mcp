VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X omni-mcp/internal/app.Version=$(VERSION)
WIN_GUI_LDFLAGS := -H=windowsgui $(LDFLAGS)
BIN := bin/omni-mcp

.PHONY: all build build-amd64 build-arm64 build-windows build-windows-desktop build-darwin build-darwin-arm64 build-darwin-amd64 build-all build-linux-amd64 build-linux-arm64 test test-policy lint fmt vet run docker docker-gateway clean check

all: check build

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/omni-mcp

build-amd64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)-linux-amd64 ./cmd/omni-mcp

build-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)-linux-arm64 ./cmd/omni-mcp

build-windows:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)-windows-amd64.exe ./cmd/omni-mcp

build-windows-desktop:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(WIN_GUI_LDFLAGS)" -o $(BIN)-desktop-windows-amd64.exe ./cmd/omni-mcp

build-darwin-arm64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)-darwin-arm64 ./cmd/omni-mcp

build-darwin-amd64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)-darwin-amd64 ./cmd/omni-mcp

build-darwin: build-darwin-arm64 build-darwin-amd64

build-linux-amd64: build-amd64
build-linux-arm64: build-arm64

build-all: build-amd64 build-arm64 build-windows build-windows-desktop build-darwin-arm64 build-darwin-amd64

test:
	go test ./... -count=1

# 策略规则改动后跑一遍：里面既有"必须放行"也有"必须拦下"的用例
test-policy:
	go test ./internal/policy/ -v -count=1

fmt:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go'))" || gofmt -w $$(find cmd internal -name '*.go')

vet:
	go vet ./...

check: fmt vet test

run: build
	./$(BIN) serve -c configs/config.yaml

docker:
	docker build -f deploy/Dockerfile -t omni-mcp:$(VERSION) --build-arg VERSION=$(VERSION) .

docker-gateway:
	docker build -f deploy/Dockerfile --target gateway -t omni-mcp:$(VERSION)-gateway --build-arg VERSION=$(VERSION) .

clean:
	rm -rf bin
