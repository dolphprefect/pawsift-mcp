BINARY_NAME=pawsift
BUILD_DIR=build
INSTALL_DIR=$(HOME)/.local/bin
GEMINI_CONFIG=$(HOME)/.gemini/settings.json
CLAUDE_CODE_CONFIG=$(HOME)/.claude.json

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
VERSION    := $(shell grep 'Version = ' main.go | sed 's/.*"\(.*\)".*/\1/')

.PHONY: all build build-all clean install deploy register uninstall release

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) .

build-all:
	@echo "Building $(BINARY_NAME) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@for platform in $(PLATFORMS); do \
		OS=$$(echo $$platform | cut -d/ -f1); \
		ARCH=$$(echo $$platform | cut -d/ -f2); \
		echo "  $$OS/$$ARCH..."; \
		EXT=$$( [ "$$OS" = "windows" ] && echo ".exe" || echo "" ); \
		GOOS=$$OS GOARCH=$$ARCH go build -o $(BUILD_DIR)/$(BINARY_NAME)-$$OS-$$ARCH$$EXT .; \
	done
	@echo "Done."

release: build-all
	@echo "Creating GitHub release v$(VERSION)..."
	@gh release create v$(VERSION) \
		$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 \
		$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 \
		$(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 \
		$(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 \
		$(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe \
		--title "v$(VERSION)" \
		--generate-notes

clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR) .pawsift

install: build
	@echo "Installing to $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)
	@install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)

register: install
	@echo "Registering PawSift in Gemini CLI config..."
	@if [ -f $(GEMINI_CONFIG) ]; then \
		cat $(GEMINI_CONFIG) | jq '.mcpServers.pawsift = {"command": "$(INSTALL_DIR)/$(BINARY_NAME)"}' > $(GEMINI_CONFIG).tmp && mv $(GEMINI_CONFIG).tmp $(GEMINI_CONFIG); \
		echo "Successfully registered in $(GEMINI_CONFIG)"; \
	else \
		echo "Gemini config not found at $(GEMINI_CONFIG). Skipping."; \
	fi
	@echo "Registering PawSift in Claude Code (CLI) config..."
	@if [ -f $(CLAUDE_CODE_CONFIG) ]; then \
		cat $(CLAUDE_CODE_CONFIG) | jq '.mcpServers.pawsift = {"command": "$(INSTALL_DIR)/$(BINARY_NAME)"}' > $(CLAUDE_CODE_CONFIG).tmp && mv $(CLAUDE_CODE_CONFIG).tmp $(CLAUDE_CODE_CONFIG); \
		echo "Successfully registered in $(CLAUDE_CODE_CONFIG)"; \
	else \
		echo "Claude Code config not found at $(CLAUDE_CODE_CONFIG). Skipping."; \
	fi

deploy: register
	@echo "Deployment complete."

uninstall:
	@echo "Uninstalling PawSift..."
	@rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Removing PawSift from Gemini CLI config..."
	@if [ -f $(GEMINI_CONFIG) ]; then \
		cat $(GEMINI_CONFIG) | jq 'del(.mcpServers.pawsift)' > $(GEMINI_CONFIG).tmp && mv $(GEMINI_CONFIG).tmp $(GEMINI_CONFIG); \
		echo "Successfully unregistered from $(GEMINI_CONFIG)"; \
	fi
	@echo "Removing PawSift from Claude Code (CLI) config..."
	@if [ -f $(CLAUDE_CODE_CONFIG) ]; then \
		cat $(CLAUDE_CODE_CONFIG) | jq 'del(.mcpServers.pawsift)' > $(CLAUDE_CODE_CONFIG).tmp && mv $(CLAUDE_CODE_CONFIG).tmp $(CLAUDE_CODE_CONFIG); \
		echo "Successfully unregistered from $(CLAUDE_CODE_CONFIG)"; \
	fi
	@echo "Uninstall complete."
