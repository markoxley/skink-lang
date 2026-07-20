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

package pkgmanager

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModFile(t *testing.T) {
	content := `
module myproject

require (
	github.com/user/repo v1.2.3
	github.com/another/repo v0.4.5-rc1
)

require github.com/single/line v3.0.0
github.com/simple/line v4.5.6
`

	deps, err := ParseModFile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []Dependency{
		{"github.com/user/repo", "v1.2.3"},
		{"github.com/another/repo", "v0.4.5-rc1"},
		{"github.com/single/line", "v3.0.0"},
		{"github.com/simple/line", "v4.5.6"},
	}

	if len(deps) != len(expected) {
		t.Fatalf("expected %d dependencies, got %d", len(expected), len(deps))
	}

	for i, dep := range deps {
		if dep.Path != expected[i].Path {
			t.Errorf("at index %d: expected Path %q, got %q", i, expected[i].Path, dep.Path)
		}
		if dep.Version != expected[i].Version {
			t.Errorf("at index %d: expected Version %q, got %q", i, expected[i].Version, dep.Version)
		}
	}
}

func TestUnpackMockZip(t *testing.T) {
	// Create a mock zip file in memory.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// In Github archives, files are nested inside a top-level directory.
	files := map[string]string{
		"repo-1.0.0/":                "",
		"repo-1.0.0/math.skink":      "module math\npub fn add(a: int, b: int) -> int { return a + b }\n",
		"repo-1.0.0/utils/sub.skink": "module utils\npub fn sub(a: int, b: int) -> int { return a - b }\n",
	}

	for name, content := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip file entry: %v", err)
		}
		if content != "" {
			_, err = f.Write([]byte(content))
			if err != nil {
				t.Fatalf("failed to write zip file content: %v", err)
			}
		}
	}
	zw.Close()

	// Temp cache dir
	tmpDir := t.TempDir()

	// Write mock ZIP to disk so DownloadAndUnpack can be simulated or we can test unpacking directly
	// Let's create a custom function or inline extraction test utilizing our exact extraction code.
	targetDir := filepath.Join(tmpDir, "github.com/mock/repo")
	err := os.MkdirAll(targetDir, 0755)
	if err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read mock zip: %v", err)
	}

	// Run exact extraction logic from DownloadAndUnpack
	for _, file := range zipReader.File {
		pathParts := strings.Split(file.Name, "/")
		if len(pathParts) <= 1 {
			continue
		}
		relPath := filepath.Join(pathParts[1:]...)

		filePath := filepath.Join(targetDir, relPath)
		if file.FileInfo().IsDir() {
			os.MkdirAll(filePath, file.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatalf("mkdir error: %v", err)
		}

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			t.Fatalf("open file error: %v", err)
		}

		srcFile, err := file.Open()
		if err != nil {
			dstFile.Close()
			t.Fatalf("open entry error: %v", err)
		}

		_, err = io.Copy(dstFile, srcFile)
		srcFile.Close()
		dstFile.Close()
		if err != nil {
			t.Fatalf("copy error: %v", err)
		}
	}

	// Verify target files are extracted properly without the nested top-level folder
	mathPath := filepath.Join(targetDir, "math.skink")
	if _, err := os.Stat(mathPath); err != nil {
		t.Errorf("expected math.skink to exist, got error: %v", err)
	}

	subPath := filepath.Join(targetDir, "utils/sub.skink")
	if _, err := os.Stat(subPath); err != nil {
		t.Errorf("expected utils/sub.skink to exist, got error: %v", err)
	}

	content, _ := os.ReadFile(mathPath)
	if !bytes.Contains(content, []byte("module math")) {
		t.Errorf("math.skink content incorrect: %s", string(content))
	}
}

func TestParseModFileEmpty(t *testing.T) {
	deps, err := ParseModFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}
}

func TestParseModFileCommentsOnly(t *testing.T) {
	content := `module test
// this is a comment
/* block comment */
`
	deps, err := ParseModFile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}
}

func TestParseModFileHashComments(t *testing.T) {
	content := `module test
# hash comment
require github.com/a/b v1.0.0
// slash comment
# another hash
`
	deps, err := ParseModFile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Path != "github.com/a/b" || deps[0].Version != "v1.0.0" {
		t.Errorf("expected github.com/a/b@v1.0.0, got %v", deps[0])
	}
}

func TestParseModFileMixedFormats(t *testing.T) {
	content := `module test

require (
	github.com/a/b v1.0.0
)

require github.com/c/d v2.0.0
github.com/e/f v3.0.0
`
	deps, err := ParseModFile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 3 {
		t.Fatalf("expected 3 deps, got %d", len(deps))
	}
	if deps[0].Path != "github.com/a/b" {
		t.Errorf("expected github.com/a/b, got %q", deps[0].Path)
	}
	if deps[1].Path != "github.com/c/d" {
		t.Errorf("expected github.com/c/d, got %q", deps[1].Path)
	}
	if deps[2].Path != "github.com/e/f" {
		t.Errorf("expected github.com/e/f, got %q", deps[2].Path)
	}
}

func TestDependencyStruct(t *testing.T) {
	d := Dependency{Path: "github.com/test/repo", Version: "v1.0.0"}
	if d.Path != "github.com/test/repo" {
		t.Errorf("Path mismatch")
	}
	if d.Version != "v1.0.0" {
		t.Errorf("Version mismatch")
	}
}
