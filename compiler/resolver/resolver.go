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

// Package resolver handles import path resolution and multi-file program assembly.
//
// Given a set of entry-point source files, the resolver:
//  1. Parses each file to extract its module declaration and imports.
//  2. Recursively resolves imports by searching the local directory, vendor
//     directory, module cache, standard library, parent directories, and
//     the SKINK_HOME environment variable.
//  3. Merges all parsed files into a single AST Program.
//  4. Checks for import cycles and reports them as errors.
//  5. Builds a symbol visibility table so the type checker can enforce
//     pub/private access rules across module boundaries.
package resolver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/cimport"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
	"github.com/skink-lang/compiler/types"
)

// parsedFile holds the AST and metadata for a single source file.
type parsedFile struct {
	path         string
	moduleName   string
	declarations []ast.Declaration
	imports      []string
}

// findCycle performs DFS cycle detection on a directed graph.
// Returns the cycle path (including repeated start node) or nil if acyclic.
func findCycle(graph map[string][]string) []string {
	state := make(map[string]int) // 0=unvisited, 1=visiting, 2=done
	var path []string

	var dfs func(node string) []string
	dfs = func(node string) []string {
		state[node] = 1
		path = append(path, node)

		for _, neighbor := range graph[node] {
			if state[neighbor] == 0 {
				if cycle := dfs(neighbor); cycle != nil {
					return cycle
				}
			} else if state[neighbor] == 1 {
				// Found cycle: extract from current path
				for i := 0; i < len(path); i++ {
					if path[i] == neighbor {
						cycle := make([]string, len(path)-i+1)
						copy(cycle, path[i:])
						cycle[len(cycle)-1] = neighbor
						return cycle
					}
				}
			}
		}

		path = path[:len(path)-1]
		state[node] = 2
		return nil
	}

	for node := range graph {
		if state[node] == 0 {
			if cycle := dfs(node); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

// Resolve parses the given entry files, resolves all imports recursively,
// and returns the merged program along with symbol visibility info.
func Resolve(paths []string) (*ast.Program, map[string]types.SymbolInfo, error) {
	// Map of file path -> parsed file (deduplication)
	parsedFiles := make(map[string]*parsedFile)
	importGraph := make(map[string][]string)

	// Look for skink.mod in the directory of the first entry file
	var manifest *Manifest
	if len(paths) > 0 {
		dir := filepath.Dir(paths[0])
		_, m, err := FindManifest(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("reading manifest: %w", err)
		}
		manifest = m
	}

	// Initial queue: files from CLI
	var queue []string
	queue = append(queue, paths...)

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		if _, done := parsedFiles[path]; done {
			continue
		}

		input, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", path, err)
		}

		l := lexer.New(string(input))
		p := parser.New(l)
		prog := p.ParseProgram()
		if len(p.Errors()) > 0 {
			return nil, nil, fmt.Errorf("parse errors in %s: %v", path, p.Errors())
		}

		pf := &parsedFile{
			path:    path,
			imports: []string{},
		}

		for _, decl := range prog.Declarations {
			switch d := decl.(type) {
			case *ast.ModuleDecl:
				pf.moduleName = d.Name
				// Validate against manifest if present
				if manifest != nil && manifest.Module != "" && d.Name != manifest.Module {
					return nil, nil, fmt.Errorf("module declaration %q in %s does not match manifest module %q", d.Name, path, manifest.Module)
				}
				pf.declarations = append(pf.declarations, decl)
			case *ast.ImportDecl:
				pf.imports = append(pf.imports, d.Path)
				pf.declarations = append(pf.declarations, decl)
			default:
				pf.declarations = append(pf.declarations, decl)
			}
		}

		parsedFiles[path] = pf

		// Build import graph and enqueue unresolved imports
		dir := filepath.Dir(path)
		var resolvedImports []string
		for _, imp := range pf.imports {
			resolved := resolveImportPath(imp, dir)
			if resolved == "" {
				// Silently ignore unresolved imports (e.g., built-in modules).
				continue
			}
			resolvedImports = append(resolvedImports, resolved)
			if _, done := parsedFiles[resolved]; !done {
				queue = append(queue, resolved)
			}
		}
		importGraph[path] = resolvedImports
	}

	// Check for import cycles after graph is built
	if cycle := findCycle(importGraph); cycle != nil {
		return nil, nil, fmt.Errorf("import cycle detected: %s", strings.Join(cycle, " -> "))
	}

	// Resolve C imports and merge declarations
	program := &ast.Program{}
	symbolInfo := make(map[string]types.SymbolInfo)
	var cimportDecls []ast.Declaration

	// Iterate parsed files in a deterministic (sorted by path) order so the
	// merged declaration order does not depend on Go's randomized map
	// iteration, which would otherwise make monomorphization and type checking
	// non-deterministic.
	sortedPaths := make([]string, 0, len(parsedFiles))
	for path := range parsedFiles {
		sortedPaths = append(sortedPaths, path)
	}
	sort.Strings(sortedPaths)

	for _, path := range sortedPaths {
		pf := parsedFiles[path]
		dir := filepath.Dir(pf.path)
		for _, imp := range pf.imports {
			if strings.HasPrefix(imp, "C:") {
				headerPath := strings.TrimPrefix(imp, "C:")
				if !filepath.IsAbs(headerPath) {
					headerPath = filepath.Join(dir, headerPath)
				}
				// Security: reject path traversal attempts
				for _, part := range strings.Split(headerPath, string(filepath.Separator)) {
					if part == ".." {
						return nil, nil, fmt.Errorf("cimport path escapes directory: %s", imp)
					}
				}
				decls, err := cimport.Declarations(headerPath)
				if err != nil {
					return nil, nil, fmt.Errorf("cimport %s: %w", headerPath, err)
				}
				cimportDecls = append(cimportDecls, decls...)
			}
		}
	}

	for _, path := range sortedPaths {
		pf := parsedFiles[path]
		// If this is a non-main module, qualify top-level declaration names/types
		if pf.moduleName != "" && pf.moduleName != "main" {
			for _, decl := range pf.declarations {
				switch d := decl.(type) {
				case *ast.FnDecl:
					if !strings.HasPrefix(d.Name, pf.moduleName+".") {
						d.Name = pf.moduleName + "." + d.Name
					}
				case *ast.StructDecl:
					if !strings.HasPrefix(d.Name, pf.moduleName+".") {
						d.Name = pf.moduleName + "." + d.Name
					}
				case *ast.VarDecl:
					if !strings.HasPrefix(d.Name, pf.moduleName+".") {
						d.Name = pf.moduleName + "." + d.Name
					}
				case *ast.ConstDecl:
					if !strings.HasPrefix(d.Name, pf.moduleName+".") {
						d.Name = pf.moduleName + "." + d.Name
					}
				case *ast.EnumDecl:
					if !strings.HasPrefix(d.Name, pf.moduleName+".") {
						d.Name = pf.moduleName + "." + d.Name
					}
				case *ast.TypeAliasDecl:
					if !strings.HasPrefix(d.Name, pf.moduleName+".") {
						d.Name = pf.moduleName + "." + d.Name
					}
				}
			}
		}

		program.Declarations = append(program.Declarations, pf.declarations...)
		for _, decl := range pf.declarations {
			name := DeclName(decl)
			if name != "" {
				si := types.SymbolInfo{
					Module: pf.moduleName,
					Pub:    IsPub(decl),
				}
				symbolInfo[name] = si
			}
		}
	}

	// Append C-generated declarations at the end (extern functions, structs, etc.)
	program.Declarations = append(program.Declarations, cimportDecls...)
	for _, decl := range cimportDecls {
		name := DeclName(decl)
		if name != "" {
			symbolInfo[name] = types.SymbolInfo{Module: "", Pub: true}
		}
	}

	return program, symbolInfo, nil
}

// SkinkHome returns the module search root directory.
// Priority: SKINK_HOME env var > ~/.skink
func SkinkHome() string {
	if home := os.Getenv("SKINK_HOME"); home != "" {
		return home
	}
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		return ""
	}
	return filepath.Join(homeDir, ".skink")
}

// tryPath checks both .skink file and /main.skink package forms for a given base directory.
func tryPath(base, imp string) string {
	candidate := filepath.Join(base, imp+".skink")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	candidate = filepath.Join(base, imp, "main.skink")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	candidate = filepath.Join(base, imp, filepath.Base(imp)+".skink")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// resolveImportPath resolves an import string to an absolute file path.
func resolveImportPath(imp, baseDir string) string {
	// 1. Try local directory first (project-local imports)
	if p := tryPath(baseDir, imp); p != "" {
		return p
	}
	// 2. Try project-local vendor/ or .skink/cache/ directory by walking up to project root
	curr := baseDir
	for {
		if p := tryPath(filepath.Join(curr, "vendor"), imp); p != "" {
			return p
		}
		if p := tryPath(filepath.Join(curr, ".skink", "cache"), imp); p != "" {
			return p
		}
		if p := tryPath(filepath.Join(curr, "std"), imp); p != "" {
			return p
		}
		if p := tryPath(curr, imp); p != "" {
			return p
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	// 3. Try SKINK_HOME or ~/.skink
	if home := SkinkHome(); home != "" {
		if p := tryPath(home, imp); p != "" {
			return p
		}
		if p := tryPath(filepath.Join(home, "cache"), imp); p != "" {
			return p
		}
	}
	return ""
}

// DeclName returns the name of a declaration, or "" if it has no name.
func DeclName(decl ast.Declaration) string {
	switch d := decl.(type) {
	case *ast.FnDecl:
		return d.Name
	case *ast.ConstDecl:
		return d.Name
	case *ast.VarDecl:
		return d.Name
	case *ast.StructDecl:
		return d.Name
	case *ast.EnumDecl:
		return d.Name
	case *ast.TypeAliasDecl:
		return d.Name
	case *ast.ExternFnDecl:
		return d.Name
	}
	return ""
}

// IsPub reports whether a declaration is public.
func IsPub(decl ast.Declaration) bool {
	switch d := decl.(type) {
	case *ast.FnDecl:
		return d.Pub
	case *ast.ConstDecl:
		return d.Pub
	case *ast.VarDecl:
		return d.Pub
	case *ast.StructDecl:
		return d.Pub
	case *ast.EnumDecl:
		return d.Pub
	case *ast.TypeAliasDecl:
		return d.Pub
	case *ast.ExternFnDecl:
		return true // extern functions are always public
	}
	return false
}
