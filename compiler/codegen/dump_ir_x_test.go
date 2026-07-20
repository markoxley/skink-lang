package codegen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/resolver"
	"github.com/skink-lang/compiler/types"
)

func TestDumpIRX(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	src := `module main
import "std/str"
import "std/math"
import "std/time"
import "std/json"
import "std/xml"
import "std/yaml"

fn main() -> int {
	print("1\n")
	s := math.Sqrt(16.0)
	a := math.Abs(-42.0)
	p := math.Pow(2.0, 3.0)
	print("2\n")
	lenVal := str.Len("Skink")
	isEq := str.Equal("hello", "hello")
	hasHello := str.Contains("hello world", "world")
	print("3\n")
	nowVal := time.Timestamp()
	time.SleepMs(1)
	print("4\n")
	jsonKeys := ["name", "version"]
	jsonValues := ["Skink", "2"]
	marshalledJson := json.MarshalEntries(jsonKeys, jsonValues, 2)
	decodedJson, errJson := json.UnmarshalEntries(marshalledJson)
	print("5\n")
	xmlKeys := ["item", "cost"]
	xmlValues := ["Widget", "42"]
	marshalledXml := xml.MarshalEntries("root", xmlKeys, xmlValues, 2)
	decodedXml, errXml := xml.UnmarshalEntries(marshalledXml)
	print("6\n")
	yamlKeys := ["greeting", "target"]
	yamlValues := ["hello", "world"]
	marshalledYaml := yaml.MarshalEntries(yamlKeys, yamlValues, 2)
	decodedYaml, errYaml := yaml.UnmarshalEntries(marshalledYaml)
	if s == 4.0 && a == 42.0 && p == 8.0 && lenVal == 5 && isEq && hasHello {
		if nowVal > 0 {
			if errJson.message == "" && str.Equal(decodedJson[0].Key(), "name") && str.Equal(decodedJson[0].Value(), "Skink") {
				if errXml.message == "" && str.Equal(decodedXml[0].Key(), "item") && str.Equal(decodedXml[0].Value(), "Widget") {
					if errYaml.message == "" && str.Equal(decodedYaml[0].Key(), "greeting") && str.Equal(decodedYaml[0].Value(), "hello") {
						return 42
					}
				}
			}
		}
	}
	return 1
}`
	tmpDir := os.TempDir()
	srcPath := filepath.Join(tmpDir, "dump_main_x.skink")
	os.WriteFile(srcPath, []byte(src), 0644)

	prog, symbolInfo, err := resolver.Resolve([]string{srcPath})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	prog = ast.Monomorphize(prog)

	checker := types.NewChecker()
	checker.SetSymbolInfo(symbolInfo)
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		t.Fatalf("type errors: %v", checker.Errors())
	}

	cg := New()
	cg.EmitProgram(prog)
	ir := cg.String()

	outPath := "/tmp/dumped_ir_x.ll"
	os.WriteFile(outPath, []byte(ir), 0644)
	t.Logf("IR dumped to %s (length %d)", outPath, len(ir))
}
