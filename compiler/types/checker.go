// Copyright 2026 Mark Oxley Oxley
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

// Package types implements the Skink type system and type checker.
//
// The checker performs two passes over the AST:
//  1. A first pass collects all top-level declarations (functions, structs,
//     enums, constants, variables, services, rulesets, templates) and builds
//     a global symbol table.
//  2. A second pass type-checks every function body, validating that
//     expressions, statements, and declarations are well-typed according
//     to Skink's type rules.
//
// The checker supports generics, method receivers, struct field access,
// pattern matching, LINQ-style queries, comptime evaluation, and C foreign
// function imports.
package types

import (
	"fmt"
	"strings"

	"github.com/skink-lang/compiler/ast"
)

// SymbolInfo records the owning module and public visibility of a top-level
// symbol. It is used during cross-module import resolution to enforce the
// pub/private visibility rules.
type SymbolInfo struct {
	Module string // module path that defines this symbol
	Pub    bool   // true if the symbol was declared with 'pub'
}

// scopeBinding tracks metadata for a single binding in a lexical scope.
type scopeBinding struct {
	typ     Type
	used    bool
	isParam bool
	isBlank bool
}

// Checker walks the AST and validates type correctness.
// It maintains a stack of lexical scopes, tracks the current function context
// for return-type validation, and keeps maps of all user-defined types.
type Checker struct {
	errors        []string                     // accumulated type error messages
	scopes        []map[string]*scopeBinding   // stack of symbol tables; each block pushes a new scope
	currentFn     *FunctionType                // function whose body is currently being checked
	structs       map[string]*ast.StructDecl   // struct name -> AST declaration
	services      map[string]*ServiceType      // service name -> service type
	rulesets      map[string]*RulesetType      // ruleset name -> ruleset type
	templates     map[string]*ast.TemplateDecl // template name -> AST declaration
	aliases       map[string]Type              // alias name -> underlying type
	symbolInfo    map[string]SymbolInfo        // top-level symbol -> module/visibility
	currentModule string                       // module being checked in pass 2
	// imports maps module name -> list of imported module names (or aliases)
	imports map[string][]string
	// importAliases maps import alias -> real module name.
	importAliases map[string]string
	// usedImports tracks which import aliases have been referenced.
	usedImports map[string]bool
}

// NewChecker creates a fresh type checker with built-in functions declared.
func NewChecker() *Checker {
	global := map[string]Type{
		"print":        &FunctionType{Params: []Type{Any}, Ret: []Type{Void}},
		"println":      &FunctionType{Params: []Type{Any}, Ret: []Type{Void}},
		"len":          &FunctionType{Params: []Type{Any}, Ret: []Type{Int}},
		"assert":       &FunctionType{Params: []Type{Bool}, Ret: []Type{Void}},
		"append":       &FunctionType{Params: []Type{Any, Any}, Ret: []Type{Any}},
		"close":        &FunctionType{Params: []Type{Any}, Ret: []Type{Void}},
		"tensor_ones":  &FunctionType{Params: []Type{Int, Int}, Ret: []Type{&TensorType{Elem: Float}}},
		"tensor_zeros": &FunctionType{Params: []Type{Int, Int}, Ret: []Type{&TensorType{Elem: Float}}},
		"tensor_get":   &FunctionType{Params: []Type{&TensorType{Elem: Float}, Int, Int}, Ret: []Type{Float}},
		"sin":          &FunctionType{Params: []Type{Float}, Ret: []Type{Float}},
		"cos":          &FunctionType{Params: []Type{Float}, Ret: []Type{Float}},
		"tan":          &FunctionType{Params: []Type{Float}, Ret: []Type{Float}},
		"sqrt":         &FunctionType{Params: []Type{Float}, Ret: []Type{Float}},
		"pow":          &FunctionType{Params: []Type{Float, Float}, Ret: []Type{Float}},
		"det":          &FunctionType{Params: []Type{&TensorType{Elem: Float}}, Ret: []Type{Float}},
		"inv":          &FunctionType{Params: []Type{&TensorType{Elem: Float}}, Ret: []Type{&TensorType{Elem: Float}}},
		"diff":         &FunctionType{Params: []Type{&FunctionType{Params: []Type{Float}, Ret: []Type{Float}}, Float}, Ret: []Type{Float}},
		"integrate":    &FunctionType{Params: []Type{&FunctionType{Params: []Type{Float}, Ret: []Type{Float}}, Float, Float}, Ret: []Type{Float}},
		"gradient":     &FunctionType{Params: []Type{Any, Any}, Ret: []Type{Any}},
		"dot":          &FunctionType{Params: []Type{Any, Any}, Ret: []Type{Float}},
		"cross":        &FunctionType{Params: []Type{Any, Any}, Ret: []Type{Any}},
		"norm":         &FunctionType{Params: []Type{Any}, Ret: []Type{Float}},
		"eigenvalues":  &FunctionType{Params: []Type{Any}, Ret: []Type{Any}},
	}
	// Built-in RuleSource template for ruleset dynamic registration.
	ruleSource := &ast.TemplateDecl{
		Name: "RuleSource",
		Methods: []*ast.FnDecl{
			{Name: "name", ReturnType: &ast.NamedType{Name: "string"}},
			{Name: "start"},
			{Name: "stop"},
			{Name: "triggered", ReturnType: &ast.NamedType{Name: "bool"}},
			{Name: "action"},
			{Name: "priority", ReturnType: &ast.NamedType{Name: "int"}},
		},
	}
	globalBindings := make(map[string]*scopeBinding, len(global))
	for name, typ := range global {
		globalBindings[name] = &scopeBinding{typ: typ, isBlank: true}
	}
	return &Checker{
		errors:        []string{},
		scopes:        []map[string]*scopeBinding{globalBindings},
		structs:       make(map[string]*ast.StructDecl),
		services:      make(map[string]*ServiceType),
		rulesets:      make(map[string]*RulesetType),
		aliases:       make(map[string]Type),
		templates:     map[string]*ast.TemplateDecl{"RuleSource": ruleSource},
		symbolInfo:    make(map[string]SymbolInfo),
		imports:       make(map[string][]string),
		importAliases: make(map[string]string),
		usedImports:   make(map[string]bool),
	}
}

// SetSymbolInfo configures module ownership and visibility metadata
// for top-level symbols. This is populated by the resolver before
// type checking begins.
func (c *Checker) SetSymbolInfo(info map[string]SymbolInfo) {
	c.symbolInfo = info
}

// Errors returns any type errors found during checking.
// The caller should verify this slice is empty before assuming
// the program is well-typed.
func (c *Checker) Errors() []string { return c.errors }

// report records a type error with formatted message.
func (c *Checker) report(format string, args ...interface{}) {
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}

// markVarUsed marks a variable as used without reporting an error if it does not exist.
func (c *Checker) markVarUsed(name string) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if b, ok := c.scopes[i][name]; ok && b != nil {
			b.used = true
			return
		}
	}
}

// --- Scope management ---

// pushScope adds a new lexical scope.
func (c *Checker) pushScope() {
	c.scopes = append(c.scopes, map[string]*scopeBinding{})
}

// popScope removes the innermost lexical scope and reports any unused local variables.
func (c *Checker) popScope() {
	if len(c.scopes) <= 1 {
		return
	}
	current := c.scopes[len(c.scopes)-1]
	for name, b := range current {
		if b != nil && !b.isBlank && !b.isParam && !b.used {
			c.report("unused variable %q", name)
		}
	}
	c.scopes = c.scopes[:len(c.scopes)-1]
}

// declare binds a name to a type in the current scope. Local variables are
// tracked for unused-variable detection; global declarations and blank
// identifiers are exempt.
func (c *Checker) declare(name string, typ Type) {
	current := c.scopes[len(c.scopes)-1]
	if _, exists := current[name]; exists && name != "_" {
		c.report("variable %q already declared in this scope", name)
		return
	}
	isLocal := len(c.scopes) > 1
	isBlank := name == "_"
	current[name] = &scopeBinding{
		typ:     typ,
		used:    !isLocal || isBlank,
		isBlank: isBlank,
	}
}

// declareParam binds a function parameter in the current scope. Parameters are
// exempt from unused-variable detection.
func (c *Checker) declareParam(name string, typ Type) {
	current := c.scopes[len(c.scopes)-1]
	if _, exists := current[name]; exists && name != "_" {
		c.report("parameter %q already declared in this scope", name)
		return
	}
	current[name] = &scopeBinding{typ: typ, isParam: true, used: true}
}

// resolveWithKey looks up a name and returns its type and the scope key.
func (c *Checker) resolveWithKey(name string) (Type, string) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if b, ok := c.scopes[i][name]; ok && b != nil {
			if i > 0 {
				b.used = true
			}
			// Visibility check for global symbols.
			if i == 0 && c.symbolInfo != nil {
				if info, hasInfo := c.symbolInfo[name]; hasInfo {
					if info.Module != "" && info.Module != c.currentModule && !info.Pub {
						c.report("cannot access private symbol %q from module %q", name, info.Module)
					}
				}
			}
			return b.typ, name
		}
	}
	// If not found in scopes, and we are in a non-main module, check for a qualified name
	if c.currentModule != "" && c.currentModule != "main" {
		qualified := c.currentModule + "." + name
		if b, ok := c.scopes[0][qualified]; ok && b != nil {
			if c.symbolInfo != nil {
				if info, hasInfo := c.symbolInfo[qualified]; hasInfo {
					if info.Module != "" && info.Module != c.currentModule && !info.Pub {
						c.report("cannot access private symbol %q from module %q", qualified, info.Module)
					}
				}
			}
			return b.typ, qualified
		}
	}
	// Also look up in imported modules!
	if c.currentModule != "" {
		if imps, exists := c.imports[c.currentModule]; exists {
			for _, impName := range imps {
				qualified := impName + "." + name
				if b, ok := c.scopes[0][qualified]; ok && b != nil {
					if c.symbolInfo != nil {
						if info, hasInfo := c.symbolInfo[qualified]; hasInfo {
							if info.Module != "" && info.Module != c.currentModule && !info.Pub {
								c.report("cannot access private symbol %q from module %q", qualified, info.Module)
							}
						}
					}
					c.markImportUsed(impName)
					return b.typ, qualified
				}
			}
		}
	}
	return nil, ""
}

// resolve looks up a name and returns its type.
func (c *Checker) resolve(name string) Type {
	typ, _ := c.resolveWithKey(name)
	if typ == nil && strings.Contains(name, ".") {
		// Fallback for mangled names that might have been qualified differently
		// e.g., reflect_test.TypeOf_reflect_test_TestStruct -> TypeOf_reflect_test_TestStruct
		if idx := strings.LastIndex(name, "."); idx >= 0 {
			typ, _ = c.resolveWithKey(name[idx+1:])
		}
	}
	return typ
}

// --- Public entry point ---

// setModuleFromDecl extracts the module name from a declaration
// for use during the second pass of type checking.
func (c *Checker) setModuleFromDecl(decl ast.Declaration) {
	if mod, ok := decl.(*ast.ModuleDecl); ok {
		c.currentModule = mod.Name
	}
}

// Check performs full type checking on a program.
func (c *Checker) CheckProgram(prog *ast.Program) error {
	// Pre-pass: collect all top-level type names.
	c.currentModule = ""
	for _, decl := range prog.Declarations {
		if mod, ok := decl.(*ast.ModuleDecl); ok {
			c.currentModule = mod.Name
			continue
		}
		c.setModuleFromDecl(decl)
		switch d := decl.(type) {
		case *ast.StructDecl:
			c.declare(d.Name, &NamedType{Name: d.Name})
			c.structs[d.Name] = d
		case *ast.EnumDecl:
			c.declare(d.Name, &NamedType{Name: d.Name})
		case *ast.ServiceDecl:
			if d.ForType == "" {
				c.declare(d.Name, &ServiceType{Name: d.Name, Methods: make(map[string]Type)})
			}
		case *ast.RulesetDecl:
			c.declare(d.Name, &RulesetType{Name: d.Name})
		case *ast.TemplateDecl:
			c.templates[d.Name] = d
		case *ast.TypeAliasDecl:
			resolved := c.astTypeToType(d.Type)
			if resolved != Unknown && resolved != nil {
				c.aliases[d.Name] = resolved
				c.declare(d.Name, resolved)
			}
		}
	}

	// First pass: register all top-level names and types.
	for _, decl := range prog.Declarations {
		if mod, ok := decl.(*ast.ModuleDecl); ok {
			c.currentModule = mod.Name
			continue
		}
		c.setModuleFromDecl(decl)

		switch d := decl.(type) {
		case *ast.ImportDecl:
			// Record the import for the current module.
			realName := d.Path
			if lastSlash := strings.LastIndex(realName, "/"); lastSlash != -1 {
				realName = realName[lastSlash+1:]
			}
			moduleKey := c.currentModule
			if moduleKey == "" {
				moduleKey = "main"
			}
			realName = strings.TrimSuffix(realName, ".skink")
			alias := d.Alias
			if alias == "" {
				alias = realName
			}
			exists := false
			for _, existing := range c.imports[moduleKey] {
				if existing == alias {
					exists = true
					break
				}
			}
			if exists {
				c.report("duplicate import alias %q in module %q", alias, moduleKey)
			} else {
				c.imports[moduleKey] = append(c.imports[moduleKey], alias)
			}
			if strings.HasPrefix(d.Path, "C:") {
				c.markImportUsed(alias)
			}
			// Record the alias mapping so module-qualified access can resolve
			// to the real module name.
			if d.Alias != "" && d.Alias != realName {
				c.importAliases[d.Alias] = realName
			}
		case *ast.FnDecl:
			fnType := c.fnTypeFromDecl(d)
			c.declare(d.Name, fnType)
		case *ast.ConstDecl:
			// Constants are typed by their value; defer exact type to second pass.
			c.declare(d.Name, Any)
		case *ast.ConstBlockDecl:
			for _, cd := range d.Decls {
				c.declare(cd.Name, Any)
			}
		case *ast.StructDecl:
			for _, m := range d.Methods {
				fnType := c.fnTypeFromDecl(m)
				c.declare(d.Name+"."+m.Name, fnType)
			}
		case *ast.EnumDecl:
			for i, v := range d.Variants {
				c.declare(v, Int)
				_ = i
			}

		case *ast.ServiceDecl:
			methods := make(map[string]Type)
			for _, m := range d.Methods {
				fnType := c.fnTypeFromDecl(m)
				methods[m.Name] = fnType
				if d.ForType == "" {
					c.declare(m.Name, fnType)
				}
			}
			if d.ForType == "" {
				svcType := &ServiceType{Name: d.Name, Methods: methods}
				// Update the already-declared ServiceType in scopes[0]
				if b, ok := c.scopes[0][d.Name]; ok {
					b.typ = svcType
				}
				c.services[d.Name] = svcType
			} else {
				for _, m := range d.Methods {
					fnType := c.fnTypeFromDecl(m)
					c.declare(d.ForType+"."+m.Name, fnType)
				}
			}
		case *ast.RulesetDecl:
			c.rulesets[d.Name] = &RulesetType{Name: d.Name}
			// Register implicit methods: start, stop, restart, reset.
			for _, m := range []string{"start", "stop", "restart", "reset"} {
				c.declare(d.Name+"."+m, &FunctionType{Params: []Type{}, Ret: []Type{Void}})
			}
			// Register dynamic rule methods.
			c.declare(d.Name+".registerRule", &FunctionType{
				Params: []Type{&TemplateType{Name: "RuleSource"}},
				Ret:    []Type{Void},
			})
			c.declare(d.Name+".registerRules", &FunctionType{
				Params: []Type{&ArrayType{Elem: &TemplateType{Name: "RuleSource"}}},
				Ret:    []Type{Void},
			})
		case *ast.TemplateDecl:
			c.templates[d.Name] = d
		case *ast.VarDecl:
			c.declare(d.Name, Any)
		case *ast.ExternFnDecl:
			fnType := c.externFnTypeFromDecl(d)
			c.declare(d.Name, fnType)
		}
	}

	// Second pass: evaluate const declarations first so their types are
	// known for functions that reference them (e.g. cimport constants).
	for _, decl := range prog.Declarations {
		if mod, ok := decl.(*ast.ModuleDecl); ok {
			c.currentModule = mod.Name
			continue
		}
		c.setModuleFromDecl(decl)
		if _, ok := decl.(*ast.ConstDecl); ok {
			c.checkDeclaration(decl)
		}
	}

	for _, decl := range prog.Declarations {
		if mod, ok := decl.(*ast.ModuleDecl); ok {
			c.currentModule = mod.Name
			continue
		}
		c.setModuleFromDecl(decl)
		if _, ok := decl.(*ast.ConstDecl); ok {
			continue
		}
		c.checkDeclaration(decl)
	}

	// Report any imports that were declared but never referenced. Blank imports
	// (alias "_") are permitted for side-effect initialization.
	for module, imps := range c.imports {
		for _, imp := range imps {
			if imp == "_" {
				continue
			}
			if !c.usedImports[imp] {
				c.report("unused import %q in module %q", imp, module)
			}
		}
	}

	if len(c.errors) > 0 {
		return fmt.Errorf("type errors: %s", strings.Join(c.errors, "\n"))
	}
	return nil
}

// markImportUsed records that an import alias (or default module name) has been referenced.
func (c *Checker) markImportUsed(alias string) {
	c.usedImports[alias] = true
}

// markInterpolatedVars scans a string literal for {name} interpolation
// placeholders and marks the referenced variables as used.
// Handles dotted access like {obj.field} by marking the base variable.
func (c *Checker) markInterpolatedVars(s string) {
	i := 0
	for i < len(s) {
		if s[i] == '{' {
			j := i + 1
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j < len(s) {
				id := s[i+1 : j]
				// Strip dotted suffix: {obj.field} -> mark "obj"
				if dotIdx := strings.Index(id, "."); dotIdx >= 0 {
					id = id[:dotIdx]
				}
				if id != "" && id != "_" {
					c.markVarUsed(id)
				}
				i = j + 1
				continue
			}
		}
		i++
	}
}

// --- Declarations ---

// checkDeclaration dispatches to the appropriate type checker
// for each kind of top-level declaration.
func (c *Checker) checkDeclaration(decl ast.Declaration) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		c.checkFnDecl(d)
	case *ast.ConstDecl:
		c.checkConstDecl(d)
	case *ast.ConstBlockDecl:
		for _, cd := range d.Decls {
			c.checkConstDecl(cd)
		}
	case *ast.StructDecl:
		c.checkStructDecl(d)
	case *ast.EnumDecl:
		// Enums are validated during the first pass.
	case *ast.ServiceDecl:
		for _, m := range d.Methods {
			c.checkFnDecl(m)
		}
	case *ast.RulesetDecl:
		for _, r := range d.Rules {
			condType := c.checkExpr(r.Condition)
			if condType != nil && !condType.Equals(Bool) && !condType.Equals(Unknown) {
				c.report("rule %q condition must be bool, got %s", r.Name, condType)
			}
			c.checkBlockStmt(r.Action)
		}
	case *ast.TemplateDecl:
		c.checkTemplateDecl(d)
	case *ast.ModuleDecl:
		// No type checking needed.
	case *ast.ImportDecl:
		// No type checking needed.
	case *ast.VarDecl:
		if d.Value != nil {
			valType := c.checkExpr(d.Value)
			if d.Type != nil {
				declared := c.astTypeToType(d.Type)
				if declared != nil && !valType.Equals(declared) && !valType.Equals(Unknown) {
					c.report("cannot assign %s to variable %q (type %s)", valType, d.Name, declared)
				}
				if b := c.scopes[0][d.Name]; b != nil {
					b.typ = declared
				} else {
					c.declare(d.Name, declared)
				}
			} else {
				if b := c.scopes[0][d.Name]; b != nil {
					b.typ = valType
				} else {
					c.declare(d.Name, valType)
				}
			}
		} else {
			if d.Type != nil {
				if b := c.scopes[0][d.Name]; b != nil {
					b.typ = c.astTypeToType(d.Type)
				} else {
					c.declare(d.Name, c.astTypeToType(d.Type))
				}
			} else {
				if b := c.scopes[0][d.Name]; b != nil {
					b.typ = Unknown
				} else {
					c.declare(d.Name, Unknown)
				}
			}
		}
	case *ast.ExternFnDecl:
		// No body to check; only signature matters.
	}
}

// checkConstDecl infers and records the type of a constant.
func (c *Checker) checkConstDecl(decl *ast.ConstDecl) {
	valType := c.checkExpr(decl.Value)
	if valType == nil || valType.Equals(Unknown) {
		c.report("cannot infer type for constant %q", decl.Name)
		return
	}
	// Update the constant's type in the top-level scope.
	if b := c.scopes[0][decl.Name]; b != nil {
		b.typ = valType
	} else {
		c.declare(decl.Name, valType)
	}
}

// checkStructDecl validates field types and method signatures.
func (c *Checker) checkStructDecl(decl *ast.StructDecl) {
	// Validate field types are resolvable.
	for _, field := range decl.Fields {
		if field.Type == nil {
			c.report("field %q in struct %q has no type", field.Name, decl.Name)
			continue
		}
		ft := c.astTypeToType(field.Type)
		if ft == nil || ft.Equals(Unknown) {
			c.report("unknown type for field %q in struct %q", field.Name, decl.Name)
		}
	}
	c.structs[decl.Name] = decl

	// Check methods
	for _, m := range decl.Methods {
		c.checkFnDecl(m)
	}
}

// checkTemplateDecl validates template method signatures.
func (c *Checker) checkTemplateDecl(decl *ast.TemplateDecl) {
	// Validate that each method signature has a self parameter of the template type.
	for _, m := range decl.Methods {
		if len(m.Params) == 0 {
			c.report("template method %q must have a receiver parameter", m.Name)
			continue
		}
		selfParam := m.Params[0]
		if selfParam.Name != "self" {
			c.report("template method %q first parameter must be named 'self'", m.Name)
		}
		// Type-check the method signature (no body to check).
		_ = c.fnTypeFromDecl(m)
	}
}

// checkFnDecl type-checks a function declaration and its body.
func (c *Checker) checkFnDecl(fn *ast.FnDecl) {
	fnType := c.fnTypeFromDecl(fn)
	oldFn := c.currentFn
	c.currentFn = fnType
	c.pushScope()
	// Declare parameters in the function scope.
	for i, p := range fn.Params {
		typ := c.astTypeToType(p.Type)
		if typ == nil {
			typ = Unknown
		}
		c.declareParam(p.Name, typ)
		fnType.Params[i] = typ
	}
	// Check body (if present — service interface methods have no body).
	if fn.Body != nil {
		c.checkBlockStmt(fn.Body)
	}
	c.popScope()
	c.currentFn = oldFn
}

// fnTypeFromDecl builds a FunctionType from an FnDecl AST node.
func (c *Checker) fnTypeFromDecl(fn *ast.FnDecl) *FunctionType {
	params := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = c.astTypeToType(p.Type)
		if params[i] == nil {
			params[i] = Unknown
		}
	}
	var ret []Type
	if fn.ReturnType != nil {
		if tup, ok := fn.ReturnType.(*ast.TupleType); ok {
			ret = make([]Type, len(tup.Types))
			for i, t := range tup.Types {
				ret[i] = c.astTypeToType(t)
				if ret[i] == nil {
					ret[i] = Unknown
				}
			}
		} else {
			ret = []Type{c.astTypeToType(fn.ReturnType)}
		}
	} else {
		ret = []Type{Void}
	}
	variadic := false
	for _, p := range fn.Params {
		if p.Variadic {
			variadic = true
			break
		}
	}
	return &FunctionType{Params: params, Ret: ret, Variadic: variadic}
}

// externFnTypeFromDecl builds a FunctionType from an ExternFnDecl AST node.
func (c *Checker) externFnTypeFromDecl(fn *ast.ExternFnDecl) *FunctionType {
	params := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = c.astTypeToType(p.Type)
		if params[i] == nil {
			params[i] = Unknown
		}
	}
	var ret []Type
	if fn.ReturnType != nil {
		ret = []Type{c.astTypeToType(fn.ReturnType)}
	} else {
		ret = []Type{Void}
	}
	return &FunctionType{Params: params, Ret: ret, Variadic: fn.Varargs}
}

// isAssignable wraps IsAssignable with interface and template duck-type support.
func (c *Checker) isAssignable(dst, src Type) bool {
	resolveAlias := func(t Type) Type {
		visited := make(map[string]bool)
		for {
			named, ok := t.(*NamedType)
			if !ok {
				return t
			}
			alias, ok := c.aliases[named.Name]
			if !ok {
				return t
			}
			if visited[named.Name] {
				return t
			}
			visited[named.Name] = true
			t = alias
		}
	}
	dst = resolveAlias(dst)
	src = resolveAlias(src)
	if IsAssignable(dst, src) {
		return true
	}
	// Check if src implements an interface dst.
	var ifaceMethods map[string]Type
	switch t := dst.(type) {
	case *InterfaceType:
		ifaceMethods = t.Methods
	case *ErrorType:
		ifaceMethods = t.Methods
	}
	if ifaceMethods != nil {
		var typeName string
		switch t := src.(type) {
		case *NamedType:
			typeName = t.Name
		case *PointerType:
			if nt, ok := t.Elem.(*NamedType); ok {
				typeName = nt.Name
			}
		}
		if typeName == "" {
			return false
		}
		for methodName := range ifaceMethods {
			found := false
			// Try fully qualified method name (module.Struct.Method).
			if c.resolve(typeName+"."+methodName) != nil {
				found = true
			}
			// Try module-prefixed method name (module.Method).
			if !found {
				parts := strings.Split(typeName, ".")
				if len(parts) >= 2 {
					moduleName := strings.Join(parts[:len(parts)-1], ".")
					if c.resolve(moduleName+"."+methodName) != nil {
						found = true
					}
				}
			}
			// Try unqualified method name (Struct.Method).
			if !found {
				parts := strings.Split(typeName, ".")
				if len(parts) > 0 {
					if c.resolve(parts[len(parts)-1]+"."+methodName) != nil {
						found = true
					}
				}
			}
			// Try bare method name.
			if !found && c.resolve(methodName) != nil {
				found = true
			}
			if !found {
				return false
			}
		}
		return true
	}
	// Check if src implements a template dst.
	tt, ok := dst.(*TemplateType)
	if !ok {
		return false
	}
	tmpl, exists := c.templates[tt.Name]
	if !exists {
		return false
	}
	// Extract the concrete type name from src.
	var typeName string
	switch t := src.(type) {
	case *NamedType:
		typeName = t.Name
	case *PointerType:
		if nt, ok := t.Elem.(*NamedType); ok {
			typeName = nt.Name
		}
	}
	if typeName == "" {
		return false
	}
	// Check that the concrete type has all required template methods.
	for _, tm := range tmpl.Methods {
		methodKey := typeName + "." + tm.Name
		if c.resolve(methodKey) == nil {
			return false
		}
	}
	return true
}

// astTypeToType converts an AST type node to a type-system Type.
func (c *Checker) astTypeToType(t ast.Type) Type {
	if t == nil {
		return Void
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		if c.currentModule != "" && c.currentModule != "main" && !strings.Contains(tt.Name, ".") {
			qualified := c.currentModule + "." + tt.Name
			if _, exists := c.scopes[0][qualified]; exists {
				tt.Name = qualified
				return LookupType(qualified)
			}
		}
		if c.currentModule != "" && !strings.Contains(tt.Name, ".") {
			if imps, exists := c.imports[c.currentModule]; exists {
				for _, impName := range imps {
					qualified := impName + "." + tt.Name
					if _, exists := c.scopes[0][qualified]; exists {
						tt.Name = qualified
						c.markImportUsed(impName)
						return LookupType(qualified)
					}
				}
			}
		}
		// Check if this is a template name.
		if _, isTemplate := c.templates[tt.Name]; isTemplate {
			return &TemplateType{Name: tt.Name}
		}
		// If the name is module-qualified (e.g., reader.Reader), check if
		// the unqualified name is a template and mark the import as used.
		if strings.Contains(tt.Name, ".") {
			parts := strings.SplitN(tt.Name, ".", 2)
			if len(parts) == 2 {
				alias, templateName := parts[0], parts[1]
				if _, isTemplate := c.templates[templateName]; isTemplate {
					c.markImportUsed(alias)
					return &TemplateType{Name: templateName}
				}
				c.markImportUsed(alias)
			}
		}
		// Resolve type aliases transparently.
		if aliased, ok := c.aliases[tt.Name]; ok {
			return aliased
		}
		return LookupType(tt.Name)
	case *ast.PointerType:
		return &PointerType{Elem: c.astTypeToType(tt.Elem)}
	case *ast.ArrayType:
		return &ArrayType{Elem: c.astTypeToType(tt.Elem)}
	case *ast.SetType:
		return &SetType{Elem: c.astTypeToType(tt.Elem)}
	case *ast.MapType:
		return &MapType{Key: c.astTypeToType(tt.Key), Elem: c.astTypeToType(tt.Elem)}
	case *ast.ChanType:
		return &ChanType{Elem: c.astTypeToType(tt.Elem)}
	case *ast.TensorType:
		return &TensorType{Elem: c.astTypeToType(tt.Elem)}
	case *ast.TupleType:
		types := make([]Type, len(tt.Types))
		for i, ty := range tt.Types {
			types[i] = c.astTypeToType(ty)
		}
		return &TupleType{Types: types}
	case *ast.FunctionType:
		params := make([]Type, len(tt.ParamTypes))
		for i, pt := range tt.ParamTypes {
			params[i] = c.astTypeToType(pt)
		}
		var ret []Type
		if tt.ReturnType != nil {
			ret = []Type{c.astTypeToType(tt.ReturnType)}
		} else {
			ret = []Type{Void}
		}
		return &FunctionType{Params: params, Ret: ret}
	}
	return Unknown
}

// --- Statements ---

// checkBlockStmt type-checks all statements in a block.
func (c *Checker) checkBlockStmt(block *ast.BlockStmt) {
	c.pushScope()
	for _, stmt := range block.Statements {
		c.checkStatement(stmt)
	}
	c.popScope()
}

// checkStatement dispatches type checking to the appropriate statement handler.
func (c *Checker) checkStatement(stmt ast.Statement) {
	switch s := stmt.(type) {
	case *ast.VarStmt:
		c.checkVarStmt(s)
	case *ast.AssignmentStmt:
		c.checkAssignmentStmt(s)
	case *ast.TupleAssignmentStmt:
		c.checkTupleAssignmentStmt(s)
	case *ast.TupleVarStmt:
		c.checkTupleVarStmt(s)
	case *ast.ExprStmt:
		c.checkExpr(s.Expr)
	case *ast.ReturnStmt:
		c.checkReturnStmt(s)
	case *ast.IfStmt:
		c.checkIfStmt(s)
	case *ast.WhileStmt:
		c.checkWhileStmt(s)
	case *ast.UntilStmt:
		c.checkUntilStmt(s)
	case *ast.DeferStmt:
		c.checkDeferStmt(s)
	case *ast.UnsafeStmt:
		c.checkUnsafeStmt(s)
	case *ast.VarBlockStmt:
		c.checkVarBlockStmt(s)
	case *ast.WithStmt:
		c.checkWithStmt(s)
	case *ast.ComptimeStmt:
		c.checkComptimeStmt(s)
	case *ast.SpawnStmt:
		c.checkSpawnStmt(s)
	case *ast.SelectStmt:
		c.checkSelectStmt(s)
	case *ast.SwitchStmt:
		c.checkSwitchStmt(s)
	case *ast.ForStmt:
		c.checkForStmt(s)
	case *ast.BlockStmt:
		c.checkBlockStmt(s)
	}
}

// checkAssignmentStmt type-checks an assignment statement.
func (c *Checker) checkAssignmentStmt(s *ast.AssignmentStmt) {
	lvalType := c.checkExpr(s.LValue)
	if lvalType == nil || lvalType.Equals(Unknown) {
		return
	}
	valType := c.checkExpr(s.Value)
	if valType == nil || valType.Equals(Unknown) {
		return
	}
	if s.Operator == "=" {
		if !c.isAssignable(lvalType, valType) {
			c.report("cannot assign %s to %s", valType, lvalType)
		}
	} else {
		// Compound assignment: +=, -=, &=, |=, etc.
		isBitwise := s.Operator == "&=" || s.Operator == "|=" || s.Operator == "^=" || s.Operator == "<<=" || s.Operator == ">>="
		if isBitwise {
			if !IsInteger(lvalType) || !IsInteger(valType) {
				c.report("bitwise compound assignment %s requires integer operands", s.Operator)
			}
		} else if !IsNumeric(lvalType) || !IsNumeric(valType) {
			c.report("compound assignment %s requires numeric operands", s.Operator)
		}
	}
}

// checkTupleAssignmentStmt type-checks a tuple assignment.
func (c *Checker) checkTupleAssignmentStmt(s *ast.TupleAssignmentStmt) {
	valType := c.checkExpr(s.Value)
	if valType == nil || valType.Equals(Unknown) {
		return
	}
	tup, ok := valType.(*TupleType)
	if !ok {
		c.report("cannot use tuple assignment with non-tuple value of type %s", valType)
		return
	}
	if len(s.LValues) != len(tup.Types) {
		c.report("tuple assignment count mismatch: expected %d, got %d", len(tup.Types), len(s.LValues))
		return
	}
	for i, lv := range s.LValues {
		lvalType := c.checkExpr(lv)
		if lvalType == nil || lvalType.Equals(Unknown) {
			continue
		}
		if !c.isAssignable(lvalType, tup.Types[i]) {
			c.report("cannot assign %s to %s in tuple assignment at position %d", tup.Types[i], lvalType, i)
		}
	}
}

// checkTupleVarStmt type-checks a tuple variable declaration.
func (c *Checker) checkTupleVarStmt(s *ast.TupleVarStmt) {
	valType := c.checkExpr(s.Value)
	if valType == nil || valType.Equals(Unknown) {
		return
	}
	tup, ok := valType.(*TupleType)
	if !ok {
		c.report("cannot use multi-variable declaration with non-tuple value of type %s", valType)
		return
	}
	if len(s.Names) != len(tup.Types) {
		c.report("multi-variable declaration count mismatch: expected %d values, got %d variables", len(tup.Types), len(s.Names))
		return
	}
	for i, name := range s.Names {
		c.declare(name, tup.Types[i])
	}
}

// checkWhileStmt type-checks a while loop.
func (c *Checker) checkWhileStmt(s *ast.WhileStmt) {
	condType := c.checkExpr(s.Condition)
	if condType != nil && !condType.Equals(Unknown) && !condType.Equals(Bool) {
		c.report("while condition must be bool, got %s", condType)
	}
	c.checkBlockStmt(s.Body)
}

// checkUntilStmt type-checks an until loop.
func (c *Checker) checkUntilStmt(s *ast.UntilStmt) {
	condType := c.checkExpr(s.Condition)
	if condType != nil && !condType.Equals(Unknown) && !condType.Equals(Bool) {
		c.report("until condition must be bool, got %s", condType)
	}
	c.checkBlockStmt(s.Body)
}

// checkDeferStmt type-checks a defer statement.
func (c *Checker) checkDeferStmt(s *ast.DeferStmt) {
	c.checkStatement(s.Statement)
}

// checkUnsafeStmt type-checks an unsafe block.
func (c *Checker) checkUnsafeStmt(s *ast.UnsafeStmt) {
	c.checkBlockStmt(s.Body)
}

// checkVarBlockStmt type-checks a var block statement.
func (c *Checker) checkVarBlockStmt(s *ast.VarBlockStmt) {
	for _, decl := range s.Decls {
		c.checkVarStmt(decl)
	}
}

// checkWithStmt type-checks a with statement.
func (c *Checker) checkWithStmt(s *ast.WithStmt) {
	valType := c.checkExpr(s.Value)
	c.declare(s.Name, valType)
	c.checkBlockStmt(s.Body)
}

// checkComptimeStmt type-checks a compile-time block.
func (c *Checker) checkComptimeStmt(s *ast.ComptimeStmt) {
	c.checkBlockStmt(s.Body)
}

// checkSpawnStmt type-checks a spawn statement.
func (c *Checker) checkSpawnStmt(s *ast.SpawnStmt) {
	c.checkExpr(s.Call)
}

// checkSelectStmt type-checks a select statement.
func (c *Checker) checkSelectStmt(s *ast.SelectStmt) {
	for _, sc := range s.Cases {
		if !sc.IsDefault && sc.Condition != nil {
			condType := c.checkExpr(sc.Condition)
			// If this is a receive-binding case (case val := <-ch), declare val.
			if sc.RecvVar != "" && sc.RecvVar != "_" {
				if prefix, ok := sc.Condition.(*ast.PrefixExpr); ok && prefix.Operator == "<-" {
					chElemType := c.checkExpr(prefix.Right)
					if ch, ok := chElemType.(*ChanType); ok {
						c.pushScope()
						c.declare(sc.RecvVar, ch.Elem)
						c.checkBlockStmt(sc.Body)
						c.popScope()
						continue
					}
				}
				c.pushScope()
				c.declare(sc.RecvVar, Unknown)
				c.checkBlockStmt(sc.Body)
				c.popScope()
				continue
			}
			_ = condType
		}
		c.checkBlockStmt(sc.Body)
	}
}

// checkSwitchStmt type-checks a switch statement.
func (c *Checker) checkSwitchStmt(s *ast.SwitchStmt) {
	subjType := c.checkExpr(s.Subject)
	hasDefault := false
	for _, sc := range s.Cases {
		if sc.IsDefault {
			hasDefault = true
		}
		for _, v := range sc.Values {
			valType := c.checkExpr(v)
			if subjType != nil && valType != nil && !subjType.Equals(Unknown) && !valType.Equals(Unknown) {
				if !subjType.Equals(valType) {
					c.report("switch case value type %s does not match subject type %s", valType, subjType)
				}
			}
		}
		c.checkBlockStmt(sc.Body)
	}
	if !hasDefault {
		c.report("switch statement must have a default case")
	}
}

// checkAsyncExpr type-checks an async expression.
func (c *Checker) checkAsyncExpr(e *ast.AsyncExpr) Type {
	return c.checkExpr(e.Expr)
}

// checkAwaitExpr type-checks an await expression.
func (c *Checker) checkAwaitExpr(e *ast.AwaitExpr) Type {
	return c.checkExpr(e.Expr)
}

// checkForStmt type-checks a for loop.
func (c *Checker) checkForStmt(s *ast.ForStmt) {
	c.pushScope()
	if s.Iterator != nil {
		// for x in iterable { }
		iterType := c.checkExpr(s.Iterator.Iterable)
		if iterType != nil {
			// If iterable is an array, element type is the array element type.
			if arr, ok := iterType.(*ArrayType); ok {
				c.declare(s.Iterator.Variable, arr.Elem)
			} else if ch, ok := iterType.(*ChanType); ok {
				c.declare(s.Iterator.Variable, ch.Elem)
			} else {
				// For now, declare iterator variable as any until we have a generic iterator protocol.
				c.declare(s.Iterator.Variable, Any)
			}
		}
		c.checkBlockStmt(s.Body)
	} else {
		// C-style for loop
		if s.Init != nil {
			c.checkStatement(s.Init)
		}
		if s.Condition != nil {
			condType := c.checkExpr(s.Condition)
			if condType != nil && !condType.Equals(Unknown) && !condType.Equals(Bool) {
				c.report("for condition must be bool, got %s", condType)
			}
		}
		if s.Post != nil {
			c.checkStatement(s.Post)
		}
		c.checkBlockStmt(s.Body)
	}
	c.popScope()
}

// checkVarStmt type-checks a variable declaration.
func (c *Checker) checkVarStmt(v *ast.VarStmt) {
	if v.Type != nil {
		annotType := c.astTypeToType(v.Type)
		if v.Value != nil {
			valType := c.checkExpr(v.Value)
			if !c.isAssignable(annotType, valType) {
				c.report("cannot assign %s to %s for variable %q", valType, annotType, v.Name)
			}
		}
		c.declare(v.Name, annotType)
	} else if v.Implicit {
		// Infer from value.
		valType := c.checkExpr(v.Value)
		if valType == nil || valType.Equals(Unknown) {
			c.report("cannot infer type for variable %q", v.Name)
			c.declare(v.Name, Unknown)
		} else {
			c.declare(v.Name, valType)
		}
	} else if v.Value != nil {
		valType := c.checkExpr(v.Value)
		if valType == nil || valType.Equals(Unknown) {
			c.report("cannot infer type for variable %q", v.Name)
			c.declare(v.Name, Unknown)
		} else {
			c.declare(v.Name, valType)
		}
	} else {
		c.declare(v.Name, Unknown)
	}
}

// checkReturnStmt type-checks a return statement against the current function signature.
func (c *Checker) checkReturnStmt(r *ast.ReturnStmt) {
	if c.currentFn == nil {
		c.report("return outside of function")
		return
	}
	expected := c.currentFn.Ret
	if len(expected) == 1 {
		exp := expected[0]
		if len(r.Values) == 0 {
			if !exp.Equals(Void) {
				c.report("missing return value (expected %s)", exp)
			}
			return
		}
		if len(r.Values) > 1 {
			c.report("too many return values (expected 1)")
			return
		}
		actual := c.checkExpr(r.Values[0])
		if actual != nil && !actual.Equals(Unknown) && !c.isAssignable(exp, actual) {
			c.report("return type mismatch: expected %s, got %s", exp, actual)
		}
		return
	}
	// Multiple return values.
	if len(r.Values) == 1 {
		// Special case: return foo() where foo returns a tuple.
		if call, ok := r.Values[0].(*ast.CallExpr); ok {
			actual := c.checkExpr(call)
			if tup, ok2 := actual.(*TupleType); ok2 && len(tup.Types) == len(expected) {
				for i, t := range tup.Types {
					if !c.isAssignable(expected[i], t) {
						c.report("return type mismatch at position %d: expected %s, got %s", i, expected[i], t)
					}
				}
				return
			}
		}
	}
	if len(r.Values) != len(expected) {
		c.report("return value count mismatch: expected %d, got %d", len(expected), len(r.Values))
		return
	}
	for i, val := range r.Values {
		actual := c.checkExpr(val)
		if actual != nil && !actual.Equals(Unknown) && !c.isAssignable(expected[i], actual) {
			c.report("return type mismatch at position %d: expected %s, got %s", i, expected[i], actual)
		}
	}
}

// checkIfStmt type-checks an if statement.
func (c *Checker) checkIfStmt(i *ast.IfStmt) {
	condType := c.checkExpr(i.Condition)
	if condType != nil && !condType.Equals(Unknown) && !condType.Equals(Bool) {
		c.report("if condition must be bool, got %s", condType)
	}
	c.checkBlockStmt(i.Consequence)
	if i.Alternative != nil {
		c.checkStatement(i.Alternative)
	}
}

// --- Expressions ---

// checkExpr infers and returns the type of an expression.
// It recursively walks the expression AST, performing type checks
// and reporting errors for any mismatches.
func (c *Checker) checkExpr(expr ast.Expression) Type {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return Int // literals are typed as int; they narrow on assignment.
	case *ast.FloatLiteral:
		return Float
	case *ast.StringLiteral:
		c.markInterpolatedVars(e.Value)
		return String
	case *ast.BooleanLiteral:
		return Bool
	case *ast.NilLiteral:
		return Nil
	case *ast.Identifier:
		typ, qName := c.resolveWithKey(e.Value)
		if typ == nil {
			c.report("undefined variable: %s", e.Value)
			return Unknown
		}
		if qName != "" && qName != e.Value {
			e.Value = qName
		}
		return typ
	case *ast.PrefixExpr:
		return c.checkPrefixExpr(e)
	case *ast.InfixExpr:
		return c.checkInfixExpr(e)
	case *ast.CallExpr:
		return c.checkCallExpr(e)
	case *ast.ArrayLiteral:
		return c.checkArrayLiteral(e)
	case *ast.MapLiteral:
		return c.checkMapLiteral(e)
	case *ast.SetLiteral:
		return c.checkSetLiteral(e)
	case *ast.SpreadExpr:
		return c.checkExpr(e.Operand)
	case *ast.IndexExpr:
		return c.checkIndexExpr(e)
	case *ast.SliceExpr:
		return c.checkSliceExpr(e)
	case *ast.RangeExpr:
		return c.checkRangeExpr(e)
	case *ast.FromEndIndexExpr:
		operandType := c.checkExpr(e.Operand)
		if operandType != nil && !IsInteger(operandType) {
			c.report("from-end index operand must be integer, got %s", operandType)
		}
		return Int
	case *ast.FieldAccessExpr:
		return c.checkFieldAccessExpr(e)
	case *ast.StructInitExpr:
		return c.checkStructInitExpr(e)
	case *ast.ErrorPropagationExpr:
		return c.checkErrorPropagationExpr(e)
	case *ast.AsyncExpr:
		return c.checkAsyncExpr(e)
	case *ast.AwaitExpr:
		return c.checkAwaitExpr(e)
	case *ast.SizeofExpr:
		return Int
	case *ast.AlignofExpr:
		return Int
	case *ast.MinExpr:
		return Int
	case *ast.MaxExpr:
		return Int
	case *ast.MakeExpr:
		if e.Capacity != nil {
			capType := c.checkExpr(e.Capacity)
			if capType != nil && !IsInteger(capType) {
				c.report("channel capacity must be an integer, got %s", capType)
			}
		}
		return c.astTypeToType(e.Type)
	case *ast.IfExpr:
		return c.checkIfExpr(e)
	case *ast.MatchExpr:
		return c.checkMatchExpr(e)
	case *ast.FnLiteral:
		return c.checkFnLiteral(e)
	case *ast.QueryExpr:
		return c.checkQueryExpr(e)
	}
	return Unknown
}

// checkQueryExpr type-checks a LINQ-style query expression.
func (c *Checker) checkQueryExpr(e *ast.QueryExpr) Type {
	// Check the from clause: determine element type from iterable.
	iterableType := c.checkExpr(e.From.Iterable)
	if iterableType == nil || iterableType.Equals(Unknown) {
		return Unknown
	}
	var elemType Type
	switch lt := iterableType.(type) {
	case *ArrayType:
		elemType = lt.Elem
	case *MapType:
		elemType = lt.Elem
	case *SetType:
		elemType = lt.Elem
	case *BuiltInType:
		if lt.Name == "string" {
			elemType = Int
		}
	}
	if elemType == nil {
		c.report("cannot query over type %s", iterableType)
		return Unknown
	}

	// Push the range variable into scope.
	c.pushScope()
	c.declare(e.From.Variable, elemType)

	// Check each clause.
	for _, clause := range e.Clauses {
		switch cl := clause.(type) {
		case *ast.WhereClause:
			condType := c.checkExpr(cl.Condition)
			if condType != nil && !condType.Equals(Unknown) && !condType.Equals(Bool) {
				c.report("where condition must be bool, got %s", condType)
			}
		case *ast.OrderByClause:
			c.checkExpr(cl.Key)
		case *ast.GroupByClause:
			c.checkExpr(cl.Key)
		case *ast.JoinClause:
			joinType := c.checkExpr(cl.Source)
			var joinElemType Type
			switch lt := joinType.(type) {
			case *ArrayType:
				joinElemType = lt.Elem
			case *MapType:
				joinElemType = lt.Elem
			case *SetType:
				joinElemType = lt.Elem
			}
			if joinElemType == nil {
				c.report("cannot join over type %s", joinType)
			}
			c.declare(cl.Variable, joinElemType)
			if cl.LeftKey != nil {
				c.checkExpr(cl.LeftKey)
			}
			if cl.RightKey != nil {
				c.checkExpr(cl.RightKey)
			}
		}
	}

	// Check select expression.
	resultType := c.checkExpr(e.Select.Expression)
	c.popScope()

	return &ArrayType{Elem: resultType}
}

// checkArrayLiteral type-checks an array literal.
func (c *Checker) checkArrayLiteral(e *ast.ArrayLiteral) Type {
	if len(e.Elements) == 0 {
		if e.Type != nil {
			return &ArrayType{Elem: c.astTypeToType(e.Type)}
		}
		return &ArrayType{Elem: Any}
	}
	var firstType Type
	for i, elem := range e.Elements {
		if spread, ok := elem.(*ast.SpreadExpr); ok {
			spreadType := c.checkExpr(spread.Operand)
			arrType, isArray := spreadType.(*ArrayType)
			if !isArray {
				c.report("spread operand must be an array, got %s", spreadType)
				if firstType == nil {
					firstType = Any
				}
				continue
			}
			if firstType == nil {
				firstType = arrType.Elem
			} else if !firstType.Equals(arrType.Elem) {
				c.report("spread element type mismatch: expected %s, got %s", firstType, arrType.Elem)
			}
			continue
		}
		elemType := c.checkExpr(elem)
		if firstType == nil {
			firstType = elemType
		} else if !firstType.Equals(elemType) {
			c.report("array element %d type mismatch: expected %s, got %s", i+1, firstType, elemType)
		}
	}
	if firstType == nil {
		firstType = Any
	}
	return &ArrayType{Elem: firstType}
}

// checkMapLiteral type-checks a map literal.
func (c *Checker) checkMapLiteral(e *ast.MapLiteral) Type {
	if len(e.Pairs) == 0 {
		return &MapType{Key: Any, Elem: Any}
	}
	firstKeyType := c.checkExpr(e.Pairs[0].Key)
	firstValType := c.checkExpr(e.Pairs[0].Value)
	for i, pair := range e.Pairs[1:] {
		keyType := c.checkExpr(pair.Key)
		valType := c.checkExpr(pair.Value)
		if !firstKeyType.Equals(keyType) {
			c.report("map key %d type mismatch: expected %s, got %s", i+2, firstKeyType, keyType)
		}
		if !firstValType.Equals(valType) {
			c.report("map value %d type mismatch: expected %s, got %s", i+2, firstValType, valType)
		}
	}
	return &MapType{Key: firstKeyType, Elem: firstValType}
}

// checkSetLiteral type-checks a set literal.
func (c *Checker) checkSetLiteral(e *ast.SetLiteral) Type {
	if len(e.Elements) == 0 {
		return &SetType{Elem: Any}
	}
	firstType := c.checkExpr(e.Elements[0])
	for i, elem := range e.Elements[1:] {
		elemType := c.checkExpr(elem)
		if !firstType.Equals(elemType) {
			c.report("set element %d type mismatch: expected %s, got %s", i+2, firstType, elemType)
		}
	}
	return &SetType{Elem: firstType}
}

// checkIndexExpr type-checks an index expression.
func (c *Checker) checkIndexExpr(e *ast.IndexExpr) Type {
	leftType := c.checkExpr(e.Left)
	indexType := c.checkExpr(e.Index)
	if leftType == nil || leftType.Equals(Unknown) {
		return Unknown
	}
	switch lt := leftType.(type) {
	case *ArrayType:
		if indexType != nil && !IsInteger(indexType) {
			c.report("array index must be integer, got %s", indexType)
		}
		return lt.Elem
	case *MapType:
		if indexType != nil && !lt.Key.Equals(indexType) && !c.isAssignable(lt.Key, indexType) {
			c.report("map key type mismatch: expected %s, got %s", lt.Key, indexType)
		}
		return lt.Elem
	case *PointerType:
		if indexType != nil && !IsInteger(indexType) {
			c.report("pointer index must be integer, got %s", indexType)
		}
		return lt.Elem
	case *BuiltInType:
		if lt.Name == "string" {
			if indexType != nil && !IsInteger(indexType) {
				c.report("string index must be integer, got %s", indexType)
			}
			return Int // byte value
		}
		c.report("cannot index type %s", leftType)
		return Unknown
	default:
		c.report("cannot index type %s", leftType)
		return Unknown
	}
}

// checkSliceExpr type-checks a slice expression.
func (c *Checker) checkSliceExpr(e *ast.SliceExpr) Type {
	leftType := c.checkExpr(e.Left)
	var startType, endType Type
	if e.Start != nil {
		startType = c.checkExpr(e.Start)
		if startType != nil && !IsInteger(startType) {
			c.report("slice start must be integer, got %s", startType)
		}
	}
	if e.End != nil {
		endType = c.checkExpr(e.End)
		if endType != nil && !IsInteger(endType) {
			c.report("slice end must be integer, got %s", endType)
		}
	}
	switch lt := leftType.(type) {
	case *ArrayType:
		return lt
	case *BuiltInType:
		if lt.Name == "string" {
			return String
		}
		c.report("cannot slice type %s", leftType)
		return Unknown
	default:
		c.report("cannot slice type %s", leftType)
		return Unknown
	}
}

// checkRangeExpr type-checks a range expression.
func (c *Checker) checkRangeExpr(e *ast.RangeExpr) Type {
	startType := c.checkExpr(e.Start)
	endType := c.checkExpr(e.End)
	if startType != nil && !IsInteger(startType) {
		c.report("range start must be integer, got %s", startType)
	}
	if endType != nil && !IsInteger(endType) {
		c.report("range end must be integer, got %s", endType)
	}
	return &ArrayType{Elem: Int}
}

// checkPrefixExpr type-checks a prefix expression.
func (c *Checker) checkPrefixExpr(e *ast.PrefixExpr) Type {
	operand := c.checkExpr(e.Right)
	if operand.Equals(Unknown) {
		return Unknown
	}
	switch e.Operator {
	case "-", "+":
		if !IsNumeric(operand) {
			c.report("unary %s requires numeric operand, got %s", e.Operator, operand)
			return Unknown
		}
		return operand
	case "!":
		if !operand.Equals(Bool) {
			c.report("! requires bool, got %s", operand)
			return Unknown
		}
		return Bool
	case "~":
		if !IsInteger(operand) {
			c.report("~ requires integer, got %s", operand)
			return Unknown
		}
		return operand
	case "*":
		// Dereference: operand must be a pointer.
		ptr, ok := operand.(*PointerType)
		if !ok {
			c.report("cannot dereference non-pointer %s", operand)
			return Unknown
		}
		return ptr.Elem
	case "&":
		// Address-of: result is pointer to operand type.
		return &PointerType{Elem: operand}
	case "<-":
		// Channel receive: operand must be a channel.
		if ch, ok := operand.(*ChanType); ok {
			return ch.Elem
		}
		c.report("cannot receive from non-channel %s", operand)
		return Unknown
	default:
		c.report("unknown prefix operator: %s", e.Operator)
		return Unknown
	}
}

// checkFnLiteral type-checks an anonymous function literal.
func (c *Checker) checkFnLiteral(e *ast.FnLiteral) Type {
	params := make([]Type, len(e.Params))
	for i, p := range e.Params {
		pt := c.astTypeToType(p.Type)
		if pt == nil {
			pt = Unknown
		}
		params[i] = pt
	}
	var ret Type = Void
	if e.ReturnType != nil {
		ret = c.astTypeToType(e.ReturnType)
		if ret == nil {
			ret = Unknown
		}
	}
	// Check body in a new scope with parameters declared.
	c.pushScope()
	for i, p := range e.Params {
		c.declareParam(p.Name, params[i])
	}
	c.checkBlockStmt(e.Body)
	c.popScope()
	e.Captures = findCaptures(e.Body, e.Params)
	return &FunctionType{Params: params, Ret: []Type{ret}}
}

// findCaptures returns the names of variables referenced in body that are not
// declared as parameters or local variables within the body.
func findCaptures(body *ast.BlockStmt, params []*ast.Param) []string {
	locals := make(map[string]bool)
	for _, p := range params {
		locals[p.Name] = true
	}
	captures := make(map[string]bool)
	collectFreeVars(body, locals, captures)
	result := make([]string, 0, len(captures))
	for name := range captures {
		result = append(result, name)
	}
	return result
}

// collectFreeVars walks node, tracking local declarations and recording free variables.
func collectFreeVars(node ast.Node, locals map[string]bool, captures map[string]bool) {
	switch n := node.(type) {
	case *ast.Identifier:
		if !locals[n.Value] {
			captures[n.Value] = true
		}
	case *ast.BlockStmt:
		childLocals := make(map[string]bool)
		for k, v := range locals {
			childLocals[k] = v
		}
		for _, stmt := range n.Statements {
			collectFreeVars(stmt, childLocals, captures)
		}
	case *ast.VarStmt:
		if n.Value != nil {
			collectFreeVars(n.Value, locals, captures)
		}
		locals[n.Name] = true
	case *ast.TupleVarStmt:
		for _, name := range n.Names {
			locals[name] = true
		}
		if n.Value != nil {
			collectFreeVars(n.Value, locals, captures)
		}
	case *ast.AssignmentStmt:
		collectFreeVars(n.LValue, locals, captures)
		collectFreeVars(n.Value, locals, captures)
	case *ast.TupleAssignmentStmt:
		for _, lv := range n.LValues {
			collectFreeVars(lv, locals, captures)
		}
		collectFreeVars(n.Value, locals, captures)
	case *ast.ExprStmt:
		collectFreeVars(n.Expr, locals, captures)
	case *ast.ReturnStmt:
		for _, v := range n.Values {
			collectFreeVars(v, locals, captures)
		}
	case *ast.IfStmt:
		collectFreeVars(n.Condition, locals, captures)
		collectFreeVars(n.Consequence, locals, captures)
		if n.Alternative != nil {
			collectFreeVars(n.Alternative, locals, captures)
		}
	case *ast.ForStmt:
		childLocals := make(map[string]bool)
		for k, v := range locals {
			childLocals[k] = v
		}
		if n.Iterator != nil {
			childLocals[n.Iterator.Variable] = true
			collectFreeVars(n.Iterator.Iterable, locals, captures)
		} else if n.Init != nil {
			collectFreeVars(n.Init, childLocals, captures)
		}
		if n.Condition != nil {
			collectFreeVars(n.Condition, childLocals, captures)
		}
		if n.Post != nil {
			collectFreeVars(n.Post, childLocals, captures)
		}
		collectFreeVars(n.Body, childLocals, captures)
	case *ast.SwitchStmt:
		collectFreeVars(n.Subject, locals, captures)
		for _, c := range n.Cases {
			for _, v := range c.Values {
				collectFreeVars(v, locals, captures)
			}
			collectFreeVars(c.Body, locals, captures)
		}
	case *ast.SelectStmt:
		for _, c := range n.Cases {
			collectFreeVars(c.Body, locals, captures)
		}
	case *ast.DeferStmt:
		collectFreeVars(n.Statement, locals, captures)
	case *ast.SpawnStmt:
		collectFreeVars(n.Call, locals, captures)
	case *ast.InfixExpr:
		collectFreeVars(n.Left, locals, captures)
		collectFreeVars(n.Right, locals, captures)
	case *ast.PrefixExpr:
		collectFreeVars(n.Right, locals, captures)
	case *ast.CallExpr:
		collectFreeVars(n.Function, locals, captures)
		for _, arg := range n.Arguments {
			collectFreeVars(arg, locals, captures)
		}
	case *ast.IndexExpr:
		collectFreeVars(n.Left, locals, captures)
		collectFreeVars(n.Index, locals, captures)
	case *ast.SliceExpr:
		collectFreeVars(n.Left, locals, captures)
		if n.Start != nil {
			collectFreeVars(n.Start, locals, captures)
		}
		if n.End != nil {
			collectFreeVars(n.End, locals, captures)
		}
	case *ast.FieldAccessExpr:
		collectFreeVars(n.Left, locals, captures)
	case *ast.ArrayLiteral:
		for _, el := range n.Elements {
			collectFreeVars(el, locals, captures)
		}
	case *ast.MapLiteral:
		for _, p := range n.Pairs {
			collectFreeVars(p.Key, locals, captures)
			collectFreeVars(p.Value, locals, captures)
		}
	case *ast.SetLiteral:
		for _, el := range n.Elements {
			collectFreeVars(el, locals, captures)
		}
	case *ast.StructInitExpr:
		for _, fv := range n.Fields {
			collectFreeVars(fv, locals, captures)
		}
	case *ast.IfExpr:
		collectFreeVars(n.Condition, locals, captures)
		collectFreeVars(n.Consequence, locals, captures)
		if n.Alternative != nil {
			collectFreeVars(n.Alternative, locals, captures)
		}
	case *ast.FnLiteral:
		// Don't recurse into nested functions; they have their own captures.
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.BooleanLiteral,
		*ast.StringLiteral, *ast.NilLiteral, *ast.ErrorPropagationExpr,
		*ast.AsyncExpr, *ast.AwaitExpr, *ast.SpreadExpr, *ast.FromEndIndexExpr,
		*ast.RangeExpr, *ast.MakeExpr:
		// No free variables.
	}
}

// checkInfixExpr type-checks a binary infix expression.
func (c *Checker) checkInfixExpr(e *ast.InfixExpr) Type {
	left := c.checkExpr(e.Left)
	right := c.checkExpr(e.Right)

	// Suppress cascading errors if either side is already Unknown.
	if left.Equals(Unknown) || right.Equals(Unknown) {
		return Unknown
	}

	// Set operations take priority over bitwise/arithmetic.
	if e.Operator == "&" || e.Operator == "|" || e.Operator == "-" {
		if st, ok := left.(*SetType); ok {
			if rt, ok := right.(*SetType); ok {
				if !st.Elem.Equals(rt.Elem) {
					c.report("set operation %s requires matching element types, got %s and %s", e.Operator, st.Elem, rt.Elem)
					return Unknown
				}
				return left
			}
			c.report("set operation %s requires two sets, got %s and %s", e.Operator, left, right)
			return Unknown
		}
	}

	switch e.Operator {
	case "+", "-", "*", "/", "%":
		// + also supports string concatenation.
		if e.Operator == "+" && left.Equals(String) && right.Equals(String) {
			return String
		}
		// Pointer arithmetic: ptr +/- int
		if e.Operator == "+" || e.Operator == "-" {
			_, leftPtr := left.(*PointerType)
			_, rightPtr := right.(*PointerType)
			if leftPtr && IsInteger(right) {
				return left
			}
			if rightPtr && IsInteger(left) && e.Operator == "+" {
				return right
			}
		}
		if !IsNumeric(left) || !IsNumeric(right) {
			c.report("operator %s requires numeric operands, got %s and %s", e.Operator, left, right)
			return Unknown
		}
		// Promote to float if either operand is float.
		if left.Equals(Float) || right.Equals(Float) {
			return Float
		}
		return left
	case "**":
		if !IsNumeric(left) || !IsNumeric(right) {
			c.report("** requires numeric operands")
			return Unknown
		}
		if left.Equals(Float) || right.Equals(Float) {
			return Float
		}
		return left
	case "<", ">", "<=", ">=":
		if !IsNumeric(left) || !IsNumeric(right) {
			// Also allow string comparisons.
			if !left.Equals(String) || !right.Equals(String) {
				c.report("comparison requires numeric or string operands, got %s and %s", left, right)
				return Unknown
			}
		}
		return Bool
	case "==", "!=":
		if IsNumeric(left) && IsNumeric(right) {
			return Bool
		}
		// Allow nil comparison with error, pointer, and string types.
		if left.Equals(Nil) || right.Equals(Nil) {
			if left.Equals(Nil) && right.Equals(Nil) {
				return Bool
			}
			var nonNil Type
			if left.Equals(Nil) {
				nonNil = right
			} else {
				nonNil = left
			}
			if nonNil.Equals(Error) || nonNil.Equals(String) {
				return Bool
			}
			if _, ok := nonNil.(*PointerType); ok {
				return Bool
			}
			if _, ok := nonNil.(*ArrayType); ok {
				return Bool
			}
			if _, ok := nonNil.(*ChanType); ok {
				return Bool
			}
			if _, ok := nonNil.(*MapType); ok {
				return Bool
			}
			if _, ok := nonNil.(*FunctionType); ok {
				return Bool
			}
			c.report("cannot compare %s with nil", nonNil)
			return Unknown
		}
		if !left.Equals(right) {
			c.report("cannot compare %s with %s", left, right)
			return Unknown
		}
		return Bool
	case "&&", "||":
		if !left.Equals(Bool) || !right.Equals(Bool) {
			c.report("logical operator %s requires bool operands", e.Operator)
			return Unknown
		}
		return Bool
	case "in":
		if st, ok := right.(*SetType); ok {
			if !left.Equals(st.Elem) {
				c.report("cannot check membership of %s in set<%s>", left, st.Elem)
				return Unknown
			}
			return Bool
		}
		c.report("'in' requires a set on the right side, got %s", right)
		return Unknown
	case "&", "|":
		if !IsInteger(left) || !IsInteger(right) {
			c.report("bitwise operator %s requires integer operands", e.Operator)
			return Unknown
		}
		return left
	case "^", "<<", ">>":
		if !IsInteger(left) || !IsInteger(right) {
			c.report("bitwise operator %s requires integer operands", e.Operator)
			return Unknown
		}
		return left
	case "@":
		if _, ok := left.(*TensorType); !ok {
			c.report("operator @ requires tensor operands, got %s", left)
			return Unknown
		}
		if _, ok := right.(*TensorType); !ok {
			c.report("operator @ requires tensor operands, got %s", right)
			return Unknown
		}
		return left
	case "<-":
		// Channel send: left must be channel, right must match element type.
		ch, ok := left.(*ChanType)
		if !ok {
			c.report("cannot send on non-channel %s", left)
			return Unknown
		}
		if !ch.Elem.Equals(right) {
			c.report("cannot send %s on channel of %s", right, ch.Elem)
			return Unknown
		}
		return Void
	case "=":
		// Assignment expression: left must be assignable to right's type.
		if !c.isAssignable(left, right) {
			c.report("cannot assign %s to %s in expression", right, left)
			return Unknown
		}
		return right
	default:
		c.report("unknown infix operator: %s", e.Operator)
		return Unknown
	}
}

// resolveImportAlias returns the real module name if name is an import alias,
// otherwise returns name unchanged.
func (c *Checker) resolveImportAlias(name string) string {
	if realName, ok := c.importAliases[name]; ok {
		return realName
	}
	return name
}

// leftmostIdent returns the leftmost identifier in a chain of field accesses,
// or the identifier itself if the expression is a plain identifier. It returns
// "" for other expression types.
func leftmostIdent(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.Identifier:
		return e.Value
	case *ast.FieldAccessExpr:
		return leftmostIdent(e.Left)
	}
	return ""
}

// checkFieldAccessExpr resolves obj.Field by looking up the struct type
// and finding the field's type.
func (c *Checker) checkFieldAccessExpr(e *ast.FieldAccessExpr) Type {
	// Handle module-qualified identifiers: moduleName.functionName
	// This must be checked BEFORE calling checkExpr on the left side,
	// since module names are not declared variables.
	if ident, ok := e.Left.(*ast.Identifier); ok {
		moduleName := c.resolveImportAlias(ident.Value)
		// Check if this identifier is an import of the current module (alias or real name).
		isModuleName := false
		if c.currentModule != "" {
			if imps, exists := c.imports[c.currentModule]; exists {
				for _, imp := range imps {
					if imp == ident.Value {
						isModuleName = true
						break
					}
				}
			}
		}
		// Fallback: check if it matches a known module name from symbolInfo.
		if !isModuleName {
			for _, info := range c.symbolInfo {
				if info.Module == moduleName {
					isModuleName = true
					break
				}
			}
		}
		if isModuleName {
			// Record that this module import (or alias) is used.
			c.markImportUsed(ident.Value)
			// Look up the field directly in the global scope.
			fqName := moduleName + "." + e.Field
			if typ := c.resolve(fqName); typ != nil {
				// Verify the symbol actually belongs to this module.
				if info, ok := c.symbolInfo[fqName]; ok && info.Module == moduleName {
					return typ
				}
			}
			if typ := c.resolve(e.Field); typ != nil {
				// Verify the symbol actually belongs to this module.
				if info, ok := c.symbolInfo[e.Field]; ok && info.Module == moduleName {
					return typ
				}
			}
			c.report("module %q has no exported symbol %q", moduleName, e.Field)
			return Unknown
		}
	}

	leftType := c.checkExpr(e.Left)
	if leftType == nil || leftType.Equals(Unknown) {
		return Unknown
	}

	// Error interface method access: err.String
	if et, ok := leftType.(*ErrorType); ok {
		if mt, found := et.Methods[e.Field]; found {
			return mt
		}
		c.report("error interface has no method %q", e.Field)
		return Unknown
	}

	// Tensor transpose access: A.T
	if tt, ok := leftType.(*TensorType); ok {
		if e.Field == "T" {
			return tt
		}
		c.report("tensor type has no property %q other than .T (transpose)", e.Field)
		return Unknown
	}
	// Service method access: ServiceName.methodName
	if svc, ok := leftType.(*ServiceType); ok {
		if mt, ok := svc.Methods[e.Field]; ok {
			return mt
		}
		c.report("service %q has no method %q", svc.Name, e.Field)
		return Unknown
	}
	// Ruleset method access: RulesetName.methodName
	if rs, ok := leftType.(*RulesetType); ok {
		fnType := c.resolve(rs.Name + "." + e.Field)
		if fnType != nil {
			return fnType
		}
		c.report("ruleset %q has no method %q", rs.Name, e.Field)
		return Unknown
	}
	// leftType is a NamedType (e.g. Point) or a PointerType to a NamedType.
	if tmplType, ok := leftType.(*TemplateType); ok {
		tmpl, exists := c.templates[tmplType.Name]
		if !exists {
			c.report("unknown template type %s", tmplType.Name)
			return Unknown
		}
		for _, method := range tmpl.Methods {
			if method.Name == e.Field {
				return c.fnTypeFromDecl(method)
			}
		}
		c.report("template %s has no method %q", tmplType.Name, e.Field)
		return Unknown
	}
	var structName string
	switch lt := leftType.(type) {
	case *NamedType:
		structName = lt.Name
	case *PointerType:
		if nt, ok := lt.Elem.(*NamedType); ok {
			structName = nt.Name
		}
	}
	if structName == "" {
		c.report("cannot access field on non-struct type %s", leftType)
		return Unknown
	}
	decl, ok := c.structs[structName]
	if !ok {
		c.report("unknown struct type %q", structName)
		return Unknown
	}
	for _, field := range decl.Fields {
		if field.Name == e.Field {
			return c.astTypeToType(field.Type)
		}
	}
	for _, method := range decl.Methods {
		if method.Name == e.Field {
			return c.fnTypeFromDecl(method)
		}
	}
	// Check embedded structs for promoted fields/methods.
	if promoted := c.findPromotedField(decl, e.Field); promoted != nil {
		return promoted
	}
	c.report("struct %q has no field %q", structName, e.Field)
	return Unknown
}

// findPromotedField searches embedded structs recursively for a promoted field or method.
func (c *Checker) findPromotedField(decl *ast.StructDecl, fieldName string) Type {
	for _, field := range decl.Fields {
		if !field.Embedded {
			continue
		}
		embeddedType := c.astTypeToType(field.Type)
		var embeddedName string
		if nt, ok := embeddedType.(*NamedType); ok {
			embeddedName = nt.Name
		}
		if embeddedName == "" {
			continue
		}
		embeddedDecl, ok := c.structs[embeddedName]
		if !ok {
			continue
		}
		// Check direct fields/methods of embedded struct.
		for _, f := range embeddedDecl.Fields {
			if f.Name == fieldName {
				return c.astTypeToType(f.Type)
			}
		}
		for _, m := range embeddedDecl.Methods {
			if m.Name == fieldName {
				return c.fnTypeFromDecl(m)
			}
		}
		// Recurse into nested embedded structs.
		if nested := c.findPromotedField(embeddedDecl, fieldName); nested != nil {
			return nested
		}
	}
	return nil
}

// checkStructInitExpr validates that all fields exist and have correct types.
func (c *Checker) checkStructInitExpr(e *ast.StructInitExpr) Type {
	structName := e.Type
	if c.currentModule != "" && c.currentModule != "main" && !strings.Contains(e.Type, ".") {
		qualified := c.currentModule + "." + e.Type
		if _, exists := c.structs[qualified]; exists {
			structName = qualified
		}
	}
	if _, ok := c.structs[structName]; !ok && c.currentModule != "" && !strings.Contains(e.Type, ".") {
		if imps, exists := c.imports[c.currentModule]; exists {
			for _, impName := range imps {
				qualified := impName + "." + e.Type
				if _, existsStruct := c.structs[qualified]; existsStruct {
					structName = qualified
					break
				}
			}
		}
	}
	e.Type = structName

	decl, ok := c.structs[e.Type]
	if !ok {
		c.report("unknown struct type %q", e.Type)
		return Unknown
	}

	// Build a set of valid field names including promoted fields from embedded structs.
	validFields := make(map[string]*ast.FieldDecl)
	for _, field := range decl.Fields {
		validFields[field.Name] = field
		if field.Embedded {
			if nt, ok := field.Type.(*ast.NamedType); ok {
				if embeddedDecl, ok := c.structs[nt.Name]; ok {
					for _, ef := range embeddedDecl.Fields {
						if _, exists := validFields[ef.Name]; !exists {
							validFields[ef.Name] = ef
						}
					}
				}
			}
		}
	}

	for _, field := range decl.Fields {
		val, provided := e.Fields[field.Name]
		ft := c.astTypeToType(field.Type)
		if !provided {
			// Embedded fields and template fields are optional in struct init.
			if field.Embedded {
				continue
			}
			if _, isTmpl := ft.(*TemplateType); isTmpl {
				continue
			}
			c.report("missing field %q in struct init for %q", field.Name, e.Type)
			continue
		}
		vt := c.checkExpr(val)
		if ft != nil && vt != nil && !ft.Equals(Unknown) && !vt.Equals(Unknown) && !c.isAssignable(ft, vt) {
			c.report("field %q type mismatch: expected %s, got %s", field.Name, ft, vt)
		}
	}
	for name := range e.Fields {
		if _, found := validFields[name]; !found {
			c.report("unknown field %q in struct init for %q", name, e.Type)
		}
	}
	return &NamedType{Name: e.Type}
}

//	func isBuiltinType(name string) bool {
//		switch name {
//		case "int", "int8", "int16", "int32", "int64",
//			"uint", "uint8", "uint16", "uint32", "uint64",
//			"float", "bool", "string", "bytes", "error":
//			return true
//		}
//		return false
//	}

// isBuiltinType reports whether name is a built-in primitive type.
func isBuiltinType(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float", "bool", "string", "bytes":
		return true
	}
	return false
}

// checkCallExpr type-checks a function call expression.
func (c *Checker) checkCallExpr(e *ast.CallExpr) Type {
	// Type cast: Type(expr) — e.g., int(3.14), float(x)
	if id, ok := e.Function.(*ast.Identifier); ok && isBuiltinType(id.Value) {
		castType := c.astTypeToType(&ast.NamedType{Name: id.Value})
		if castType != nil && !castType.Equals(Unknown) {
			if len(e.Arguments) != 1 {
				c.report("type cast requires exactly 1 argument, got %d", len(e.Arguments))
				return castType
			}
			argType := c.checkExpr(e.Arguments[0])
			if argType == nil || argType.Equals(Unknown) {
				return castType
			}
			// Allow casts between numeric types, bool<->numeric, and pointer<->pointer.
			if IsNumeric(castType) && IsNumeric(argType) {
				return castType
			}
			if castType.Equals(Bool) && (IsInteger(argType) || argType.Equals(Float)) {
				return castType
			}
			if (IsInteger(castType) || castType.Equals(Float)) && argType.Equals(Bool) {
				return castType
			}
			if IsInteger(castType) {
				if _, argPtr := argType.(*PointerType); argPtr {
					return castType
				}
			}
			if _, castPtr := castType.(*PointerType); castPtr {
				if IsInteger(argType) {
					return castType
				}
				if _, argPtr := argType.(*PointerType); argPtr {
					return castType
				}
				if argType.Equals(Nil) {
					return castType
				}
			}
			if castType.Equals(String) && argType.Equals(Bytes) {
				return castType
			}
			if castType.Equals(Bytes) && argType.Equals(String) {
				return castType
			}
			// Allow casting String/Bytes to and from integer types (e.g., int(s) or string(i)) for low-level pointer manipulations in standard library
			if (castType.Equals(String) || castType.Equals(Bytes)) && IsInteger(argType) {
				return castType
			}
			if IsInteger(castType) && (argType.Equals(String) || argType.Equals(Bytes)) {
				return castType
			}
			c.report("cannot cast %s to %s", argType, castType)
			return castType
		}
	}

	// Built-in append(arr, val) — returns an array with one more element.
	if id, ok := e.Function.(*ast.Identifier); ok && id.Value == "append" {
		if len(e.Arguments) != 2 {
			c.report("append() takes exactly 2 arguments, got %d", len(e.Arguments))
			return Unknown
		}
		arrType := c.checkExpr(e.Arguments[0])
		valType := c.checkExpr(e.Arguments[1])
		if arr, ok := arrType.(*ArrayType); ok {
			if !arr.Elem.Equals(valType) && !valType.Equals(Any) && !arr.Elem.Equals(Any) {
				c.report("append value type mismatch: expected %s, got %s", arr.Elem, valType)
			}
			return arrType
		}
		c.report("append requires an array as first argument, got %s", arrType)
		return Unknown
	}
	// Built-in panic(msg) — aborts execution.
	if id, ok := e.Function.(*ast.Identifier); ok && id.Value == "panic" {
		if len(e.Arguments) != 1 {
			c.report("panic() takes exactly 1 argument, got %d", len(e.Arguments))
			return Void
		}
		argType := c.checkExpr(e.Arguments[0])
		if argType != nil && !argType.Equals(Unknown) && !argType.Equals(String) {
			c.report("panic() argument must be string, got %s", argType)
		}
		return Void
	}
	// Method call syntax: obj.method(args) where function is a FieldAccessExpr.
	if fa, ok := e.Function.(*ast.FieldAccessExpr); ok {
		// Handle module-qualified top-level function call (e.g., math.add) BEFORE checking left side
		if ident, ok := fa.Left.(*ast.Identifier); ok {
			moduleName := c.resolveImportAlias(ident.Value)
			isModuleName := false
			if c.currentModule != "" {
				if imps, exists := c.imports[c.currentModule]; exists {
					for _, imp := range imps {
						if imp == ident.Value {
							isModuleName = true
							break
						}
					}
				}
			}
			if !isModuleName {
				for _, info := range c.symbolInfo {
					if info.Module == moduleName {
						isModuleName = true
						break
					}
				}
			}
			if isModuleName {
				// Record that this module import (or alias) is used.
				c.markImportUsed(ident.Value)
				// Resolve the function from the module!
				fqName := moduleName + "." + fa.Field
				typ := c.resolve(fqName)
				if typ == nil {
					typ = c.resolve(fa.Field)
				}
				if typ != nil {
					if info, ok := c.symbolInfo[fqName]; ok && info.Module == moduleName {
						ft, ok := typ.(*FunctionType)
						if !ok {
							c.report("cannot call non-function %s", typ)
							return Unknown
						}
						if len(e.Arguments) != len(ft.Params) {
							c.report("wrong number of arguments: expected %d, got %d", len(ft.Params), len(e.Arguments))
							return Unknown
						}
						for i, arg := range e.Arguments {
							argType := c.checkExpr(arg)
							if argType != nil && !c.isAssignable(ft.Params[i], argType) {
								c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i], argType)
							}
						}
						if len(ft.Ret) == 1 {
							return ft.Ret[0]
						}
						return &TupleType{Types: ft.Ret}
					}
					if info, ok := c.symbolInfo[fa.Field]; ok && info.Module == moduleName {
						ft, ok := typ.(*FunctionType)
						if !ok {
							c.report("cannot call non-function %s", typ)
							return Unknown
						}
						if len(e.Arguments) != len(ft.Params) {
							c.report("wrong number of arguments: expected %d, got %d", len(ft.Params), len(e.Arguments))
							return Unknown
						}
						for i, arg := range e.Arguments {
							argType := c.checkExpr(arg)
							if argType != nil && !c.isAssignable(ft.Params[i], argType) {
								c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i], argType)
							}
						}
						if len(ft.Ret) == 1 {
							return ft.Ret[0]
						}
						return &TupleType{Types: ft.Ret}
					}
				}
				c.report("module %q has no exported symbol %q", moduleName, fa.Field)
				return Unknown
			}
		}

		recvType := c.checkExpr(fa.Left)
		if recvType == nil || recvType.Equals(Unknown) {
			return Unknown
		}
		// Service method call: ServiceName.method(args)
		if svc, ok := recvType.(*ServiceType); ok {
			mt, found := svc.Methods[fa.Field]
			if !found {
				c.report("unknown method %q on service %q", fa.Field, svc.Name)
				return Unknown
			}
			ft, ok := mt.(*FunctionType)
			if !ok {
				c.report("cannot call non-function %s", mt)
				return Unknown
			}
			if len(e.Arguments) != len(ft.Params) {
				c.report("wrong number of arguments: expected %d, got %d", len(ft.Params), len(e.Arguments))
				return Unknown
			}
			for i, arg := range e.Arguments {
				argType := c.checkExpr(arg)
				if argType != nil && !c.isAssignable(ft.Params[i], argType) {
					c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i], argType)
				}
			}
			if len(ft.Ret) == 1 {
				return ft.Ret[0]
			}
			return &TupleType{Types: ft.Ret}
		}
		// Ruleset method call: RulesetName.method(args)
		if rs, ok := recvType.(*RulesetType); ok {
			fnType := c.resolve(rs.Name + "." + fa.Field)
			if fnType == nil {
				c.report("unknown method %q on ruleset %q", fa.Field, rs.Name)
				return Unknown
			}
			ft, ok := fnType.(*FunctionType)
			if !ok {
				c.report("cannot call non-function %s", fnType)
				return Unknown
			}
			if len(e.Arguments) != len(ft.Params) {
				c.report("wrong number of arguments: expected %d, got %d", len(ft.Params), len(e.Arguments))
				return Unknown
			}
			for i, arg := range e.Arguments {
				argType := c.checkExpr(arg)
				if argType != nil && !c.isAssignable(ft.Params[i], argType) {
					c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i], argType)
				}
			}
			if len(ft.Ret) == 1 {
				return ft.Ret[0]
			}
			return &TupleType{Types: ft.Ret}
		}
		var structName string
		isStaticCall := false
		// Handle template type method calls.
		if tmplType, ok := recvType.(*TemplateType); ok {
			tmpl, exists := c.templates[tmplType.Name]
			if !exists {
				c.report("unknown template type %s", tmplType.Name)
				return Unknown
			}
			var found bool
			for _, tm := range tmpl.Methods {
				if tm.Name == fa.Field {
					found = true
					ft := c.fnTypeFromDecl(tm)
					minArgs := len(ft.Params) - 1
					maxArgs := minArgs
					if ft.Variadic {
						maxArgs = -1
					}
					if len(e.Arguments) < minArgs || (maxArgs >= 0 && len(e.Arguments) > maxArgs) {
						if ft.Variadic {
							c.report("wrong number of arguments: expected at least %d, got %d", minArgs, len(e.Arguments))
						} else {
							c.report("wrong number of arguments: expected %d, got %d", minArgs, len(e.Arguments))
						}
						return Unknown
					}
					for i, arg := range e.Arguments {
						argType := c.checkExpr(arg)
						if i+1 < len(ft.Params) && argType != nil && !c.isAssignable(ft.Params[i+1], argType) {
							c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i+1], argType)
						}
					}
					if len(ft.Ret) == 1 {
						return ft.Ret[0]
					}
					return &TupleType{Types: ft.Ret}
				}
			}
			if !found {
				c.report("template %s has no method %q", tmplType.Name, fa.Field)
			}
			return Unknown
		}
		// Handle interface type method calls.
		var ifaceMethods map[string]Type
		switch t := recvType.(type) {
		case *InterfaceType:
			ifaceMethods = t.Methods
		case *ErrorType:
			ifaceMethods = t.Methods
		}
		if ifaceMethods != nil {
			mt, found := ifaceMethods[fa.Field]
			if !found {
				c.report("interface has no method %q", fa.Field)
				return Unknown
			}
			ft, ok := mt.(*FunctionType)
			if !ok {
				c.report("cannot call non-function %s", mt)
				return Unknown
			}
			minArgs := len(ft.Params) - 1
			maxArgs := minArgs
			if ft.Variadic {
				maxArgs = -1
			}
			if len(e.Arguments) < minArgs || (maxArgs >= 0 && len(e.Arguments) > maxArgs) {
				if ft.Variadic {
					c.report("wrong number of arguments: expected at least %d, got %d", minArgs, len(e.Arguments))
				} else {
					c.report("wrong number of arguments: expected %d, got %d", minArgs, len(e.Arguments))
				}
				return Unknown
			}
			for i, arg := range e.Arguments {
				argType := c.checkExpr(arg)
				if i+1 < len(ft.Params) && argType != nil && !c.isAssignable(ft.Params[i+1], argType) {
					c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i+1], argType)
				}
			}
			if len(ft.Ret) == 1 {
				return ft.Ret[0]
			}
			return &TupleType{Types: ft.Ret}
		}
		switch t := recvType.(type) {
		case *NamedType:
			structName = t.Name
			if _, exists := c.structs[structName]; exists {
				if id, ok := fa.Left.(*ast.Identifier); ok {
					hasVar := false
					for i := len(c.scopes) - 1; i > 0; i-- {
						if _, ok := c.scopes[i][id.Value]; ok {
							hasVar = true
							break
						}
					}
					if !hasVar {
						isStaticCall = true
					}
				} else {
					// Module-qualified type access (e.g., time.Duration.FromSeconds)
					// is a static call if the leftmost identifier is not a variable.
					leftmost := leftmostIdent(fa.Left)
					if leftmost != "" {
						hasVar := false
						for i := len(c.scopes) - 1; i > 0; i-- {
							if _, ok := c.scopes[i][leftmost]; ok {
								hasVar = true
								break
							}
						}
						if !hasVar {
							isStaticCall = true
						}
					}
				}
			}
		case *PointerType:
			if nt, ok := t.Elem.(*NamedType); ok {
				structName = nt.Name
			}
		}
		var fnType Type
		if structName != "" {
			fnType = c.resolve(structName + "." + fa.Field)
		}
		if fnType == nil {
			fnType = c.resolve(fa.Field)
		}
		if fnType == nil {
			c.report("unknown method %q on type %s", fa.Field, recvType)
			return Unknown
		}
		ft, ok := fnType.(*FunctionType)
		if !ok {
			c.report("cannot call non-function %s", fnType)
			return Unknown
		}

		if isStaticCall {
			if len(e.Arguments) != len(ft.Params) {
				c.report("wrong number of arguments: expected %d, got %d", len(ft.Params), len(e.Arguments))
				return Unknown
			}
			for i, arg := range e.Arguments {
				argType := c.checkExpr(arg)
				if argType != nil && !c.isAssignable(ft.Params[i], argType) {
					c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i], argType)
				}
			}
			if len(ft.Ret) == 1 {
				return ft.Ret[0]
			}
			return &TupleType{Types: ft.Ret}
		}

		if len(ft.Params) == 0 {
			c.report("method %q has no receiver parameter", fa.Field)
			return Unknown
		}
		receiverCompatible := false
		expectedReceiver := ft.Params[0]
		if expectedReceiver.Equals(recvType) || expectedReceiver.Equals(Any) {
			receiverCompatible = true
		} else {
			if ptr, ok := expectedReceiver.(*PointerType); ok && ptr.Elem.Equals(recvType) {
				receiverCompatible = true
			} else if ptr, ok := recvType.(*PointerType); ok && ptr.Elem.Equals(expectedReceiver) {
				receiverCompatible = true
			}
		}
		if !receiverCompatible {
			c.report("method %q receiver type mismatch: expected %s, got %s", fa.Field, expectedReceiver, recvType)
			return Unknown
		}
		// Check remaining arguments against params[1:].
		minArgs := len(ft.Params) - 1
		maxArgs := minArgs
		if ft.Variadic {
			maxArgs = -1
		}
		if len(e.Arguments) < minArgs || (maxArgs >= 0 && len(e.Arguments) > maxArgs) {
			if ft.Variadic {
				c.report("wrong number of arguments: expected at least %d, got %d", minArgs, len(e.Arguments))
			} else {
				c.report("wrong number of arguments: expected %d, got %d", minArgs, len(e.Arguments))
			}
			return Unknown
		}
		for i, arg := range e.Arguments {
			argType := c.checkExpr(arg)
			if i+1 < len(ft.Params) && argType != nil && !c.isAssignable(ft.Params[i+1], argType) {
				c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i+1], argType)
			}
		}
		if len(ft.Ret) == 1 {
			return ft.Ret[0]
		}
		return &TupleType{Types: ft.Ret}
	}

	fnType := c.checkExpr(e.Function)
	if fnType == nil || fnType.Equals(Unknown) {
		return Unknown
	}
	ft, ok := fnType.(*FunctionType)
	if !ok {
		c.report("cannot call non-function %s", fnType)
		return Unknown
	}
	minArgs := len(ft.Params)
	maxArgs := minArgs
	if ft.Variadic {
		minArgs--
		maxArgs = -1 // unlimited
	}
	if len(e.Arguments) < minArgs || (maxArgs >= 0 && len(e.Arguments) > maxArgs) {
		if ft.Variadic {
			c.report("wrong number of arguments: expected at least %d, got %d", minArgs, len(e.Arguments))
		} else {
			c.report("wrong number of arguments: expected %d, got %d", minArgs, len(e.Arguments))
		}
		return Unknown
	}
	for i, arg := range e.Arguments {
		argType := c.checkExpr(arg)
		if i < len(ft.Params) && argType != nil && !c.isAssignable(ft.Params[i], argType) {
			c.report("argument %d type mismatch: expected %s, got %s", i+1, ft.Params[i], argType)
		}
	}
	if len(ft.Ret) == 1 {
		return ft.Ret[0]
	}
	return &TupleType{Types: ft.Ret}
}

// checkErrorPropagationExpr type-checks the error propagation operator ?.
func (c *Checker) checkErrorPropagationExpr(e *ast.ErrorPropagationExpr) Type {
	call, ok := e.Expr.(*ast.CallExpr)
	if !ok {
		c.report("? can only be applied to function calls")
		return Unknown
	}
	// Verify enclosing function returns an error.
	if c.currentFn == nil || len(c.currentFn.Ret) == 0 {
		c.report("? can only be used inside a function that returns an error")
		return Unknown
	}
	lastRet := c.currentFn.Ret[len(c.currentFn.Ret)-1]
	if !lastRet.Equals(Error) {
		c.report("? requires the enclosing function to return an error as its last value")
		return Unknown
	}
	// Verify callee returns a tuple with error as last element.
	fnType := c.checkExpr(call.Function)
	if fnType == nil || fnType.Equals(Unknown) {
		return Unknown
	}
	ft, ok := fnType.(*FunctionType)
	if !ok {
		c.report("? can only be applied to function calls")
		return Unknown
	}
	if len(ft.Ret) < 2 {
		c.report("? requires a function that returns multiple values with error as the last")
		return Unknown
	}
	lastCalleeRet := ft.Ret[len(ft.Ret)-1]
	if !lastCalleeRet.Equals(Error) {
		c.report("? requires the callee to return an error as its last value, got %s", lastCalleeRet)
		return Unknown
	}
	// Type of foo()? is the tuple without the last error element.
	if len(ft.Ret) == 2 {
		return ft.Ret[0]
	}
	return &TupleType{Types: ft.Ret[:len(ft.Ret)-1]}
}

// checkIfExpr type-checks an if expression.
func (c *Checker) checkIfExpr(e *ast.IfExpr) Type {
	condType := c.checkExpr(e.Condition)
	if condType != nil && !condType.Equals(Unknown) && !condType.Equals(Bool) {
		c.report("if condition must be bool, got %s", condType)
	}
	// Get the type of the last expression in each branch.
	trueType := c.blockLastExprType(e.Consequence)
	falseType := c.blockLastExprType(e.Alternative)
	if trueType == nil || trueType.Equals(Unknown) {
		return falseType
	}
	if falseType == nil || falseType.Equals(Unknown) {
		return trueType
	}
	if !trueType.Equals(falseType) {
		c.report("if expression branches have different types: %s and %s", trueType, falseType)
		return trueType
	}
	return trueType
}

// checkMatchExpr type-checks a match expression.
func (c *Checker) checkMatchExpr(e *ast.MatchExpr) Type {
	_ = c.checkExpr(e.Subject)
	var resultType Type
	hasWildcard := false
	for i, arm := range e.Arms {
		if id, ok := arm.Pattern.(*ast.Identifier); ok && id.Value == "_" {
			hasWildcard = true
		}
		// Check guard if present.
		if arm.Guard != nil {
			guardType := c.checkExpr(arm.Guard)
			if guardType != nil && !guardType.Equals(Unknown) && !guardType.Equals(Bool) {
				c.report("match guard must be bool, got %s", guardType)
			}
		}
		armType := c.blockLastExprType(arm.Body)
		if i == 0 {
			resultType = armType
		} else if resultType != nil && armType != nil && !resultType.Equals(armType) && !resultType.Equals(Unknown) && !armType.Equals(Unknown) {
			c.report("match arm %d has different type %s (expected %s)", i+1, armType, resultType)
		}
	}
	if !hasWildcard {
		c.report("match expression must have a wildcard (_) arm")
	}
	return resultType
}

// blockLastExprType returns the type of the last expression statement in a block.
func (c *Checker) blockLastExprType(block *ast.BlockStmt) Type {
	if block == nil || len(block.Statements) == 0 {
		return Void
	}
	last := block.Statements[len(block.Statements)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		return c.checkExpr(es.Expr)
	}
	return Void
}
