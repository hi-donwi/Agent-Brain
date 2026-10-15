.PHONY: build install test race vet fmt fmt-check tidy-check cross static-check vulncheck smoke check clean release

BINARY  := brain
PKG     := github.com/hi-donwi/Agent-Brain
VERSION ?= 0.0.0-dev
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
           -X $(PKG)/internal/buildinfo.Version=$(VERSION) \
           -X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
           -X $(PKG)/internal/buildinfo.Date=$(DATE)

# lokal ships as one static binary per platform (ADR-0001). CGO stays off so a
# dependency that needs a C toolchain fails the build here, not on a user's machine.
export CGO_ENABLED := 0

# Pinned: govulncheck is a security gate; a floating @latest would let the scanner
# itself change without review. v1.1.4 panics building SSA for the Go 1.27 standard
# library, so the pin must move with the newest Go release CI uses.
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/brain

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/brain

test:
	go test -count=1 ./...

# -race needs cgo; it is the only target that turns it back on.
race:
	CGO_ENABLED=1 go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "Not gofmt-formatted:"; echo "$$unformatted"; exit 1; fi

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		echo "-> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o bin/$(BINARY)-$$os-$$arch$$ext ./cmd/brain || exit 1; \
	done

static-check: cross
	file bin/$(BINARY)-linux-amd64 | grep -q "statically linked"
	file bin/$(BINARY)-linux-arm64 | grep -q "statically linked"

vulncheck:
	go run $(GOVULNCHECK) ./...

# doctor exits 1 on warnings, which a CI runner may legitimately have; only a
# failure (2) or a crash fails the smoke run.
smoke: build
	./bin/$(BINARY) version
	./bin/$(BINARY) version --json
	./bin/$(BINARY) doctor || [ $$? -eq 1 ]
	./bin/$(BINARY) doctor --json > /dev/null || [ $$? -eq 1 ]

release: cross
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		archive=dist/$(BINARY)-$$os-$$arch.tar.gz; \
		echo "Packaging $$archive..."; \
		tar -czf $$archive -C bin $(BINARY)-$$os-$$arch$$ext; \
	done
	@cd dist && (shasum -a 256 * > SHA256SUMS 2>/dev/null || sha256sum * > SHA256SUMS)
	@echo "Release artifacts and SHA256SUMS ready in dist/"

# The same steps CI runs on every push, in the same order.
check: fmt-check tidy-check vet race smoke

clean:
	rm -rf bin dist coverage.out
