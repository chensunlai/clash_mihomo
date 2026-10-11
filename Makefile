GO ?= go
VERSION ?= dev
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build test release clean
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/clashctl ./cmd/clashctl

test:
	$(GO) test -race ./...
	$(GO) vet ./...

release:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/clashctl-linux-amd64 ./cmd/clashctl
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/clashctl-linux-arm64 ./cmd/clashctl
	cd dist && sha256sum clashctl-linux-amd64 clashctl-linux-arm64 > SHA256SUMS

clean:
	$(GO) clean
