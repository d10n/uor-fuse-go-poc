GO := go

GO_BUILD_PACKAGES := ./main/...
GO_BUILD_BINDIR := ./bin
GO_BUILD_BIN := uor-fuse-go
GIT_COMMIT := $(or $(SOURCE_GIT_COMMIT),$(shell git rev-parse --short HEAD))
GIT_TAG :="$(shell git tag | sort -V | tail -1)"

GO_LD_EXTRAFLAGS :=-X github.com/uor-framework/uor-fuse-go/cli.version="$(shell git tag | sort -V | tail -1)" \
                   -X github.com/uor-framework/uor-fuse-go/cli.buildData="dev" \
                   -X github.com/uor-framework/uor-fuse-go/cli.commit="$(GIT_COMMIT)" \
                   -X github.com/uor-framework/uor-fuse-go/cli.buildDate="$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')"

GO_FILES = $(shell find . -type f -name '*.go')

build: $(GO_BUILD_BINDIR)/$(GO_BUILD_BIN)

$(GO_BUILD_BINDIR)/$(GO_BUILD_BIN): $(GO_FILES)
	@mkdir -p ${GO_BUILD_BINDIR}
	$(GO) build -o $(GO_BUILD_BINDIR)/$(GO_BUILD_BIN)  -ldflags="$(GO_LD_EXTRAFLAGS)" $(GO_BUILD_PACKAGES)

clean:
	@rm -rf ./$(GO_BUILD_BINDIR)/*
.PHONY: clean

info:
	$(info $(GO_FILES))
.PHONY: info

all: clean build
.PHONY: all
