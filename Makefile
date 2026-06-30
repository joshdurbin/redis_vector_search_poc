.DEFAULT_GOAL := build

BINARY  := vector_search_poc
GEN_DIR := gen
PROTO   := products.proto

export PATH := $(PATH):$(shell go env GOPATH)/bin

.PHONY: proto build clean run pull-data

proto:
	@which protoc > /dev/null || (echo "protoc not found — run: brew install protobuf" && exit 1)
	@which protoc-gen-go > /dev/null || (echo "protoc-gen-go not found — run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest" && exit 1)
	@which protoc-gen-go-grpc > /dev/null || (echo "protoc-gen-go-grpc not found — run: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" && exit 1)
	@mkdir -p $(GEN_DIR)
	protoc \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		$(PROTO)

build:
	CGO_ENABLED=1 go build -o $(BINARY) ./cmd/vector-search-poc/

clean:
	rm -f $(BINARY)
	rm -rf $(GEN_DIR)

run: build
	./$(BINARY) serve

pull-data:
	wget https://github.com/wayfair/WANDS/raw/refs/heads/main/dataset/product.csv
