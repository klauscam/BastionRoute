# Makefile for BastionRoute

.PHONY: all deps build clean

# Output directory for compiled binaries
BIN_DIR := bin

all: deps build

deps:
	@echo "Resolving Go modules and downloading dependencies..."
	go mod download
	go mod tidy

build:
	@echo "Creating output directory..."
	mkdir -p $(BIN_DIR)
	
	@echo "Compiling BastionRoute Shim..."
	go build -o $(BIN_DIR)/bastionroute-shim ./cmd/bastionroute-shim
	
	@echo "Compiling BastionRoute Relay..."
	go build -o $(BIN_DIR)/bastionroute-relay ./cmd/bastionroute-relay
	
	@echo "✅ Build complete! Binaries are located in the './$(BIN_DIR)' directory."

clean:
	@echo "Cleaning up binaries..."
	rm -rf $(BIN_DIR)
