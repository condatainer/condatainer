.PHONY: build clean test

# Binary name
BINARY_NAME=condatainer_go
BINARY_PATH=bin/$(BINARY_NAME)

# A local build reports <version in config.go>+<git describe>, so it is told apart
# from a release. A release passes GO_LDFLAGS itself, and a tree without git keeps
# the version in config.go.
VERSION := $(shell sed -n 's/^var Version = "\(.*\)"/\1/p' internal/config/config.go)
BUILD := $(shell git describe --always --dirty 2>/dev/null)
GO_LDFLAGS ?= $(if $(BUILD),-X github.com/condatainer/condatainer/internal/config.Version=$(VERSION)+$(BUILD))

# Build the binary. CGO_ENABLED=0 keeps it statically linked: the binary is
# bind-mounted into containers whose glibc may be older than the build host's.
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	@CGO_ENABLED=0 go build $(if $(GO_LDFLAGS),-ldflags "$(GO_LDFLAGS)") -o $(BINARY_PATH) .
	@echo "Built successfully: $(BINARY_PATH)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f $(BINARY_PATH)
	@echo "Cleaned: $(BINARY_PATH)"

# Run tests
test:
	@echo "Running tests..."
	@go test ./...

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod download

# Run the binary
run: build
	@./$(BINARY_PATH)

# Default target
.DEFAULT_GOAL := build
