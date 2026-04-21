VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v-*//')
DIST     := dist
GOOS     := linux
GOARCH   := arm64
CGO_ENABLED := 0

BINARIES := oceano-source-detector oceano-state-manager oceano-web oceano-setup

.PHONY: all build package release test clean check-nfpm

all: build

build: $(addprefix $(DIST)/,$(BINARIES))

$(DIST)/oceano-source-detector: $(shell find cmd/oceano-source-detector -name '*.go') $(shell find internal -name '*.go') go.mod go.sum
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -o $@ ./cmd/oceano-source-detector

$(DIST)/oceano-state-manager: $(shell find cmd/oceano-state-manager -name '*.go') $(shell find internal -name '*.go') go.mod go.sum
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -o $@ ./cmd/oceano-state-manager

$(DIST)/oceano-web: $(shell find cmd/oceano-web -name '*.go') $(shell find internal -name '*.go') go.mod go.sum
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -o $@ ./cmd/oceano-web

$(DIST)/oceano-setup: $(shell find cmd/oceano-setup -name '*.go') go.mod go.sum
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -o $@ ./cmd/oceano-setup

package: check-nfpm build
	@mkdir -p $(DIST)
	VERSION=$(VERSION) nfpm package --config nfpm.yaml --packager deb --target $(DIST)/

release: package

test:
	go test ./...

clean:
	rm -rf $(DIST)

check-nfpm:
	@command -v nfpm >/dev/null 2>&1 || { \
		echo "nfpm not found. Install it with:"; \
		echo "  brew install nfpm          (macOS)"; \
		echo "  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest  (any)"; \
		exit 1; \
	}
