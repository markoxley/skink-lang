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

// manifest_test.go contains tests for Skink module manifest parsing.
package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkinkHomeEnv(t *testing.T) {
	tmpDir := t.TempDir()
	old := os.Getenv("SKINK_HOME")
	os.Setenv("SKINK_HOME", tmpDir)
	defer os.Setenv("SKINK_HOME", old)

	if got := SkinkHome(); got != tmpDir {
		t.Errorf("expected SKINK_HOME=%q, got %q", tmpDir, got)
	}
}

func TestSkinkHomeDefault(t *testing.T) {
	old := os.Getenv("SKINK_HOME")
	os.Unsetenv("SKINK_HOME")
	defer os.Setenv("SKINK_HOME", old)

	got := SkinkHome()
	if got == "" {
		t.Fatal("SkinkHome() returned empty string")
	}
	// Should be ~/.skink
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestTryPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Test .skink file resolution
	os.WriteFile(filepath.Join(tmpDir, "foo.skink"), []byte(""), 0644)
	if got := tryPath(tmpDir, "foo"); got == "" {
		t.Error("expected to find foo.skink")
	}

	// Test main.skink package resolution
	pkgDir := filepath.Join(tmpDir, "bar")
	os.MkdirAll(pkgDir, 0755)
	os.WriteFile(filepath.Join(pkgDir, "main.skink"), []byte(""), 0644)
	if got := tryPath(tmpDir, "bar"); got == "" {
		t.Error("expected to find bar/main.skink")
	}

	// Test missing module
	if got := tryPath(tmpDir, "nonexistent"); got != "" {
		t.Error("expected no match for nonexistent")
	}
}

func TestResolveImportPath(t *testing.T) {
	localDir := t.TempDir()
	globalDir := t.TempDir()

	// Set up global module
	old := os.Getenv("SKINK_HOME")
	os.Setenv("SKINK_HOME", globalDir)
	defer os.Setenv("SKINK_HOME", old)

	os.WriteFile(filepath.Join(globalDir, "global_mod.skink"), []byte(""), 0644)
	os.WriteFile(filepath.Join(localDir, "local_mod.skink"), []byte(""), 0644)

	// Local takes priority
	if got := resolveImportPath("local_mod", localDir); got == "" {
		t.Error("expected to find local_mod")
	}

	// Global fallback
	if got := resolveImportPath("global_mod", localDir); got == "" {
		t.Error("expected to find global_mod")
	}

	// Nonexistent
	if got := resolveImportPath("nonexistent", localDir); got != "" {
		t.Error("expected no match for nonexistent")
	}
}

func TestParseManifest(t *testing.T) {
	tmpDir := t.TempDir()
	modPath := filepath.Join(tmpDir, "skink.mod")
	content := `module myproject
version 0.1.0
require github.com/example/lib v1.0.0
`
	if err := os.WriteFile(modPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseManifest(modPath)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	if m.Module != "myproject" {
		t.Errorf("expected module 'myproject', got %q", m.Module)
	}
	if m.Version != "0.1.0" {
		t.Errorf("expected version '0.1.0', got %q", m.Version)
	}
	if len(m.Requires) != 1 || m.Requires[0].Path != "github.com/example/lib" || m.Requires[0].Version != "v1.0.0" {
		t.Errorf("expected requires [{github.com/example/lib v1.0.0}], got %v", m.Requires)
	}
}

func TestParseManifestAllSyntaxForms(t *testing.T) {
	content := `# Complete manifest demonstration
module github.com/username/complete-demo
version 2.4.1

// Single-line explicit require
require github.com/example/lib-a v1.0.0

# Block require syntax
require (
	github.com/example/math-pkg v2.3.4
	github.com/example/json-utils v0.9.1-beta
)

// Implicit declaration without keyword
github.com/example/logger v4.5.6

// Alternative spelling 'requires'
requires github.com/example/utility-helpers v3.2.1
`

	m, err := ParseManifestContent(content)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	if m.Module != "github.com/username/complete-demo" {
		t.Errorf("expected module 'github.com/username/complete-demo', got %q", m.Module)
	}
	if m.Version != "2.4.1" {
		t.Errorf("expected version '2.4.1', got %q", m.Version)
	}

	expected := []Dependency{
		{"github.com/example/lib-a", "v1.0.0"},
		{"github.com/example/math-pkg", "v2.3.4"},
		{"github.com/example/json-utils", "v0.9.1-beta"},
		{"github.com/example/logger", "v4.5.6"},
		{"github.com/example/utility-helpers", "v3.2.1"},
	}

	if len(m.Requires) != len(expected) {
		t.Fatalf("expected %d dependencies, got %d: %v", len(expected), len(m.Requires), m.Requires)
	}

	for i, dep := range m.Requires {
		if dep.Path != expected[i].Path {
			t.Errorf("dep[%d].Path: expected %q, got %q", i, expected[i].Path, dep.Path)
		}
		if dep.Version != expected[i].Version {
			t.Errorf("dep[%d].Version: expected %q, got %q", i, expected[i].Version, dep.Version)
		}
	}
}

func TestParseManifestHashComments(t *testing.T) {
	content := `module test
# this is a hash comment
require github.com/a/b v1.0.0
`
	m, err := ParseManifestContent(content)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(m.Requires) != 1 || m.Requires[0].Path != "github.com/a/b" {
		t.Errorf("expected 1 require, got %v", m.Requires)
	}
}

func TestFindManifest(t *testing.T) {
	tmpDir := t.TempDir()
	modPath := filepath.Join(tmpDir, "skink.mod")
	if err := os.WriteFile(modPath, []byte("module testmod\n"), 0644); err != nil {
		t.Fatal(err)
	}

	foundPath, m, err := FindManifest(tmpDir)
	if err != nil {
		t.Fatalf("find manifest: %v", err)
	}
	if foundPath != modPath {
		t.Errorf("expected path %q, got %q", modPath, foundPath)
	}
	if m == nil {
		t.Fatal("expected manifest, got nil")
	}
	if m.Module != "testmod" {
		t.Errorf("expected module 'testmod', got %q", m.Module)
	}
}
