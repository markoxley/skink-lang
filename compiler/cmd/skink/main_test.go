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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindLibDirFromEnv(t *testing.T) {
	// Create a temporary lib directory with a testing.skink file.
	libDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(libDir, "testing.skink"), []byte(""), 0644); err != nil {
		t.Fatalf("creating dummy testing.skink: %v", err)
	}

	oldHome := os.Getenv("SKINK_HOME")
	os.Setenv("SKINK_HOME", libDir)
	defer os.Setenv("SKINK_HOME", oldHome)

	found := findLibDir()
	if found != libDir {
		t.Fatalf("expected %q, got %q", libDir, found)
	}
}

func TestFindLibDirFallback(t *testing.T) {
	// Clear SKINK_HOME and verify fallback still finds the lib dir.
	oldHome := os.Getenv("SKINK_HOME")
	os.Unsetenv("SKINK_HOME")
	defer os.Setenv("SKINK_HOME", oldHome)

	found := findLibDir()
	if found == "" {
		t.Fatal("findLibDir returned empty string")
	}
	if _, err := os.Stat(filepath.Join(found, "testing.skink")); err != nil {
		t.Fatalf("found dir %q does not contain testing.skink", found)
	}
}

func TestHasTesting(t *testing.T) {
	libDir := findLibDir()
	if !hasTesting(libDir) {
		t.Fatalf("hasTesting(%q) should be true", libDir)
	}

	tmpDir := t.TempDir()
	if hasTesting(tmpDir) {
		t.Fatalf("hasTesting(%q) should be false for empty dir", tmpDir)
	}
}

func TestGenerateTestMain(t *testing.T) {
	mainSrc := generateTestMain("foo_test", []string{"TestA", "TestB"})

	if !strings.Contains(mainSrc, "module main") {
		t.Error("generated main missing 'module main'")
	}
	if !strings.Contains(mainSrc, `import "testing"`) {
		t.Error("generated main missing testing import")
	}
	if !strings.Contains(mainSrc, `import "foo_test"`) {
		t.Error("generated main missing module import")
	}
	if !strings.Contains(mainSrc, "TestA()") {
		t.Error("generated main missing TestA call")
	}
	if !strings.Contains(mainSrc, "TestB()") {
		t.Error("generated main missing TestB call")
	}
	if !strings.Contains(mainSrc, "Reset()") {
		t.Error("generated main missing Reset call")
	}
	if !strings.Contains(mainSrc, "Failed()") {
		t.Error("generated main missing Failed check")
	}
}

func TestGenerateTestMainEmpty(t *testing.T) {
	mainSrc := generateTestMain("empty_test", []string{})

	if !strings.Contains(mainSrc, "module main") {
		t.Error("generated main missing 'module main'")
	}
	// Should still compile and exit 0 even with no tests.
	if !strings.Contains(mainSrc, "return 0") {
		t.Error("generated main should have return 0")
	}
}
