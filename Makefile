# Makefile for Skink programming language
# Usage:
#   make            	- build the skink compiler
#   make install    	- install the compiler and standard library (installs dependencies and builds compiler)
#   make install-only	- install the compiler and standard library (skips dependency installation and compiler build) if already built
#   make test       	- run standard library tests
#   make clean      	- remove build artifacts
#   make fmt        	- format the code
#   make vet        	- vet the code
#   make install-deps	- install system dependencies	
#   make uninstall  	- uninstall the compiler and standard library
#   make help       	- show this help

# Installation directories (override with PREFIX=...)
PREFIX      ?= /usr/local
BINDIR      ?= $(PREFIX)/bin
LIBDIR      ?= $(PREFIX)/lib/skink
MANDIR      ?= $(PREFIX)/share/man/man1

# Go build settings
GO          ?= go
GOBUILD     := $(GO) build
SKINK_SRC   := ./cmd/skink
SKINK_BIN   := skink

# Compiler flags
BUILD_FLAGS ?=

# Helper: use sudo if not running as root
SUDO := $(shell if [ `id -u` -ne 0 ]; then echo sudo; fi)

# Test settings
TEST_DIR    := ./std
TEST_PATTERN?= "*_test.skink"

# Standard library files to install
STD_FILES   := $(wildcard $(TEST_DIR)/*.skink)

# Runtime C files to install
RUNTIME_FILES := $(wildcard ./compiler/runtime/*.c)

.PHONY: all build static install install-only install-deps uninstall test test-std clean fmt vet help

# Default target: build the compiler
all: build

build:
	@echo "Building skink compiler..."
	cd compiler && $(GOBUILD) $(BUILD_FLAGS) -o ../$(SKINK_BIN) $(SKINK_SRC)
	@echo "Built: $(SKINK_BIN)"

static:
	@echo "Building 100% statically linked skink compiler..."
	cd compiler && CGO_ENABLED=1 $(GOBUILD) -ldflags "-linkmode external -extldflags '-static'" -o ../$(SKINK_BIN) $(SKINK_SRC)
	@echo "Built static binary: $(SKINK_BIN)"

# Install compiler binary, standard library, and runtime
# This also installs system dependencies and builds the compiler if needed.
install: install-deps build
	@echo "Installing skink to $(BINDIR) ..."
	$(SUDO) install -d $(BINDIR)
	$(SUDO) cp -f $(SKINK_BIN) $(BINDIR)/
	$(SUDO) chmod 755 $(BINDIR)/$(SKINK_BIN)
	@echo "Installing standard library to $(LIBDIR) ..."
	$(SUDO) install -d $(LIBDIR)
	$(SUDO) rm -rf $(LIBDIR)/std $(LIBDIR)/runtime $(LIBDIR)/lib
	$(SUDO) install -d $(LIBDIR)/std
	$(SUDO) install -m 644 $(STD_FILES) $(LIBDIR)/std/
	@echo "Installing runtime to $(LIBDIR) ..."
	$(SUDO) install -d $(LIBDIR)/runtime
	$(SUDO) install -m 644 $(RUNTIME_FILES) $(LIBDIR)/runtime/
	@echo "Installing lib support files to $(LIBDIR)/lib ..."
	$(SUDO) install -d $(LIBDIR)/lib
	$(SUDO) install -m 644 ./compiler/lib/*.skink $(LIBDIR)/lib/
	@echo ""
	@echo "Installation complete."
	@echo "Add $(BINDIR) to your PATH if not already present."
	@echo "Set SKINK_HOME=$(LIBDIR) to use the installed standard library."

# Install only (skip dependency installation and compiler build)
install-only:
	@echo "Installing skink to $(BINDIR) ..."
	$(SUDO) install -d $(BINDIR)
	$(SUDO) cp -f $(SKINK_BIN) $(BINDIR)/
	$(SUDO) chmod 755 $(BINDIR)/$(SKINK_BIN)
	@echo "Installing standard library to $(LIBDIR) ..."
	$(SUDO) install -d $(LIBDIR)
	$(SUDO) rm -rf $(LIBDIR)/std $(LIBDIR)/runtime $(LIBDIR)/lib
	$(SUDO) install -d $(LIBDIR)/std
	$(SUDO) install -m 644 $(STD_FILES) $(LIBDIR)/std/
	@echo "Installing runtime to $(LIBDIR) ..."
	$(SUDO) install -d $(LIBDIR)/runtime
	$(SUDO) install -m 644 $(RUNTIME_FILES) $(LIBDIR)/runtime/
	@echo "Installing lib support files to $(LIBDIR)/lib ..."
	$(SUDO) install -d $(LIBDIR)/lib
	$(SUDO) install -m 644 ./compiler/lib/*.skink $(LIBDIR)/lib/
	@echo ""
	@echo "Installation complete."
	@echo "Add $(BINDIR) to your PATH if not already present."
	@echo "Set SKINK_HOME=$(LIBDIR) to use the installed standard library."

# Install external C library dependencies (excludes Go and LLVM)
install-deps:
	@echo "Detecting operating system and package manager..."
	@OS=$$(uname -s); \
	if [ "$$OS" = "Linux" ]; then \
		if command -v apt-get >/dev/null 2>&1; then \
			echo "Detected: apt (Debian/Ubuntu)"; \
			$(SUDO) apt-get update && $(SUDO) apt-get install -y libsqlite3-dev libllama-dev libmosquitto-dev libssl-dev zlib1g-dev; \
		elif command -v dnf >/dev/null 2>&1; then \
			echo "Detected: dnf (Fedora/RHEL)"; \
			$(SUDO) dnf install -y sqlite-devel llama-cpp mosquitto-devel openssl-devel zlib-devel; \
		elif command -v yum >/dev/null 2>&1; then \
			echo "Detected: yum (RHEL/CentOS)"; \
			$(SUDO) yum install -y sqlite-devel llama-cpp mosquitto-devel openssl-devel zlib-devel; \
		elif command -v pacman >/dev/null 2>&1; then \
			echo "Detected: pacman (Arch Linux)"; \
			$(SUDO) pacman -S --needed --noconfirm sqlite libmosquitto openssl zlib llama-cpp; \
		elif command -v zypper >/dev/null 2>&1; then \
			echo "Detected: zypper (openSUSE)"; \
			$(SUDO) zypper install -y sqlite3-devel libmosquitto-devel libopenssl-devel zlib-devel; \
			echo "Note: llama-cpp may not be available in openSUSE repos. Build from source if needed."; \
		elif command -v apk >/dev/null 2>&1; then \
			echo "Detected: apk (Alpine)"; \
			$(SUDO) apk add sqlite-dev libmosquitto-dev openssl-dev zlib-dev; \
			echo "Note: llama-cpp may not be available in Alpine repos. Build from source if needed."; \
		else \
			echo "Could not detect a supported package manager."; \
			echo "Please install the dependencies manually. See manual/35-external-dependencies.md"; \
			exit 1; \
		fi; \
	elif [ "$$OS" = "Darwin" ]; then \
		if command -v brew >/dev/null 2>&1; then \
			echo "Detected: Homebrew (macOS)"; \
			brew install sqlite3 llama.cpp mosquitto openssl zlib; \
		else \
			echo "Could not detect Homebrew."; \
			echo "Please install the dependencies manually. See manual/35-external-dependencies.md"; \
			exit 1; \
		fi; \
	else \
		echo "Unsupported operating system: $$OS"; \
		echo "Please install the dependencies manually. See manual/35-external-dependencies.md"; \
		exit 1; \
	fi

# Uninstall everything
uninstall:
	@echo "Removing skink from $(BINDIR) ..."
	$(SUDO) rm -f $(BINDIR)/$(SKINK_BIN)
	@echo "Removing $(LIBDIR) ..."
	$(SUDO) rm -rf $(LIBDIR)

# Run standard library tests using the built compiler
test: build
	@echo "Running standard library tests in $(TEST_DIR) ..."
	cd $(TEST_DIR) && ../$(SKINK_BIN) test

# Run a specific test pattern
test-pattern: build
	@echo "Running tests matching pattern '$(PATTERN)' ..."
	cd $(TEST_DIR) && ../$(SKINK_BIN) test $(PATTERN)

# Clean build artifacts
clean:
	@echo "Removing build artifacts ..."
	@rm -f $(SKINK_BIN) $(SKINK_BIN).exe
	@find . -name "*.o" -delete
	@find . -name "*.ll" -delete
	@find . -name "*.s" -delete
	@find . -name "main" -type f -delete
	@find . -name ".skink-test-*" -type d -exec rm -rf {} + 2>/dev/null || true
	@echo "Clean complete."

# Go-specific maintenance targets
fmt:
	cd compiler && $(GO) fmt ./...

vet:
	cd compiler && $(GO) vet ./...

# Development build with race detection
dev:
	cd compiler && $(GOBUILD) -race -o ../$(SKINK_BIN) $(SKINK_SRC)

help:
	@echo "Usage:"
	@echo "  make            	- build the skink compiler"
	@echo "  make install    	- install the compiler and standard library"
	@echo "			  (installs dependencies and builds compiler)"
	@echo "  make install-only	- install the compiler and standard library"
	@echo "			  (skips dependency installation and compiler build) if already built"
	@echo "  make install-deps	- install system dependencies"
	@echo "  make uninstall  	- uninstall the compiler and standard library"
	@echo "			  (does not remove dependencies)"
	@echo "  make test       	- run standard library tests"
	@echo "  make clean      	- remove build artifacts"
	@echo "  make fmt        	- format the code"
	@echo "  make vet        	- vet the code"
	@echo "  make help       	- show this help"