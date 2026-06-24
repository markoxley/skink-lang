package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/codegen"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
	"github.com/skink-lang/compiler/resolver"
	"github.com/skink-lang/compiler/types"
)

// generateTestMain creates a Skink source file that runs all Test* functions.
func generateTestMain(moduleName string, testNames []string) string {
	var b strings.Builder
	b.WriteString("module main\n")
	b.WriteString("import \"testing\"\n")
	b.WriteString("import \"std/io\"\n")
	b.WriteString("import \"" + moduleName + "\"\n")
	b.WriteString("\n")
	b.WriteString("fn main() -> int {\n")
	b.WriteString("    passed := true\n")
	for _, name := range testNames {
		b.WriteString("    {\n")
		b.WriteString("        io.PrintLine(\"RUNNING " + name + "\")\n")
		b.WriteString("        Reset()\n")
		b.WriteString("        " + name + "()\n")
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

func main() {
	os.Setenv("SKINK_HOME", "/home/mark/Documents/Projects/skink-lang/skink/compiler/lib")
	cwd := "/home/mark/Documents/Projects/skink-lang/skink/std"
	testFile := "streams_test.skink"

	data, _ := os.ReadFile(filepath.Join(cwd, testFile))
	l := lexer.New(string(data))
	p := parser.New(l)
	prog := p.ParseProgram()

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

	tmpDir, _ := os.MkdirTemp(cwd, ".skink-test-")
	// DO NOT defer remove here so we can inspect

	entries, _ := os.ReadDir(cwd)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".skink") || strings.HasSuffix(name, ".h") || strings.HasSuffix(name, ".c") {
			src := filepath.Join(cwd, name)
			dst := filepath.Join(tmpDir, name)
			data, _ := os.ReadFile(src)
			os.WriteFile(dst, data, 0644)
		}
	}

	mainSrc := generateTestMain(moduleName, testNames)
	mainPath := filepath.Join(tmpDir, "main.skink")
	os.WriteFile(mainPath, []byte(mainSrc), 0644)

	program, symbolInfo, err := resolver.Resolve([]string{mainPath})
	if err != nil {
		fmt.Println("resolve error:", err)
		os.Exit(1)
	}

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(program)
	if len(checker.Errors()) > 0 {
		fmt.Println("type errors:", checker.Errors())
		os.Exit(1)
	}

	ast.TemplateMonomorphize(program)

	llPath := filepath.Join(tmpDir, "main.ll")
	binPath := filepath.Join(tmpDir, "testbin")
	_, err = codegen.Build(program, mainPath, codegen.BuildOptions{OutputPath: binPath, SaveTemps: true})
	var irData []byte
	irData, _ = os.ReadFile(llPath)
	outPath := filepath.Join(cwd, "debug_main.ll")
	os.WriteFile(outPath, irData, 0644)
	if err != nil {
		fmt.Println("build error:", err)
		fmt.Println("Saved IR to", outPath, "len:", len(irData))
		os.Exit(1)
	}
	fmt.Println("Build OK. Running binary...")
	{
		os.WriteFile("/tmp/debug_main_streams.ll", irData, 0644)
	}

	cmd := exec.Command(binPath)
	out, err := cmd.CombinedOutput()
	fmt.Printf("Exit Code: %v, Error: %v\n", cmd.ProcessState.ExitCode(), err)
	fmt.Println("Output:\n", string(out))
	fmt.Println("Temporary directory kept at:", tmpDir)
	// os.RemoveAll(tmpDir)
}
