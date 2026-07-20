package codegen

import (
"os"
"path/filepath"
"testing"
"github.com/skink-lang/compiler/ast"
"github.com/skink-lang/compiler/resolver"
"github.com/skink-lang/compiler/types"
)

func TestDumpIR3(t *testing.T) {
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
print("json marshalled: ")
print(marshalledJson)
print("\n")
decodedJson := json.UnmarshalEntries(marshalledJson)
print("decoded json length\n")
print("5\n")
xmlKeys := ["item", "cost"]
xmlValues := ["Widget", "42"]
marshalledXml := xml.MarshalEntries("root", xmlKeys, xmlValues, 2)
print("xml marshalled: ")
print(marshalledXml)
print("\n")
decodedXml := xml.UnmarshalEntries(marshalledXml)
print("decoded xml length\n")
print("6\n")
yamlKeys := ["greeting", "target"]
yamlValues := ["hello", "world"]
marshalledYaml := yaml.MarshalEntries(yamlKeys, yamlValues, 2)
print("yaml marshalled: ")
print(marshalledYaml)
print("\n")
decodedYaml := yaml.UnmarshalEntries(marshalledYaml)
print("decoded yaml length\n")
print("check s\n")
print("check isEq\n")
print("check nowVal\n")
if s == 4.0 && a == 42.0 && p == 8.0 && lenVal == 5 && isEq && hasHello {
if nowVal > 0 {
print("7\n")
if str.Equal(decodedJson[0].key, "name") && str.Equal(decodedJson[0].value, "Skink") {
print("8\n")
if str.Equal(decodedXml[0].key, "item") && str.Equal(decodedXml[0].value, "Widget") {
print("9\n")
if str.Equal(decodedYaml[0].key, "greeting") && str.Equal(decodedYaml[0].value, "hello") {
print("10\n")
return 42
}
}
}
}
}
return 1
}`
tmpDir := os.TempDir()
srcPath := filepath.Join(tmpDir, "dump_main3.skink")
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

outPath := "/tmp/dumped_ir3.ll"
os.WriteFile(outPath, []byte(ir), 0644)
t.Logf("IR dumped to %s (length %d)", outPath, len(ir))
}
