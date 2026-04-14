BINARY_NAME := gogoclaw
BUILD_DIR := ./bin
GO_MODULE := github.com/DonScott603/gogoclaw
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build test lint install clean stress-smoke stress-sustained stress-spike stress-breakpoint stress-all

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/gogoclaw/

test:
	go test ./... -count=1

lint:
	golangci-lint run ./...

install:
	go install $(LDFLAGS) ./cmd/gogoclaw/

clean:
	rm -rf $(BUILD_DIR)

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

stress-smoke:
	go run ./cmd/stresstest -scenario smoke

stress-sustained:
	go run ./cmd/stresstest -scenario sustained

stress-spike:
	go run ./cmd/stresstest -scenario spike

stress-breakpoint:
	go run ./cmd/stresstest -scenario breakpoint

stress-all: stress-smoke stress-sustained stress-spike stress-breakpoint
