PKG := github.com/vantaboard/go-googlesqlite

# Same dev image and GO_CACHE_ROOT as go-googlesql (see ../go-googlesql/Makefile).
GO_GOOGLESQL_ROOT ?= $(abspath $(CURDIR)/../go-googlesql)
DOCKER_DEV_IMAGE ?= go-googlesql:dev
GO_CACHE_ROOT ?= $(HOME)/.cache/go-googlesql
# Default GoogleSQL CGO tags (match go-googlesql Taskfile / docs/prebuilt-cgo.md).
GOOGLESQL_BUILD_TAGS ?= googlesql,googlesql_unified_prebuilt

GOBIN := $(CURDIR)/bin
PKGS := $(shell go list ./... | grep -v cmd | grep -v benchmarks )
COVER_PKGS := $(foreach pkg,$(PKGS),$(subst $(PKG),.,$(pkg)))

COMMA := ,
EMPTY :=
SPACE := $(EMPTY) $(EMPTY)
COVERPKG_OPT := $(subst $(SPACE),$(COMMA),$(COVER_PKGS))

$(GOBIN):
	@mkdir -p $(GOBIN)

.PHONY: build
build:
	cd ./cmd/googlesqlite-cli && go build .

.PHONY: cover
cover:
	go test -coverpkg=$(COVERPKG_OPT) -coverprofile=cover.out ./...

.PHONY: cover-html
cover-html: cover
	go tool cover -html=cover.out

.PHONY: lint
lint: lint/install
	$(GOBIN)/golangci-lint run --timeout 30m

lint/install: | $(GOBIN)
	# binary will be $(go env GOPATH)/bin/golangci-lint
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(GOBIN) v2.4.0

# Host test with the same CGO/linker env as go-googlesql Task (requires sibling go-googlesql + prebuilts).
.PHONY: test/prebuilt
test/prebuilt:
	bash -c 'set -euo pipefail; source "$(GO_GOOGLESQL_ROOT)/scripts/go-googlesql-stack-bootstrap.sh"; go test -tags "$(GOOGLESQL_BUILD_TAGS)" -p 1 -count=1 ./...'

# Run tests in the same go-googlesql:dev toolchain + shared GO_CACHE_ROOT as ../go-googlesql.
.PHONY: docker/build-dev-googlesql test/linux
docker/build-dev-googlesql:
	$(MAKE) -C "$(GO_GOOGLESQL_ROOT)" docker/build-dev

# test/linux depends on docker/build-dev-googlesql so the local go-googlesql:dev image exists (Docker otherwise tries to pull it).
test/linux: docker/build-dev-googlesql
	docker run --rm \
		-e CGO_ENABLED=1 -e CC=clang -e CXX=clang++ \
		-e CCACHE_DIR=/root/.ccache -e CCACHE_COMPRESS=1 \
		-v "$(CURDIR)":/work/go-googlesqlite \
		-v "$(GO_GOOGLESQL_ROOT)":/work/go-googlesql \
		-v "$(GO_CACHE_ROOT)/gocache":/root/.cache/go-build \
		-v "$(GO_CACHE_ROOT)/gomodcache":/go/pkg/mod \
		-v "$(GO_CACHE_ROOT)/ccache":/root/.ccache \
		-w /work/go-googlesqlite \
		$(DOCKER_DEV_IMAGE) \
		bash -c 'set -euo pipefail; source /work/go-googlesql/scripts/go-googlesql-stack-bootstrap.sh; go test -race -v ./... -count=1'
