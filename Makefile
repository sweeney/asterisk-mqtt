.PHONY: build test lint clean

BINARY_BRIDGE = bin/asterisk-mqtt
BINARY_WIRETAP = bin/wiretap

GO = go
GOFLAGS = -trimpath
LDFLAGS = -s -w

build: build-bridge build-wiretap

build-bridge:
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY_BRIDGE) ./cmd/asterisk-mqtt

build-wiretap:
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY_WIRETAP) ./cmd/wiretap

build-linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY_BRIDGE)-linux-amd64 ./cmd/asterisk-mqtt
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY_WIRETAP)-linux-amd64 ./cmd/wiretap

build-linux-arm:
	GOOS=linux GOARCH=arm GOARM=6 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY_BRIDGE)-linux-arm ./cmd/asterisk-mqtt
	GOOS=linux GOARCH=arm GOARM=6 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY_WIRETAP)-linux-arm ./cmd/wiretap

test:
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ coverage.out
