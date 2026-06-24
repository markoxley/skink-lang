# Skink Programming Language

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://golang.org)

Skink is a modern systems programming language that compiles to native code via LLVM. It combines the safety and expressiveness of high-level languages with the performance of low-level systems programming.

## Features

- **Type Safety**: Strong static typing with generics and type inference
- **Performance**: Compiles to native code via LLVM for optimal performance
- **Concurrency**: Built-in support for async/await, channels, and goroutine-like tasks
- **Modern Syntax**: Clean, readable syntax inspired by Go and Rust
- **Interoperability**: Seamless C FFI for using existing libraries
- **Standard Library**: Comprehensive standard library covering:
  - Networking (HTTP, TCP/UDP, MQTT)
  - Database (SQLite)
  - File I/O and filesystem operations
  - JSON, XML, YAML, CSV parsing
  - Cryptography and encryption
  - Compression (gzip, bz2)
  - LLM integration (llama.cpp)
  - Hardware interfaces (GPIO, I2C, SPI)
  - Time, math, string utilities
  - Regular expressions
  - And much more

## Installation

### Prerequisites

- Go 1.21 or later
- LLVM (llc)
- C compiler (gcc, clang, or MSVC)
- System dependencies:
  - SQLite3
  - llama.cpp (optional, for LLM support)
  - Mosquitto (optional, for MQTT)
  - OpenSSL
  - zlib

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/skink-lang.git
cd skink-lang

# Install dependencies and build
make install

# Or build only
make build
```

The compiler binary will be available as `./skink` in the repository root.

### Manual Installation

```bash
# Build the compiler
cd compiler
go build -o ../skink ./cmd/skink
cd ..

# Set environment variables
export SKINK_HOME=$(pwd)
export PATH=$PATH:$(pwd)
```

## Quick Start

Create a file `hello.skink`:

```skink
module main

fn main() -> int {
    println("Hello, World!")
    return 0
}
```

Compile and run:

```bash
skink hello.skink
./hello
```

## Language Examples

### Functions and Types

```skink
module main

// Generic function
fn max[T](a: T, b: T) -> T {
    if a > b {
        return a
    }
    return b
}

// Struct with methods
pub struct Point {
    x: float
    y: float

    pub fn New(x: float, y: float) -> Point {
        return Point{x: x, y: y}
    }

    pub fn Distance(self: Point, other: Point) -> float {
        dx := self.x - other.x
        dy := self.y - other.y
        return (dx * dx + dy * dy).sqrt()
    }
}

fn main() -> int {
    p1 := Point.New(0.0, 0.0)
    p2 := Point.New(3.0, 4.0)
    println("Distance: {}", p1.Distance(p2))
    return 0
}
```

### Concurrency

```skink
module main
import "std/sync"

fn worker(id: int, ch: chan int) {
    for i in 0..5 {
        ch <- id * 10 + i
    }
}

fn main() -> int {
    ch := make(chan int, 10)
    
    spawn worker(1, ch)
    spawn worker(2, ch)
    
    for i in 0..10 {
        val := <-ch
        println("Received: {}", val)
    }
    
    return 0
}
```

### Error Handling

```skink
module main
import "std/errors"
import "std/io"

fn readFile(path: string) -> (string, errors.Error) {
    data, err := io.ReadFile(path)
    if err.message != "" {
        return "", err
    }
    return data, errors.New("")
}

fn main() -> int {
    content, err := readFile("data.txt")?
    println("Content: {}", content)
    return 0
}
```

## Compiler Usage

```bash
# Compile and link
skink program.skink

# Type-check only
skink -check program.skink

# Generate LLVM IR
skink -emit-ll program.skink

# Compile to object file only
skink -c program.skink

# Print tokens
skink -lex program.skink

# Print AST
skink -ast program.skink

# Run tests
skink test [pattern]

# Fetch dependencies
skink get
```

## Standard Library Modules

Skink includes a comprehensive standard library organized into modules:

- **api**: HTTP client primitives
- **base64**: Base64 encoding/decoding
- **benchmark**: Performance benchmarking
- **buf**: Byte buffers
- **compress**: Compression (gzip, bz2)
- **context**: Cancellation and deadlines
- **crypto**: Cryptographic primitives
- **csv**: CSV parsing and generation
- **db**: SQLite database access
- **encrypt**: Encryption (AES, RSA)
- **errors**: Error handling
- **fmt**: Formatted I/O
- **fs**: Filesystem operations
- **gpio**: Raspberry Pi GPIO
- **hashmap**: Hash map data structure
- **i2c**: I2C communication
- **io**: I/O primitives
- **json**: JSON parsing and serialization
- **libc**: C library bindings
- **llm**: Large Language Model integration
- **log**: Logging
- **math**: Mathematical functions
- **mcp**: Model Context Protocol
- **mqtt**: MQTT client
- **net**: Networking (TCP/UDP)
- **os**: Operating system interface
- **path**: Path manipulation
- **rand**: Random number generation
- **reader**: Reader interface
- **reflect**: Runtime reflection
- **regex**: Regular expressions
- **rules**: Rule engine
- **sort**: Sorting algorithms
- **spi**: SPI communication
- **str**: String utilities
- **streams**: Stream processing
- **sync**: Synchronization primitives
- **tensor**: Tensor operations
- **time**: Time and date
- **waitgroup**: WaitGroup for concurrency
- **web**: Web server and routing
- **writer**: Writer interface
- **xml**: XML parsing
- **yaml**: YAML parsing

## Development

### Running Tests

```bash
# Run all standard library tests
make test

# Run specific test pattern
make test-pattern PATTERN=json
```

### Building

```bash
# Build the compiler
make build

# Build with race detection
make dev

# Build static binary
make static
```

### Code Quality

```bash
# Format code
make fmt

# Vet code
make vet
```

## Project Structure

```
skink-lang/
├── compiler/          # Compiler source code (Go)
│   ├── ast/          # Abstract Syntax Tree
│   ├── lexer/        # Lexical analyzer
│   ├── parser/       # Parser
│   ├── types/        # Type checker
│   ├── codegen/      # LLVM IR code generator
│   ├── resolver/     # Import resolution
│   ├── runtime/      # Runtime C code
│   └── cmd/skink/    # CLI entry point
├── std/              # Standard library (Skink)
│   ├── api.skink
│   ├── io.skink
│   └── ...
├── examples/         # Example programs
├── Makefile          # Build configuration
└── README.md         # This file
```

## Language Reference

### Type System

Skink supports:
- Primitive types: `int`, `float`, `bool`, `string`, `bytes`
- Composite types: arrays, maps, sets, tuples
- Custom types: structs, enums
- Generic types with type parameters
- Pointer types
- Function types
- Channel types

### Control Flow

- `if`, `else if`, `else`
- `for`, `while`, `until`
- `match` (pattern matching)
- `switch`
- `defer` (deferred execution)
- `with` (resource management)

### Concurrency

- `spawn` (start concurrent task)
- `async`/`await` (async/await syntax)
- Channels with `<-` operator
- `select` statement for channel operations
- `Mutex`, `RWMutex`, `Cond` from `std/sync`

### Advanced Features

- Generics with type parameters
- Templates for interface-like behavior
- Services for trait-like abstractions
- Rulesets for rule-based programming
- Comptime blocks for compile-time evaluation
- C FFI with `extern fn`
- Attributes (e.g., `[cuda]`, `[packed]`)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Author

**Mark Oxley**

## Acknowledgments

- LLVM project for the excellent compiler infrastructure
- The Go programming language for inspiration
- The Rust community for modern language design patterns
- llama.cpp for LLM integration capabilities
