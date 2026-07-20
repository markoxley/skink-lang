// Copyright 2026 Mark Oxley
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// integration_test.go contains end-to-end integration tests that compile
// and execute Skink programs to verify correct runtime behavior.
package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
	"github.com/skink-lang/compiler/resolver"
	"github.com/skink-lang/compiler/types"
)

// compileAndRun parses, type-checks, compiles, and executes a Skink program.
// It returns the exit code and any captured stdout.
func compileAndRun(t *testing.T, name string, input string) (int, string) {
	t.Helper()

	// Parse
	l := lexer.New(input)
	p := parser.New(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	// Monomorphize generics before type-checking.
	prog = ast.Monomorphize(prog)

	// Type-check
	checker := types.NewChecker()
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	// Monomorphize templates before codegen.
	prog = ast.TemplateMonomorphize(prog)

	// Compile to binary (use a temp dir to avoid polluting the repo)
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, name+".skink")
	if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	binPath, err := Build(prog, srcPath, BuildOptions{OutputPath: filepath.Join(tmpDir, name), SaveTemps: true})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Run binary
	cmd := exec.Command(binPath)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

// compileAndRunWithObjects is like compileAndRun but links extra .o files.
func compileAndRunWithObjects(t *testing.T, name string, input string, extraObjs []string) (int, string) {
	t.Helper()

	l := lexer.New(input)
	p := parser.New(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	prog = ast.Monomorphize(prog)

	checker := types.NewChecker()
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, name+".skink")
	if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	binPath, err := Build(prog, srcPath, BuildOptions{
		OutputPath:   filepath.Join(tmpDir, name),
		ExtraObjects: extraObjs,
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd := exec.Command(binPath)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

// compileAndRunDebug compiles a Skink program with debug info enabled
// and returns the exit code and stdout.
func compileAndRunDebug(t *testing.T, name string, input string) (int, string) {
	t.Helper()

	l := lexer.New(input)
	p := parser.New(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	prog = ast.Monomorphize(prog)

	checker := types.NewChecker()
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, name+".skink")
	if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	binPath, err := Build(prog, srcPath, BuildOptions{
		OutputPath: filepath.Join(tmpDir, name),
		Debug:      true,
		SourcePath: srcPath,
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd := exec.Command(binPath)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

// compileAndRunMulti parses, type-checks, compiles, and executes a multi-file Skink program.
func compileAndRunMulti(t *testing.T, name string, inputs map[string]string) (int, string) {
	t.Helper()

	tmpDir := t.TempDir()
	prog := &ast.Program{}
	var srcPaths []string

	for fname, input := range inputs {
		srcPath := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		srcPaths = append(srcPaths, srcPath)
		l := lexer.New(input)
		p := parser.New(l)
		pprog := p.ParseProgram()
		if len(p.Errors()) > 0 {
			t.Fatalf("parse errors in %s: %v", fname, p.Errors())
		}
		prog.Declarations = append(prog.Declarations, pprog.Declarations...)
	}

	prog = ast.Monomorphize(prog)

	checker := types.NewChecker()
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	binPath, err := Build(prog, srcPaths[0], BuildOptions{OutputPath: filepath.Join(tmpDir, name), SaveTemps: true})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd := exec.Command(binPath)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

// compileAndRunResolved writes files to a temp dir, resolves imports, compiles and runs.
// Optional args are passed as command-line arguments to the compiled binary.
func compileAndRunResolved(t *testing.T, name string, inputs map[string]string, args ...string) (int, string) {
	t.Helper()

	tmpDir := t.TempDir()
	var srcPaths []string

	for fname, input := range inputs {
		srcPath := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		srcPaths = append(srcPaths, srcPath)
	}

	prog, symbolInfo, err := resolver.Resolve(srcPaths)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	prog = ast.Monomorphize(prog)

	// Update symbolInfo with any newly monomorphized declarations.
	for _, decl := range prog.Declarations {
		name := resolver.DeclName(decl)
		if name != "" {
			if _, exists := symbolInfo[name]; !exists {
				// Inherit module information by finding a demangled baseline name.
				module := ""
				pub := true
				if idx := strings.Index(name, "_"); idx != -1 {
					baseName := name[:idx]
					if info, ok := symbolInfo[baseName]; ok {
						module = info.Module
						pub = info.Pub
					}
				}
				symbolInfo[name] = types.SymbolInfo{
					Module: module,
					Pub:    pub,
				}
			}
		}
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	binPath, err := Build(prog, srcPaths[0], BuildOptions{OutputPath: filepath.Join(tmpDir, name), SaveTemps: true})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd := exec.Command(binPath, args...)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

// compileAndRunResolvedWithObjects is like compileAndRunResolved but links
// extra .o files into the final executable.
func compileAndRunResolvedWithObjects(t *testing.T, name string, inputs map[string]string, extraObjs []string) (int, string) {
	t.Helper()

	tmpDir := t.TempDir()
	var srcPaths []string

	for fname, input := range inputs {
		srcPath := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		srcPaths = append(srcPaths, srcPath)
	}

	prog, symbolInfo, err := resolver.Resolve(srcPaths)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	prog = ast.Monomorphize(prog)

	for _, decl := range prog.Declarations {
		name := resolver.DeclName(decl)
		if name != "" {
			if _, exists := symbolInfo[name]; !exists {
				module := ""
				pub := true
				if idx := strings.Index(name, "_"); idx != -1 {
					baseName := name[:idx]
					if info, ok := symbolInfo[baseName]; ok {
						module = info.Module
						pub = info.Pub
					}
				}
				symbolInfo[name] = types.SymbolInfo{
					Module: module,
					Pub:    pub,
				}
			}
		}
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	binPath, err := Build(prog, srcPaths[0], BuildOptions{
		OutputPath:   filepath.Join(tmpDir, name),
		ExtraObjects: extraObjs,
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd := exec.Command(binPath)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

// compileAndRunCImport tests importing a C header and linking with a C object file.
// headerCode is written to helper.h and parsed by cimport.
// cCode is written to helper.c and compiled by the C compiler.
func compileAndRunCImport(t *testing.T, name string, synFiles map[string]string, headerCode, cCode string) (int, string) {
	t.Helper()

	tmpDir := t.TempDir()
	var srcPaths []string

	for fname, input := range synFiles {
		srcPath := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		srcPaths = append(srcPaths, srcPath)
	}

	// Write C header and source.
	headerPath := filepath.Join(tmpDir, "helper.h")
	if err := os.WriteFile(headerPath, []byte(headerCode), 0644); err != nil {
		t.Fatalf("write C header: %v", err)
	}
	cPath := filepath.Join(tmpDir, "helper.c")
	if err := os.WriteFile(cPath, []byte(cCode), 0644); err != nil {
		t.Fatalf("write C source: %v", err)
	}
	objPath := filepath.Join(tmpDir, "helper.o")
	linker := "cc"
	if _, err := exec.LookPath("clang"); err == nil {
		linker = "clang"
	}
	cmd := exec.Command(linker, "-c", "-o", objPath, cPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compiling C code failed: %v\n%s", err, out)
	}

	prog, symbolInfo, err := resolver.Resolve(srcPaths)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	prog = ast.Monomorphize(prog)

	// Update symbolInfo with any newly monomorphized declarations.
	for _, decl := range prog.Declarations {
		name := resolver.DeclName(decl)
		if name != "" {
			if _, exists := symbolInfo[name]; !exists {
				// Inherit module information by finding a demangled baseline name.
				module := ""
				pub := true
				if idx := strings.Index(name, "_"); idx != -1 {
					baseName := name[:idx]
					if info, ok := symbolInfo[baseName]; ok {
						module = info.Module
						pub = info.Pub
					}
				}
				symbolInfo[name] = types.SymbolInfo{
					Module: module,
					Pub:    pub,
				}
			}
		}
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	binPath, err := Build(prog, srcPaths[0], BuildOptions{
		OutputPath:   filepath.Join(tmpDir, name),
		ExtraObjects: []string{objPath},
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd = exec.Command(binPath)
	out, _ := cmd.CombinedOutput()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return exitCode, strings.TrimSpace(string(out))
}

func TestIntegrationCImport(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_test",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_add(10, 32)
}`,
		},
		`int c_add(int a, int b);`,
		`int c_add(int a, int b) { return a + b; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportEnum(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_enum",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	if (get_red() == RED) {
		return 42
	}
	return 0
}`,
		},
		`enum Color { RED, GREEN, BLUE };
int get_red(void);`,
		`enum Color { RED, GREEN, BLUE };
int get_red(void) { return RED; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportStruct(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_struct",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	p := Point{x: 10, y: 32}
	return p.x + p.y
}`,
		},
		`struct Point { int x; int y; };
int c_get_ten(void);`,
		`struct Point { int x; int y; };
int c_get_ten(void) { return 10; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportTypedef(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_typedef",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_get_ten()
}`,
		},
		`typedef int int32_t;
int32_t c_get_ten(void);`,
		`typedef int int32_t;
int32_t c_get_ten(void) { return 10; }`,
	)
	if code != 10 {
		t.Errorf("expected exit code 10, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportTypedefStruct(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_typedef_struct",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	p := Point{x: 10, y: 32}
	return p.x + p.y
}`,
		},
		`typedef struct { int x; int y; } Point;
int c_get_ten(void);`,
		`typedef struct { int x; int y; } Point;
int c_get_ten(void) { return 10; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportTypedefEnum(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_typedef_enum",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	if (get_red() == RED) {
		return 42
	}
	return 0
}`,
		},
		`typedef enum { RED, GREEN } Color;
int get_red(void);`,
		`typedef enum { RED, GREEN } Color;
int get_red(void) { return RED; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportVariadic(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_variadic",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_sum(3, 10, 20, 12)
}`,
		},
		`int c_sum(int n, ...);`,
		`#include <stdarg.h>
int c_sum(int n, ...) {
	va_list args;
	va_start(args, n);
	int sum = 0;
	for (int i = 0; i < n; i++) {
		sum += va_arg(args, int);
	}
	va_end(args);
	return sum;
}`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportDefineExpr(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_define_expr",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	if (EOF == -1) {
		return 42
	}
	return 0
}`,
		},
		`#define EOF (-1)
int c_get_eof(void);`,
		`int c_get_eof(void) { return -1; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportDefineShift(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_define_shift",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return FLAG
}`,
		},
		`#define FLAG (1 << 3)
int c_get_flag(void);`,
		`int c_get_flag(void) { return 8; }`,
	)
	if code != 8 {
		t.Errorf("expected exit code 8, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportDefineCrossRef(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_define_cross",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return COMB
}`,
		},
		`#define A 10
#define B 32
#define COMB (A + B)
int c_get_comb(void);`,
		`int c_get_comb(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportIfdef(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_ifdef",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return VALUE
}`,
		},
		`#define FEATURE_ON 1
#ifdef FEATURE_ON
#define VALUE 42
#else
#define VALUE 0
#endif
int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportIfndef(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_ifndef",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return VALUE
}`,
		},
		`#ifndef SKIP_FEATURE
#define VALUE 42
#else
#define VALUE 0
#endif
int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportIf(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_if",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return VALUE
}`,
		},
		`#define LEVEL 3
#if LEVEL > 2
#define VALUE 42
#else
#define VALUE 0
#endif
int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportNestedIfdef(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_nested",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return VALUE
}`,
		},
		`#define OUTER
#ifdef OUTER
#define INNER 1
#ifdef INNER
#define VALUE 42
#else
#define VALUE 0
#endif
#else
#define VALUE 99
#endif
int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportFunctionPointer(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_fnpointer",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_call_it()
}`,
		},
		`typedef int (*fnptr_t)(int);
int c_call_it(void);`,
		`typedef int (*fnptr_t)(int);
int my_fn(int x) { return x * 8 + 2; }
int c_call_it(void) {
	fnptr_t f = my_fn;
	return f(5);
}`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportInclude(t *testing.T) {
	tmpDir := t.TempDir()

	// Write extra header
	typesPath := filepath.Join(tmpDir, "types.h")
	os.WriteFile(typesPath, []byte(`struct Point { int x; int y; };
int c_get_ten(void);
`), 0644)

	headerPath := filepath.Join(tmpDir, "helper.h")
	os.WriteFile(headerPath, []byte(`#include "types.h"
`), 0644)

	cPath := filepath.Join(tmpDir, "helper.c")
	os.WriteFile(cPath, []byte(`struct Point { int x; int y; };
int c_get_ten(void) { return 10; }
`), 0644)

	srcPath := filepath.Join(tmpDir, "main.skink")
	os.WriteFile(srcPath, []byte(`import "C:helper.h"
fn main() -> int {
	p := Point{x: 10, y: 32}
	return p.x + p.y
}`), 0644)

	objPath := filepath.Join(tmpDir, "helper.o")
	linker := "cc"
	if _, err := exec.LookPath("clang"); err == nil {
		linker = "clang"
	}
	cmd := exec.Command(linker, "-c", "-o", objPath, cPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compiling C code failed: %v\n%s", err, out)
	}

	prog, symbolInfo, err := resolver.Resolve([]string{srcPath})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	binPath, err := Build(prog, srcPath, BuildOptions{
		OutputPath:   filepath.Join(tmpDir, "cimport_include"),
		ExtraObjects: []string{objPath},
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	cmd = exec.Command(binPath)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if exitCode != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", exitCode, strings.TrimSpace(string(out)))
	}
}

func TestIntegrationCImportIfDefined(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_if_defined",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return VALUE
}`,
		},
		`#define FEATURE 1
#if defined(FEATURE) && FEATURE > 0
#define VALUE 42
#else
#define VALUE 0
#endif
int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportIfComparison(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_if_comp",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return VALUE
}`,
		},
		`#define VERSION 2011
#if VERSION >= 2011 && VERSION != 1999
#define VALUE 42
#else
#define VALUE 0
#endif
int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportAttribute(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_attr",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_get_val()
}`,
		},
		`int c_get_val(void) __attribute__((visibility("default")));`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportExternC(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_extern_c",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_get_val()
}`,
		},
		`#ifdef __cplusplus
extern "C" {
#endif
int c_get_val(void);
#ifdef __cplusplus
}
#endif`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportAttributeBefore(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_attr_before",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_get_val()
}`,
		},
		`__attribute__((visibility("default"))) int c_get_val(void);`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCImportExternCUnguarded(t *testing.T) {
	code, out := compileAndRunCImport(t, "cimport_extern_c2",
		map[string]string{
			"main.skink": `import "C:helper.h"
fn main() -> int {
	return c_get_val()
}`,
		},
		`extern "C" {
int c_get_val(void);
}`,
		`int c_get_val(void) { return 42; }`,
	)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPower(t *testing.T) {
	code, out := compileAndRun(t, "power", `fn main() -> int {
		return 2 ** 3
	}`)
	if code != 8 {
		t.Errorf("expected exit code 8, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMinMax(t *testing.T) {
	code, out := compileAndRun(t, "minmax", `fn main() -> int {
		return max(int8) - min(int8)
	}`)
	if code != 255 {
		t.Errorf("expected exit code 255, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSetIn(t *testing.T) {
	code, out := compileAndRun(t, "set_in", `fn main() -> int {
		s := set{1, 2, 3}
		if 2 in s {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSetInNotFound(t *testing.T) {
	code, out := compileAndRun(t, "set_in_not", `fn main() -> int {
		s := set{1, 2, 3}
		if 5 in s {
			return 42
		}
		return 0
	}`)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSetUnion(t *testing.T) {
	code, out := compileAndRun(t, "set_union", `fn main() -> int {
		a := set{1, 2}
		b := set{2, 3}
		c := a | b
		if 1 in c && 2 in c && 3 in c {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSetIntersection(t *testing.T) {
	code, out := compileAndRun(t, "set_intersection", `fn main() -> int {
		a := set{1, 2}
		b := set{2, 3}
		c := a & b
		if 2 in c {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSetDifference(t *testing.T) {
	code, out := compileAndRun(t, "set_difference", `fn main() -> int {
		a := set{1, 2, 3}
		b := set{2}
		c := a - b
		if 1 in c && 3 in c && !(2 in c) {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPanic(t *testing.T) {
	code, out := compileAndRun(t, "panic_test", `fn main() -> int {
		panic("oh no")
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
	if !strings.Contains(out, "oh no") {
		t.Errorf("expected output to contain 'oh no', got %q", out)
	}
}

func TestIntegrationMakeSet(t *testing.T) {
	code, out := compileAndRun(t, "make_set", `fn main() -> int {
		s := make(set<int>)
		if 5 in s {
			return 0
		}
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStructMethod(t *testing.T) {
	code, out := compileAndRun(t, "struct_method", `struct Point {
		x: int
		y: int
	}

	fn sum(p: Point) -> int {
		return p.x + p.y
	}

	fn main() -> int {
		p := Point{x: 10, y: 32}
		return p.sum()
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationService(t *testing.T) {
	code, out := compileAndRun(t, "service_test", `service Greeter {
		fn greet(name: string) {
			print("Hello, " + name)
		}
	}

	fn main() -> int {
		Greeter.greet("World")
		return 0
	}`)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d (output: %q)", code, out)
	}
	if out != "Hello, World" {
		t.Errorf("expected output %q, got %q", "Hello, World", out)
	}
}

func TestIntegrationTensor(t *testing.T) {
	code, _ := compileAndRun(t, "tensor_test", `fn main() -> int {
		A := tensor_ones(2, 2)
		B := tensor_ones(2, 2)
		C := A @ B
		result := tensor_get(C, 0, 0)
		return int(result)
	}`)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
}

func TestIntegrationAdvancedMath(t *testing.T) {
	code, out := compileAndRun(t, "adv_math", `
	fn myFunc(x: float) -> float {
		return x * x
	}

	fn main() -> int {
		s := sin(0.0)
		c := cos(0.0)
		sq := sqrt(4.0)
		if sq != 2.0 || s != 0.0 || c != 1.0 {
			return 1
		}

		d := diff(myFunc, 3.0)
		if d < 5.9 || d > 6.1 {
			return 2
		}

		i := integrate(myFunc, 0.0, 3.0)
		if i < 8.9 || i > 9.1 {
			return 3
		}

		A := tensor_ones(2, 2)
		A_T := A.T
		val := tensor_get(A_T, 0, 0)
		if val != 1.0 {
			return 4
		}

		n := norm(A)
		if n < 1.99 || n > 2.01 {
			return 5
		}

		d2 := dot(A, A)
		if d2 < 3.99 || d2 > 4.01 {
			return 6
		}

		return 42
	}`)

	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBitfield(t *testing.T) {
	code, out := compileAndRun(t, "bitfield_test", `struct Flags {
		active: bool : 1
		count: int : 4
	}

	fn main() -> int {
		f := Flags{active: true, count: 5}
		if f.active {
			return f.count + 1
		}
		return 0
	}`)
	if code != 6 {
		t.Errorf("expected exit code 6, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPackedStruct(t *testing.T) {
	code, out := compileAndRun(t, "packed_struct_test", `[packed] struct SensorPacket {
		id: int8
		val: int
	}

	fn main() -> int {
		p := SensorPacket{id: int8(12), val: 30}
		return int(p.id) + p.val
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStructLayoutAndSizing(t *testing.T) {
	code, out := compileAndRun(t, "struct_layout_test", `[packed] struct SensorPacket {
		id: int8
		val: int
	}

	[align(16)] struct AlignedStruct {
		id: int8
		val: int
	}

	struct NormalStruct {
		id: int8
		val: int
	}

	fn main() -> int {
		if sizeof(SensorPacket) == 5 && alignof(SensorPacket) == 1 {
			if sizeof(NormalStruct) == 8 && alignof(NormalStruct) == 4 {
				if sizeof(AlignedStruct) == 16 && alignof(AlignedStruct) == 16 {
					return 42
				}
			}
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationUnsignedSemantics(t *testing.T) {
	code, out := compileAndRun(t, "unsigned_test", `
	fn main() -> int {
		a := uint32(4294967292)
		b := uint32(2)

		divResult := a / b
		isGreater := a > b
		isSmaller := a < b

		if divResult == uint32(2147483646) && isGreater && !isSmaller {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationRuleset(t *testing.T) {
	code, out := compileAndRun(t, "ruleset_test", `var count: int = 0

ruleset CounterRules {
    rule increment when count < 3 {
        action: {
            count = count + 1
            if count >= 3 {
                CounterRules.stop()
            }
        }
    }
}

extern fn usleep(usec: int) -> int

fn main() -> int {
    CounterRules.start()
    // Wait until the background coroutine increments count to 3
    while count < 3 {
        usleep(1000)
    }
    return count
}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationForRangeLoop(t *testing.T) {
	code, out := compileAndRun(t, "for_range", `fn main() -> int {
		ch := make(chan<int>)
		ch <- 10
		ch <- 20
		close(ch)
		sum := 0
		for v := range ch {
			sum = sum + v
		}
		return sum
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestIntegrationUInt16Type(t *testing.T) {
	code, out := compileAndRun(t, "uint16_type", `fn main() -> int {
		x := uint16(100)
		return int(x)
	}`)
	if code != 100 {
		t.Errorf("expected exit code 100, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringInterpolation(t *testing.T) {
	code, out := compileAndRun(t, "interpolation_test", `
	fn main() -> int {
		age := 25
		pi := 3.14
		name := "Alice"
		print("Name: {name}, Age: {age}, Pi: {pi}")
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
	if !strings.Contains(out, "Name: Alice") || !strings.Contains(out, "Age: 25") || !strings.Contains(out, "Pi: 3.14") {
		t.Errorf("expected output to contain Name: Alice, Age: 25, Pi: 3.14, got %q", out)
	}
}

func TestIntegrationGenericStruct(t *testing.T) {
	code, out := compileAndRun(t, "generic_struct", `struct Box<T> {
		value: T
	}

	fn main() -> int {
		b := Box<int>{value: 42}
		return b.value
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationGenericFunction(t *testing.T) {
	code, out := compileAndRun(t, "generic_fn", `fn identity<T>(x: T) -> T {
		return x
	}

	fn main() -> int {
		return identity<int>(5)
	}`)
	if code != 5 {
		t.Errorf("expected exit code 5, got %d (output: %q)", code, out)
	}
}

func TestIntegrationGenericTypeChecking(t *testing.T) {
	input := `struct Box<T> {
		value: T
	}

	fn main() -> int {
		b := Box<int>{value: "not an int"}
		return b.value
	}`

	l := lexer.New(input)
	p := parser.New(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	prog = ast.Monomorphize(prog)

	checker := types.NewChecker()
	checker.CheckProgram(prog)
	if len(checker.Errors()) == 0 {
		t.Fatalf("expected type checking error for unmatched generic assignment, got none")
	}
}

func TestIntegrationLocalCacheResolution(t *testing.T) {
	tmpDir := t.TempDir()

	cachePath := filepath.Join(tmpDir, ".skink", "cache", "github.com", "mock", "math")
	if err := os.MkdirAll(cachePath, 0755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}
	mathSrc := `module math
	pub fn add(a: int, b: int) -> int {
		return a + b
	}`
	if err := os.WriteFile(filepath.Join(cachePath, "math.skink"), []byte(mathSrc), 0644); err != nil {
		t.Fatalf("failed to write math.skink: %v", err)
	}

	mainSrc := `module main
	import "github.com/mock/math"

	fn main() -> int {
		return math.add(10, 32)
	}`
	mainPath := filepath.Join(tmpDir, "main.skink")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
		t.Fatalf("failed to write main.skink: %v", err)
	}

	program, symbolInfo, err := resolver.Resolve([]string{mainPath})
	if err != nil {
		t.Fatalf("resolver error: %v", err)
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(program)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type checker error: %v", checker.Errors())
	}

	binPath := filepath.Join(tmpDir, "testbin")
	artifact, err := Build(program, mainPath, BuildOptions{OutputPath: binPath})
	if err != nil {
		t.Fatalf("codegen error: %v", err)
	}

	cmd := exec.Command(artifact)
	out, _ := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}

	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSimpleReturn(t *testing.T) {
	code, out := compileAndRun(t, "simple_return", `fn main() -> int {
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAddFunction(t *testing.T) {
	code, out := compileAndRun(t, "add_fn", `fn add(a: int, b: int) -> int {
		return a + b
	}
	fn main() -> int {
		return add(10, 32)
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationVarDecl(t *testing.T) {
	code, out := compileAndRun(t, "var_decl", `fn main() -> int {
		x := 7
		y := 8
		return x + y
	}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationIfElse(t *testing.T) {
	code, out := compileAndRun(t, "if_else", `fn max(a: int, b: int) -> int {
		if a > b {
			return a
		} else {
			return b
		}
	}
	fn main() -> int {
		return max(5, 10)
	}`)
	if code != 10 {
		t.Errorf("expected exit code 10, got %d (output: %q)", code, out)
	}
}

func TestIntegrationWhileLoop(t *testing.T) {
	code, out := compileAndRun(t, "while_loop", `fn sum(n: int) -> int {
		total := 0
		i := 1
		while i <= n {
			total = total + i
			i = i + 1
		}
		return total
	}
	fn main() -> int {
		return sum(5)
	}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationForLoop(t *testing.T) {
	code, out := compileAndRun(t, "for_loop", `fn factorial(n: int) -> int {
		result := 1
		for i := 1; i <= n; i = i + 1 {
			result = result * i
		}
		return result
	}
	fn main() -> int {
		return factorial(5)
	}`)
	if code != 120 {
		t.Errorf("expected exit code 120, got %d (output: %q)", code, out)
	}
}

func TestIntegrationArrayLiteral(t *testing.T) {
	code, out := compileAndRun(t, "array_lit", `fn main() -> int {
		arr := [10, 20, 30]
		return arr[0] + arr[1] + arr[2]
	}`)
	if code != 60 {
		t.Errorf("expected exit code 60, got %d (output: %q)", code, out)
	}
}

func TestIntegrationForInLoop(t *testing.T) {
	code, out := compileAndRun(t, "forin", `fn main() -> int {
		items := [1, 2, 3, 4]
		sum := 0
		for i in items {
			sum = sum + i
		}
		return sum
	}`)
	if code != 10 {
		t.Errorf("expected exit code 10, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStructInitAndFieldAccess(t *testing.T) {
	code, out := compileAndRun(t, "struct", `struct Point {
		x: int
		y: int
	}
	fn main() -> int {
		p := Point{x: 3, y: 4}
		return p.x + p.y
	}`)
	if code != 7 {
		t.Errorf("expected exit code 7, got %d (output: %q)", code, out)
	}
}

func TestIntegrationConstInline(t *testing.T) {
	code, out := compileAndRun(t, "const_inline", `const MAGIC = 99
	fn main() -> int {
		return MAGIC
	}`)
	if code != 99 {
		t.Errorf("expected exit code 99, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPrintOutput(t *testing.T) {
	code, out := compileAndRun(t, "print_out", `fn main() -> int {
		print("Hello")
		return 0
	}`)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if out != "Hello" {
		t.Errorf("expected stdout 'Hello', got %q", out)
	}
}

func TestIntegrationPrefixOps(t *testing.T) {
	code, out := compileAndRun(t, "prefix", `fn main() -> int {
		a := 5
		b := -3
		c := ~0
		return a + b + c
	}`)
	// a=5, b=-3, c=~0=-1 (i32 bitwise NOT of 0)
	// 5 + (-3) + (-1) = 1
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationComparisonAndLogic(t *testing.T) {
	code, out := compileAndRun(t, "compare", `fn main() -> int {
		if 1 < 2 {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAssignment(t *testing.T) {
	code, out := compileAndRun(t, "assign", `fn main() -> int {
		x := 1
		x = 100
		return x
	}`)
	if code != 100 {
		t.Errorf("expected exit code 100, got %d (output: %q)", code, out)
	}
}

func TestIntegrationNestedIfReturns(t *testing.T) {
	code, out := compileAndRun(t, "nested_if", `fn sign(n: int) -> int {
		if n < 0 {
			return -1
		}
		if n == 0 {
			return 0
		}
		return 1
	}
	fn main() -> int {
		return sign(0) + sign(5) + sign(-3)
	}`)
	// 0 + 1 + (-1) = 0
	if code != 0 {
		t.Errorf("expected exit code 0, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPointerDeref(t *testing.T) {
	code, out := compileAndRun(t, "ptr_deref", `fn main() -> int {
		x := 42
		p := &x
		return *p
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBreakInWhile(t *testing.T) {
	code, out := compileAndRun(t, "break_while", `fn main() -> int {
		sum := 0
		i := 1
		while i <= 100 {
			sum = sum + i
			if sum > 10 {
				break
			}
			i = i + 1
		}
		return sum
	}`)
	// 1 + 2 + 3 + 4 = 10, next iteration adds 5 -> 15 > 10, break
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationContinueInFor(t *testing.T) {
	code, out := compileAndRun(t, "continue_for", `fn main() -> int {
		sum := 0
		for i := 1; i <= 5; i = i + 1 {
			if i == 3 {
				continue
			}
			sum = sum + i
		}
		return sum
	}`)
	// 1 + 2 + 4 + 5 = 12 (skips 3)
	if code != 12 {
		t.Errorf("expected exit code 12, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBreakInForIn(t *testing.T) {
	code, out := compileAndRun(t, "break_forin", `fn main() -> int {
		items := [10, 20, 30, 40]
		sum := 0
		for i in items {
			sum = sum + i
			if sum >= 30 {
				break
			}
		}
		return sum
	}`)
	// 10 + 20 = 30, then break before adding 30
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestIntegrationLenArray(t *testing.T) {
	code, out := compileAndRun(t, "len_arr", `fn main() -> int {
		items := [10, 20, 30]
		return len(items)
	}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationLenArrayLiteral(t *testing.T) {
	code, out := compileAndRun(t, "len_lit", `fn main() -> int {
		return len([1, 2, 3, 4, 5])
	}`)
	if code != 5 {
		t.Errorf("expected exit code 5, got %d (output: %q)", code, out)
	}
}

func TestIntegrationLenString(t *testing.T) {
	code, out := compileAndRun(t, "len_str", `fn main() -> int {
		return len("hello")
	}`)
	if code != 5 {
		t.Errorf("expected exit code 5, got %d (output: %q)", code, out)
	}
}

func TestIntegrationLenInForLoop(t *testing.T) {
	code, out := compileAndRun(t, "len_for", `fn main() -> int {
		items := [1, 2, 3, 4]
		sum := 0
		for i := 0; i < len(items); i = i + 1 {
			sum = sum + i
		}
		return sum
	}`)
	// 0 + 1 + 2 + 3 = 6
	if code != 6 {
		t.Errorf("expected exit code 6, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringConcat(t *testing.T) {
	code, out := compileAndRun(t, "str_concat", `fn main() -> int {
		greeting := "Hello, "
		name := "World"
		msg := greeting + name
		print(msg)
		return 0
	}`)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if out != "Hello, World" {
		t.Errorf("expected stdout 'Hello, World', got %q", out)
	}
}

func TestIntegrationStringEqual(t *testing.T) {
	code, out := compileAndRun(t, "str_eq", `fn main() -> int {
		a := "hello"
		b := "hello"
		if a == b {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringNotEqual(t *testing.T) {
	code, out := compileAndRun(t, "str_ne", `fn main() -> int {
		a := "hello"
		b := "world"
		if a != b {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringLess(t *testing.T) {
	code, out := compileAndRun(t, "str_lt", `fn main() -> int {
		a := "apple"
		b := "banana"
		if a < b {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringGreater(t *testing.T) {
	code, out := compileAndRun(t, "str_gt", `fn main() -> int {
		a := "zebra"
		b := "apple"
		if a > b {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringLessEqual(t *testing.T) {
	code, out := compileAndRun(t, "str_le", `fn main() -> int {
		a := "same"
		b := "same"
		if a <= b {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringGreaterEqual(t *testing.T) {
	code, out := compileAndRun(t, "str_ge", `fn main() -> int {
		a := "zebra"
		b := "apple"
		if a >= b {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationNestedStructFieldAccess(t *testing.T) {
	code, out := compileAndRun(t, "nested_struct", `struct Inner {
		x: int
	}

	struct Outer {
		inner: Inner
	}

	fn main() -> int {
		o := Outer{inner: Inner{x: 42}}
		return o.inner.x
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationArrayParameter(t *testing.T) {
	code, out := compileAndRun(t, "arr_param", `fn sum(arr: []int) -> int {
		s := 0
		for i := 0; i < 3; i = i + 1 {
			s = s + arr[i]
		}
		return s
	}

	fn main() -> int {
		items := [1, 2, 3]
		return sum(items)
	}`)
	if code != 6 {
		t.Errorf("expected exit code 6, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStructFieldAssignment(t *testing.T) {
	code, out := compileAndRun(t, "struct_assign", `struct Point {
		x: int
		y: int
	}

	fn main() -> int {
		p := Point{x: 10, y: 20}
		p.x = 42
		return p.x
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationArrayElementAssignment(t *testing.T) {
	code, out := compileAndRun(t, "arr_assign", `fn main() -> int {
		arr := [1, 2, 3]
		arr[0] = 42
		return arr[0]
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationNestedStructFieldAssignment(t *testing.T) {
	code, out := compileAndRun(t, "nested_assign", `struct Inner {
		x: int
	}

	struct Outer {
		inner: Inner
	}

	fn main() -> int {
		o := Outer{inner: Inner{x: 10}}
		o.inner.x = 42
		return o.inner.x
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCompoundAssignment(t *testing.T) {
	code, out := compileAndRun(t, "compound", `fn main() -> int {
		x := 10
		x += 5
		return x
	}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCompoundFieldAssignment(t *testing.T) {
	code, out := compileAndRun(t, "compound_field", `struct Point {
		x: int
		y: int
	}

	fn main() -> int {
		p := Point{x: 10, y: 20}
		p.x += 5
		return p.x
	}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationCompoundIndexAssignment(t *testing.T) {
	code, out := compileAndRun(t, "compound_idx", `fn main() -> int {
		arr := [10, 20, 30]
		arr[0] += 5
		return arr[0]
	}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStringIndexing(t *testing.T) {
	code, out := compileAndRun(t, "str_idx", `fn main() -> int {
		s := "ABC"
		return s[0]
	}`)
	if code != 65 {
		t.Errorf("expected exit code 65, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMultipleReturnValues(t *testing.T) {
	code, out := compileAndRun(t, "multi_ret", `fn swap(a: int, b: int) -> (int, int) {
		return b, a
	}

	fn main() -> int {
		x := 10
		y := 20
		x, y = swap(x, y)
		return x
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMultipleReturnVarDecl(t *testing.T) {
	code, out := compileAndRun(t, "multi_ret_var", `fn swap(a: int, b: int) -> (int, int) {
		return b, a
	}

	fn main() -> int {
		x, _ := swap(10, 20)
		return x
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMultipleReturnPassThrough(t *testing.T) {
	code, out := compileAndRun(t, "multi_ret_pass", `fn swap(a: int, b: int) -> (int, int) {
		return b, a
	}

	fn pass(a: int, b: int) -> (int, int) {
		return swap(a, b)
	}

	fn main() -> int {
		x, _ := pass(10, 20)
		return x
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationErrorPropagationSuccess(t *testing.T) {
	code, out := compileAndRun(t, "err_prop_ok", `struct MyError {
		message: string
	}

	fn String(self: MyError) -> string {
		return self.message
	}

	fn divide(a: float, b: float) -> (float, error) {
		if b == 0.0 {
			return 0.0, MyError{message: "division by zero"}
		}
		return a / b, nil
	}

	fn compute() -> (float, error) {
		x := divide(10.0, 2.0)?
		y := divide(20.0, 4.0)?
		return x + y, nil
	}

	fn main() -> int {
		result, err := compute()
		if err != nil {
			return 1
		}
		if result == 10.0 {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationErrorPropagationFail(t *testing.T) {
	code, out := compileAndRun(t, "err_prop_fail", `struct MyError {
		message: string
	}

	fn String(self: MyError) -> string {
		return self.message
	}

	fn divide(a: float, b: float) -> (float, error) {
		if b == 0.0 {
			return 0.0, MyError{message: "division by zero"}
		}
		return a / b, nil
	}

	fn compute() -> (float, error) {
		x := divide(10.0, 0.0)?
		y := divide(20.0, 4.0)?
		return x + y, nil
	}

	fn main() -> int {
		result, err := compute()
		if err != nil {
			return 99
		}
		if result == 10.0 {
			return 42
		}
		return 0
	}`)
	if code != 99 {
		t.Errorf("expected exit code 99, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBitwiseOperators(t *testing.T) {
	code, out := compileAndRun(t, "bitwise", `fn main() -> int {
		// 5 & 3 = 1, 5 | 3 = 7, 5 ^ 3 = 6
		if (5 & 3) == 1 && (5 | 3) == 7 && (5 ^ 3) == 6 {
			// 1 << 3 = 8, 8 >> 2 = 2
			if (1 << 3) == 8 && (8 >> 2) == 2 {
				// ~0 = -1 (all bits set)
				if ~0 == -1 {
					return 42
				}
			}
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationIfExpression(t *testing.T) {
	code, out := compileAndRun(t, "if_expr", `fn max(a: int, b: int) -> int {
		return if a > b { a } else { b }
	}

	fn main() -> int {
		return max(10, 20)
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationUntilLoop(t *testing.T) {
	code, out := compileAndRun(t, "until", `fn main() -> int {
		i := 0
		until i >= 5 {
			i = i + 1
		}
		return i
	}`)
	if code != 5 {
		t.Errorf("expected exit code 5, got %d (output: %q)", code, out)
	}
}

func TestIntegrationDefer(t *testing.T) {
	code, out := compileAndRun(t, "defer", `fn main() -> int {
		x := 0
		defer x = x + 10
		x = x + 1
		return x
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMatchExpression(t *testing.T) {
	code, out := compileAndRun(t, "match", `fn grade(score: int) -> string {
		return match score {
			90 => "A",
			80 => "B",
			_ => "F",
		}
	}

	fn main() -> int {
		g := grade(80)
		if g == "B" {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationEnum(t *testing.T) {
	code, out := compileAndRun(t, "enum", `enum Status {
		Idle,
		Running,
		Error
	}

	fn main() -> int {
		if Idle == 0 && Running == 1 && Error == 2 {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSlice(t *testing.T) {
	code, out := compileAndRun(t, "slice", `fn main() -> int {
		arr := [1, 2, 3, 4, 5]
		s := arr[1:4]
		return s[0] + s[1] + s[2]
	}`)
	if code != 9 {
		t.Errorf("expected exit code 9, got %d (output: %q)", code, out)
	}
}

func TestIntegrationConstBlock(t *testing.T) {
	code, out := compileAndRun(t, "const_block", `const {
		MAX = 100
		MIN = 10
	}

	fn main() -> int {
		return MAX + MIN
	}`)
	if code != 110 {
		t.Errorf("expected exit code 110, got %d (output: %q)", code, out)
	}
}

func TestIntegrationVarBlock(t *testing.T) {
	code, out := compileAndRun(t, "var_block", `fn main() -> int {
		var {
			a = 10
			b = 20
		}
		return a + b
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestIntegrationWithStmt(t *testing.T) {
	code, out := compileAndRun(t, "with", `fn main() -> int {
		with x = 42 {
			return x
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMapLiteral(t *testing.T) {
	code, out := compileAndRun(t, "map", `fn main() -> int {
		m := {"a": 1, "b": 2, "c": 3}
		return m["b"]
	}`)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAssert(t *testing.T) {
	code, out := compileAndRun(t, "assert", `fn main() -> int {
		assert(1 < 2)
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationComptimeStmt(t *testing.T) {
	code, out := compileAndRun(t, "comptime", `fn main() -> int {
		comptime {
			assert(1 + 1 == 2)
		}
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAppend(t *testing.T) {
	code, out := compileAndRun(t, "append", `fn main() -> int {
		arr := [1, 2, 3]
		arr = append(arr, 4)
		return arr[3]
	}`)
	if code != 4 {
		t.Errorf("expected exit code 4, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAsyncAwait(t *testing.T) {
	code, out := compileAndRun(t, "async", `fn add(a: int, b: int) -> int {
		return a + b
	}
	fn main() -> int {
		future := async add(1, 2)
		result := await future
		return result
	}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSpawnSelect(t *testing.T) {
	code, out := compileAndRun(t, "spawn", `fn helper() -> int {
		return 42
	}
	fn main() -> int {
		spawn helper()
		select {
			case true:
				return 10
			default:
				return 20
		}
	}`)
	if code != 10 {
		t.Errorf("expected exit code 10, got %d (output: %q)", code, out)
	}
}

func TestIntegrationChannelSendRecv(t *testing.T) {
	code, _ := compileAndRun(t, "chan_test", `fn main() -> int {
		ch := make(chan<int>)
		ch <- 42
		val := <-ch
		return val
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}
}

func TestIntegrationSpawnWithChannelArg(t *testing.T) {
	code, _ := compileAndRun(t, "spawn_chan", `fn worker(ch: chan<int>) -> int {
		ch <- 99
		return 0
	}
	fn main() -> int {
		ch := make(chan<int>)
		spawn worker(ch)
		val := <-ch
		return val
	}`)
	if code != 99 {
		t.Errorf("expected exit code 99, got %d", code)
	}
}

func TestIntegrationMixedIntFloatAdd(t *testing.T) {
	code, out := compileAndRun(t, "mix_add", `fn main() -> int {
		if 1 + 2.0 == 3.0 {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSizeofAlignof(t *testing.T) {
	code, out := compileAndRun(t, "sizeof", `fn main() -> int {
		return sizeof(int) + alignof(int)
	}`)
	if code != 8 {
		t.Errorf("expected exit code 8, got %d (output: %q)", code, out)
	}
}

func TestIntegrationForInRange(t *testing.T) {
	code, out := compileAndRun(t, "range", `fn main() -> int {
		sum := 0
		for i in 1..5 {
			sum = sum + i
		}
		return sum
	}`)
	if code != 10 {
		t.Errorf("expected exit code 10, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPointerArithmetic(t *testing.T) {
	code, out := compileAndRun(t, "ptr_arith", `fn main() -> int {
		arr := [10, 20, 30, 40, 50]
		p1 := &arr[0]
		p2 := p1 + 2
		return *p2
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMixedIntFloatMul(t *testing.T) {
	code, out := compileAndRun(t, "mix_mul", `fn main() -> int {
		if 3 * 2.0 == 6.0 {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationNestedForInRange(t *testing.T) {
	code, out := compileAndRun(t, "nested_range", `fn main() -> int {
		sum := 0
		for _ in 0..3 {
			for _ in 0..3 {
				sum = sum + 1
			}
		}
		return sum
	}`)
	if code != 9 {
		t.Errorf("expected exit code 9, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMapMissingKey(t *testing.T) {
	code, out := compileAndRun(t, "map_missing", `fn main() -> int {
		m := {"a": 1}
		return m["z"]
	}`)
	if code != 0 {
		t.Errorf("expected exit code 0 (missing key returns 0), got %d (output: %q)", code, out)
	}
}

func TestIntegrationAppendMultiple(t *testing.T) {
	code, out := compileAndRun(t, "append_multi", `fn main() -> int {
		arr := [1]
		arr = append(arr, 2)
		arr = append(arr, 3)
		return arr[2]
	}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationPointerSub(t *testing.T) {
	code, out := compileAndRun(t, "ptr_sub", `fn main() -> int {
		arr := [10, 20, 30]
		p := &arr[2]
		p2 := p - 1
		return *p2
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMultiFile(t *testing.T) {
	code, out := compileAndRunMulti(t, "multi", map[string]string{
		"main.skink": `module main
fn main() -> int {
	return add(10, 32)
}`,
		"helper.skink": `module helper
pub fn add(a: int, b: int) -> int {
	return a + b
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportResolution(t *testing.T) {
	code, out := compileAndRunResolved(t, "import_res", map[string]string{
		"main.skink": `module main
import "helper"
fn main() -> int {
	return add(10, 32)
}`,
		"helper.skink": `module helper
pub fn add(a: int, b: int) -> int {
	return a + b
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportBlock(t *testing.T) {
	code, out := compileAndRunResolved(t, "import_block", map[string]string{
		"main.skink": `module main
import { "helper", "utils" }
fn main() -> int {
	return add(10, 32) + double(5)
}`,
		"helper.skink": `module helper
pub fn add(a: int, b: int) -> int {
	return a + b
}`,
		"utils.skink": `module utils
pub fn double(x: int) -> int {
	return x * 2
}`,
	})
	if code != 52 {
		t.Errorf("expected exit code 52, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportResolutionPrivate(t *testing.T) {
	tmpDir := t.TempDir()
	mainPath := filepath.Join(tmpDir, "main.skink")
	helperPath := filepath.Join(tmpDir, "helper.skink")
	os.WriteFile(mainPath, []byte(`module main
import "helper"
fn main() -> int {
	return secret()
}`), 0644)
	os.WriteFile(helperPath, []byte(`module helper
fn secret() -> int {
	return 42
}`), 0644)

	prog, symbolInfo, err := resolver.Resolve([]string{mainPath})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)

	found := false
	for _, e := range checker.Errors() {
		if strings.Contains(e, "private symbol") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected private symbol access error, got errors: %v", checker.Errors())
	}
}

func TestIntegrationModuleAndImport(t *testing.T) {
	code, out := compileAndRun(t, "mod_import", `module main
import _ "fmt"
fn main() -> int {
	return 7}`)
	if code != 7 {
		t.Errorf("expected exit code 7, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStandardLibraryModules(t *testing.T) {
	// Set SKINK_HOME to workspace folder so it finds std
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolved(t, "stdlib_test", map[string]string{
		"main.skink": `module main
import "std/str"
import "std/math"
import "std/time"
import "std/json"
import "std/xml"
import "std/yaml"

fn main() -> int {
	// 1. Test Math
	print("1\n")
	s := math.Sqrt(16.0) // should be 4.0
	a := math.Abs(-42.0) // should be 42.0
	p := math.Pow(2.0, 3.0) // should be 8.0

	// 2. Test String Extensions
	print("2\n")
	lenVal := str.Len("Skink") // 5
	isEq := str.Equal("hello", "hello") // true
	hasHello := str.Contains("hello world", "world") // true

	// 3. Test Time
	print("3\n")
	nowVal := time.Timestamp()
	time.SleepMs(1) // small pause

	// 4. Test JSON marshalling and unmarshalling
	print("4\n")
	jsonKeys := ["name", "version"]
	jsonValues := ["Skink", "2"]
	marshalledJson := json.MarshalEntries(jsonKeys, jsonValues, 2)
	print("json marshalled: ")
	print(marshalledJson)
	print("\n")
	decodedJson, errJson := json.UnmarshalEntries(marshalledJson)
	print("decoded json length\n")

	// 5. Test XML marshalling and unmarshalling
	print("5\n")
	xmlKeys := ["item", "cost"]
	xmlValues := ["Widget", "42"]
	marshalledXml := xml.MarshalEntries("root", xmlKeys, xmlValues, 2)
	print("xml marshalled: ")
	print(marshalledXml)
	print("\n")
	decodedXml, errXml := xml.UnmarshalEntries(marshalledXml)
	print("decoded xml length\n")

	// 6. Test YAML marshalling and unmarshalling
	print("6\n")
	yamlKeys := ["greeting", "target"]
	yamlValues := ["hello", "world"]
	marshalledYaml := yaml.MarshalEntries(yamlKeys, yamlValues, 2)
	print("yaml marshalled: ")
	print(marshalledYaml)
	print("\n")
	decodedYaml, errYaml := yaml.UnmarshalEntries(marshalledYaml)
	print("decoded yaml length\n")

	print("check s\n")
	print("check isEq\n")
	print("check nowVal\n")

	if s == 4.0 && a == 42.0 && p == 8.0 && lenVal == 5 && isEq && hasHello {
		if nowVal > 0 {
			print("7\n")
			if errJson.message == "" && str.Equal(decodedJson[0].Key(), "name") && str.Equal(decodedJson[0].Value(), "Skink") {
				print("8\n")
				if errXml.message == "" && str.Equal(decodedXml[1].Key(), "cost") && str.Equal(decodedXml[1].Value(), "42") {
					print("9\n")
					if errYaml.message == "" && str.Equal(decodedYaml[0].Key(), "greeting") && str.Equal(decodedYaml[0].Value(), "hello") {
						print("10\n")
						return 42
					}
				}
			}
		}
	}
	return 0
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationExternFn(t *testing.T) {
	code, out := compileAndRun(t, "extern", `module main
extern fn putchar(c: int) -> int
fn main() -> int {
	putchar(65)
	return 0
}`)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d (output: %q)", code, out)
	}
}

func TestIntegrationReflectionAndHashMap(t *testing.T) {
	// Set SKINK_HOME to workspace folder so it finds std
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolved(t, "reflect_hashmap_test", map[string]string{
		"main.skink": `module main
import "std/reflect"
import "std/hashmap"
import "std/str"

struct Developer {
	name: string
	age: int
	salary: float
}

fn main() -> int {
	// 1. Test Reflection
	dev := Developer{name: "Alice", age: 30, salary: 120000.0}
	devType := reflect.TypeOf<Developer>(dev)

	print("Developer Type name: ")
	print(devType.name)
	print("\n")

	if !str.Equal(devType.name, "Developer") {
		return 1
	}

	if devType.numFields != 3 {
		return 2
	}

	// 2. Test Get/Set Field Values via reflection
	devPtr := int64(&dev)
	fName := devType.fields[0]
	fAge := devType.fields[1]
	fSalary := devType.fields[2]

	nameVal := reflect.GetString(devPtr, fName)
	if !str.Equal(nameVal, "Alice") {
		return 3
	}

	ageVal := reflect.GetInt(devPtr, fAge)
	if ageVal != 30 {
		return 4
	}

	salaryVal := reflect.GetFloat(devPtr, fSalary)
	if salaryVal != 120000.0 {
		return 5
	}

	reflect.SetString(devPtr, fName, "Bob")
	reflect.SetInt(devPtr, fAge, 31)
	reflect.SetFloat(devPtr, fSalary, 130000.0)

	if !str.Equal(dev.name, "Bob") {
		return 6
	}
	if dev.age != 31 {
		return 7
	}
	if dev.salary != 130000.0 {
		return 8
	}

	// 3. Test generic HashMap Put and Get
	m := hashmap.New<string, int>()
	m.Put("one", 101)
	m.Put("two", 202)
	m.Put("three", 303)

	v1, err1 := m.Get("one")
	if err1.message != "" || v1 != 101 {
		return 9
	}
	v2, err2 := m.Get("two")
	if err2.message != "" || v2 != 202 {
		return 10
	}
	v3, err3 := m.Get("three")
	if err3.message != "" || v3 != 303 {
		return 11
	}

	if !m.Has("two") {
		return 12
	}
	if m.Has("four") {
		return 13
	}

	return 42
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationGlobalVar(t *testing.T) {
	code, out := compileAndRun(t, "global_var", `module main
var x: int = 99
fn main() -> int {
	return x + 1
}`)
	if code != 100 {
		t.Errorf("expected exit code 100, got %d (output: %q)", code, out)
	}
}

func TestIntegrationTypeCast(t *testing.T) {
	code, out := compileAndRun(t, "cast", `fn main() -> int {
		f := 3.14
		i := int(f)
		return i
	}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationTypeCastFloat(t *testing.T) {
	code, out := compileAndRun(t, "cast_float", `fn main() -> int {
		f := float(5)
		return int(f * 2.0)
	}`)
	if code != 10 {
		t.Errorf("expected exit code 10, got %d (output: %q)", code, out)
	}
}

func TestIntegrationTypeCastBool(t *testing.T) {
	code, out := compileAndRun(t, "cast_bool", `fn main() -> int {
		b := true
		i := int(b)
		return i
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationFunctionPointer(t *testing.T) {
	code, out := compileAndRun(t, "fn_ptr", `fn add(a: int, b: int) -> int {
		return a + b
	}
	fn apply(f: fn(int, int) -> int, a: int, b: int) -> int {
		return f(a, b)
	}
	fn main() -> int {
		return apply(add, 10, 32)
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMixedIntFloatCmp(t *testing.T) {
	code, out := compileAndRun(t, "mix_cmp", `fn main() -> int {
		if 1 < 1.5 {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMixedIntFloatEq(t *testing.T) {
	code, out := compileAndRun(t, "mix_eq", `fn main() -> int {
		if 2 == 2.0 {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationDebugBuild(t *testing.T) {
	code, out := compileAndRunDebug(t, "debug_build", `fn main() -> int {
	return 42
}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationRealStructMethods(t *testing.T) {
	code, out := compileAndRun(t, "real_struct_methods", `struct Point {
		x: int
		y: int

		fn sum(self: Point) -> int {
			return self.x + self.y
		}

		fn scale(self: *Point, factor: int) {
			self.x = self.x * factor
			self.y = self.y * factor
		}
	}

	fn main() -> int {
		p := Point{x: 10, y: 32}
		p.scale(2)
		return p.sum()
	}`)
	if code != 84 {
		t.Errorf("expected exit code 84, got %d (output: %q)", code, out)
	}
}

func TestIntegrationGenericStructMethods(t *testing.T) {
	code, out := compileAndRun(t, "generic_struct_methods", `struct Box<T> {
		value: T

		fn get(self: Box<T>) -> T {
			return self.value
		}
	}

	fn main() -> int {
		b := Box<int>{value: 100}
		return b.get()
	}`)
	if code != 100 {
		t.Errorf("expected exit code 100, got %d (output: %q)", code, out)
	}
}

func TestIntegrationOsArgs(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")
	// Pass two command-line arguments; argc should be 3 (prog + 2 args).
	code, out := compileAndRunResolved(t, "os_args", map[string]string{
		"main.skink": `module main
import "std/os"

fn main() -> int {
	count := os.ArgsCount()
	if count < 3 {
		return 0
	}
	// argv[1] should be "hello" and argv[2] should be "world"
	if os.Args()[1] == "hello" && os.Args()[2] == "world" {
		return 42
	}
	return 0
}`,
	}, "hello", "world")
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSwitchStmt(t *testing.T) {
	code, out := compileAndRun(t, "switch_test", `fn main() -> int {
		x := 2
		switch x {
		case 1:
			return 10
		case 2, 3:
			return 20
		default:
			return 30
		}
		return 0
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSwitchStmtDefault(t *testing.T) {
	code, out := compileAndRun(t, "switch_default", `fn main() -> int {
		x := 99
		switch x {
		case 1:
			return 10
		case 2:
			return 20
		default:
			return 30
		}
		return 0
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSwitchStmtBreak(t *testing.T) {
	code, out := compileAndRun(t, "switch_break", `fn main() -> int {
		x := 2
		result := 0
		switch x {
		case 1:
			result = 10
			break
		case 2:
			result = 20
			break
		default:
			result = 30
			break
		}
		return result
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBuffer(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")
	code, out := compileAndRunResolved(t, "buf_test", map[string]string{
		"main.skink": `module main
import "std/buf"

fn main() -> int {
	b := buf.NewBuffer()
	b.WriteString("hello")
	b.WriteString(" ")
	b.WriteString("world")
	s := b.String()
	b.Free()
	if s == "hello world" {
		return 42
	}
	return 0
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBufferWriteInt(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")
	code, out := compileAndRunResolved(t, "buf_int_test", map[string]string{
		"main.skink": `module main
import "std/buf"

fn main() -> int {
	b := buf.NewBuffer()
	b.WriteInt(123)
	b.WriteByte(45) // '-'
	b.WriteInt(456)
	s := b.String()
	b.Free()
	if s == "123-456" {
		return 42
	}
	return 0
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMapStringString(t *testing.T) {
	code, out := compileAndRun(t, "map_string_string", `fn main() -> int {
		m := {"a": "one", "b": "two"}
		if m["b"] == "two" {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMapIntInt(t *testing.T) {
	code, out := compileAndRun(t, "map_int_int", `fn main() -> int {
		m := {1: 10, 2: 20, 3: 30}
		return m[2]
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationMapIntString(t *testing.T) {
	code, out := compileAndRun(t, "map_int_string", `fn main() -> int {
		m := {1: "one", 2: "two"}
		if m[2] == "two" {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSliceOmittedBounds(t *testing.T) {
	code, out := compileAndRun(t, "slice_omit", `fn main() -> int {
		arr := [10, 20, 30, 40, 50]
		// arr[..3] => elements 0,1,2 => [10,20,30]
		// For now, slices return pointers, so just test start offset
		s1 := arr[..3]
		if s1[0] == 10 && s1[1] == 20 && s1[2] == 30 {
			return 1
		}
		return 0
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSliceOmittedEnd(t *testing.T) {
	code, out := compileAndRun(t, "slice_omit_end", `fn main() -> int {
		arr := [10, 20, 30, 40, 50]
		// arr[2..] => elements 2,3,4 => [30,40,50]
		s := arr[2..]
		if s[0] == 30 && s[1] == 40 && s[2] == 50 {
			return 2
		}
		return 0
	}`)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSliceFullCopy(t *testing.T) {
	code, out := compileAndRun(t, "slice_full", `fn main() -> int {
		arr := [10, 20, 30]
		s := arr[..]
		if s[0] == 10 && s[1] == 20 && s[2] == 30 {
			return 3
		}
		return 0
	}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationFromEndIndex(t *testing.T) {
	code, out := compileAndRun(t, "from_end", `fn main() -> int {
		arr := [10, 20, 30, 40, 50]
		// arr[^1] should be last element = 50
		if arr[^1] == 50 {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBracketMapLiteral(t *testing.T) {
	code, out := compileAndRun(t, "bracket_map", `fn main() -> int {
		m := ["a": 1, "b": 2]
		if m["a"] == 1 && m["b"] == 2 {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSkinkTokenModule(t *testing.T) {
	code, out := compileAndRunResolved(t, "skink_token_test", map[string]string{
		"main.skink": `module main
import "token"

fn main() -> int {
	if token.LookupIdent("fn") != "FN" {
		return 1
	}
	if token.LookupIdent("struct") != "STRUCT" {
		return 2
	}
	if token.LookupIdent("return") != "RETURN" {
		return 3
	}
	if token.LookupIdent("myVar") != "IDENT" {
		return 4
	}
	if token.LookupIdent("switch") != "SWITCH" {
		return 5
	}
	return 42
}`,
		"token.skink": `module token
struct Token {
    Type: string
    Literal: string
    Line: int
    Column: int
}
const ILLEGAL = "ILLEGAL"
const EOF = "EOF"
const IDENT = "IDENT"
const INT = "INT"
const FLOAT = "FLOAT"
const STRING = "STRING"
const ASSIGN = "="
const PLUS = "+"
const MINUS = "-"
const FN = "FN"
const PUB = "PUB"
const STRUCT = "STRUCT"
const RETURN = "RETURN"
const SWITCH = "SWITCH"
const IF = "IF"
const ELSE = "ELSE"
const FOR = "FOR"
const VAR = "VAR"
const TRUE = "TRUE"
const FALSE = "FALSE"
const NIL = "NIL"
pub fn LookupIdent(ident: string) -> string {
    switch ident {
    case "fn": return FN
    case "struct": return STRUCT
    case "return": return RETURN
    case "switch": return SWITCH
    case "if": return IF
    case "else": return ELSE
    case "for": return FOR
    case "var": return VAR
    case "pub": return PUB
    case "true": return TRUE
    case "false": return FALSE
    case "nil": return NIL
    default: return IDENT
    }
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationArraySpread(t *testing.T) {
	code, out := compileAndRun(t, "array_spread", `fn main() -> int {
		a := [10, 20, 30]
		b := [..a, 40, 50]
		if b[0] == 10 && b[1] == 20 && b[2] == 30 && b[3] == 40 && b[4] == 50 {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationArraySpreadMultiple(t *testing.T) {
	code, out := compileAndRun(t, "array_spread_multi", `fn main() -> int {
		a := [1, 2]
		b := [3, 4]
		c := [..a, ..b, 5]
		if c[0] == 1 && c[1] == 2 && c[2] == 3 && c[3] == 4 && c[4] == 5 {
			return 42
		}
		return 0
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationReturnArray(t *testing.T) {
	code, out := compileAndRun(t, "return_array", `fn makeArr() -> []int {
		return [1, 2, 3]
	}
	fn main() -> int {
		arr := makeArr()
		return len(arr)
	}`)
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationReturnArrayAccess(t *testing.T) {
	code, out := compileAndRun(t, "return_array_access", `fn makeArr() -> []int {
		return [10, 20, 30]
	}
	fn main() -> int {
		arr := makeArr()
		return arr[1]
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestIntegrationRangeValue(t *testing.T) {
	code, out := compileAndRun(t, "range_value", `fn main() -> int {
		_ := 2..5
		// Range compiles as a { i32, i32 } aggregate.
		// For now, just verify it doesn't crash.
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationTemplateDecl(t *testing.T) {
	code, out := compileAndRun(t, "template_decl", `template Writer {
		fn Write(self: Writer, b: []byte)
	}

	fn main() -> int {
		return 42
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAnonymousFunction(t *testing.T) {
	code, out := compileAndRun(t, "anon_fn", `fn apply(f: fn(int, int) -> int, a: int, b: int) -> int {
		return f(a, b)
	}
	fn main() -> int {
		add := fn(a: int, b: int) -> int { return a + b }
		return apply(add, 10, 32)
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationAnonymousFunctionDirectCall(t *testing.T) {
	code, out := compileAndRun(t, "anon_fn_direct", `fn main() -> int {
		result := fn(x: int) -> int { return x * 2 }(21)
		return result
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationStructEmbedding(t *testing.T) {
	code, out := compileAndRun(t, "struct_embed", `struct Point {
		x: int
		y: int
	}
	struct LabelledPoint {
		Point
		label: string
	}
	fn main() -> int {
		p := LabelledPoint{x: 3, y: 4, label: "A"}
		return p.x + p.y
	}`)
	if code != 7 {
		t.Errorf("expected exit code 7, got %d (output: %q)", code, out)
	}
}

func TestIntegrationVariadicFunction(t *testing.T) {
	code, out := compileAndRun(t, "variadic", `fn add(a: int, b: ...int) -> int {
		return a
	}
	fn main() -> int {
		return add(42)
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestTemplateMethodDispatch(t *testing.T) {
	code, out := compileAndRun(t, "template_dispatch", `
	template Adder {
		fn Add(self: Adder, a: int, b: int) -> int
	}

	struct SimpleAdder {
		offset: int
	}

	fn SimpleAdder.Add(self: SimpleAdder, a: int, b: int) -> int {
		return a + b + self.offset
	}

	fn useAdder(a: Adder, x: int, y: int) -> int {
		return a.Add(x, y)
	}

	fn main() -> int {
		adder := SimpleAdder{offset: 10}
		return useAdder(adder, 5, 7)
	}`)
	if code != 22 {
		t.Errorf("expected exit code 22, got %d (output: %q)", code, out)
	}
}

func TestBracketMapLiteral(t *testing.T) {
	code, out := compileAndRun(t, "bracket_map", `
	fn main() -> int {
		m := ["a": 10, "b": 20]
		return m["b"]
	}`)
	if code != 20 {
		t.Errorf("expected exit code 20, got %d (output: %q)", code, out)
	}
}

func TestSpreadOperator(t *testing.T) {
	code, out := compileAndRun(t, "spread", `
	fn main() -> int {
		a := [1, 2]
		b := [..a, 3, 4]
		return b[3]
	}`)
	if code != 4 {
		t.Errorf("expected exit code 4, got %d (output: %q)", code, out)
	}
}

func TestFromEndIndex(t *testing.T) {
	code, out := compileAndRun(t, "from_end", `
	fn main() -> int {
		arr := [10, 20, 30, 40]
		return arr[^1]
	}`)
	if code != 40 {
		t.Errorf("expected exit code 40, got %d (output: %q)", code, out)
	}
}

func TestOmittedSliceBounds(t *testing.T) {
	code, out := compileAndRun(t, "slice_bounds", `
	fn main() -> int {
		arr := [10, 20, 30, 40, 50]
		fromTwo := arr[2..]
		return fromTwo[0]
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestLinqQueryMap(t *testing.T) {
	code, out := compileAndRun(t, "linq_map", `
	fn main() -> int {
		arr := [1, 2, 3, 4, 5]
		result := from x in arr select x * 10
		return result[2]
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestLinqQueryFilter(t *testing.T) {
	code, out := compileAndRun(t, "linq_filter", `
	fn main() -> int {
		arr := [1, 2, 3, 4, 5]
		result := from x in arr where x > 2 select x * 10
		return result[0]
	}`)
	if code != 30 {
		t.Errorf("expected exit code 30, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSyncBasic(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolvedWithObjects(t, "sync_basic", map[string]string{
		"main.skink": `module main
import "std/sync"

fn noop() {}

fn main() -> int {
	// Mutex
	mu := sync.NewMutex()
	mu.Lock()
	mu.Unlock()
	mu.Free()

	// Atomics
	val := 10
	if sync.AtomicAdd(&val, 5) != 15 { return 1 }
	if sync.AtomicLoad(&val) != 15 { return 2 }
	if sync.AtomicSub(&val, 3) != 12 { return 3 }
	old := sync.AtomicExchange(&val, 99)
	if old != 12 { return 4 }
	if sync.AtomicLoad(&val) != 99 { return 5 }

	exp := 99
	ok := sync.AtomicCAS(&val, &exp, 42)
	if ok == false { return 6 }
	if sync.AtomicLoad(&val) != 42 { return 7 }

	// RWMutex
	rw := sync.NewRWMutex()
	rw.RLock()
	rw.RUnlock()
	if rw.TryRLock() == false { return 8 }
	rw.RUnlock()
	rw.Lock()
	if rw.TryRLock() == true { return 9 }
	rw.Unlock()
	rw.Free()

	// Once
	o := sync.NewOnce()
	o.Do(noop)
	o.Do(noop)

	// Cond
	mu2 := sync.NewMutex()
	cv := sync.NewCond(mu2)
	cv.Free()
	mu2.Free()

	return 42
}`,
	}, []string{})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationSyncConcurrentMutex(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolved(t, "sync_concurrent", map[string]string{
		"main.skink": `module main
import "std/sync"

fn worker(ch: chan<int>) -> int {
	ch <- 1
	return 0
}

fn main() -> int {
	mu := sync.NewMutex()
	ch := make(chan<int>)

	mu.Lock()
	spawn worker(ch)
	val := <-ch
	mu.Unlock()
	mu.Free()

	return val
}`,
	})
	if code != 1 {
		t.Errorf("expected exit code 1, got %d (output: %q)", code, out)
	}
}

func TestIntegrationBufferedChannel(t *testing.T) {
	code, _ := compileAndRun(t, "buf_chan", `fn main() -> int {
		ch := make(chan<int>, 5)
		ch <- 1
		ch <- 2
		ch <- 3
		return <-ch
	}`)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestIntegrationWaitGroup(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolvedWithObjects(t, "wg_test", map[string]string{
		"main.skink": `module main
import "std/waitgroup"

fn main() -> int {
	wg := waitgroup.New()
	wg.Add(2)
	wg.Done()
	wg.Done()
	// Multiple Wait calls should all return immediately when count is 0
	wg.Wait()
	wg.Wait()
	return 42
}`,
	}, []string{})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationDBSQLite(t *testing.T) {
	// Skip if libsqlite3 is not installed on the system.
	if _, err := exec.LookPath("pkg-config"); err == nil {
		cmd := exec.Command("pkg-config", "--exists", "sqlite3")
		if err := cmd.Run(); err != nil {
			t.Skip("sqlite3 not installed, skipping DB integration test")
		}
	} else {
		// Fallback: try to link a trivial program with -lsqlite3.
		tmp := t.TempDir()
		testC := filepath.Join(tmp, "test.c")
		os.WriteFile(testC, []byte("int main(){}"), 0644)
		if err := exec.Command("cc", testC, "-lsqlite3", "-o", filepath.Join(tmp, "test")).Run(); err != nil {
			t.Skip("sqlite3 not installed, skipping DB integration test")
		}
	}

	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolved(t, "db_sqlite", map[string]string{
		"main.skink": `module main
import "std/db"

fn main() -> int {
	conn, err := db.Open("sqlite3", ":memory:")
	if err.message != "" {
		return 1
	}
	if conn.handle == int64(0) {
		return 2
	}
	errClose := conn.Close()
	if errClose.message != "" {
		return 3
	}
	return 42
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationTypeAliasPrimitive(t *testing.T) {
	code, out := compileAndRun(t, "type_alias_prim", `type MyInt = int
	fn main() -> int {
		var x: MyInt = 42
		return x
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationTypeAliasFunction(t *testing.T) {
	code, out := compileAndRun(t, "type_alias_fn", `type IntFn = fn(int) -> int
	fn apply(f: IntFn, x: int) -> int {
		return f(x)
	}
	fn main() -> int {
		return apply(fn(x: int) -> int { return x * 2 }, 21)
	}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationWebTemplate(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")
	code, out := compileAndRunResolved(t, "web_template_test", map[string]string{
		"main.skink": `module main
import "std/web/tmpl"
import "std/str"

fn main() -> int {
	t, err := tmpl.Parse("Hello, {{.Name}}!")
	if err.message != "" {
		return 1
	}
	data := make(map[string]string)
	data["Name"] = "Skink"
	result, err2 := tmpl.Execute(t, data)
	if err2.message != "" {
		return 2
	}
	if result == "Hello, Skink!" {
		return 42
	}
	return 3
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationWebRouter(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")
	code, out := compileAndRunResolved(t, "web_router_test", map[string]string{
		"main.skink": `module main
import "std/web/router"
import "std/web/types"
import "std/str"

fn main() -> int {
	r := router.NewRouter()
	called := false
	r.Get("/hello", fn(req: types.Request, w: *types.ResponseWriter) {
		called = true
		w.WriteHeader(200)
		w.Write("hi")
	})

	// Simulate a request
	req := types.Request{method: "GET", path: "/hello", query: "", headers: make(map[string]string), body: ""}
	w := types.ResponseWriter{status: 200, headers: make(map[string]string), body: ""}
	r.ServeHTTP(req, &w)

	if !called {
		return 1
	}
	if w.Status() != 200 {
		return 2
	}
	return 42
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportAlias(t *testing.T) {
	code, out := compileAndRunResolved(t, "import_alias",
		map[string]string{
			"helpers.skink": `module helpers

pub fn Add(a: int, b: int) -> int {
	return a + b
}
`,
			"main.skink": `module main
import "helpers" as h

fn main() -> int {
	return h.Add(2, 3)
}
`,
		})
	if code != 5 {
		t.Errorf("expected exit code 5, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportAliasBlock(t *testing.T) {
	code, out := compileAndRunResolved(t, "import_alias_block",
		map[string]string{
			"foo.skink": `module foo
pub fn Foo() -> int { return 1 }
`,
			"bar.skink": `module bar
pub fn Bar() -> int { return 2 }
`,
			"main.skink": `module main
import {
	"foo" as f,
	"bar" as b
}

fn main() -> int {
	return f.Foo() + b.Bar()
}
`,
		})
	if code != 3 {
		t.Errorf("expected exit code 3, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportAliasLegacy(t *testing.T) {
	code, out := compileAndRunResolved(t, "import_alias_legacy",
		map[string]string{
			"helpers.skink": `module helpers

pub fn Add(a: int, b: int) -> int {
	return a + b
}
`,
			"main.skink": `module main
import h "helpers"

fn main() -> int {
	return h.Add(2, 3)
}
`,
		})
	if code != 5 {
		t.Errorf("expected exit code 5, got %d (output: %q)", code, out)
	}
}

func TestIntegrationImportAliasDuplicateError(t *testing.T) {
	tmpDir := t.TempDir()
	inputs := map[string]string{
		"helpers.skink": `module helpers
pub fn Add(a: int, b: int) -> int { return a + b }
`,
		"main.skink": `module main
import "helpers" as h
import "helpers" as h

fn main() -> int { return 0 }
`,
	}
	var srcPaths []string
	for fname, input := range inputs {
		srcPath := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(srcPath, []byte(input), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		srcPaths = append(srcPaths, srcPath)
	}
	prog, symbolInfo, err := resolver.Resolve(srcPaths)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	prog = ast.Monomorphize(prog)
	for _, decl := range prog.Declarations {
		name := resolver.DeclName(decl)
		if name != "" {
			if _, exists := symbolInfo[name]; !exists {
				module := ""
				pub := true
				if idx := strings.Index(name, "_"); idx != -1 {
					baseName := name[:idx]
					if info, ok := symbolInfo[baseName]; ok {
						module = info.Module
						pub = info.Pub
					}
				}
				symbolInfo[name] = types.SymbolInfo{Module: module, Pub: pub}
			}
		}
	}
	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)
	if len(checker.Errors()) == 0 {
		t.Errorf("expected duplicate import alias error, got none")
	} else {
		found := false
		for _, e := range checker.Errors() {
			if strings.Contains(e, "duplicate import alias") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected duplicate import alias error, got: %v", checker.Errors())
		}
	}
}
