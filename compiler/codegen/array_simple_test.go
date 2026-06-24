package codegen

import (
"testing"
"os"
"path/filepath"
)

func TestArraySimple(t *testing.T) {
os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
defer os.Unsetenv("SKINK_HOME")

code, out := compileAndRunResolved(t, "array_simple_test", map[string]string{
"main.skink": `module main
import "std/str"
fn main() -> int {
    keys := ["name", "version"]
    if str.Equal(keys[0], "name") {
        return 42
    }
    return 1
}`,
})
if code != 42 {
t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
}
}
