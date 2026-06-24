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

package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skink-lang/compiler/ast"
)

func TestDeclName(t *testing.T) {
	tests := []struct {
		decl ast.Declaration
		want string
	}{
		{&ast.FnDecl{Name: "main"}, "main"},
		{&ast.ConstDecl{Name: "MAX"}, "MAX"},
		{&ast.VarDecl{Name: "x"}, "x"},
		{&ast.StructDecl{Name: "Point"}, "Point"},
		{&ast.EnumDecl{Name: "Color"}, "Color"},
		{&ast.ExternFnDecl{Name: "printf"}, "printf"},
		{&ast.ModuleDecl{Name: "foo"}, ""},
		{&ast.ImportDecl{Path: "std/io"}, ""},
	}
	for _, tt := range tests {
		got := DeclName(tt.decl)
		if got != tt.want {
			t.Errorf("DeclName(%T) = %q, want %q", tt.decl, got, tt.want)
		}
	}
}

func TestIsPub(t *testing.T) {
	if !IsPub(&ast.FnDecl{Pub: true}) {
		t.Error("IsPub(pub fn) should be true")
	}
	if IsPub(&ast.FnDecl{Pub: false}) {
		t.Error("IsPub(private fn) should be false")
	}
	if !IsPub(&ast.ConstDecl{Pub: true}) {
		t.Error("IsPub(pub const) should be true")
	}
	if !IsPub(&ast.VarDecl{Pub: true}) {
		t.Error("IsPub(pub var) should be true")
	}
	if !IsPub(&ast.StructDecl{Pub: true}) {
		t.Error("IsPub(pub struct) should be true")
	}
	if !IsPub(&ast.EnumDecl{Pub: true}) {
		t.Error("IsPub(pub enum) should be true")
	}
	if !IsPub(&ast.ExternFnDecl{}) {
		t.Error("IsPub(extern fn) should be true")
	}
	if IsPub(&ast.ModuleDecl{}) {
		t.Error("IsPub(module) should be false")
	}
}

func TestResolveImportCycle(t *testing.T) {
	tmpDir := t.TempDir()

	// Create A.skink that imports B
	aPath := filepath.Join(tmpDir, "A.skink")
	os.WriteFile(aPath, []byte("module A\nimport \"B\"\npub fn a() -> int { return 1 }\n"), 0644)

	// Create B.skink that imports A (cycle!)
	bPath := filepath.Join(tmpDir, "B.skink")
	os.WriteFile(bPath, []byte("module B\nimport \"A\"\npub fn b() -> int { return 2 }\n"), 0644)

	_, _, err := Resolve([]string{aPath})
	if err == nil {
		t.Fatal("expected import cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "import cycle detected") {
		t.Fatalf("expected 'import cycle detected' in error, got: %v", err)
	}
}

func TestResolveImportCycleThreeWay(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "A.skink"), []byte("module A\nimport \"B\"\npub fn a() -> int { return 1 }\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "B.skink"), []byte("module B\nimport \"C\"\npub fn b() -> int { return 2 }\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "C.skink"), []byte("module C\nimport \"A\"\npub fn c() -> int { return 3 }\n"), 0644)

	_, _, err := Resolve([]string{filepath.Join(tmpDir, "A.skink")})
	if err == nil {
		t.Fatal("expected import cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "import cycle detected") {
		t.Fatalf("expected 'import cycle detected' in error, got: %v", err)
	}
}

func TestResolveManifestModuleMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create manifest declaring module "expected"
	modPath := filepath.Join(tmpDir, "skink.mod")
	os.WriteFile(modPath, []byte("module expected\nversion 1.0.0\n"), 0644)

	// Create source file with different module name
	srcPath := filepath.Join(tmpDir, "main.skink")
	os.WriteFile(srcPath, []byte("module wrongname\npub fn main() -> int { return 0 }\n"), 0644)

	_, _, err := Resolve([]string{srcPath})
	if err == nil {
		t.Fatal("expected manifest mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not match manifest module") {
		t.Fatalf("expected manifest mismatch error, got: %v", err)
	}
}

func TestResolveManifestMatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create manifest
	modPath := filepath.Join(tmpDir, "skink.mod")
	os.WriteFile(modPath, []byte("module expected\nversion 1.0.0\n"), 0644)

	// Create source file with matching module name
	srcPath := filepath.Join(tmpDir, "main.skink")
	os.WriteFile(srcPath, []byte("module expected\npub fn main() -> int { return 0 }\n"), 0644)

	_, _, err := Resolve([]string{srcPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
