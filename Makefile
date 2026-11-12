BIN     := ai-ssh
VERSION ?= 0.1.0
# A tag name is usually v0.1.0; assets and the binary use 0.1.0.
override VERSION := $(patsubst v%,%,$(VERSION))

# Target platform. CI passes GOOS/GOARCH (e.g. linux/arm64 from an x86 runner);
# locally they default to the host. They are unexported below so `go run` keeps
# using the host toolchain: a Windows runner building linux must not try to run
# a linux buildtool.
HOST_GOOS   := $(shell go env GOHOSTOS)
HOST_GOARCH := $(shell go env GOHOSTARCH)
ifeq ($(origin GOOS),command line)
TARGET_GOOS := $(GOOS)
else
TARGET_GOOS ?= $(HOST_GOOS)
endif
ifeq ($(origin GOARCH),command line)
TARGET_GOARCH := $(GOARCH)
else
TARGET_GOARCH ?= $(HOST_GOARCH)
endif
unexport GOOS GOARCH

EXE := $(if $(filter windows,$(TARGET_GOOS)),.exe,)

# Everything local goes into build/: the development binary, per-platform
# staging, fetched terminal libraries, release archives and SHA256SUMS.txt.
OUT        := build
ARCHIVE    := $(OUT)/$(BIN)-$(VERSION)-$(TARGET_GOOS)-$(TARGET_GOARCH)
BUILD_DIR  := $(OUT)/$(TARGET_GOOS)-$(TARGET_GOARCH)
STAGE_DIR  := $(OUT)/pkg/$(TARGET_GOOS)-$(TARGET_GOARCH)
NATIVE_DIR := $(OUT)/native/$(TARGET_GOOS)-$(TARGET_GOARCH)

LDFLAGS := -s -w -X github.com/nonex-code/ai-ssh/internal/version.Value=$(VERSION)
ifeq ($(TARGET_GOOS),windows)
ARCHIVE := $(ARCHIVE).zip
else
ARCHIVE := $(ARCHIVE).tar.gz
endif

# The program is pure Go (mygo uses purego); static binaries are what the
# release archives expect. -race is not used through make.
export CGO_ENABLED := 0

.PHONY: all build build-dev run gui package checksums test vet fmt icon native embed clean help

all: build

# A self-contained binary in build/: the terminal library for the target is
# embedded and extracted to ~/.ai-ssh/lib/vt/<sha256>/ on first use. This is
# the same layout as a release archive, so one file can be copied between
# machines of the same platform.
build: embed
	@go run ./scripts/buildtool mkdir $(OUT)
	go run ./scripts/buildtool build -goos $(TARGET_GOOS) -goarch $(TARGET_GOARCH) \
		-version $(VERSION) -out $(OUT)/$(BIN)$(EXE) \
		-tags ai_ssh_embedvt $(if $(filter windows,$(TARGET_GOOS)),-windowsgui,)
	@echo "binary: $(OUT)/$(BIN)$(EXE)"

# Fast local build for working on the UI: no embedded library, so the terminal
# downloads libghostty-vt at runtime (mygo's cache) like a development build.
build-dev:
	@go run ./scripts/buildtool mkdir $(OUT)
	go build -trimpath -ldflags "$(LDFLAGS)$(if $(filter windows,$(HOST_GOOS)), -H=windowsgui)" \
		-o $(OUT)/$(BIN)$(if $(filter windows,$(HOST_GOOS)),.exe,) .
	@echo "dev binary: $(OUT)/$(BIN)$(if $(filter windows,$(HOST_GOOS)),.exe,) (runtime terminal library)"

run: build
	@$(OUT)/$(BIN)$(EXE) $(ARGS)

gui: build

# One platform per runner. The terminal library is fetched for the target and
# embedded in every release binary, so Linux/macOS releases do not download it
# on first use either.
package: embed
	@go run ./scripts/buildtool mkdir $(BUILD_DIR)
	go run ./scripts/buildtool build -goos $(TARGET_GOOS) -goarch $(TARGET_GOARCH) \
		-version $(VERSION) -out $(BUILD_DIR)/$(BIN)$(EXE) \
		-tags ai_ssh_embedvt $(if $(filter windows,$(TARGET_GOOS)),-windowsgui,)
	go run ./scripts/buildtool package \
		-goos $(TARGET_GOOS) -goarch $(TARGET_GOARCH) -version $(VERSION) \
		-bin $(BUILD_DIR)/$(BIN)$(EXE) -stage $(STAGE_DIR) -out $(OUT)
	@echo "archive: $(ARCHIVE)"

# One SHA256SUMS.txt for all archives, used by the release job after it has
# downloaded every matrix artifact.
checksums:
	@go run ./scripts/buildtool checksums -dir $(OUT)

# Fetch libghostty-vt for the target platform (sha256 checked). It lands in
# build/native/<goos>-<goarch>/.
native:
	go run scripts/fetchnative/main.go -goos $(TARGET_GOOS) -goarch $(TARGET_GOARCH) \
		-out $(NATIVE_DIR) -mirror "$(MYGO_MIRROR)"

# Copy the fetched library where the platform's go:embed can see it.
embed: native
	@go run ./scripts/buildtool embedvt -goos $(TARGET_GOOS) -goarch $(TARGET_GOARCH) \
		-native $(NATIVE_DIR) -out internal/vtlib

# Regenerate all platform icons from logo.svg: ico, icns, PNG and the Windows
# PE resource. The generated files are kept in the tree, so a plain build does
# not need MinGW.
icon:
	go run scripts/genicon/main.go
ifeq ($(HOST_GOOS),windows)
	windres -O coff -F pe-x86-64 -i rsrc.rc -o rsrc_windows_amd64.syso
else
	@echo "windres is needed to rebuild rsrc_windows_amd64.syso" >&2
endif

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	@go run ./scripts/buildtool clean

help:
	@echo "make build              self-contained binary in build/"
	@echo "make build-dev          fast build without the embedded terminal library"
	@echo "make run                build and run it (ARGS=...)"
	@echo "make package VERSION=x.y.z one platform's release archive in build/"
	@echo "make checksums          build/SHA256SUMS.txt over every archive"
	@echo "make icon               regenerate ico/icns/png (and the Windows syso)"
	@echo "make test | vet | fmt   checks"
	@echo "make clean              remove build/"