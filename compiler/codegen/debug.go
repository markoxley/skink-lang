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
	"fmt"
	"path/filepath"
	"strings"
)

// debugInfo generates LLVM IR debug metadata for LLDB / GDB source-level debugging.
type debugInfo struct {
	sourcePath string
	dir        string
	filename   string
	nextID     int

	// metadata IDs
	compileUnit int
	file        int

	// scope tracking
	scopeStack []int // DISubprogram IDs
}

// newDebugInfo creates a debug metadata generator for sourcePath.
func newDebugInfo(sourcePath string) *debugInfo {
	dir := filepath.Dir(sourcePath)
	filename := filepath.Base(sourcePath)
	d := &debugInfo{
		sourcePath: sourcePath,
		dir:        dir,
		filename:   filename,
		nextID:     0,
	}
	d.compileUnit = d.allocID()
	d.file = d.allocID()
	return d
}

// allocID returns the next unique metadata ID.
func (d *debugInfo) allocID() int {
	d.nextID++
	return d.nextID
}

// ModuleHeader returns the debug metadata declarations that must appear
// in the LLVM module header.
func (d *debugInfo) ModuleHeader() string {
	var b strings.Builder
	b.WriteString("!llvm.dbg.cu = !{")
	b.WriteString(fmt.Sprintf("!%d", d.compileUnit))
	b.WriteString("}\n")
	b.WriteString(fmt.Sprintf("!%d = distinct !DICompileUnit(language: DW_LANG_C, file: !%d, producer: \"Skink\", isOptimized: false, runtimeVersion: 0, emissionKind: FullDebug)\n",
		d.compileUnit, d.file))
	b.WriteString(fmt.Sprintf("!%d = !DIFile(filename: \"%s\", directory: \"%s\")\n",
		d.file, d.filename, d.dir))
	return b.String()
}

// Subprogram creates a DISubprogram metadata node for a function.
// Returns the metadata ID.
func (d *debugInfo) Subprogram(name string, line int) int {
	id := d.allocID()
	// Parent scope is the file.
	return id
}

// SubprogramDef returns the metadata definition string for a subprogram.
func (d *debugInfo) SubprogramDef(id int, name string, line int) string {
	return fmt.Sprintf("!%d = distinct !DISubprogram(name: \"%s\", linkageName: \"%s\", scope: !%d, file: !%d, line: %d, type: !%d, scopeLine: %d, spFlags: DISPFlagDefinition, unit: !%d)\n",
		id, name, name, d.file, d.file, line, id+1, line, d.compileUnit)
}

// SubroutineTypeDef returns a placeholder subroutine type definition.
func (d *debugInfo) SubroutineTypeDef(id int) string {
	return fmt.Sprintf("!%d = !DISubroutineType(types: !%d)\n", id, id+1)
}

// TypeListDef returns an empty type list.
func (d *debugInfo) TypeListDef(id int) string {
	return fmt.Sprintf("!%d = !{}\n", id)
}

// Location creates a DILocation metadata node.
func (d *debugInfo) Location(line, column, scopeID int) int {
	id := d.allocID()
	return id
}

// LocationDef returns the metadata definition string for a location.
func (d *debugInfo) LocationDef(id int, line, column, scopeID int) string {
	return fmt.Sprintf("!%d = !DILocation(line: %d, column: %d, scope: !%d)\n", id, line, column, scopeID)
}

// FinalMetadata returns all metadata definitions collected during codegen.
// For now we emit metadata inline as we go, so this is a no-op.
func (d *debugInfo) FinalMetadata() string { return "" }
