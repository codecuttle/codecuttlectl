BINARY_NAME := codecuttlectl
BUILD_DIR := ./bin
PLUGIN_DIR := $(BUILD_DIR)/plugins
CMD_DIR := ./cmd/codecuttlectl
GO := go

# Build flags
LDFLAGS := -s -w
GOFLAGS := -trimpath

# Plugin sources
PLUGINS := $(wildcard plugins/cuttlebone-*)

.PHONY: all build build-plugins clean test lint run

all: build build-plugins

## build: Compile the codecuttlectl binary
build:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)

## build-plugins: Compile all Cuttlebone plugin binaries
build-plugins: $(PLUGINS)
	@mkdir -p $(PLUGIN_DIR)
	@for plugin in $(PLUGINS); do \
		name=$$(basename $$plugin); \
		echo "Building plugin: $$name"; \
		$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(PLUGIN_DIR)/$$name ./$$plugin; \
	done

## run: Build and run in interactive mode
run: all
	$(BUILD_DIR)/$(BINARY_NAME) -verbose -plugin-dir $(PLUGIN_DIR)

## test: Run all unit tests
test:
	$(GO) test -v ./...

## test-integration: Build everything and run an integration test
test-integration: all
	@echo "=== Integration Test: Animal Age Calculator ==="
	@echo "Sending task to codecuttlectl with plugins..."
	$(BUILD_DIR)/$(BINARY_NAME) -verbose -plugin-dir $(PLUGIN_DIR) -message \
		"Write a Python script to /home/coder/workspace/test_animal_age/animal_age.py that calculates an animal's equivalent 'human' age. The script should: 1) Accept command line arguments for animal type (dog, cat, rabbit) and animal age in years. 2) Use these conversion factors: dogs age 7x, cats first 2 years = 12.5 each then 4 per year, rabbits age 8x. 3) Print the result clearly. First, check if the directory exists and create it if not."

## proto: Regenerate protobuf Go code
proto:
	protoc --proto_path=proto \
		--go_out=internal/cuttlebone/v1 --go_opt=paths=source_relative \
		--go-grpc_out=internal/cuttlebone/v1 --go-grpc_opt=paths=source_relative \
		proto/cuttlebone.proto

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -rf /home/coder/workspace/test_animal_age

## fmt: Format Go source code
fmt:
	$(GO) fmt ./...

## vet: Run go vet
vet:
	$(GO) vet ./...

## tidy: Tidy go.mod
tidy:
	$(GO) mod tidy

## help: Show this help message
help:
	@echo "Available targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
