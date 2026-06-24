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

// Package ast (monomorphize.go) implements generic monomorphization:
// replacing generic type parameters and function instantiations with
// concrete specialized copies before type checking and code generation.
package ast

import (
	"sort"
	"strings"
)

// Monomorphize replaces all generic declarations and instantiations with
// concrete specialized copies. It returns a new Program with all generics
// resolved.
func Monomorphize(program *Program) *Program {
	m := &monomorphizer{program: program}
	m.run()
	return m.program
}

type monomorphizer struct {
	program                  *Program
	genericStructs           map[string]*StructDecl // name -> generic struct decl
	genericFns               map[string]*FnDecl     // name -> generic fn decl
	instantiations           map[string]bool        // mangled name -> already created
	currentModule            string
	moduleStructs            map[string]bool // qualified structure names
	moduleSymbols            map[string]bool // all qualified top-level symbol names
	currentParams            map[string]bool // currently active generic type parameter names
	generatedSpecializations map[string]bool // mangled names already specialized
}

// run executes the monomorphization pipeline: collect generics, discover
// instantiations, generate specializations, and substitute references.
func (m *monomorphizer) run() {
	m.genericStructs = make(map[string]*StructDecl)
	m.genericFns = make(map[string]*FnDecl)
	m.instantiations = make(map[string]bool)
	m.moduleStructs = make(map[string]bool)
	m.moduleSymbols = make(map[string]bool)
	m.currentParams = make(map[string]bool)
	m.generatedSpecializations = make(map[string]bool)

	// First pass: collect all generic declarations and structures.
	m.currentModule = ""
	for _, decl := range m.program.Declarations {
		switch d := decl.(type) {
		case *ModuleDecl:
			m.currentModule = d.Name
		case *StructDecl:
			m.moduleStructs[d.Name] = true
			m.moduleSymbols[d.Name] = true
			if len(d.TypeParams) > 0 {
				m.genericStructs[d.Name] = d
			}
		case *FnDecl:
			m.moduleSymbols[d.Name] = true
			if len(d.TypeParams) > 0 {
				m.genericFns[d.Name] = d
			}
		case *VarDecl:
			m.moduleSymbols[d.Name] = true
		case *ConstDecl:
			m.moduleSymbols[d.Name] = true
		}
	}

	// Loop collect and spec until no more new instantiations are discovered
	for {
		oldSize := len(m.instantiations)
		m.collectInstantiations()
		m.generateSpecializations()
		if len(m.instantiations) == oldSize {
			break
		}
	}

	// Fourth pass: replace generic instantiations with concrete references.
	m.substituteInstantiations()
}

// collectInstantiations scans the entire AST for uses of generic types/functions
// with concrete arguments and records them.
func (m *monomorphizer) collectInstantiations() {
	m.currentModule = "main" // default to main
	for _, decl := range m.program.Declarations {
		if mod, ok := decl.(*ModuleDecl); ok {
			m.currentModule = mod.Name
			continue
		}
		m.collectInDecl(decl)
	}
}

// tryCollectMangled checks if a name is a mangled generic instantiation
// and records it for specialization.
func (m *monomorphizer) tryCollectMangled(name string) {
	namesToTry := []string{name}
	if !strings.Contains(name, ".") && m.currentModule != "" && m.currentModule != "main" {
		namesToTry = append(namesToTry, m.currentModule+"."+name)
	}
	// If name is module-qualified, also try the unqualified suffix.
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		namesToTry = append(namesToTry, name[idx+1:])
	}

	for _, try := range namesToTry {
		for _, genName := range sortedGenericStructNames(m.genericStructs) {
			decl := m.genericStructs[genName]
			if argTypes, ok := demangleName(try, genName, decl.TypeParams); ok {
				hasGenericParam := false
				for _, arg := range argTypes {
					if nt, ok := arg.(*NamedType); ok {
						if m.currentParams[nt.Name] {
							hasGenericParam = true
							break
						}
						// Qualify the argument type if possible.
						if !strings.Contains(nt.Name, ".") && m.currentModule != "" {
							qualified := m.currentModule + "." + nt.Name
							if m.moduleSymbols[qualified] {
								nt.Name = qualified
							}
						}
					}
				}
				if !hasGenericParam {
					mangled := MangleName(genName, argTypes)
					m.instantiations[mangled] = true
				}
			}
		}
		for _, genName := range sortedGenericFnNames(m.genericFns) {
			decl := m.genericFns[genName]
			if argTypes, ok := demangleName(try, genName, decl.TypeParams); ok {
				hasGenericParam := false
				for _, arg := range argTypes {
					if nt, ok := arg.(*NamedType); ok {
						if m.currentParams[nt.Name] {
							hasGenericParam = true
							break
						}
						// Qualify the argument type if possible.
						if !strings.Contains(nt.Name, ".") && m.currentModule != "" {
							qualified := m.currentModule + "." + nt.Name
							if m.moduleSymbols[qualified] {
								nt.Name = qualified
							}
						}
					}
				}
				if !hasGenericParam {
					mangled := MangleName(genName, argTypes)
					m.instantiations[mangled] = true
				}
			}
		}
	}
}

// generateSpecializations creates concrete copies of generic declarations.
func (m *monomorphizer) generateSpecializations() {
	mangledNames := make([]string, 0, len(m.instantiations))
	for mangled := range m.instantiations {
		mangledNames = append(mangledNames, mangled)
	}
	sort.Strings(mangledNames)
	for _, mangled := range mangledNames {
		if m.generatedSpecializations[mangled] {
			continue
		}
		m.generatedSpecializations[mangled] = true
		var found bool
		for _, genName := range sortedGenericStructNames(m.genericStructs) {
			decl := m.genericStructs[genName]
			if args, ok := demangleName(mangled, genName, decl.TypeParams); ok {
				spec := m.specializeStruct(decl, args)
				m.program.Declarations = append(m.program.Declarations, spec)
				found = true
				break
			}
		}
		if found {
			continue
		}
		for _, genName := range sortedGenericFnNames(m.genericFns) {
			decl := m.genericFns[genName]
			if args, ok := demangleName(mangled, genName, decl.TypeParams); ok {
				spec := m.specializeFn(decl, args)
				m.program.Declarations = append(m.program.Declarations, spec)
				break
			}
		}
	}
}

// substituteInstantiations replaces generic NamedType references with concrete
// mangled names and removes the original generic declarations.
func (m *monomorphizer) substituteInstantiations() {
	newDecls := []Declaration{}
	for _, decl := range m.program.Declarations {
		switch d := decl.(type) {
		case *StructDecl:
			if len(d.TypeParams) > 0 {
				continue // skip original generic
			}
		case *FnDecl:
			if len(d.TypeParams) > 0 {
				continue // skip original generic
			}
		}
		newDecls = append(newDecls, decl)
	}
	m.program.Declarations = newDecls

	// Replace NamedType Args in all remaining declarations
	m.currentModule = "main"
	for _, decl := range m.program.Declarations {
		if mod, ok := decl.(*ModuleDecl); ok {
			m.currentModule = mod.Name
			continue
		}
		m.replaceInDecl(decl)
	}
}
