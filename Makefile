BINARY  := stk
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%d)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build install test vet fmt lint clean dist

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" .

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint: vet
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

clean:
	rm -rf $(BINARY) dist

# dist cross-compiles every supported target. The only runtime dependency is
# git itself, so the binaries are static.
dist: clean
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$${os}_$${arch}/$(BINARY) . || exit 1; \
		tar -czf dist/$(BINARY)_$(VERSION)_$${os}_$${arch}.tar.gz -C dist/$(BINARY)_$${os}_$${arch} $(BINARY); \
		rm -rf dist/$(BINARY)_$${os}_$${arch}; \
	done
	@cd dist && shasum -a 256 *.tar.gz > checksums.txt
	@ls -l dist
