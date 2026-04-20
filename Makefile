.PHONY: build install clean test lint fmt check

BINARY=cci
BUILD_DIR=./build

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/cci

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

# 静态分析和格式化
lint:
	golangci-lint run ./...

fmt:
	goimports -w .
	gofmt -w .

# 提交前完整检查：格式化 + lint + 测试
check: fmt lint test
