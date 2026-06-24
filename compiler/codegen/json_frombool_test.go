package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJsonFromBool(t *testing.T) {
	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	code, out := compileAndRunResolved(t, "json_frombool", map[string]string{
		"main.skink": `module main
import "std/json"
fn main() -> int {
	v := json.FromBool(true)
	if v.kind == 1 {
		return 42
	}
	return 1
}`,
	})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}
