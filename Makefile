.PHONY: build server cli mcp clean install gallery deploy vet lint

DEPLOY_HOST ?= your-server
DEPLOY_PATH ?= /opt/openberth

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -X main.version=$(VERSION)

# Cross-compile: make server GOOS=linux GOARCH=arm64
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Native build → bin/berth-server; cross → bin/berth-server-linux-arm64
NATIVE_OS   := $(shell go env GOOS)
NATIVE_ARCH := $(shell go env GOARCH)
ifeq ($(GOOS)/$(GOARCH),$(NATIVE_OS)/$(NATIVE_ARCH))
  SERVER_BIN = bin/berth-server
else
  SERVER_BIN = bin/berth-server-$(GOOS)-$(GOARCH)
endif

build: server install-cli install-mcp

gallery:
	cd apps/server/gallery && npm run build

server: gallery
	cd apps/server && CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags '$(LDFLAGS)' -o ../../$(SERVER_BIN) .
	@echo "→ $(SERVER_BIN)"

cli:
	cd apps/cli && CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth .

mcp:
	cd apps/mcp && CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth-mcp .

install-cli: cli
	sudo rm -f /usr/local/bin/berth
	sudo cp bin/berth /usr/local/bin/

install-mcp: mcp
	sudo rm -f /usr/local/bin/berth-mcp
	sudo cp bin/berth-mcp /usr/local/bin/

# Cross-compile CLI for multiple platforms
cli-all:
	cd apps/cli && GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth-linux-amd64 .
	cd apps/cli && GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth-linux-arm64 .
	cd apps/cli && GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth-darwin-amd64 .
	cd apps/cli && GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth-darwin-arm64 .
	cd apps/cli && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o ../../bin/berth-windows-amd64.exe .

install: build
	sudo cp bin/berth-server /usr/local/bin/
	sudo cp bin/berth /usr/local/bin/

vet:
	cd apps/server && go vet ./...
	cd apps/cli && go vet ./...
	cd apps/mcp && go vet ./...

lint:
	cd apps/server && golangci-lint run ./...
	cd apps/cli && golangci-lint run ./...
	cd apps/mcp && golangci-lint run ./...

clean:
	rm -rf bin/
