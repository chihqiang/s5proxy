version := $(shell git describe --tags --always)
OUTPUT := s5proxy
MAIN := main.go

build:
	@echo "🔧 Building $(OUTPUT) with version $(version)..."
	GO111MODULE=on CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(version)" -o $(OUTPUT) $(MAIN)
	@echo "✅ Build complete: $(OUTPUT)"

