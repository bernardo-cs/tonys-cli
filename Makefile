BINARY := tonys
PKG := ./cmd/tonys
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/bernardo-cs/tonys-cli/internal/cli.Version=$(VERSION)

.PHONY: build install test vet fmt cover clean cross release-snapshot

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

clean:
	rm -f $(BINARY) coverage.out
	rm -rf dist

# Build a full release locally (no publish) to validate .goreleaser.yaml.
release-snapshot:
	goreleaser release --snapshot --clean --skip=publish

# Cross-compile common targets into dist/ (quick local smoke; real releases use
# goreleaser — see .goreleaser.yaml).
cross:
	@mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64  $(PKG)
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64  $(PKG)
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64   $(PKG)
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64   $(PKG)
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe $(PKG)
