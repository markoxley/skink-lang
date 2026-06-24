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

// readall_test.go verifies that the io.ReadAll function produces valid
// LLVM IR when compiled.
package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationFsReadAll(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolved(t, "fs_readall", map[string]string{
		"main.skink": `module main
import "std/fs"

fn main() -> int {
	content, err := fs.ReadAll("` + testFile + `")
	if err.message != "" {
		return 99
	}
	if len(content) == 11 {
		if content[0] == 104 && content[6] == 119 {
			return 42
		}
	}
	return len(content)
}`})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}
