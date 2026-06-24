package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMinimalJson(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	code, out := compileAndRunResolved(t, "min_json", map[string]string{
		"main.skink": `module main
import "std/json"
fn main() -> int {
	marshalledJson := json.MarshalEntries(["name", "version"], ["Skink", "2"], 2)
	decodedJson, err := json.UnmarshalEntries(marshalledJson)
	if err.message != "" {
		return 1
	}
	if len(decodedJson) == 2 {
		return 42
	}
	return 2
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}
