.PHONY: build run generate test lint docker clean

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS = -s -w -X github.com/Daviey/mockllm/internal/server.Version=$(VERSION)
SPECS_DIR ?= ./providers

build:
	go build -ldflags "$(LDFLAGS)" -o mockllm ./cmd/mockllm
	go build -o mockllm-gen ./cmd/mockllm-gen

run: build
	./mockllm -specs $(SPECS_DIR) -port $(PORT)

generate:
	go run ./cmd/mockllm-gen -output $(SPECS_DIR) $(ARGS)

generate-all:
	go run ./cmd/mockllm-gen -output ./providers-all

test:
	go test -race -v ./...

lint:
	go vet ./...

docker:
	docker build -t mockllm:$(VERSION) .

clean:
	rm -f mockllm mockllm-gen
	rm -rf providers-generated providers-all dist/
