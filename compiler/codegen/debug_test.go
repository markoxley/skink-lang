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

package codegen

import (
	"strings"
	"testing"
)

func TestNewDebugInfo(t *testing.T) {
	d := newDebugInfo("/home/user/project/main.skink")
	if d == nil {
		t.Fatal("newDebugInfo returned nil")
	}
	if d.sourcePath != "/home/user/project/main.skink" {
		t.Errorf("sourcePath = %q", d.sourcePath)
	}
	if d.filename != "main.skink" {
		t.Errorf("filename = %q, want %q", d.filename, "main.skink")
	}
	// filepath.Dir uses OS-specific separators
	if d.dir == "" {
		t.Error("dir should not be empty")
	}
	if d.compileUnit == 0 {
		t.Error("compileUnit should be allocated")
	}
	if d.file == 0 {
		t.Error("file should be allocated")
	}
}

func TestAllocID(t *testing.T) {
	d := newDebugInfo("test.skink")
	id1 := d.allocID()
	id2 := d.allocID()
	if id2 != id1+1 {
		t.Errorf("allocID not monotonic: %d then %d", id1, id2)
	}
}

func TestModuleHeader(t *testing.T) {
	d := newDebugInfo("test.skink")
	h := d.ModuleHeader()
	if !strings.Contains(h, "!llvm.dbg.cu") {
		t.Error("ModuleHeader missing llvm.dbg.cu")
	}
	if !strings.Contains(h, "DICompileUnit") {
		t.Error("ModuleHeader missing DICompileUnit")
	}
	if !strings.Contains(h, "DIFile") {
		t.Error("ModuleHeader missing DIFile")
	}
}

func TestSubprogram(t *testing.T) {
	d := newDebugInfo("test.skink")
	id := d.Subprogram("main", 1)
	if id == 0 {
		t.Error("Subprogram returned 0")
	}
}

func TestSubprogramDef(t *testing.T) {
	d := newDebugInfo("test.skink")
	def := d.SubprogramDef(42, "foo", 10)
	if !strings.Contains(def, "foo") {
		t.Error("SubprogramDef missing name")
	}
	if !strings.Contains(def, "DISPFlagDefinition") {
		t.Error("SubprogramDef missing DISPFlagDefinition")
	}
}

func TestSubroutineTypeDef(t *testing.T) {
	d := newDebugInfo("test.skink")
	def := d.SubroutineTypeDef(1)
	if !strings.Contains(def, "DISubroutineType") {
		t.Error("SubroutineTypeDef missing DISubroutineType")
	}
}

func TestTypeListDef(t *testing.T) {
	d := newDebugInfo("test.skink")
	def := d.TypeListDef(1)
	if !strings.Contains(def, "!{}") {
		t.Error("TypeListDef missing empty list")
	}
}

func TestLocation(t *testing.T) {
	d := newDebugInfo("test.skink")
	id := d.Location(10, 5, 99)
	if id == 0 {
		t.Error("Location returned 0")
	}
}

func TestLocationDef(t *testing.T) {
	d := newDebugInfo("test.skink")
	def := d.LocationDef(1, 10, 5, 99)
	if !strings.Contains(def, "DILocation") {
		t.Error("LocationDef missing DILocation")
	}
	if !strings.Contains(def, "line: 10") {
		t.Error("LocationDef missing line")
	}
	if !strings.Contains(def, "column: 5") {
		t.Error("LocationDef missing column")
	}
}

func TestFinalMetadata(t *testing.T) {
	d := newDebugInfo("test.skink")
	if d.FinalMetadata() != "" {
		t.Error("FinalMetadata should be empty for now")
	}
}
