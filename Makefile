BINARY  := stk
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%d)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# sha256sum on most Linux distributions, shasum on macOS. Both print the same
# "<digest>  <name>" format, which is what install.sh and stk upgrade parse.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo 'shasum -a 256')

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
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck install.sh; \
	else \
		echo "shellcheck not installed; skipping install.sh"; \
	fi

clean:
	rm -rf $(BINARY) dist

# dist cross-compiles every supported target. The only runtime dependency is
# git itself, so the binaries are static.
#
# The archive names are a published interface: install.sh and stk upgrade both
# construct them from the tag and the platform, so they cannot change without
# breaking every installed copy's ability to upgrade itself.
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
	@cd dist && $(SHA256) *.tar.gz > checksums.txt
	@ls -l dist
