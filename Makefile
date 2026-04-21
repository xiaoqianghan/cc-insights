.PHONY: build install clean test lint fmt check

BINARY=cci
BUILD_DIR=./build
INSTALL_DIR=$(HOME)/.local/bin

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/cci

install: build
	@mkdir -p $(INSTALL_DIR)
	install -m 755 $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed to $(INSTALL_DIR)/$(BINARY)"
	@case ":$$PATH:" in *":$(INSTALL_DIR):"*) ;; *) echo "Warning: $(INSTALL_DIR) is not in PATH" ;; esac

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
