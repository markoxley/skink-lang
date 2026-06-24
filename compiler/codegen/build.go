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

package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/skink-lang/compiler/ast"
)

// BuildOptions controls the compilation pipeline.
type BuildOptions struct {
	// EmitLL stops after generating .ll IR text.
	EmitLL bool
	// CompileOnly stops after generating .o object file.
	CompileOnly bool
	// OutputPath is the desired executable name (default: source base name).
	OutputPath string
	// SaveTemps keeps intermediate .ll and .o files.
	SaveTemps bool
	// ExtraObjects are additional .o files to link into the final executable.
	ExtraObjects []string
	// Debug enables DWARF debug info emission for LLDB / GDB.
	Debug bool
	// SourcePath is the original source file path (used for debug info).
	SourcePath string
}

// Build runs the three-phase compile pipeline for a Skink program:
//
//	Phase 1:  AST  ->  .ll   (LLVM IR text, via Codegen)
//	Phase 2:  .ll  ->  .o    (object file, via llc)
//	Phase 3:  .o   ->  bin  (native executable, via clang/cc/gcc)
//
// BuildOptions can stop the pipeline early (-emit-ll stops after Phase 1,
// -c stops after Phase 2).
//
// The sourcePath is used only to derive the default output file names.
// On success the path to the final artifact is returned.
func Build(program *ast.Program, sourcePath string, opts BuildOptions) (string, error) {
	// Validate that a main() function exists.
	hasMain := false
	for _, decl := range program.Declarations {
		if fn, ok := decl.(*ast.FnDecl); ok && fn.Name == "main" {
			hasMain = true
			break
		}
	}
	if !hasMain {
		return "", fmt.Errorf("no main() function found — entry point is required")
	}

	base := filepath.Base(sourcePath)
	if ext := filepath.Ext(base); ext == ".skink" {
		base = base[:len(base)-len(ext)]
	}

	outPath := opts.OutputPath
	if outPath == "" {
		outPath = base
	}
	outDir := filepath.Dir(outPath)
	if outDir == "" {
		outDir = "."
	}
	llPath := filepath.Join(outDir, base+".ll")
	objPath := filepath.Join(outDir, base+".o")

	// Always clean up intermediates unless requested to keep them.
	if !opts.SaveTemps {
		if !opts.EmitLL {
			defer os.Remove(llPath)
		}
		defer os.Remove(objPath)
	}

	// Monomorphize generics before codegen.
	program = ast.Monomorphize(program)
	// Monomorphize templates before codegen.
	program = ast.TemplateMonomorphize(program)

	// Phase 1: emit LLVM IR.
	cg := New()
	if opts.Debug {
		srcPath := opts.SourcePath
		if srcPath == "" {
			srcPath = sourcePath
		}
		cg.SetDebug(srcPath)
	}
	cg.EmitProgram(program)
	if errs := cg.Errors(); len(errs) > 0 {
		return "", fmt.Errorf("codegen errors: %v", errs)
	}
	// Print IR for debugging standard library test if env var is set
	if os.Getenv("DEBUG_STDLIB_IR") == "1" {
		fmt.Printf("--- GENERATED LLVM IR ---\n%s\n--- END GENERATED LLVM IR ---\n", cg.String())
	}
	if err := os.WriteFile(llPath, []byte(cg.String()), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", llPath, err)
	}
	if opts.EmitLL {
		return llPath, nil
	}

	// Phase 2a: run LLVM optimization pass to eliminate allocas in loops.
	optPath := llPath
	if !opts.EmitLL {
		optPath = filepath.Join(outDir, base+"_opt.ll")
		if !opts.SaveTemps {
			defer os.Remove(optPath)
		}
		optArgs := []string{"-O1", "-S", "-o", optPath, llPath}
		if err := runCommand("opt", optArgs...); err != nil {
			found := false
			for _, suffix := range []string{"15", "14", "13", "12", "11", "10"} {
				if runCommand("opt-"+suffix, optArgs...) == nil {
					found = true
					break
				}
			}
			if !found {
				// opt not available; fall back to unoptimized IR.
				optPath = llPath
			}
		}
	}

	// Phase 2b: compile optimized .ll to .o via llc.
	llcArgs := []string{"-filetype=obj", "-relocation-model=pic", "-o", objPath, optPath}
	if err := runCommand("llc", llcArgs...); err != nil {
		// Try llc-15, llc-14, etc.
		found := false
		for _, suffix := range []string{"15", "14", "13", "12", "11", "10"} {
			if runCommand("llc-"+suffix, llcArgs...) == nil {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("llc failed: %w (is LLVM installed?)", err)
		}
	}
	if opts.CompileOnly {
		return objPath, nil
	}

	// Phase 3: link .o to executable via clang or cc.
	linker := pickLinker()
	if linker == "" {
		return "", fmt.Errorf("no C linker found (tried clang, cc, gcc)")
	}
	linkArgs := []string{objPath}
	linkArgs = append(linkArgs, opts.ExtraObjects...)
	// Always link the ARC runtime.
	_, buildFile, _, _ := runtime.Caller(0)
	codegenDir := filepath.Dir(buildFile)
	rcRtPath := filepath.Join(codegenDir, "..", "runtime", "rc_rt.c")
	if _, err := os.Stat(rcRtPath); err == nil {
		linkArgs = append(linkArgs, rcRtPath)
	}
	linkArgs = append(linkArgs, "-o", outPath, "-lm", "-lpthread")
	// Scan imports for modules that need extra linker libraries.
	for _, decl := range program.Declarations {
		switch d := decl.(type) {
		case *ast.ImportDecl:
			if d.Path == "std/db" {
				if p := resolveLibPath("sqlite3"); p != "" {
					linkArgs = append(linkArgs, p)
				} else {
					linkArgs = append(linkArgs, "-lsqlite3")
				}
			}
			if d.Path == "std/llm" {
				// Prefer system-installed llama.cpp libraries; fall back to bundled llama-dist.
				llamaPath := resolveLibPath("llama")
				ggmlPath := resolveLibPath("ggml")
				ggmlBasePath := resolveLibPath("ggml-base")
				ggmlCpuPath := resolveLibPath("ggml-cpu")
				if llamaPath != "" && ggmlPath != "" && ggmlBasePath != "" && ggmlCpuPath != "" {
					linkArgs = append(linkArgs, llamaPath, ggmlPath, ggmlBasePath, ggmlCpuPath, "-lstdc++")
				} else {
					_, buildFile, _, _ := runtime.Caller(0)
					codegenDir := filepath.Dir(buildFile)
					llamaLibDir := filepath.Join(codegenDir, "..", "llama-dist", "lib")
					linkArgs = append(linkArgs, "-L"+llamaLibDir, "-Wl,-rpath,"+llamaLibDir)
					linkArgs = append(linkArgs, "-lllama", "-lggml", "-lggml-base", "-lggml-cpu", "-lstdc++")
				}
			}
			if d.Path == "std/mqtt" {
				if p := resolveLibPath("mosquitto"); p != "" {
					linkArgs = append(linkArgs, p)
				}
			}
			if d.Path == "std/crypto" {
				for _, lib := range []string{"ssl", "crypto"} {
					if p := resolveLibPath(lib); p != "" {
						linkArgs = append(linkArgs, p)
					} else {
						linkArgs = append(linkArgs, "-l"+lib)
					}
				}
			}
			if d.Path == "std/compress" {
				if p := resolveLibPath("z"); p != "" {
					linkArgs = append(linkArgs, p)
				} else {
					linkArgs = append(linkArgs, "-lz")
				}
			}
		case *ast.ImportBlockDecl:
			for _, imp := range d.Decls {
				if imp.Path == "std/db" {
					if p := resolveLibPath("sqlite3"); p != "" {
						linkArgs = append(linkArgs, p)
					} else {
						linkArgs = append(linkArgs, "-lsqlite3")
					}
				}
				if imp.Path == "std/llm" {
					llamaPath := resolveLibPath("llama")
					ggmlPath := resolveLibPath("ggml")
					ggmlBasePath := resolveLibPath("ggml-base")
					ggmlCpuPath := resolveLibPath("ggml-cpu")
					if llamaPath != "" && ggmlPath != "" && ggmlBasePath != "" && ggmlCpuPath != "" {
						linkArgs = append(linkArgs, llamaPath, ggmlPath, ggmlBasePath, ggmlCpuPath, "-lstdc++")
					} else {
						_, buildFile, _, _ := runtime.Caller(0)
						codegenDir := filepath.Dir(buildFile)
						llamaLibDir := filepath.Join(codegenDir, "..", "llama-dist", "lib")
						linkArgs = append(linkArgs, "-L"+llamaLibDir, "-Wl,-rpath,"+llamaLibDir)
						linkArgs = append(linkArgs, "-lllama", "-lggml", "-lggml-base", "-lggml-cpu", "-lstdc++")
					}
				}
				if imp.Path == "std/mqtt" {
					if p := resolveLibPath("mosquitto"); p != "" {
						linkArgs = append(linkArgs, p)
					}
				}
				if imp.Path == "std/crypto" {
					for _, lib := range []string{"ssl", "crypto"} {
						if p := resolveLibPath(lib); p != "" {
							linkArgs = append(linkArgs, p)
						} else {
							linkArgs = append(linkArgs, "-l"+lib)
						}
					}
				}
				if imp.Path == "std/compress" {
					if p := resolveLibPath("z"); p != "" {
						linkArgs = append(linkArgs, p)
					} else {
						linkArgs = append(linkArgs, "-lz")
					}
				}
			}
		}
	}
	// Auto-detect and compile C runtime objects.
	runtimeObjs, err := compileRuntimeObjectsForProgram(program, filepath.Dir(outPath))
	if err != nil {
		return "", fmt.Errorf("runtime compilation failed: %w", err)
	}
	linkArgs = append(linkArgs, runtimeObjs...)
	neededRT := detectNeededRuntimeFiles(program)
	if neededRT["tls_rt.c"] {
		linkArgs = append(linkArgs, "-lssl", "-lcrypto")
	}
	if opts.Debug {
		linkArgs = append(linkArgs, "-g")
	}
	if err := runCommand(linker, linkArgs...); err != nil {
		return "", fmt.Errorf("linking failed: %w", err)
	}

	return outPath, nil
}

// detectNeededRuntimeFiles scans the resolved program for imports and
// extern declarations to determine which C runtime .c files need to be linked.
func detectNeededRuntimeFiles(program *ast.Program) map[string]bool {
	needed := make(map[string]bool)
	for _, decl := range program.Declarations {
		switch d := decl.(type) {
		case *ast.ImportDecl:
			switch d.Path {
			case "std/sync", "std/waitgroup":
				needed["sync_rt.c"] = true
			case "std/tensor":
				needed["tensor_rt.c"] = true
			case "std/conc":
				needed["conc_rt.c"] = true
			case "std/db":
				needed["db_rt.c"] = true
			case "std/llm":
				needed["llm_rt.c"] = true
			case "std/mqtt":
				needed["mqtt_rt.c"] = true
			case "std/compress":
				needed["compress_rt.c"] = true
			case "std/reflect":
				needed["reflect_rt.c"] = true
			case "std/web/server":
				needed["tls_rt.c"] = true
			}
		case *ast.ExternFnDecl:
			name := d.Name
			if strings.HasPrefix(name, "Skink_mutex_") ||
				strings.HasPrefix(name, "Skink_rwlock_") ||
				strings.HasPrefix(name, "Skink_cond_") ||
				strings.HasPrefix(name, "Skink_atomic_") {
				needed["sync_rt.c"] = true
			}
			if strings.HasPrefix(name, "Skink_tensor_") {
				needed["tensor_rt.c"] = true
			}
			if strings.HasPrefix(name, "Skink_chan_") ||
				strings.HasPrefix(name, "Skink_future_") ||
				strings.HasPrefix(name, "Skink_spawn") ||
				name == "Skink_free" {
				needed["conc_rt.c"] = true
			}
			if strings.HasPrefix(name, "Skink_db_") {
				needed["db_rt.c"] = true
			}
			if strings.HasPrefix(name, "Skink_llm_") {
				needed["llm_rt.c"] = true
			}
			if strings.HasPrefix(name, "Skink_compress") || strings.HasPrefix(name, "Skink_uncompress") || strings.HasPrefix(name, "Skink_crc32") {
				needed["compress_rt.c"] = true
			}
			if strings.HasPrefix(name, "Skink_tls_") {
				needed["tls_rt.c"] = true
			}
		case *ast.FnDecl:
			if d.Body != nil && hasConcurrencyConstructs(d.Body.Statements) {
				needed["conc_rt.c"] = true
			}
			if d.Body != nil && hasTensorBuiltinCalls(d.Body.Statements) {
				needed["tensor_rt.c"] = true
			}
			for _, p := range d.Params {
				if isChanType(p.Type) {
					needed["conc_rt.c"] = true
				}
			}
			if isChanType(d.ReturnType) {
				needed["conc_rt.c"] = true
			}
		case *ast.StructDecl:
			for _, f := range d.Fields {
				if isChanType(f.Type) {
					needed["conc_rt.c"] = true
				}
			}
		case *ast.RulesetDecl:
			// Generated start() always calls Skink_spawn and the ruleset C runtime.
			needed["conc_rt.c"] = true
			needed["rules_rt.c"] = true
		}
	}
	return needed
}

// isChanType checks whether a type is a channel type.
func isChanType(t ast.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.(*ast.ChanType)
	return ok
}

// hasConcurrencyConstructs recursively scans statements for spawn/select or
// make(chan) expressions.
func hasConcurrencyConstructs(stmts []ast.Statement) bool {
	for _, s := range stmts {
		switch st := s.(type) {
		case *ast.SpawnStmt:
			return true
		case *ast.SelectStmt:
			return true
		case *ast.BlockStmt:
			if hasConcurrencyConstructs(st.Statements) {
				return true
			}
		case *ast.IfStmt:
			if hasConcurrencyConstructs(st.Consequence.Statements) {
				return true
			}
			if st.Alternative != nil {
				if altBlock, ok := st.Alternative.(*ast.BlockStmt); ok && hasConcurrencyConstructs(altBlock.Statements) {
					return true
				}
			}
		case *ast.ForStmt:
			if st.Body != nil && hasConcurrencyConstructs(st.Body.Statements) {
				return true
			}
		case *ast.WhileStmt:
			if st.Body != nil && hasConcurrencyConstructs(st.Body.Statements) {
				return true
			}
		case *ast.VarStmt:
			if isChanType(st.Type) {
				return true
			}
			if st.Value != nil && exprUsesConcurrency(st.Value) {
				return true
			}
		case *ast.ExprStmt:
			if exprUsesConcurrency(st.Expr) {
				return true
			}
		case *ast.AssignmentStmt:
			if exprUsesConcurrency(st.Value) {
				return true
			}
		case *ast.ReturnStmt:
			for _, v := range st.Values {
				if exprUsesConcurrency(v) {
					return true
				}
			}
		case *ast.DeferStmt:
			if es, ok := st.Statement.(*ast.ExprStmt); ok && exprUsesConcurrency(es.Expr) {
				return true
			}
		}
	}
	return false
}

// exprUsesConcurrency checks whether an expression involves channel make,
// async/await, or close.
func exprUsesConcurrency(e ast.Expression) bool {
	if e == nil {
		return false
	}
	switch expr := e.(type) {
	case *ast.MakeExpr:
		return isChanType(expr.Type)
	case *ast.AsyncExpr:
		return true
	case *ast.AwaitExpr:
		return true
	case *ast.CallExpr:
		for _, a := range expr.Arguments {
			if exprUsesConcurrency(a) {
				return true
			}
		}
		if id, ok := expr.Function.(*ast.Identifier); ok && id.Value == "close" {
			return true
		}
	case *ast.FieldAccessExpr:
		return exprUsesConcurrency(expr.Left)
	case *ast.IndexExpr:
		return exprUsesConcurrency(expr.Left) || exprUsesConcurrency(expr.Index)
	case *ast.InfixExpr:
		return exprUsesConcurrency(expr.Left) || exprUsesConcurrency(expr.Right)
	case *ast.PrefixExpr:
		return exprUsesConcurrency(expr.Right)
	case *ast.ArrayLiteral:
		for _, elem := range expr.Elements {
			if exprUsesConcurrency(elem) {
				return true
			}
		}
	case *ast.MapLiteral:
		for _, pair := range expr.Pairs {
			if exprUsesConcurrency(pair.Value) {
				return true
			}
		}
	case *ast.StructInitExpr:
		for _, v := range expr.Fields {
			if exprUsesConcurrency(v) {
				return true
			}
		}
	}
	return false
}

// tensorBuiltinNames is the set of built-in tensor/math function names that
// require tensor_rt.c to be linked.
var tensorBuiltinNames = map[string]bool{
	"tensor_ones": true, "tensor_zeros": true, "tensor_get": true,
	"matmul": true, "norm": true, "det": true, "inv": true,
	"gradient": true, "cross": true, "eigenvalues": true, "dot": true,
	"diff": true, "integrate": true,
}

// hasTensorBuiltinCalls recursively scans statements for calls to built-in
// tensor functions that require tensor_rt.c.
func hasTensorBuiltinCalls(stmts []ast.Statement) bool {
	for _, s := range stmts {
		switch st := s.(type) {
		case *ast.BlockStmt:
			if hasTensorBuiltinCalls(st.Statements) {
				return true
			}
		case *ast.IfStmt:
			if hasTensorBuiltinCalls(st.Consequence.Statements) {
				return true
			}
			if st.Alternative != nil {
				if altBlock, ok := st.Alternative.(*ast.BlockStmt); ok && hasTensorBuiltinCalls(altBlock.Statements) {
					return true
				}
			}
		case *ast.ForStmt:
			if st.Body != nil && hasTensorBuiltinCalls(st.Body.Statements) {
				return true
			}
		case *ast.WhileStmt:
			if st.Body != nil && hasTensorBuiltinCalls(st.Body.Statements) {
				return true
			}
		case *ast.VarStmt:
			if st.Value != nil && exprUsesTensorBuiltin(st.Value) {
				return true
			}
		case *ast.ExprStmt:
			if exprUsesTensorBuiltin(st.Expr) {
				return true
			}
		case *ast.AssignmentStmt:
			if exprUsesTensorBuiltin(st.Value) {
				return true
			}
		case *ast.ReturnStmt:
			for _, v := range st.Values {
				if exprUsesTensorBuiltin(v) {
					return true
				}
			}
		}
	}
	return false
}

// exprUsesTensorBuiltin checks whether an expression contains a call to a
// built-in tensor function.
func exprUsesTensorBuiltin(e ast.Expression) bool {
	if e == nil {
		return false
	}
	switch expr := e.(type) {
	case *ast.CallExpr:
		if id, ok := expr.Function.(*ast.Identifier); ok {
			if tensorBuiltinNames[id.Value] {
				return true
			}
		}
		for _, a := range expr.Arguments {
			if exprUsesTensorBuiltin(a) {
				return true
			}
		}
	case *ast.FieldAccessExpr:
		return exprUsesTensorBuiltin(expr.Left)
	case *ast.IndexExpr:
		return exprUsesTensorBuiltin(expr.Left) || exprUsesTensorBuiltin(expr.Index)
	case *ast.InfixExpr:
		return exprUsesTensorBuiltin(expr.Left) || exprUsesTensorBuiltin(expr.Right)
	case *ast.PrefixExpr:
		return exprUsesTensorBuiltin(expr.Right)
	case *ast.ArrayLiteral:
		for _, elem := range expr.Elements {
			if exprUsesTensorBuiltin(elem) {
				return true
			}
		}
	case *ast.MapLiteral:
		for _, pair := range expr.Pairs {
			if exprUsesTensorBuiltin(pair.Value) {
				return true
			}
		}
	case *ast.StructInitExpr:
		for _, v := range expr.Fields {
			if exprUsesTensorBuiltin(v) {
				return true
			}
		}
	}
	return false
}

// compileRuntimeObjectsForProgram detects which C runtime files are needed,
// compiles them to .o files, and returns the paths to the .o files.
func compileRuntimeObjectsForProgram(program *ast.Program, outDir string) ([]string, error) {
	needed := detectNeededRuntimeFiles(program)
	if len(needed) == 0 {
		return nil, nil
	}

	// Locate the compiler runtime directory.
	_, buildFile, _, _ := runtime.Caller(0)
	codegenDir := filepath.Dir(buildFile)
	runtimeDir := filepath.Join(codegenDir, "..", "runtime")

	// Use a temporary directory for the .o files.
	tmpDir, err := os.MkdirTemp(outDir, ".skink-rt-")
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime temp dir: %w", err)
	}
	// Note: we intentionally do NOT clean up tmpDir here.
	// The caller (skink test) removes the entire test temp dir after running.

	var objs []string
	for name := range needed {
		srcPath := filepath.Join(runtimeDir, name)
		if _, err := os.Stat(srcPath); err != nil {
			return nil, fmt.Errorf("runtime source not found: %s", srcPath)
		}
		objPath := filepath.Join(tmpDir, strings.TrimSuffix(name, ".c")+".o")
		args := []string{"-fPIC", "-c", srcPath, "-o", objPath}
		if name == "llm_rt.c" {
			_, buildFile, _, _ := runtime.Caller(0)
			codegenDir := filepath.Dir(buildFile)
			llamaIncludeDir := filepath.Join(codegenDir, "..", "llama-dist", "include")
			args = append(args, "-I", llamaIncludeDir)
		}
		cmd := exec.Command("cc", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("failed to compile %s: %v\n%s", name, err, out)
		}
		objs = append(objs, objPath)
	}
	return objs, nil
}

// resolveLibPath attempts to find the actual shared library file for a given
// library name (e.g. "ssl" -> "/usr/lib64/libssl.so.3").  It first looks for
// the unversioned .so symlink (development package), then falls back to the
// versioned .so.X file (runtime-only package).
func resolveLibPath(name string) string {
	candidates := []string{
		"/usr/lib64/lib" + name + ".so",
		"/usr/lib/x86_64-linux-gnu/lib" + name + ".so",
		"/usr/lib/lib" + name + ".so",
		"/usr/local/lib/lib" + name + ".so",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Fallback: scan directories for versioned .so.X files.
	for _, dir := range []string{"/usr/lib64", "/usr/lib/x86_64-linux-gnu", "/usr/lib", "/usr/local/lib"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "lib"+name+".so.") {
				return filepath.Join(dir, entry.Name())
			}
		}
	}
	return ""
}

// pickLinker searches the system PATH for a suitable C linker.
// It prefers clang, then cc, then gcc.
// Returns an empty string if none is found.
func pickLinker() string {
	for _, name := range []string{"clang", "cc", "gcc"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// runCommand executes an external command, forwarding stdout and stderr
// to the current process so the user sees compiler / linker messages.
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
