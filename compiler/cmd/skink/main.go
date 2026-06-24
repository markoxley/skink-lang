// Copyright 2026 Mark Oxley Oxley
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

// skink is the command-line compiler for the Skink programming language.
//
// Usage:
//
//	skink [flags] <file.skink> [...]   compile and build
//	skink -lex  file.skink            print token stream
//	skink -ast  file.skink            print AST
//	skink -check file.skink           type-check only
//	skink -emit-ll file.skink         emit LLVM IR
//	skink -c file.skink               compile to object file
//	skink test [pattern]              run test files matching pattern
//	skink get                         fetch dependencies
//
// The compiler pipeline is: lex -> parse -> resolve imports -> monomorphize
// generics -> type check -> monomorphize templates -> codegen -> llc -> gcc.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/codegen"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
	"github.com/skink-lang/compiler/pkgmanager"
	"github.com/skink-lang/compiler/resolver"
	"github.com/skink-lang/compiler/types"
)

const version = "0.1.5"

func dependencyVersion(name string, pkgNames []string, fallback ...string) string {
	for _, pkg := range pkgNames {
		if out, err := exec.Command("pkg-config", "--modversion", pkg).Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	for _, cmd := range fallback {
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			continue
		}
		out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
		if err != nil && len(out) == 0 {
			continue
		}
		s := string(out)
		switch name {
		case "sqlite3":
			if v := strings.Fields(s); len(v) > 0 {
				return v[0]
			}
		case "openssl":
			if i := strings.Index(s, "OpenSSL"); i >= 0 {
				rest := strings.TrimSpace(s[i+len("OpenSSL"):])
				if v := strings.Fields(rest); len(v) > 0 {
					return v[0]
				}
			}
		case "mosquitto":
			s = strings.ToLower(s)
			if i := strings.Index(s, "version"); i >= 0 {
				rest := strings.TrimSpace(s[i+len("version"):])
				if v := strings.Fields(rest); len(v) > 0 {
					return v[0]
				}
			}
		}
	}
	return "not found"
}

func main() {
	var (
		versionFlag = flag.Bool("version", false, "print version")
		lexFlag     = flag.Bool("lex", false, "print tokens (lex only)")
		astFlag     = flag.Bool("ast", false, "print AST (parse only)")
		checkFlag   = flag.Bool("check", false, "type check only")
		emitLLFlag  = flag.Bool("emit-ll", false, "emit LLVM IR (.ll)")
		compileFlag = flag.Bool("c", false, "compile only (produce .o)")
		outFlag     = flag.String("o", "", "output file name")
		extraObjStr = flag.String("extra-obj", "", "comma-separated list of extra .o files to link")
	)
	flag.Parse()

	if flag.NArg() >= 1 && flag.Arg(0) == "test" {
		pattern := "*_test.skink"
		if flag.NArg() >= 2 {
			pattern = flag.Arg(1) + "_test.skink"
		}
		runTests(pattern)
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "get" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error getting working directory: %v\n", err)
			os.Exit(1)
		}
		if err := pkgmanager.FetchAll(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "get error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *versionFlag {
		fmt.Printf("skink %s\n", version)
		fmt.Printf("  go: %s\n", runtime.Version())
		if out, err := exec.Command("llc", "--version").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if i := strings.Index(line, "LLVM version"); i >= 0 {
					ver := strings.TrimSpace(line[i+len("LLVM version"):])
					fmt.Printf("  llvm: %s\n", ver)
					break
				}
			}
		} else {
			fmt.Println("  llvm: not found")
		}
		fmt.Printf("  sqlite3: %s\n", dependencyVersion("sqlite3", []string{"sqlite3"}, "sqlite3 --version"))
		fmt.Printf("  llama.cpp: %s\n", dependencyVersion("llama", []string{"llama", "llama-cpp"}))
		fmt.Printf("  mosquitto: %s\n", dependencyVersion("mosquitto", []string{"libmosquitto"}, "mosquitto -h"))
		fmt.Printf("  openssl: %s\n", dependencyVersion("openssl", []string{"openssl"}, "openssl version"))
		fmt.Printf("  zlib: %s\n", dependencyVersion("zlib", []string{"zlib", "zlib-ng", "zlib-ng-compat"}))
		return
	}

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: skink [flags] <file.skink> [file2.skink ...]\n")
		fmt.Fprintf(os.Stderr, "       skink get\n")
		fmt.Fprintf(os.Stderr, "       skink test [pattern]\n")
		os.Exit(1)
	}

	paths := flag.Args()
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		if err == nil {
			paths[i] = abs
		}
	}

	if *lexFlag {
		input, err := os.ReadFile(paths[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", paths[0], err)
			os.Exit(1)
		}
		lexOnly(string(input))
		return
	}

	// Parse all source files, resolve imports, and merge into a single program.
	program, symbolInfo, err := resolver.Resolve(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Monomorphize generics before type-checking.
	program = ast.Monomorphize(program)

	// Update symbolInfo with any newly monomorphized declarations.
	for _, decl := range program.Declarations {
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
					} else {
						// Try to find a module-qualified baseline like "reflect.TypeOf"
						for key, info := range symbolInfo {
							if strings.HasSuffix(key, "."+baseName) {
								module = info.Module
								pub = info.Pub
								break
							}
						}
					}
				}
				symbolInfo[name] = types.SymbolInfo{
					Module: module,
					Pub:    pub,
				}
			}
		}
	}

	if *astFlag {
		printAST(program)
		return
	}

	if *checkFlag {
		checker := types.NewChecker()
		checker.SetSymbolInfo(symbolInfo)
		checker.CheckProgram(program)
		if len(checker.Errors()) > 0 {
			for _, err := range checker.Errors() {
				fmt.Fprintf(os.Stderr, "type error: %s\n", err)
			}
			os.Exit(1)
		}
		fmt.Println("type check passed")
		return
	}

	// Type-check before codegen.
	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(program)
	if len(checker.Errors()) > 0 {
		for _, err := range checker.Errors() {
			fmt.Fprintf(os.Stderr, "type error: %s\n", err)
		}
		os.Exit(1)
	}

	// Monomorphize templates before codegen.
	program = ast.TemplateMonomorphize(program)

	var extraObjs []string
	if *extraObjStr != "" {
		extraObjs = strings.Split(*extraObjStr, ",")
	}
	opts := codegen.BuildOptions{
		EmitLL:       *emitLLFlag,
		CompileOnly:  *compileFlag,
		OutputPath:   *outFlag,
		ExtraObjects: extraObjs,
	}

	artifact, err := codegen.Build(program, paths[0], opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build error: %v\n", err)
		os.Exit(1)
	}

	if *emitLLFlag {
		fmt.Printf("wrote %s\n", artifact)
	} else if *compileFlag {
		fmt.Printf("compiled %s\n", artifact)
	} else {
		fmt.Printf("built %s\n", artifact)
	}
}

// lexOnly runs the lexer and prints every token.
func lexOnly(input string) {
	l := lexer.New(input)
	for {
		tok := l.NextToken()
		fmt.Printf("%4d:%-3d %-12s %q\n", tok.Line, tok.Column, tok.Type, tok.Literal)
		if tok.Type == "EOF" {
			break
		}
	}
}

// printAST prints all declarations in the program.
func printAST(program *ast.Program) {
	for _, decl := range program.Declarations {
		fmt.Printf("%s\n", decl.String())
	}
}

// runTests discovers and runs test files matching the given pattern.
func runTests(pattern string) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "glob error: %v\n", err)
		os.Exit(1)
	}
	if len(matches) == 0 {
		fmt.Println("no tests found")
		return
	}

	passed := 0
	failed := 0

	for _, testFile := range matches {
		// Parse test file to discover module name and test functions.
		input, err := os.ReadFile(testFile)
		if err != nil {
			fmt.Printf("FAIL %s (read: %v)\n", testFile, err)
			failed++
			continue
		}

		l := lexer.New(string(input))
		p := parser.New(l)
		prog := p.ParseProgram()
		if len(p.Errors()) > 0 {
			fmt.Printf("FAIL %s (parse: %v)\n", testFile, p.Errors())
			failed++
			continue
		}

		var moduleName string
		var testNames []string
		for _, decl := range prog.Declarations {
			if mod, ok := decl.(*ast.ModuleDecl); ok {
				moduleName = mod.Name
			}
			if fn, ok := decl.(*ast.FnDecl); ok && strings.HasPrefix(fn.Name, "Test") {
				testNames = append(testNames, fn.Name)
			}
		}

		if moduleName == "" {
			moduleName = strings.TrimSuffix(filepath.Base(testFile), "_test.skink")
		}

		if len(testNames) == 0 {
			fmt.Printf("SKIP %s (no Test functions)\n", testFile)
			continue
		}

		// Copy all .skink and .h files from current dir to temp dir.
		cwd, _ := os.Getwd()

		// Create temp directory and copy project files so imports resolve.
		tmpDir, err := os.MkdirTemp(cwd, ".skink-test-")
		if err != nil {
			fmt.Printf("FAIL %s (tmpdir: %v)\n", testFile, err)
			failed++
			continue
		}
		// Copy the test file itself to tmpDir
		testData, _ := os.ReadFile(testFile)
		testPathInTmp := filepath.Join(tmpDir, filepath.Base(testFile))
		os.WriteFile(testPathInTmp, testData, 0644)
		// Copy all other .skink and .h files from the test file's directory
		testDir := filepath.Dir(testFile)
		entries, _ := os.ReadDir(testDir)
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".skink") || strings.HasSuffix(name, ".h") || strings.HasSuffix(name, ".c") {
				src := filepath.Join(testDir, name)
				dst := filepath.Join(tmpDir, name)
				data, _ := os.ReadFile(src)
				os.WriteFile(dst, data, 0644)
			}
		}

		// Generate wrapper main.
		mainSrc := generateTestMain(moduleName, testNames)
		mainPath := filepath.Join(tmpDir, "main.skink")
		if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
			fmt.Printf("FAIL %s (write main: %v)\n", testFile, err)
			os.RemoveAll(tmpDir)
			failed++
			continue
		}

		// Point SKINK_HOME at tmpDir so the test module can be found.
		// But also copy lib/testing.skink to tmpDir so it's available.
		libDir := findLibDir()
		oldHome := os.Getenv("SKINK_HOME")
		os.Setenv("SKINK_HOME", tmpDir)
		// Copy testing.skink from lib to tmpDir
		testingSrc := filepath.Join(libDir, "testing.skink")
		testingDst := filepath.Join(tmpDir, "testing.skink")
		if data, err := os.ReadFile(testingSrc); err == nil {
			os.WriteFile(testingDst, data, 0644)
		}

		// Resolve, type-check, build.
		paths := []string{mainPath}
		program, symbolInfo, err := resolver.Resolve(paths)
		if err != nil {
			fmt.Printf("FAIL %s (resolve: %v)\n", testFile, err)
			os.Setenv("SKINK_HOME", oldHome)
			os.RemoveAll(tmpDir)
			failed++
			continue
		}

		// Monomorphize generics before type-checking.
		program = ast.Monomorphize(program)

		// Update symbolInfo with any newly monomorphized declarations.
		for _, decl := range program.Declarations {
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
						} else {
							for key, info := range symbolInfo {
								if strings.HasSuffix(key, "."+baseName) {
									module = info.Module
									pub = info.Pub
									break
								}
							}
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
		checker.CheckProgram(program)
		if len(checker.Errors()) > 0 {
			fmt.Printf("FAIL %s (type: %v)\n", testFile, checker.Errors())
			os.Setenv("SKINK_HOME", oldHome)
			os.RemoveAll(tmpDir)
			failed++
			continue
		}

		binPath := filepath.Join(tmpDir, "testbin")
		artifact, err := codegen.Build(program, mainPath, codegen.BuildOptions{OutputPath: binPath})
		if err != nil {
			fmt.Printf("FAIL %s (build: %v)\n", testFile, err)
			os.Setenv("SKINK_HOME", oldHome)
			os.RemoveAll(tmpDir)
			failed++
			continue
		}
		os.Setenv("SKINK_HOME", oldHome)

		// Run binary.
		cmd := exec.Command(artifact)
		out, _ := cmd.CombinedOutput()
		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		os.RemoveAll(tmpDir)

		if exitCode == 0 {
			fmt.Printf("PASS %s\n", testFile)
			passed++
		} else {
			fmt.Printf("FAIL %s\n", testFile)
			if len(out) > 0 {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					fmt.Printf("    %s\n", line)
				}
			}
			failed++
		}
	}

	fmt.Printf("\n%d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// findLibDir searches for the compiler's lib/ directory.
func findLibDir() string {
	// 1. SKINK_HOME env var.
	if home := os.Getenv("SKINK_HOME"); home != "" {
		if hasTesting(home) {
			return home
		}
	}
	// 2. Next to the executable.
	exe, _ := os.Executable()
	if dir := filepath.Join(filepath.Dir(exe), "lib"); hasTesting(dir) {
		return dir
	}
	// 3. In current working directory.
	if cwd, _ := os.Getwd(); cwd != "" {
		if dir := filepath.Join(cwd, "lib"); hasTesting(dir) {
			return dir
		}
	}
	// 4. Relative to this source file (works when binary is built from source).
	_, srcFile, _, _ := runtime.Caller(0)
	if dir := filepath.Join(filepath.Dir(srcFile), "..", "..", "lib"); hasTesting(dir) {
		return dir
	}
	// 5. Hard-coded fallback for repo layout.
	if dir := "/home/mark/Documents/Projects/skink-lang/skink/compiler/lib"; hasTesting(dir) {
		return dir
	}
	return ""
}

// hasTesting reports whether dir contains a testing.skink file.
func hasTesting(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "testing.skink"))
	return err == nil
}

// generateTestMain creates a Skink source file that runs all Test* functions.
func generateTestMain(moduleName string, testNames []string) string {
	var b strings.Builder
	b.WriteString("module main\n")
	b.WriteString("import \"testing\"\n")
	fmt.Fprintf(&b, "import \"%s\"\n", moduleName)
	b.WriteString("\n")
	b.WriteString("fn main() -> int {\n")
	b.WriteString("    passed := true\n")
	for _, name := range testNames {
		b.WriteString("    {\n")
		b.WriteString("        Reset()\n")
		fmt.Fprintf(&b, "        %s.%s()\n", moduleName, name)
		b.WriteString("        if Failed() {\n")
		b.WriteString("            passed = false\n")
		b.WriteString("        }\n")
		b.WriteString("    }\n")
	}
	b.WriteString("    if passed {\n")
	b.WriteString("        return 0\n")
	b.WriteString("    }\n")
	b.WriteString("    return 1\n")
	b.WriteString("}\n")
	return b.String()
}
