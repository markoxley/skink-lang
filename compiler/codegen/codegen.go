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

// Package codegen translates a fully-typed Skink AST into LLVM IR text.
//
// The code generator walks the AST recursively, emitting LLVM instructions
// for each expression, statement, and declaration. It manages:
//   - Register allocation and naming
//   - Struct layout and field access
//   - Array (slice) metadata including length and heap allocation tracking
//   - Function definitions and calls (including generics and closures)
//   - Control flow (if, for, while, switch, select)
//   - String and array runtime operations
//   - C foreign function interface (extern declarations)
//
// The output is LLVM IR text that can be compiled to object code by
// llc and linked into an executable.
package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/token"
)

// Codegen walks the AST and produces LLVM IR text.
// arrayMeta stores the size and element type of an array variable,
// plus whether it was allocated on the heap (e.g. by append).
type arrayMeta struct {
	len       int
	elemType  string
	heapAlloc bool
}

// ensureMapTypesFromAST recursively walks the provided AST type and emits map
// type declarations for any nested map usages so later GEPs have sized bases.
func (cg *Codegen) ensureMapTypesFromAST(t ast.Type) {
	if t == nil {
		return
	}
	t = resolveTypeAliases(t)
	switch tt := t.(type) {
	case *ast.MapType:
		keyLL := llvmType(tt.Key)
		valLL := llvmType(tt.Elem)
		cg.emitMapTypeDecl(mapTypeName(keyLL, valLL), keyLL, valLL)
		cg.ensureMapTypesFromAST(tt.Key)
		cg.ensureMapTypesFromAST(tt.Elem)
	case *ast.ArrayType:
		cg.ensureMapTypesFromAST(tt.Elem)
	case *ast.PointerType:
		cg.ensureMapTypesFromAST(tt.Elem)
	case *ast.SetType:
		cg.ensureMapTypesFromAST(tt.Elem)
	case *ast.ChanType:
		cg.ensureMapTypesFromAST(tt.Elem)
	case *ast.TensorType:
		cg.ensureMapTypesFromAST(tt.Elem)
	case *ast.TupleType:
		for _, et := range tt.Types {
			cg.ensureMapTypesFromAST(et)
		}
	case *ast.FunctionType:
		for _, pt := range tt.ParamTypes {
			cg.ensureMapTypesFromAST(pt)
		}
		if tt.ReturnType != nil {
			cg.ensureMapTypesFromAST(tt.ReturnType)
		}
	}
}

// structLayout records the LLVM type, ordered field names, and field LLVM
// types of a struct type.
type structLayout struct {
	llType     string
	fields     []string
	fieldTypes []string // LLVM type of each field (e.g. "i32", "%struct.Inner")
	isUnsigned []bool   // sign of each field
	size       int      // total size in bytes
	alignment  int      // total alignment in bytes
	offsets    []int    // byte offset for each field
}

// loopLabels tracks the target labels for break and continue in nested loops.
// break jumps to endLabel; continue jumps to continueLabel.
type loopLabels struct {
	continueLabel string // target for continue (cond for while/for-in, post for C-style for)
	endLabel      string // target for break
}

// CodegenError represents a recoverable error during code generation.
type CodegenError struct {
	Msg string
}

// Error implements the error interface for CodegenError.
func (e CodegenError) Error() string { return e.Msg }

type Codegen struct {
	moduleHeader  strings.Builder // module ID, source_filename, external declares
	stringGlobals strings.Builder // module-level string constants
	out           strings.Builder // function bodies and basic blocks
	spawnThunks   strings.Builder // generated spawn thunk functions (appended after all functions)
	anonFns       strings.Builder // generated anonymous function definitions
	metadata      strings.Builder // debug metadata definitions
	indent        string

	regCounter            int
	labelCounter          int
	strCounter            int                        // unique counter for module-level string globals
	spawnThunkCounter     int                        // unique counter for thunk registers
	anonFnCounter         int                        // unique counter for anonymous functions
	scopeVars             []map[string]scopeVar      // stack of scopes: varName -> {alloca, llvmType}
	terminated            bool                       // current block already has a terminator
	currentFnRetType      string                     // LLVM return type of the function being emitted (legacy, use currentFnRetTypes)
	currentFnRetTypes     []string                   // LLVM return types of the current function (for multi-return)
	fnRetTypes            map[string][]string        // function name -> LLVM return types (for multi-return calls)
	fnParamTypes          map[string][]string        // function name -> LLVM param types (for function pointer types)
	arraySizes            map[string]arrayMeta       // variable name -> array dimensions (for for-in)
	structLayouts         map[string]structLayout    // struct name -> layout
	structDecls           map[string]*ast.StructDecl // struct name -> ast declaration
	structLLNames         map[string]string          // llvm struct name (underscores) -> original struct name (dots)
	fnVariadic            map[string]bool            // function name -> variadic
	consts                map[string]ast.Expression  // const name -> value (for inlining)
	loopLabels            []loopLabels               // stack of loop labels (innermost last)
	deferred              []ast.Statement            // deferred statements for current function
	errors                []error                    // recoverable codegen errors
	globalVarTypes        map[string]string          // global var name -> LLVM type
	services              map[string]bool            // service name -> exists
	rulesets              map[string]bool            // ruleset name -> exists
	currentFnBody         *ast.BlockStmt             // body of the current function being emitted (for escape analysis)
	currentFnName         string                     // name of the function currently being emitted
	hasMainWrapper        bool                       // true if we generated a wrapper @main for @_skink_main
	mainRetType           string                     // LLVM return type of the user's main (for wrapper generation)
	runtimeHelpersEmitted bool                       // true once helper globals/functions have been emitted
	mapTypes              map[string]bool            // map type name -> declaration emitted
	closureEnv            map[string]closureEnvInfo  // captured var name -> env info for current closure
	closureEnvs           map[string]string          // variable name -> env global name
	rulesetWrappers       map[string]bool            // concrete type name -> wrapper functions already emitted
	aliases               map[string]ast.Type        // alias name -> underlying AST type
	fnAliasLLTypes        map[string]string          // function alias name -> named LLVM type (e.g. "%fp_types_Handler")
	cudaKernels           []string                   // names of functions marked with [cuda]
	importAliases         map[string]string          // import alias -> real module name

	// Debug info
	debug      bool       // emit debug metadata
	debugInfo  *debugInfo // debug metadata generator
	debugScope int        // current DISubprogram metadata ID
	debugLine  int        // current source line for debug locations
	debugCol   int        // current source column for debug locations
}

type scopeVar struct {
	alloca     string
	llType     string
	isUnsigned bool
	synType    ast.Type
}

type closureEnvInfo struct {
	envStruct  string // name of the env struct type (e.g. struct.anon_env_0)
	globalName string // name of the global env struct (e.g. @anon_env_0)
	fieldIndex int    // index in the env struct
	llType     string // LLVM type of the captured variable
}

// New creates a fresh Codegen.
func New() *Codegen {
	typeAliases = make(map[string]ast.Type)
	return &Codegen{
		scopeVars:       []map[string]scopeVar{{}},
		arraySizes:      make(map[string]arrayMeta),
		structLayouts:   make(map[string]structLayout),
		structDecls:     make(map[string]*ast.StructDecl),
		structLLNames:   make(map[string]string),
		fnRetTypes:      make(map[string][]string),
		fnParamTypes:    make(map[string][]string),
		fnVariadic:      make(map[string]bool),
		consts:          make(map[string]ast.Expression),
		loopLabels:      make([]loopLabels, 0),
		globalVarTypes:  make(map[string]string),
		services:        make(map[string]bool),
		rulesets:        make(map[string]bool),
		mapTypes:        make(map[string]bool),
		closureEnv:      make(map[string]closureEnvInfo),
		closureEnvs:     make(map[string]string),
		rulesetWrappers: make(map[string]bool),
		aliases:         make(map[string]ast.Type),
		fnAliasLLTypes:  make(map[string]string),
		importAliases:   make(map[string]string),
	}
}

// Reset clears all generated IR and resets internal state.
func (cg *Codegen) Reset() {
	cg.moduleHeader.Reset()
	cg.stringGlobals.Reset()
	cg.out.Reset()
	cg.spawnThunks.Reset()
	cg.metadata.Reset()

	// Break reference links for maps to free nested heap elements
	cg.scopeVars = []map[string]scopeVar{{}}
	cg.arraySizes = make(map[string]arrayMeta)
	cg.structLayouts = make(map[string]structLayout)
	cg.structDecls = make(map[string]*ast.StructDecl)
	cg.structLLNames = make(map[string]string)
	cg.fnRetTypes = make(map[string][]string)
	cg.fnParamTypes = make(map[string][]string)
	cg.consts = make(map[string]ast.Expression)
	cg.loopLabels = make([]loopLabels, 0)
	cg.deferred = nil
	cg.errors = nil
	cg.debug = false
	cg.debugInfo = nil
	cg.mapTypes = make(map[string]bool)
	cg.closureEnv = make(map[string]closureEnvInfo)
	cg.closureEnvs = make(map[string]string)
	cg.rulesetWrappers = make(map[string]bool)
	cg.aliases = make(map[string]ast.Type)
	typeAliases = make(map[string]ast.Type)
	cg.debugScope = 0
	cg.debugLine = 0
	cg.debugCol = 0
	cg.strCounter = 0
	cg.spawnThunkCounter = 0
	cg.anonFnCounter = 0
	cg.globalVarTypes = make(map[string]string)
	cg.services = make(map[string]bool)
	cg.rulesets = make(map[string]bool)
	cg.hasMainWrapper = false
	cg.mainRetType = ""
	cg.runtimeHelpersEmitted = false
	cg.fnAliasLLTypes = make(map[string]string)
	cg.cudaKernels = nil
	cg.importAliases = make(map[string]string)
}

// resolveImportAlias returns the real module name for a module-qualified
// identifier. If name is an import alias, it returns the aliased module name;
// otherwise it returns name unchanged.
func (cg *Codegen) resolveImportAlias(name string) string {
	if realName, ok := cg.importAliases[name]; ok {
		return realName
	}
	return name
}

// Errorf records a recoverable codegen error.
func (cg *Codegen) Errorf(format string, args ...interface{}) {
	cg.errors = append(cg.errors, CodegenError{Msg: fmt.Sprintf(format, args...)})
}

// Errors returns all recoverable errors collected during codegen.
func (cg *Codegen) Errors() []error {
	return cg.errors
}

// String returns the complete generated IR in correct order:
//  1. module header (; ModuleID, source_filename, external declares)
//  2. module-level string constants
//  3. function bodies and basic blocks
//  4. debug metadata definitions
func (cg *Codegen) String() string {
	return cg.moduleHeader.String() + cg.stringGlobals.String() + cg.out.String() + cg.spawnThunks.String() + cg.anonFns.String() + cg.metadata.String()
}

// --- Output helpers ---

// writef appends a formatted line to the function-body IR buffer.
func (cg *Codegen) writef(format string, args ...interface{}) {
	fmt.Fprintf(&cg.out, format, args...)
}

// writeStringGlobal appends a module-level string constant declaration.
// These are collected separately so they can be emitted before any function.
func (cg *Codegen) writeStringGlobal(format string, args ...interface{}) {
	fmt.Fprintf(&cg.stringGlobals, format, args...)
}

// writeln appends a single string followed by a newline.
func (cg *Codegen) writeln(s string) {
	cg.out.WriteString(s)
	cg.out.WriteByte('\n')
}

// emitFuncBody redirects output to a temporary buffer while fn() runs,
// then hoists every line containing "= alloca" to the function entry
// block before emitting the remaining body lines.  This prevents stack
// overflow when loops declare many temporary variables.
func (cg *Codegen) emitFuncBody(fn func()) {
	savedOut := cg.out
	cg.out = strings.Builder{}
	fn()
	body := cg.out.String()
	cg.out = savedOut

	var allocas []string
	var rest []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "= alloca") {
			allocas = append(allocas, line)
		} else if line != "" {
			rest = append(rest, line)
		}
	}
	for _, line := range allocas {
		cg.writeln(line)
	}
	for _, line := range rest {
		cg.writeln(line)
	}
}

// SetDebug enables debug metadata emission for the given source file.
func (cg *Codegen) SetDebug(sourcePath string) {
	cg.debug = true
	cg.debugInfo = newDebugInfo(sourcePath)
}

// setDebugLoc updates the current source location from an AST node token.
func (cg *Codegen) setDebugLoc(line, col int) {
	cg.debugLine = line
	cg.debugCol = col
}

// dbgTag returns an inline !dbg tag and records the metadata definition
// when debug info is enabled. Returns empty string otherwise.
func (cg *Codegen) dbgTag() string {
	if cg.debug && cg.debugScope != 0 && cg.debugLine != 0 {
		locID := cg.debugInfo.Location(cg.debugLine, cg.debugCol, cg.debugScope)
		cg.metadata.WriteString(cg.debugInfo.LocationDef(locID, cg.debugLine, cg.debugCol, cg.debugScope))
		return fmt.Sprintf(", !dbg !%d", locID)
	}
	return ""
}

// --- SSA register / label generation ---

// nextReg generates a new virtual register name (%r1, %r2, ...).
// Registers are scoped per-function for deterministic output.
func (cg *Codegen) nextReg() string {
	cg.regCounter++
	return "%r" + strconv.Itoa(cg.regCounter)
}

// nextThunkReg generates a register name for spawn thunk IR.
func (cg *Codegen) nextThunkReg() string {
	cg.spawnThunkCounter++
	return "%r" + strconv.Itoa(cg.spawnThunkCounter)
}

// nextLabel generates a new basic-block label (L1, L2, ...).
// Labels are scoped per-function for deterministic output.
func (cg *Codegen) nextLabel() string {
	cg.labelCounter++
	return "L" + strconv.Itoa(cg.labelCounter)
}

// --- Variable scoping ---

// pushScope adds a new lexical scope on the scope stack.
// Used when entering a function body, block, or control-flow branch.
func (cg *Codegen) pushScope() {
	cg.scopeVars = append(cg.scopeVars, map[string]scopeVar{})
}

// popScope discards the innermost lexical scope.
// Must be paired 1:1 with every pushScope call.
func (cg *Codegen) popScope() {
	if len(cg.scopeVars) > 1 {
		// Emit block-level deallocations specifically for the current scope before we pop it
		cg.emitDeallocationsForScope(len(cg.scopeVars) - 1)
		cg.scopeVars = cg.scopeVars[:len(cg.scopeVars)-1]
	}
}

// emitDeallocationsForScope emits clean-up destructions for all non-escaping dynamic variables
// in the specified scope (referenced by 0-based scope index into cg.scopeVars).
func (cg *Codegen) emitDeallocationsForScope(scopeIdx int) {
	if scopeIdx < 0 || scopeIdx >= len(cg.scopeVars) {
		return
	}
	scope := cg.scopeVars[scopeIdx]
	for name, v := range scope {
		if cg.isDeallocCandidate(name, v) {
			if !cg.isEscaped(name, cg.currentFnBody) {
				cg.emitDeallocVar(name, v)
			}
		}
	}
}

// emitAllDeallocations emits clean-up destructions for all non-escaping dynamic variables
// across all currently active scoped stack frames (e.g. before returning).
// Scope 0 holds function parameters and is skipped — the caller retains ownership.
func (cg *Codegen) emitAllDeallocations() {
	for i := len(cg.scopeVars) - 1; i > 0; i-- {
		cg.emitDeallocationsForScope(i)
	}
}

// isHeapLLType reports whether an LLVM type is managed by ARC.
func (cg *Codegen) isHeapLLType(llType string) bool {
	if llType == "i8*" {
		return true
	}
	if strings.HasSuffix(llType, "*") && !strings.HasSuffix(llType, "**") {
		return true
	}
	return false
}

// emitRetain emits a Skink_rc_retain for heap types and returns the result register.
func (cg *Codegen) emitRetain(reg string, llType string) string {
	if !cg.isHeapLLType(llType) {
		return reg
	}
	r := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_retain(i8* %s)\n", r, reg)
	return r
}

// emitRelease emits a Skink_rc_release for heap types.
func (cg *Codegen) emitRelease(reg string, llType string) {
	if !cg.isHeapLLType(llType) {
		return
	}
	cg.writef("  call void @Skink_rc_release(i8* %s)\n", reg)
}

// emitBoxError boxes a concrete pointer value into the error interface fat pointer.
// concreteReg is the LLVM register holding the concrete value.
// concreteLLType is the LLVM type of the concrete value (e.g. "%struct.errors.Error*").
func (cg *Codegen) emitBoxError(concreteReg string, concreteLLType string) string {
	// Determine the struct name from the concrete type.
	structName := ""
	t := concreteLLType
	t = strings.TrimSuffix(t, "*")
	if strings.HasPrefix(t, "%struct.") {
		structName = strings.TrimPrefix(t, "%struct.")
	}
	if structName == "" {
		// Cannot determine struct type; return zeroinitializer.
		return "zeroinitializer"
	}
	methodName := structName + ".String"
	if _, ok := cg.fnRetTypes[methodName]; !ok {
		// Try replacing underscores with dots to reconstruct the original
		// module-qualified name (e.g. errors_Error -> errors.Error).
		dottedName := strings.ReplaceAll(structName, "_", ".")
		candidate := dottedName + ".String"
		if _, ok := cg.fnRetTypes[candidate]; ok {
			methodName = candidate
		} else {
			// Try module-qualified method name (e.g. errors.String).
			// For mangled names like errors_Error, the first underscore
			// separates module from struct name.
			dotIdx := strings.Index(structName, "_")
			if dotIdx > 0 {
				modName := structName[:dotIdx]
				candidate = modName + ".String"
				if _, ok := cg.fnRetTypes[candidate]; ok {
					methodName = candidate
				}
			}
			// Try splitting by dots (for non-mangled names).
			if _, ok := cg.fnRetTypes[methodName]; !ok || methodName == structName+".String" {
				parts := strings.Split(structName, ".")
				if len(parts) >= 2 {
					moduleName := strings.Join(parts[:len(parts)-1], ".")
					candidate = moduleName + ".String"
					if _, ok := cg.fnRetTypes[candidate]; ok {
						methodName = candidate
					} else if len(parts) > 0 {
						methodName = parts[len(parts)-1] + ".String"
						if _, ok := cg.fnRetTypes[methodName]; !ok {
							methodName = "String"
						}
					}
				} else if len(parts) > 0 {
					methodName = parts[len(parts)-1] + ".String"
					if _, ok := cg.fnRetTypes[methodName]; !ok {
						methodName = "String"
					}
				}
			}
		}
	}
	// Bitcast concrete value to i8* for the data pointer.
	dataReg := cg.nextReg()
	cg.writef("  %s = bitcast %s %s to i8*\n", dataReg, concreteLLType, concreteReg)
	// Bitcast method pointer to i8* (i8*)* for the vtable entry.
	// Struct parameters are always passed as pointers in LLVM IR.
	structType := strings.TrimSuffix(concreteLLType, "*")
	fnReg := cg.nextReg()
	cg.writef("  %s = bitcast i8* (%s*)* @%s to i8* (i8*)*\n", fnReg, structType, methodName)
	// Build the fat pointer.
	t1 := cg.nextReg()
	cg.writef("  %s = insertvalue %%error undef, i8* (i8*)* %s, 0\n", t1, fnReg)
	t2 := cg.nextReg()
	cg.writef("  %s = insertvalue %%error %s, i8* %s, 1\n", t2, t1, dataReg)
	return t2
}

// isDeallocCandidate checks if a variable is a potential heap allocated dynamic object candidate.
func (cg *Codegen) isDeallocCandidate(name string, v scopeVar) bool {
	if v.synType == nil {
		return cg.isHeapLLType(v.llType)
	}
	switch v.synType.(type) {
	case *ast.SetType, *ast.MapType, *ast.ChanType, *ast.TensorType:
		return true
	case *ast.ArrayType:
		allocaKey := cg.currentFnName + ":" + v.alloca
		if meta, ok := cg.arraySizes[allocaKey]; ok && meta.heapAlloc {
			return true
		}
	case *ast.NamedType:
		nt := v.synType.(*ast.NamedType)
		if nt.Name == "tensor" || nt.Name == "string" {
			return true
		}
		if _, ok := cg.structLayouts[nt.Name]; ok {
			return true
		}
	}
	return cg.isHeapLLType(v.llType)
}

// isConstructorExpr reports whether an expression produces a fresh heap
// allocation that already starts with a refcount of 1.
func (cg *Codegen) isConstructorExpr(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.ArrayLiteral, *ast.MapLiteral, *ast.SetLiteral:
		return true
	case *ast.InfixExpr:
		// String concatenation: "a" + "b"
		if e.Operator == "+" && cg.exprLLType(e.Left) == "i8*" && cg.exprLLType(e.Right) == "i8*" {
			return true
		}
	case *ast.CallExpr:
		if id, ok := e.Function.(*ast.Identifier); ok && id.Value == "append" {
			return true
		}
	}
	return false
}

// isEscaped checks if a variable escapes its function block.
func (cg *Codegen) isEscaped(varName string, body ast.Node) bool {
	if body == nil {
		return false
	}
	escaped := false
	var check func(n ast.Node)
	check = func(n ast.Node) {
		if n == nil || escaped {
			return
		}
		switch node := n.(type) {
		case *ast.ReturnStmt:
			for _, val := range node.Values {
				if hasIdentifier(val, varName) {
					escaped = true
					return
				}
			}
		case *ast.SpawnStmt:
			if hasIdentifier(node.Call, varName) {
				escaped = true
				return
			}
		case *ast.AsyncExpr:
			if hasIdentifier(node.Expr, varName) {
				escaped = true
				return
			}
		case *ast.AssignmentStmt:
			if hasIdentifier(node.Value, varName) {
				if id, ok := node.LValue.(*ast.Identifier); ok {
					if _, isGlobal := cg.globalVarTypes[id.Value]; isGlobal {
						escaped = true
						return
					}
				} else {
					escaped = true
					return
				}
			}
		case *ast.TupleAssignmentStmt:
			if hasIdentifier(node.Value, varName) {
				escaped = true
				return
			}
		case *ast.BlockStmt:
			for _, stmt := range node.Statements {
				check(stmt)
			}
		case *ast.IfStmt:
			check(node.Condition)
			check(node.Consequence)
			check(node.Alternative)
		case *ast.WhileStmt:
			check(node.Condition)
			check(node.Body)
		case *ast.UntilStmt:
			check(node.Condition)
			check(node.Body)
		case *ast.ForStmt:
			if node.Iterator != nil {
				check(node.Iterator.Iterable)
			}
			check(node.Body)
		case *ast.SelectStmt:
			for _, c := range node.Cases {
				check(c.Condition)
				check(c.Body)
			}
		case *ast.SwitchStmt:
			check(node.Subject)
			for _, c := range node.Cases {
				for _, v := range c.Values {
					check(v)
				}
				check(c.Body)
			}
		case *ast.DeferStmt:
			check(node.Statement)
		case *ast.WithStmt:
			check(node.Value)
			check(node.Body)
		case *ast.VarStmt:
			check(node.Value)
		case *ast.TupleVarStmt:
			check(node.Value)
		}
	}
	check(body)
	return escaped
}

// hasIdentifier recursively checks if node contains any references to name.
func hasIdentifier(n ast.Node, name string) bool {
	if n == nil {
		return false
	}
	switch node := n.(type) {
	case *ast.Identifier:
		return node.Value == name
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.BooleanLiteral, *ast.StringLiteral:
		return false
	case *ast.PrefixExpr:
		return hasIdentifier(node.Right, name)
	case *ast.InfixExpr:
		return hasIdentifier(node.Left, name) || hasIdentifier(node.Right, name)
	case *ast.IndexExpr:
		return hasIdentifier(node.Left, name) || hasIdentifier(node.Index, name)
	case *ast.FromEndIndexExpr:
		return hasIdentifier(node.Operand, name)
	case *ast.SpreadExpr:
		return hasIdentifier(node.Operand, name)
	case *ast.SliceExpr:
		return hasIdentifier(node.Left, name) || hasIdentifier(node.Start, name) || hasIdentifier(node.End, name)
	case *ast.CallExpr:
		for _, arg := range node.Arguments {
			if hasIdentifier(arg, name) {
				return true
			}
		}
		return hasIdentifier(node.Function, name)
	case *ast.ArrayLiteral:
		for _, el := range node.Elements {
			if hasIdentifier(el, name) {
				return true
			}
		}
		return false
	case *ast.MapLiteral:
		for _, pair := range node.Pairs {
			if hasIdentifier(pair.Key, name) || hasIdentifier(pair.Value, name) {
				return true
			}
		}
		return false
	case *ast.SetLiteral:
		for _, el := range node.Elements {
			if hasIdentifier(el, name) {
				return true
			}
		}
		return false
	case *ast.FieldAccessExpr:
		return hasIdentifier(node.Left, name)
	case *ast.StructInitExpr:
		for _, val := range node.Fields {
			if hasIdentifier(val, name) {
				return true
			}
		}
		return false
	case *ast.AsyncExpr:
		return hasIdentifier(node.Expr, name)
	case *ast.AwaitExpr:
		return hasIdentifier(node.Expr, name)
	case *ast.FnLiteral:
		return false // anonymous function literal doesn't reference enclosing vars in its definition for hasIdentifier
	case *ast.ErrorPropagationExpr:
		return hasIdentifier(node.Expr, name)
	case *ast.IfExpr:
		return hasIdentifier(node.Condition, name) || hasIdentifier(node.Consequence, name) || hasIdentifier(node.Alternative, name)
	case *ast.MatchExpr:
		if hasIdentifier(node.Subject, name) {
			return true
		}
		for _, arm := range node.Arms {
			if hasIdentifier(arm.Pattern, name) || hasIdentifier(arm.Guard, name) || hasIdentifier(arm.Body, name) {
				return true
			}
		}
		return false
	case *ast.BlockStmt:
		for _, stmt := range node.Statements {
			if hasIdentifier(stmt, name) {
				return true
			}
		}
		return false
	case *ast.VarStmt:
		return hasIdentifier(node.Value, name)
	case *ast.TupleVarStmt:
		return hasIdentifier(node.Value, name)
	case *ast.AssignmentStmt:
		return hasIdentifier(node.LValue, name) || hasIdentifier(node.Value, name)
	case *ast.TupleAssignmentStmt:
		return hasIdentifier(node.Value, name)
	case *ast.ReturnStmt:
		for _, v := range node.Values {
			if hasIdentifier(v, name) {
				return true
			}
		}
		return false
	case *ast.IfStmt:
		return hasIdentifier(node.Condition, name) || hasIdentifier(node.Consequence, name) || hasIdentifier(node.Alternative, name)
	case *ast.WhileStmt:
		return hasIdentifier(node.Condition, name) || hasIdentifier(node.Body, name)
	case *ast.UntilStmt:
		return hasIdentifier(node.Condition, name) || hasIdentifier(node.Body, name)
	case *ast.ForStmt:
		if node.Iterator != nil {
			if hasIdentifier(node.Iterator.Iterable, name) {
				return true
			}
		}
		return hasIdentifier(node.Body, name)
	case *ast.SpawnStmt:
		return hasIdentifier(node.Call, name)
	case *ast.SelectStmt:
		for _, c := range node.Cases {
			if hasIdentifier(c.Condition, name) || hasIdentifier(c.Body, name) {
				return true
			}
		}
		return false
	case *ast.DeferStmt:
		return hasIdentifier(node.Statement, name)
	case *ast.WithStmt:
		return hasIdentifier(node.Value, name) || hasIdentifier(node.Body, name)
	}
	return false
}

// emitDeallocVar generates the physical deallocation LLVM instructions for a local variable.
func (cg *Codegen) emitDeallocVar(name string, v scopeVar) {
	if cg.terminated {
		return
	}
	handled := false
	switch v.synType.(type) {
	case *ast.SetType:
		handled = true
		setTypeName := "%set.Int"
		elemType := "i32*"
		if v.llType == "%set.Str*" {
			setTypeName = "%set.Str"
			elemType = "i8**"
		}
		setPtr := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", setPtr, v.llType, v.llType, v.alloca)
		elemPtr := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s %s, i32 0, i32 0\n", elemPtr, setTypeName, v.llType, setPtr)
		elems := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", elems, elemType, elemType, elemPtr)
		elemsCast := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to i8*\n", elemsCast, elemType, elems)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", elemsCast)
		setCast := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to i8*\n", setCast, v.llType, setPtr)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", setCast)

	case *ast.MapType:
		handled = true
		mapStructType := strings.TrimSuffix(v.llType, "*")
		keyLL, valLL, _ := parseMapType(v.llType)
		mapPtr := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", mapPtr, v.llType, v.llType, v.alloca)

		keysPtr := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n", keysPtr, mapStructType, mapStructType, mapPtr)
		keys := cg.nextReg()
		cg.writef("  %s = load %s*, %s** %s\n", keys, keyLL, keyLL, keysPtr)
		keysCast := cg.nextReg()
		cg.writef("  %s = bitcast %s* %s to i8*\n", keysCast, keyLL, keys)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", keysCast)

		valsPtr := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n", valsPtr, mapStructType, mapStructType, mapPtr)
		vals := cg.nextReg()
		cg.writef("  %s = load %s*, %s** %s\n", vals, valLL, valLL, valsPtr)
		valsCast := cg.nextReg()
		cg.writef("  %s = bitcast %s* %s to i8*\n", valsCast, valLL, vals)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", valsCast)

		mapCast := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to i8*\n", mapCast, v.llType, mapPtr)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", mapCast)

	case *ast.ChanType:
		handled = true
		chanPtr := cg.nextReg()
		cg.writef("  %s = load i8*, i8** %s\n", chanPtr, v.alloca)
		cg.writef("  call void @Skink_chan_close(i8* %s)\n", chanPtr)
		// Channels are allocated by conc_rt.c via calloc, not RC.
		// Use free directly; rc_release's magic-number guard would
		// safely ignore it, but the memory would leak.
		cg.writef("  call void @free(i8* %s)\n", chanPtr)

	case *ast.TensorType:
		handled = true
		tensorPtr := cg.nextReg()
		cg.writef("  %s = load i8*, i8** %s\n", tensorPtr, v.alloca)
		cg.writef("  call void @Skink_tensor_free(i8* %s)\n", tensorPtr)

	case *ast.ArrayType:
		handled = true
		arrPtr := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", arrPtr, v.llType, v.llType, v.alloca)
		castReg := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to i8*\n", castReg, v.llType, arrPtr)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", castReg)

	case *ast.NamedType:
		nt := v.synType.(*ast.NamedType)
		if nt.Name == "tensor" {
			handled = true
			tensorPtr := cg.nextReg()
			cg.writef("  %s = load i8*, i8** %s\n", tensorPtr, v.alloca)
			cg.writef("  call void @Skink_tensor_free(i8* %s)\n", tensorPtr)
		} else if nt.Name == "string" {
			handled = true
			strPtr := cg.nextReg()
			cg.writef("  %s = load i8*, i8** %s\n", strPtr, v.alloca)
			cg.writef("  call void @Skink_rc_release(i8* %s)\n", strPtr)
		} else if layout, ok := cg.structLayouts[nt.Name]; ok {
			handled = true
			// Determine whether the variable is stored as a pointer (heap) or
			// by-value (stack).  In both cases we must release any
			// heap-allocated fields *before* releasing the struct block.
			isPtr := strings.HasSuffix(v.llType, "*")
			var structPtr string
			if isPtr {
				structPtr = cg.nextReg()
				cg.writef("  %s = load %s, %s* %s\n", structPtr, v.llType, v.llType, v.alloca)
			} else {
				structPtr = v.alloca
			}
			// Release heap-allocated fields.
			for i, ft := range layout.fieldTypes {
				if cg.isHeapLLType(ft) {
					fieldPtr := cg.nextReg()
					cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
						fieldPtr, layout.llType, layout.llType, structPtr, i)
					fieldVal := cg.nextReg()
					cg.writef("  %s = load %s, %s* %s\n", fieldVal, ft, ft, fieldPtr)
					fieldCast := cg.nextReg()
					cg.writef("  %s = bitcast %s %s to i8*\n", fieldCast, ft, fieldVal)
					cg.writef("  call void @Skink_rc_release(i8* %s)\n", fieldCast)
				}
			}
			// If heap-allocated, release the struct block itself.
			if isPtr {
				cast := cg.nextReg()
				cg.writef("  %s = bitcast %s %s to i8*\n", cast, v.llType, structPtr)
				cg.writef("  call void @Skink_rc_release(i8* %s)\n", cast)
			}
		}
	}
	if !handled && v.synType == nil && v.llType == "i8*" {
		handled = true
		strPtr := cg.nextReg()
		cg.writef("  %s = load i8*, i8** %s\n", strPtr, v.alloca)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", strPtr)
	}
	// Fallback: any remaining heap type gets released via RC.
	if !handled && cg.isHeapLLType(v.llType) {
		ptr := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", ptr, v.llType, v.llType, v.alloca)
		cast := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to i8*\n", cast, v.llType, ptr)
		cg.writef("  call void @Skink_rc_release(i8* %s)\n", cast)
	}
}

// declareVar binds a Skink variable name to its LLVM alloca register
// and LLVM type within the current scope.
func (cg *Codegen) declareVar(name, allocaReg, llType string, isUnsigned bool, synType ast.Type) {
	cg.scopeVars[len(cg.scopeVars)-1][name] = scopeVar{alloca: allocaReg, llType: llType, isUnsigned: isUnsigned, synType: synType}
}

// exprASTType returns the AST type of an expression.
func (cg *Codegen) exprASTType(expr ast.Expression) ast.Type {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return &ast.NamedType{Name: "int"}
	case *ast.FloatLiteral:
		return &ast.NamedType{Name: "float"}
	case *ast.BooleanLiteral:
		return &ast.NamedType{Name: "bool"}
	case *ast.StringLiteral:
		return &ast.NamedType{Name: "string"}
	case *ast.AsyncExpr:
		return cg.exprASTType(e.Expr)
	case *ast.AwaitExpr:
		return cg.exprASTType(e.Expr)
	case *ast.FromEndIndexExpr:
		return &ast.NamedType{Name: "int"}
	case *ast.SpreadExpr:
		return cg.exprASTType(e.Operand)
	case *ast.RangeExpr:
		return &ast.NamedType{Name: "Range"}
	case *ast.Identifier:
		for i := len(cg.scopeVars) - 1; i >= 0; i-- {
			if v, ok := cg.scopeVars[i][e.Value]; ok {
				return v.synType
			}
		}
	case *ast.MakeExpr:
		return e.Type
	case *ast.PrefixExpr:
		if e.Operator == "<-" {
			chType := cg.exprASTType(e.Right)
			if ct, ok := chType.(*ast.ChanType); ok {
				return ct.Elem
			}
		}
		return cg.exprASTType(e.Right)
	case *ast.CallExpr:
		if id, ok := e.Function.(*ast.Identifier); ok {
			switch id.Value {
			case "int", "uint", "int32", "uint32":
				return &ast.NamedType{Name: "int"}
			case "int8", "uint8":
				return &ast.NamedType{Name: "int8"}
			case "int16", "uint16":
				return &ast.NamedType{Name: "int16"}
			case "int64", "uint64":
				return &ast.NamedType{Name: "int64"}
			case "float":
				return &ast.NamedType{Name: "float"}
			case "bool":
				return &ast.NamedType{Name: "bool"}
			case "string":
				return &ast.NamedType{Name: "string"}
			}
		}
		fnName := ""
		if id, ok := e.Function.(*ast.Identifier); ok {
			fnName = id.Value
		} else if fa, ok := e.Function.(*ast.FieldAccessExpr); ok {
			fnName = fa.Field
		}
		if rts, ok := cg.fnRetTypes[fnName]; ok && len(rts) > 0 {
			switch rts[0] {
			case "double":
				return &ast.NamedType{Name: "float"}
			case "i1":
				return &ast.NamedType{Name: "bool"}
			case "i8*":
				return &ast.NamedType{Name: "string"}
			case "void":
				return &ast.NamedType{Name: "void"}
			default:
				if strings.HasPrefix(rts[0], "%struct.") {
					name := strings.TrimPrefix(rts[0], "%struct.")
					if idx := strings.Index(name, "*"); idx > 0 {
						name = name[:idx]
					}
					// dots were replaced by underscores in llvmType, but keep original name in AST
					return &ast.NamedType{Name: name}
				}
				return &ast.NamedType{Name: "int"}
			}
		}
	case *ast.FnLiteral:
		var paramTypes []ast.Type
		for _, p := range e.Params {
			paramTypes = append(paramTypes, p.Type)
		}
		return &ast.FunctionType{ParamTypes: paramTypes, ReturnType: e.ReturnType}
	case *ast.StructInitExpr:
		return &ast.NamedType{Name: e.Type}
	case *ast.FieldAccessExpr:
		leftType := cg.exprASTType(e.Left)
		if leftType != nil {
			var nt *ast.NamedType
			if n, ok := leftType.(*ast.NamedType); ok {
				nt = n
			} else if ptr, ok := leftType.(*ast.PointerType); ok {
				if n, ok := ptr.Elem.(*ast.NamedType); ok {
					nt = n
				}
			}
			if nt != nil {
				structName := nt.Name
				if !strings.Contains(structName, ".") && cg.currentFnName != "" {
					parts := strings.SplitN(cg.currentFnName, ".", 2)
					if len(parts) == 2 {
						fqName := parts[0] + "." + structName
						if _, exists := cg.structDecls[fqName]; exists {
							structName = fqName
						}
					}
				}
				if decl, exists := cg.structDecls[structName]; exists {
					for _, f := range decl.Fields {
						if f.Name == e.Field {
							return f.Type
						}
					}
				}
				if structName == "error" && e.Field == "String" {
					return &ast.NamedType{Name: "string"}
				}
				if nt == nil {
					lt := cg.exprLLType(e.Left)
					if lt == "%error" && e.Field == "String" {
						return &ast.NamedType{Name: "string"}
					}
				}
				if structName == "Reader" || structName == "Writer" || structName == "reader.Reader" || structName == "writer.Writer" {
					return &ast.FunctionType{ReturnType: &ast.NamedType{Name: "int"}}
				}
			}
		}
	}
	return nil
}

// isUnsignedType reports whether t is an unsigned integer named type.
func isUnsignedType(t ast.Type) bool {
	if nt, ok := t.(*ast.NamedType); ok {
		return strings.HasPrefix(nt.Name, "uint")
	}
	return false
}

// isUnsignedVar reports whether a variable has an unsigned integer type.
func (cg *Codegen) isUnsignedVar(name string) bool {
	for i := len(cg.scopeVars) - 1; i >= 0; i-- {
		if v, ok := cg.scopeVars[i][name]; ok {
			return v.isUnsigned
		}
	}
	return false
}

// isUnsignedExpr reports whether an expression has an unsigned integer type.
func (cg *Codegen) isUnsignedExpr(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return false
	case *ast.Identifier:
		return cg.isUnsignedVar(e.Value)
	case *ast.CallExpr:
		if id, ok := e.Function.(*ast.Identifier); ok {
			if strings.HasPrefix(id.Value, "uint") {
				return true
			}
		}
	case *ast.FieldAccessExpr:
		leftType := cg.exprLLType(e.Left)
		if strings.HasPrefix(leftType, "%struct.") {
			structName := strings.TrimPrefix(leftType, "%struct.")
			if idx := strings.Index(structName, "*"); idx > 0 {
				structName = structName[:idx]
			}
			mangledName := strings.ReplaceAll(structName, ".", "_")
			if layout, ok := cg.structLayouts[mangledName]; ok {
				for i, f := range layout.fields {
					if f == e.Field {
						return layout.isUnsigned[i]
					}
				}
			}
		}
	case *ast.IndexExpr:
		return cg.isUnsignedExpr(e.Left)
	case *ast.InfixExpr:
		return cg.isUnsignedExpr(e.Left)
	case *ast.PrefixExpr:
		return cg.isUnsignedExpr(e.Right)
	}
	return false
}

// resolveVar looks up a variable by name, walking from innermost to
// outermost scope.  Returns the alloca register and LLVM type, or
// ("", "") if the variable is not found (e.g. a global).
func (cg *Codegen) resolveVar(name string) (string, string) {
	for i := len(cg.scopeVars) - 1; i >= 0; i-- {
		if v, ok := cg.scopeVars[i][name]; ok {
			return v.alloca, v.llType
		}
	}
	return "", ""
}

// --- Type mapping: Skink types -> LLVM IR types ---

// mapTypeName generates a sanitized LLVM type name from key/value LLVM types.
func mapTypeName(keyLL, valLL string) string {
	sanitize := func(s string) string {
		s = strings.ReplaceAll(s, "%", "")
		s = strings.ReplaceAll(s, "*", "Ptr")
		s = strings.ReplaceAll(s, ".", "_")
		s = strings.ReplaceAll(s, " ", "_")
		s = strings.ReplaceAll(s, "{", "")
		s = strings.ReplaceAll(s, "}", "")
		s = strings.ReplaceAll(s, ",", "_")
		return s
	}
	return "map_" + sanitize(keyLL) + "__" + sanitize(valLL)
}

// Mapping rules:
//
//	int    -> i32
//	float  -> double
//	bool   -> i1
//	string -> i8*
//	void   -> void
//	struct -> %struct.<Name>
//	*T     -> <T>*
//	[]T    -> [0 x <T>]*  (opaque pointer for now)

// llvmFunctionPointerType builds an LLVM function pointer type string like "i32 (i32, i32)*".
func llvmFunctionPointerType(retType string, paramTypes []string) string {
	return retType + " (" + strings.Join(paramTypes, ", ") + ")*"
}

// parseFunctionPointerParams extracts the parameter LLVM type strings from a
// function pointer type such as "i32 (%struct.S*, i32)*".
func parseFunctionPointerParams(fnType string) []string {
	// Strip the trailing pointer marker.
	if strings.HasSuffix(fnType, "*") {
		fnType = strings.TrimSuffix(fnType, "*")
	}
	// Find the first '(' after the return type.
	start := strings.Index(fnType, "(")
	if start < 0 {
		return nil
	}
	// Find the matching ')'.
	depth := 1
	end := -1
	for i := start + 1; i < len(fnType); i++ {
		switch fnType[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	inner := fnType[start+1 : end]
	if strings.TrimSpace(inner) == "" {
		return nil
	}
	// Split by commas at depth 0.
	var params []string
	depth = 0
	last := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				params = append(params, strings.TrimSpace(inner[last:i]))
				last = i + 1
			}
		}
	}
	params = append(params, strings.TrimSpace(inner[last:]))
	return params
}

// typeAliases holds type alias mappings populated during code generation.
var typeAliases map[string]ast.Type

// currentCodegen points to the active Codegen instance during code generation,
// allowing package-level functions like llvmType to access codegen state.
var currentCodegen *Codegen

// resolveTypeAliases recursively resolves NamedType aliases to their underlying type.
func resolveTypeAliases(t ast.Type) ast.Type {
	if typeAliases == nil {
		return t
	}
	seen := make(map[string]bool)
	for {
		nt, ok := t.(*ast.NamedType)
		if !ok {
			return t
		}
		if seen[nt.Name] {
			return t // cycle detected, return as-is
		}
		seen[nt.Name] = true
		if aliased, ok := typeAliases[nt.Name]; ok {
			t = aliased
		} else {
			return t
		}
	}
}

// llvmType returns the LLVM IR type string for an AST type.
func llvmType(t ast.Type) string {
	if t == nil {
		return "void"
	}
	// Check for named function type aliases before resolving, to use named LLVM types
	// that avoid comma-parsing issues in extractvalue/insertvalue.
	if nt, ok := t.(*ast.NamedType); ok {
		if cg := currentCodegen; cg != nil {
			if namedLL, ok2 := cg.fnAliasLLTypes[nt.Name]; ok2 {
				return namedLL
			}
		}
	}
	// Resolve type aliases before mapping.
	t = resolveTypeAliases(t)
	switch tt := t.(type) {
	case *ast.NamedType:
		if tt.Name == "Reader" || tt.Name == "Writer" || tt.Name == "reader.Reader" || tt.Name == "writer.Writer" {
			return "i8*"
		}
		switch tt.Name {
		case "int":
			return "i32"
		case "int8":
			return "i8"
		case "int16":
			return "i16"
		case "int32":
			return "i32"
		case "int64":
			return "i64"
		case "uint":
			return "i32"
		case "uint8":
			return "i8"
		case "uint16":
			return "i16"
		case "uint32":
			return "i32"
		case "uint64":
			return "i64"
		case "float":
			return "double"
		case "bool":
			return "i1"
		case "string":
			return "i8*"
		case "bytes":
			return "i8*"
		case "void":
			return "void"
		case "error":
			return "%error"
		default:
			return fmt.Sprintf("%%struct.%s", strings.ReplaceAll(tt.Name, ".", "_"))
		}
	case *ast.PointerType:
		return llvmType(tt.Elem) + "*"
	case *ast.ArrayType:
		return llvmType(tt.Elem) + "*" // arrays are passed as pointers to element
	case *ast.SetType:
		et := llvmType(tt.Elem)
		if et == "i8*" {
			return "%set.Str*"
		}
		return "%set.Int*"
	case *ast.ChanType:
		return "i8*" // channels are opaque runtime handles
	case *ast.MapType:
		return "%" + mapTypeName(llvmType(tt.Key), llvmType(tt.Elem)) + "*"
	case *ast.TensorType:
		return "i8*"
	case *ast.TupleType:
		var parts []string
		for _, t := range tt.Types {
			parts = append(parts, llvmType(t))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case *ast.FunctionType:
		var paramParts []string
		for _, pt := range tt.ParamTypes {
			lt := llvmType(pt)
			if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
				paramParts = append(paramParts, lt+"*")
			} else {
				paramParts = append(paramParts, lt)
			}
		}
		ret := llvmType(tt.ReturnType)
		if ret == "" {
			ret = "void"
		}
		return ret + " (" + strings.Join(paramParts, ", ") + ")*"
	}
	return "i32"
}

// defaultValue returns the LLVM zero-value literal for a given Skink type.
// Used when a variable is declared without an initialiser.
func defaultValue(t ast.Type) string {
	if t == nil {
		return ""
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		switch tt.Name {
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64":
			return "0"
		case "float":
			return "0.0"
		case "bool":
			return "false"
		case "string":
			return "null"
		case "bytes":
			return "null"
		case "error":
			return "zeroinitializer"
		default:
			return "zeroinitializer"
		}
	case *ast.ArrayType:
		return "null"
	case *ast.PointerType:
		return "null"
	case *ast.SetType:
		return "null"
	}
	return "0"
}

// --- Top-level emission ---

// EmitProgram generates the complete LLVM module for a Skink Program.
// It emits the module header, declares runtime functions (printf), and
// then walks every top-level declaration in order.
func (cg *Codegen) EmitProgram(prog *ast.Program) {
	currentCodegen = cg
	// Module header goes into moduleHeader so it precedes string globals.
	cg.moduleHeader.WriteString("; ModuleID = 'Skink'\n")
	if cg.debug && cg.debugInfo != nil {
		cg.moduleHeader.WriteString(fmt.Sprintf("source_filename = \"%s\"\n\n", cg.debugInfo.filename))
		cg.moduleHeader.WriteString(cg.debugInfo.ModuleHeader())
	} else {
		cg.moduleHeader.WriteString("source_filename = \"Skink\"\n\n")
	}
	// Declare printf for built-in print support.
	cg.moduleHeader.WriteString("declare i32 @printf(i8*, ...)\n\n")
	// Newline string constant for print/println.
	cg.moduleHeader.WriteString("@str.newline = private constant [2 x i8] c\"\\0A\\00\"\n\n")
	// Declare libc functions for string concatenation and comparison.
	cg.moduleHeader.WriteString("declare i8* @malloc(i64)\n")
	cg.moduleHeader.WriteString("declare void @free(i8*)\n")
	// Declare Skink ARC runtime functions.
	cg.moduleHeader.WriteString("declare i8* @Skink_rc_alloc(i64)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_rc_retain(i8*)\n")
	cg.moduleHeader.WriteString("declare void @Skink_rc_release(i8*)\n")
	cg.moduleHeader.WriteString("declare i64 @strlen(i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @strcpy(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @strcat(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare i32 @strcmp(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare void @exit(i32)\n")
	cg.moduleHeader.WriteString("declare i32 @usleep(i32)\n")
	cg.moduleHeader.WriteString("declare i8* @calloc(i64, i64)\n")
	cg.moduleHeader.WriteString("declare i8* @strncpy(i8*, i8*, i64)\n")
	cg.moduleHeader.WriteString("declare double @pow(double, double)\n")
	cg.moduleHeader.WriteString("declare double @sin(double)\n")
	cg.moduleHeader.WriteString("declare double @cos(double)\n")
	cg.moduleHeader.WriteString("declare double @tan(double)\n")
	cg.moduleHeader.WriteString("declare double @sqrt(double)\n\n")
	// Declare tensor runtime functions.
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_ones(i32, i32)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_zeros(i32, i32)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_matmul(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare double @Skink_tensor_get(i8*, i32, i32)\n")
	cg.moduleHeader.WriteString("declare void @Skink_tensor_free(i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_transpose(i8*)\n")
	cg.moduleHeader.WriteString("declare double @Skink_tensor_det(i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_inv(i8*)\n")
	cg.moduleHeader.WriteString("declare double @Skink_math_diff(double (double)*, double)\n")
	cg.moduleHeader.WriteString("declare double @Skink_math_integrate(double (double)*, double, double)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_gradient(double (double*)*, i8*)\n")
	cg.moduleHeader.WriteString("declare double @Skink_tensor_dot(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_cross(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare double @Skink_tensor_norm(i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_tensor_eigenvalues(i8*)\n\n")
	// Declare concurrency runtime functions.
	cg.moduleHeader.WriteString("declare i8* @Skink_chan_make(i32, i32)\n")
	cg.moduleHeader.WriteString("declare void @Skink_chan_send(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_chan_recv(i8*)\n")
	cg.moduleHeader.WriteString("declare void @Skink_chan_close(i8*)\n")
	cg.moduleHeader.WriteString("declare i32 @Skink_chan_select(i32, i8**, i32*, i8**, i8**, i32)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_future_make()\n")
	cg.moduleHeader.WriteString("declare void @Skink_future_set(i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_future_get(i8*)\n")
	cg.moduleHeader.WriteString("declare void @Skink_spawn(i8*, i8*, i8*)\n")
	cg.moduleHeader.WriteString("declare void @Skink_free(i8*)\n\n")
	// Declare ruleset runtime functions.
	cg.moduleHeader.WriteString("declare i8* @Skink_ruleset_create()\n")
	cg.moduleHeader.WriteString("declare void @Skink_ruleset_add_rule(i8*, i8*, void (i8*)*, void (i8*)*, void (i8*)*, i32)\n")
	cg.moduleHeader.WriteString("declare void @Skink_ruleset_start(i8*)\n")
	cg.moduleHeader.WriteString("declare void @Skink_ruleset_stop(i8*)\n")
	cg.moduleHeader.WriteString("declare void @Skink_ruleset_reset(i8*)\n")
	cg.moduleHeader.WriteString("declare i32 @Skink_ruleset_is_running(i8*)\n")
	cg.moduleHeader.WriteString("declare i8* @Skink_ruleset_loop(i8*)\n\n")
	// Set type definitions for integer and string sets.
	cg.moduleHeader.WriteString("%set.Int = type { i32*, i32 }\n")
	cg.moduleHeader.WriteString("%set.Str = type { i8**, i32 }\n")
	// Error fat pointer type (function pointer + data pointer).
	cg.moduleHeader.WriteString("%error = type { i8* (i8*)*, i8* }\n\n")

	// Declare reflect runtime functions (definitions live in reflect_rt.c).
	cg.moduleHeader.WriteString("declare i1 @reflect_get_bool(i64, i32)\n")
	cg.moduleHeader.WriteString("declare void @reflect_set_bool(i64, i32, i1)\n")
	cg.moduleHeader.WriteString("declare i32 @reflect_get_int(i64, i32)\n")
	cg.moduleHeader.WriteString("declare void @reflect_set_int(i64, i32, i32)\n")
	cg.moduleHeader.WriteString("declare i64 @reflect_get_int64(i64, i32)\n")
	cg.moduleHeader.WriteString("declare void @reflect_set_int64(i64, i32, i64)\n")
	cg.moduleHeader.WriteString("declare i8* @reflect_get_string(i64, i32)\n")
	cg.moduleHeader.WriteString("declare void @reflect_set_string(i64, i32, i8*)\n")
	cg.moduleHeader.WriteString("declare double @reflect_get_float(i64, i32)\n")
	cg.moduleHeader.WriteString("declare void @reflect_set_float(i64, i32, double)\n")
	cg.moduleHeader.WriteString("declare i32 @reflect_hash(i64, i8*)\n\n")

	// String constants are collected in stringGlobals as we encounter them
	// during function emission; they are automatically inserted between
	// the module header and the function bodies when String() is called.

	// Pre-populate constants and struct layouts so forward references work
	// (e.g. cimport enums and structs appended at the end of declarations).
	for _, decl := range prog.Declarations {
		switch d := decl.(type) {
		case *ast.ImportDecl:
			parts := strings.Split(d.Path, "/")
			realName := parts[len(parts)-1]
			alias := realName
			if d.Alias != "" {
				alias = d.Alias
			}
			if alias != realName {
				cg.importAliases[alias] = realName
			}
		case *ast.ConstDecl:
			cg.consts[d.Name] = d.Value
		case *ast.ConstBlockDecl:
			for _, cd := range d.Decls {
				cg.consts[cd.Name] = cd.Value
			}
		case *ast.EnumDecl:
			for i, v := range d.Variants {
				cg.consts[v] = &ast.IntegerLiteral{Value: int64(i)}
			}
		case *ast.StructDecl:
			if len(d.TypeParams) > 0 {
				continue
			}
			cg.structDecls[d.Name] = d
			// Also store with underscores to support reflection on mangled names
			if strings.Contains(d.Name, ".") {
				cg.structDecls[strings.ReplaceAll(d.Name, ".", "_")] = d
			}
			fields := make([]string, len(d.Fields))
			fieldTypes := make([]string, len(d.Fields))
			isUnsigned := make([]bool, len(d.Fields))
			for i, f := range d.Fields {
				fields[i] = f.Name
				if f.BitWidth != nil {
					fieldTypes[i] = fmt.Sprintf("i%d", *f.BitWidth)
				} else {
					fieldTypes[i] = llvmType(f.Type)
				}
				isUnsigned[i] = isUnsignedType(f.Type)
			}
			cg.structLayouts[d.Name] = structLayout{fields: fields, fieldTypes: fieldTypes, isUnsigned: isUnsigned}
			if strings.Contains(d.Name, ".") {
				cg.structLayouts[strings.ReplaceAll(d.Name, ".", "_")] = cg.structLayouts[d.Name]
			}
			for _, m := range d.Methods {
				fullName := d.Name + "." + m.Name
				var retTypes []string
				if m.ReturnType != nil {
					if tup, ok := m.ReturnType.(*ast.TupleType); ok {
						for _, t := range tup.Types {
							rt := llvmType(t)
							if rt == "" {
								rt = "void"
							}
							retTypes = append(retTypes, rt)
						}
					} else {
						rt := llvmType(m.ReturnType)
						if rt == "" {
							rt = "void"
						}
						retTypes = append(retTypes, rt)
					}
				} else {
					retTypes = append(retTypes, "void")
				}
				cg.fnRetTypes[fullName] = retTypes
				var paramTypes []string
				for _, p := range m.Params {
					pt := llvmType(p.Type)
					if pt == "" {
						pt = "i32"
					}
					if (strings.HasPrefix(pt, "%struct.") || strings.HasPrefix(pt, "%map_") || strings.HasPrefix(pt, "%set.")) && !strings.HasSuffix(pt, "*") {
						pt = pt + "*"
					}
					paramTypes = append(paramTypes, pt)
				}
				cg.fnParamTypes[fullName] = paramTypes
			}
		case *ast.FnDecl:
			if len(d.TypeParams) > 0 {
				continue
			}
			var retTypes []string
			if d.ReturnType != nil {
				if tup, ok := d.ReturnType.(*ast.TupleType); ok {
					for _, t := range tup.Types {
						rt := llvmType(t)
						if rt == "" {
							rt = "void"
						}
						retTypes = append(retTypes, rt)
					}
				} else {
					rt := llvmType(d.ReturnType)
					if rt == "" {
						rt = "void"
					}
					retTypes = append(retTypes, rt)
				}
			} else {
				retTypes = append(retTypes, "void")
			}
			cg.fnRetTypes[d.Name] = retTypes
			var paramTypes []string
			for _, p := range d.Params {
				pt := llvmType(p.Type)
				if pt == "" {
					pt = "i32"
				}
				if (strings.HasPrefix(pt, "%struct.") || strings.HasPrefix(pt, "%map_") || strings.HasPrefix(pt, "%set.")) && !strings.HasSuffix(pt, "*") {
					pt = pt + "*"
				}
				paramTypes = append(paramTypes, pt)
			}
			cg.fnParamTypes[d.Name] = paramTypes
		case *ast.ExternFnDecl:
			var retTypes []string
			if d.ReturnType != nil {
				rt := llvmType(d.ReturnType)
				if rt == "" {
					rt = "void"
				}
				retTypes = append(retTypes, rt)
			} else {
				retTypes = append(retTypes, "void")
			}
			cg.fnRetTypes[d.Name] = retTypes
			var paramTypes []string
			for _, p := range d.Params {
				pt := llvmType(p.Type)
				if pt == "" {
					pt = "i32"
				}
				if (strings.HasPrefix(pt, "%struct.") || strings.HasPrefix(pt, "%map_") || strings.HasPrefix(pt, "%set.")) && !strings.HasSuffix(pt, "*") {
					pt = pt + "*"
				}
				paramTypes = append(paramTypes, pt)
			}
			cg.fnParamTypes[d.Name] = paramTypes
		case *ast.ServiceDecl:
			cg.services[d.Name] = true
			if d.ForType == "" {
				for _, m := range d.Methods {
					methodName := m.Name
					var retTypes []string
					if m.ReturnType != nil {
						if tup, ok := m.ReturnType.(*ast.TupleType); ok {
							for _, t := range tup.Types {
								rt := llvmType(t)
								if rt == "" {
									rt = "void"
								}
								retTypes = append(retTypes, rt)
							}
						} else {
							rt := llvmType(m.ReturnType)
							if rt == "" {
								rt = "void"
							}
							retTypes = append(retTypes, rt)
						}
					} else {
						retTypes = append(retTypes, "void")
					}
					cg.fnRetTypes[methodName] = retTypes
					var paramTypes []string
					for _, p := range m.Params {
						pt := llvmType(p.Type)
						if pt == "" {
							pt = "i32"
						}
						if (strings.HasPrefix(pt, "%struct.") || strings.HasPrefix(pt, "%map_") || strings.HasPrefix(pt, "%set.")) && !strings.HasSuffix(pt, "*") {
							pt = pt + "*"
						}
						paramTypes = append(paramTypes, pt)
					}
					cg.fnParamTypes[methodName] = paramTypes
				}
			}
		case *ast.RulesetDecl:
			cg.rulesets[d.Name] = true
			for _, r := range d.Rules {
				cg.fnRetTypes[d.Name+"."+r.Name] = []string{"void"}
				cg.fnParamTypes[d.Name+"."+r.Name] = []string{}
			}
			for _, m := range []string{"start", "stop", "restart", "reset"} {
				cg.fnRetTypes[d.Name+"."+m] = []string{"void"}
				cg.fnParamTypes[d.Name+"."+m] = []string{}
			}
		}
	}

	// Register type aliases first so struct fields with aliased types resolve correctly.
	for _, decl := range prog.Declarations {
		if d, ok := decl.(*ast.TypeAliasDecl); ok {
			cg.aliases[d.Name] = d.Type
			if typeAliases != nil {
				typeAliases[d.Name] = d.Type
			}
			// If the alias resolves to a function type, emit a named LLVM type
			// to avoid comma-parsing issues in extractvalue/insertvalue.
			if _, ok := d.Type.(*ast.FunctionType); ok {
				namedLL := "%fp_" + strings.ReplaceAll(d.Name, ".", "_")
				inlineLL := llvmType(d.Type)
				cg.moduleHeader.WriteString(namedLL + " = type " + inlineLL + "\n")
				cg.fnAliasLLTypes[d.Name] = namedLL
			}
		}
	}
	// Emit struct type definitions next so they're available for alloca.
	for _, decl := range prog.Declarations {
		if d, ok := decl.(*ast.StructDecl); ok {
			if len(d.TypeParams) > 0 {
				continue
			}
			cg.emitStructDecl(d)
		}
	}
	// Emit everything else.
	for _, decl := range prog.Declarations {
		cg.emitDeclaration(decl)
	}

	// If the user's main() was parameterless, emit the wrapper that
	// captures argc/argv for the os.Args abstraction.
	if cg.hasMainWrapper {
		cg.emitMainWrapper()
	}
}

// emitDeclaration dispatches a top-level declaration to its specialised emitter.
func (cg *Codegen) emitDeclaration(decl ast.Declaration) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		if len(d.TypeParams) > 0 {
			return
		}
		cg.emitFnDecl(d)
	case *ast.ConstDecl:
		// Global constant
		cg.emitGlobalConst(d)
	case *ast.ConstBlockDecl:
		for _, cd := range d.Decls {
			cg.emitGlobalConst(cd)
		}
	case *ast.StructDecl:
		if len(d.TypeParams) > 0 {
			return
		}
		for _, m := range d.Methods {
			origName := m.Name
			m.Name = d.Name + "." + origName
			cg.emitFnDecl(m)
			m.Name = origName
		}
	case *ast.EnumDecl:
		cg.emitEnumDecl(d)
	case *ast.ModuleDecl:
		// No LLVM output; metadata only.
	case *ast.ImportDecl:
		// No LLVM output; handled at link time.
	case *ast.VarDecl:
		cg.emitGlobalVar(d)
	case *ast.ExternFnDecl:
		cg.emitExternFnDecl(d)
	case *ast.ServiceDecl:
		for _, m := range d.Methods {
			if d.ForType != "" {
				cp := *m
				cp.Name = d.ForType + "." + m.Name
				cg.emitFnDecl(&cp)
			} else {
				cg.emitFnDecl(m)
			}
		}
	case *ast.RulesetDecl:
		cg.emitRulesetDecl(d)
	case *ast.TypeAliasDecl:
		cg.aliases[d.Name] = d.Type
		if typeAliases != nil {
			typeAliases[d.Name] = d.Type
		}
	}
}

// emitRulesetDecl generates LLVM IR for a ruleset declaration backed by the C runtime.
// Each ruleset is represented by a global i8* pointer to a SkinkRuleset struct.
func (cg *Codegen) emitRulesetDecl(d *ast.RulesetDecl) {
	rsVar := d.Name
	cg.writef("@%s = global i8* null\n", rsVar)
	cg.globalVarTypes[rsVar] = "i8*"

	// Emit each static rule as a void function.
	for _, r := range d.Rules {
		ruleFn := d.Name + "." + r.Name
		cg.writef("define void @%s() {\n", ruleFn)
		cg.terminated = false
		cg.pushScope()

		// Evaluate condition.
		condReg := cg.emitExpression(r.Condition)
		condType := cg.exprLLType(r.Condition)
		if condType == "i32" {
			cmpReg := cg.nextReg()
			cg.writef("  %s = icmp ne i32 %s, 0\n", cmpReg, condReg)
			condReg = cmpReg
		}
		thenLabel := cg.nextLabel()
		mergeLabel := cg.nextLabel()
		cg.writef("  br i1 %s, label %%%s, label %%%s\n", condReg, thenLabel, mergeLabel)

		cg.writeln(thenLabel + ":")
		cg.terminated = false
		cg.emitBlockStmt(r.Action)
		if !cg.terminated {
			cg.writef("  br label %%%s\n", mergeLabel)
		}

		cg.writeln(mergeLabel + ":")
		cg.terminated = false
		cg.writef("  ret void\n")
		cg.terminated = true

		cg.popScope()
		cg.writef("}\n")
	}

	// Emit wrapper functions for static rules (C-compatible void (*)(void*)).
	for _, r := range d.Rules {
		wrapperFn := "__skink_wrapper_" + d.Name + "." + r.Name
		cg.writef("define void @%s(i8* %%self) {\n", wrapperFn)
		cg.writef("  call void @%s.%s()\n", d.Name, r.Name)
		cg.writef("  ret void\n")
		cg.writef("}\n")
	}

	// Emit start() method: create ruleset if needed, register static rules, start loop.
	startFn := d.Name + ".start"
	cg.writef("define void @%s() {\n", startFn)
	cg.terminated = false
	checkReg := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", checkReg, rsVar)
	nullReg := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", nullReg, checkReg)
	needsCreate := cg.nextLabel()
	alreadyCreated := cg.nextLabel()
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", nullReg, needsCreate, alreadyCreated)

	cg.writeln(needsCreate + ":")
	createReg := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_ruleset_create()\n", createReg)
	cg.writef("  store i8* %s, i8** @%s\n", createReg, rsVar)
	cg.writef("  br label %%%s\n", alreadyCreated)

	cg.writeln(alreadyCreated + ":")
	rsReg := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg, rsVar)
	// Register static rules.
	for _, r := range d.Rules {
		wrapperFn := "__skink_wrapper_" + d.Name + "." + r.Name
		wrapperCast := cg.nextReg()
		cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", wrapperCast, wrapperFn)
		nullFn := cg.nextReg()
		cg.writef("  %s = inttoptr i64 0 to void (i8*)*\n", nullFn)
		priority := 0
		if r.Priority != 0 {
			priority = r.Priority
		}
		cg.writef("  call void @Skink_ruleset_add_rule(i8* %s, i8* null, void (i8*)* %s, void (i8*)* %s, void (i8*)* %s, i32 %d)\n", rsReg, wrapperCast, nullFn, nullFn, priority)
	}
	cg.writef("  call void @Skink_ruleset_start(i8* %s)\n", rsReg)
	// Spawn the background loop thread.
	loopFn := cg.nextReg()
	cg.writef("  %s = bitcast i8* (i8*)* @Skink_ruleset_loop to i8*\n", loopFn)
	cg.writef("  call void @Skink_spawn(i8* %s, i8* %s, i8* null)\n", loopFn, rsReg)
	cg.writef("  ret void\n")
	cg.writef("}\n\n")

	// Emit stop() method.
	stopFn := d.Name + ".stop"
	cg.writef("define void @%s() {\n", stopFn)
	cg.terminated = false
	rsReg2 := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg2, rsVar)
	cg.writef("  call void @Skink_ruleset_stop(i8* %s)\n", rsReg2)
	cg.writef("  ret void\n")
	cg.writef("}\n")

	// Emit restart() method.
	restartFn := d.Name + ".restart"
	cg.writef("define void @%s() {\n", restartFn)
	cg.writef("  call void @%s()\n", stopFn)
	cg.writef("  call void @%s()\n", startFn)
	cg.writef("  ret void\n")
	cg.writef("}\n")

	// Emit reset() method.
	resetFn := d.Name + ".reset"
	cg.writef("define void @%s() {\n", resetFn)
	cg.terminated = false
	rsReg3 := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg3, rsVar)
	cg.writef("  call void @Skink_ruleset_reset(i8* %s)\n", rsReg3)
	cg.writef("  ret void\n")
	cg.writef("}\n")
}

// getConcreteTypeName extracts the original struct name from an expression,
// using the LLVM type as the primary source and falling back to AST type.
func (cg *Codegen) getConcreteTypeName(expr ast.Expression) string {
	llType := cg.exprLLType(expr)
	t := llType
	t = strings.TrimSuffix(t, "*")
	if strings.HasPrefix(t, "%struct.") {
		llName := strings.TrimPrefix(t, "%struct.")
		if origName, ok := cg.structLLNames[llName]; ok {
			return origName
		}
	}
	// Fallback to AST type.
	astType := cg.exprASTType(expr)
	if astType == nil {
		return ""
	}
	switch tt := astType.(type) {
	case *ast.NamedType:
		return tt.Name
	case *ast.PointerType:
		if nt, ok := tt.Elem.(*ast.NamedType); ok {
			return nt.Name
		}
	}
	return ""
}

// emitRulesetWrapperFns generates C-compatible wrapper functions for a concrete
// RuleSource type. Wrappers are cached so each type is emitted once per module.
func (cg *Codegen) emitRulesetWrapperFns(typeName string) {
	if cg.rulesetWrappers[typeName] {
		return
	}
	cg.rulesetWrappers[typeName] = true

	structType := "%struct." + strings.ReplaceAll(typeName, ".", "_")

	// eval wrapper: triggered() then action() if true
	evalFn := "__skink_ruleset_eval_" + typeName
	oldOut := cg.out
	cg.out = cg.anonFns
	cg.regCounter = 0
	cg.labelCounter = 0
	cg.terminated = false
	cg.writef("define void @%s(i8* %%self) {\n", evalFn)
	castReg := cg.nextReg()
	cg.writef("  %s = bitcast i8* %%self to %s*\n", castReg, structType)
	condReg := cg.nextReg()
	cg.writef("  %s = call i1 @%s.triggered(%s* %s)\n", condReg, typeName, structType, castReg)
	thenLabel := cg.nextLabel()
	mergeLabel := cg.nextLabel()
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", condReg, thenLabel, mergeLabel)
	cg.writeln(thenLabel + ":")
	cg.terminated = false
	cg.writef("  call void @%s.action(%s* %s)\n", typeName, structType, castReg)
	cg.writef("  br label %%%s\n", mergeLabel)
	cg.writeln(mergeLabel + ":")
	cg.terminated = false
	cg.writef("  ret void\n")
	cg.writef("}\n")
	cg.anonFns = cg.out
	cg.out = oldOut

	// start wrapper
	startFn := "__skink_ruleset_start_" + typeName
	oldOut = cg.out
	cg.out = cg.anonFns
	cg.regCounter = 0
	cg.labelCounter = 0
	cg.terminated = false
	cg.writef("define void @%s(i8* %%self) {\n", startFn)
	castReg = cg.nextReg()
	cg.writef("  %s = bitcast i8* %%self to %s*\n", castReg, structType)
	cg.writef("  call void @%s.start(%s* %s)\n", typeName, structType, castReg)
	cg.writef("  ret void\n")
	cg.writef("}\n")
	cg.anonFns = cg.out
	cg.out = oldOut

	// stop wrapper
	stopFn := "__skink_ruleset_stop_" + typeName
	oldOut = cg.out
	cg.out = cg.anonFns
	cg.regCounter = 0
	cg.labelCounter = 0
	cg.terminated = false
	cg.writef("define void @%s(i8* %%self) {\n", stopFn)
	castReg = cg.nextReg()
	cg.writef("  %s = bitcast i8* %%self to %s*\n", castReg, structType)
	cg.writef("  call void @%s.stop(%s* %s)\n", typeName, structType, castReg)
	cg.writef("  ret void\n")
	cg.writef("}\n")
	cg.anonFns = cg.out
	cg.out = oldOut
}

// emitRulesetRegisterRule generates the LLVM IR to register a single dynamic
// rule with the C runtime. It creates the ruleset if necessary, emits wrappers
// for the concrete type, calls priority(), and adds the rule.
func (cg *Codegen) emitRulesetRegisterRule(rulesetName string, arg ast.Expression) {
	// Ensure ruleset pointer exists.
	rsReg := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg, rulesetName)
	nullCheck := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", nullCheck, rsReg)
	createLabel := cg.nextLabel()
	haveLabel := cg.nextLabel()
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", nullCheck, createLabel, haveLabel)
	cg.writeln(createLabel + ":")
	newRs := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_ruleset_create()\n", newRs)
	cg.writef("  store i8* %s, i8** @%s\n", newRs, rulesetName)
	cg.writef("  br label %%%s\n", haveLabel)
	cg.writeln(haveLabel + ":")
	rsReg2 := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg2, rulesetName)

	// Get concrete type and emit wrappers.
	typeName := cg.getConcreteTypeName(arg)
	if typeName == "" {
		cg.Errorf("cannot determine concrete type for registerRule argument")
		return
	}
	cg.emitRulesetWrapperFns(typeName)

	// Get self pointer.
	argReg := cg.emitExpression(arg)
	argType := cg.exprLLType(arg)
	selfReg := argReg
	if !strings.HasSuffix(argType, "*") {
		tmpReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", tmpReg, argType)
		cg.writef("  store %s %s, %s* %s\n", argType, argReg, argType, tmpReg)
		selfReg = tmpReg
	}
	selfCast := cg.nextReg()
	cg.writef("  %s = bitcast %s %s to i8*\n", selfCast, argType, selfReg)

	// Call priority() to get priority value.
	structType := "%struct." + strings.ReplaceAll(typeName, ".", "_")
	priorityReg := cg.nextReg()
	cg.writef("  %s = call i32 @%s.priority(%s* %s)\n", priorityReg, typeName, structType, selfReg)

	// Cast wrapper function pointers.
	evalFn := "__skink_ruleset_eval_" + typeName
	startFn := "__skink_ruleset_start_" + typeName
	stopFn := "__skink_ruleset_stop_" + typeName
	evalCast := cg.nextReg()
	startCast := cg.nextReg()
	stopCast := cg.nextReg()
	cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", evalCast, evalFn)
	cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", startCast, startFn)
	cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", stopCast, stopFn)

	// Call Skink_ruleset_add_rule.
	cg.writef("  call void @Skink_ruleset_add_rule(i8* %s, i8* %s, void (i8*)* %s, void (i8*)* %s, void (i8*)* %s, i32 %s)\n",
		rsReg2, selfCast, evalCast, startCast, stopCast, priorityReg)
}

// emitRulesetRegisterRules generates LLVM IR to register multiple dynamic rules
// from a slice. It reads the slice length at runtime and iterates over elements.
func (cg *Codegen) emitRulesetRegisterRules(rulesetName string, arg ast.Expression) {
	arrReg := cg.emitExpression(arg)
	arrType := cg.exprLLType(arg)

	// Read slice length from prefix at offset -8.
	rawPtr := cg.nextReg()
	cg.writef("  %s = bitcast %s %s to i8*\n", rawPtr, arrType, arrReg)
	isNull := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", isNull, rawPtr)
	nullLabel := cg.nextLabel()
	nonNullLabel := cg.nextLabel()
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", isNull, nullLabel, nonNullLabel)
	cg.writeln(nullLabel + ":")
	cg.writef("  br label %%%s\n", nonNullLabel)
	cg.writeln(nonNullLabel + ":")

	lenPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 -8\n", lenPtr, rawPtr)
	lenTyped := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", lenTyped, lenPtr)
	lenVal := cg.nextReg()
	cg.writef("  %s = load i64, i64* %s\n", lenVal, lenTyped)

	// Loop over elements.
	idxAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxAlloca)
	cg.writef("  store i32 0, i32* %s\n", idxAlloca)
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	postLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.writef("  br label %%%s\n", condLabel)

	cg.writeln(condLabel + ":")
	idxLoad := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxLoad, idxAlloca)
	cmpReg := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cmpReg, idxLoad, lenVal)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cmpReg, bodyLabel, endLabel)

	cg.writeln(bodyLabel + ":")
	idxLoad2 := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxLoad2, idxAlloca)
	// Determine element type from the slice/array type.
	elemType := "i32"
	if strings.HasSuffix(arrType, "*") {
		elemType = strings.TrimSuffix(arrType, "*")
	}
	elemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", elemPtr, elemType, elemType, arrReg, idxLoad2)
	elemVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", elemVal, elemType, elemType, elemPtr)
	cg.emitRulesetRegisterRuleInline(rulesetName, elemType, elemPtr)
	cg.writef("  br label %%%s\n", postLabel)

	cg.writeln(postLabel + ":")
	idxLoad3 := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxLoad3, idxAlloca)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, idxLoad3)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	cg.writeln(endLabel + ":")
}

// emitRulesetRegisterRuleInline registers a single rule given an element pointer
// and type, used inside the registerRules loop.
func (cg *Codegen) emitRulesetRegisterRuleInline(rulesetName string, elemType string, elemPtr string) {
	// Ensure ruleset pointer exists.
	rsReg := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg, rulesetName)
	nullCheck := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", nullCheck, rsReg)
	createLabel := cg.nextLabel()
	haveLabel := cg.nextLabel()
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", nullCheck, createLabel, haveLabel)
	cg.writeln(createLabel + ":")
	newRs := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_ruleset_create()\n", newRs)
	cg.writef("  store i8* %s, i8** @%s\n", newRs, rulesetName)
	cg.writef("  br label %%%s\n", haveLabel)
	cg.writeln(haveLabel + ":")
	rsReg2 := cg.nextReg()
	cg.writef("  %s = load i8*, i8** @%s\n", rsReg2, rulesetName)

	// We need the concrete type name. For the inline version, extract from elemType.
	typeName := ""
	t := elemType
	t = strings.TrimSuffix(t, "*")
	if strings.HasPrefix(t, "%struct.") {
		llName := strings.TrimPrefix(t, "%struct.")
		if origName, ok := cg.structLLNames[llName]; ok {
			typeName = origName
		}
	}
	if typeName == "" {
		cg.Errorf("cannot determine concrete type for registerRules element")
		return
	}
	cg.emitRulesetWrapperFns(typeName)

	// self pointer
	selfCast := cg.nextReg()
	cg.writef("  %s = bitcast %s* %s to i8*\n", selfCast, elemType, elemPtr)

	// Call priority()
	structType := "%struct." + strings.ReplaceAll(typeName, ".", "_")
	priorityReg := cg.nextReg()
	cg.writef("  %s = call i32 @%s.priority(%s* %s)\n", priorityReg, typeName, structType, elemPtr)

	// Cast wrapper function pointers.
	evalFn := "__skink_ruleset_eval_" + typeName
	startFn := "__skink_ruleset_start_" + typeName
	stopFn := "__skink_ruleset_stop_" + typeName
	evalCast := cg.nextReg()
	startCast := cg.nextReg()
	stopCast := cg.nextReg()
	cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", evalCast, evalFn)
	cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", startCast, startFn)
	cg.writef("  %s = bitcast void (i8*)* @%s to void (i8*)*\n", stopCast, stopFn)

	cg.writef("  call void @Skink_ruleset_add_rule(i8* %s, i8* %s, void (i8*)* %s, void (i8*)* %s, void (i8*)* %s, i32 %s)\n",
		rsReg2, selfCast, evalCast, startCast, stopCast, priorityReg)
}

// emitGlobalConst emits a global constant declaration and records the value
// for inline substitution at use sites.
func (cg *Codegen) emitGlobalConst(d *ast.ConstDecl) {
	cg.consts[d.Name] = d.Value
	// For now, support only integer constants as globals
	if lit, ok := d.Value.(*ast.IntegerLiteral); ok {
		cg.writef("@%s = constant %s %d\n", d.Name, llvmType(&ast.NamedType{Name: "int"}), lit.Value)
	}
}

// emitGlobalVar emits a global variable declaration.
func (cg *Codegen) emitGlobalVar(d *ast.VarDecl) {
	varType := "i32"
	if d.Type != nil {
		varType = llvmType(d.Type)
	} else if d.Value != nil {
		varType = inferLLType(d.Value)
	}
	cg.globalVarTypes[d.Name] = varType
	if lit, ok := d.Value.(*ast.IntegerLiteral); ok {
		if varType == "double" || varType == "float" {
			cg.writef("@%s = global %s %d.0\n", d.Name, varType, lit.Value)
		} else {
			cg.writef("@%s = global %s %d\n", d.Name, varType, lit.Value)
		}
	} else if flit, ok := d.Value.(*ast.FloatLiteral); ok {
		cg.writef("@%s = global %s %v\n", d.Name, varType, formatFloat(flit.Value))
	} else {
		zeroVal := "0"
		if varType == "double" || varType == "float" {
			zeroVal = "0.000000e+00"
		}
		cg.writef("@%s = global %s %s\n", d.Name, varType, zeroVal)
	}
}

// formatFloat formats a float64 for LLVM IR, ensuring it always has a decimal point.
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// emitStructDecl emits an LLVM structure type definition:
//
//	%struct.<Name> = type { <field-types> }
//
// For structs with bitfield members, emits a packed struct <{ ... }>
// and uses i<N> for fields with an explicit bit width.
func (cg *Codegen) emitStructDecl(d *ast.StructDecl) {
	hasBitfield := false
	for _, f := range d.Fields {
		cg.ensureMapTypesFromAST(f.Type)
		if f.BitWidth != nil {
			hasBitfield = true
			break
		}
	}
	isPacked := hasBitfield
	customAlign := 0
	for _, attr := range d.Attributes {
		if attr == "packed" {
			isPacked = true
		} else if strings.HasPrefix(attr, "align(") && strings.HasSuffix(attr, ")") {
			inside := attr[len("align(") : len(attr)-1]
			if val, err := strconv.Atoi(inside); err == nil {
				customAlign = val
			}
		}
	}
	var b strings.Builder
	structName := strings.ReplaceAll(d.Name, ".", "_")
	cg.structLLNames[structName] = d.Name
	if isPacked {
		cg.writef("%%struct.%s = type <{ ", structName)
		b.WriteString("<{ ")
	} else {
		cg.writef("%%struct.%s = type { ", structName)
		b.WriteString("{ ")
	}
	fields := make([]string, len(d.Fields))
	fieldTypes := make([]string, len(d.Fields))
	isUnsigned := make([]bool, len(d.Fields))
	for i, f := range d.Fields {
		if i > 0 {
			b.WriteString(", ")
			cg.out.WriteString(", ")
		}
		var lt string
		if f.BitWidth != nil {
			lt = fmt.Sprintf("i%d", *f.BitWidth)
		} else {
			lt = llvmType(f.Type)
		}
		b.WriteString(lt)
		cg.out.WriteString(lt)
		fields[i] = f.Name
		fieldTypes[i] = lt
		isUnsigned[i] = isUnsignedType(f.Type)
	}
	if isPacked {
		b.WriteString(" }>")
		cg.writeln(" }>")
	} else {
		b.WriteString(" }")
		cg.writeln(" }")
	}

	// Calculate size, alignment and offsets
	var offsets []int
	var structAlign int = 1
	var size int = 0

	if isPacked {
		structAlign = 1
		for _, f := range d.Fields {
			offsets = append(offsets, size)
			var fs int
			if f.BitWidth != nil {
				fs = (*f.BitWidth + 7) / 8
			} else {
				fs, _ = cg.typeSizeAndAlign(f.Type)
			}
			size += fs
		}
		if customAlign > 0 {
			structAlign = customAlign
		}
		size = alignUp(size, structAlign)
	} else {
		maxAlign := 1
		for _, f := range d.Fields {
			var fs, fa int
			if f.BitWidth != nil {
				fs = (*f.BitWidth + 7) / 8
				fa = 1
			} else {
				fs, fa = cg.typeSizeAndAlign(f.Type)
			}
			if fa > maxAlign {
				maxAlign = fa
			}
			size = alignUp(size, fa)
			offsets = append(offsets, size)
			size += fs
		}
		if customAlign > 0 {
			structAlign = customAlign
		} else {
			structAlign = maxAlign
		}
		size = alignUp(size, structAlign)
	}

	cg.structLayouts[d.Name] = structLayout{
		llType:     b.String(),
		fields:     fields,
		fieldTypes: fieldTypes,
		isUnsigned: isUnsigned,
		size:       size,
		alignment:  structAlign,
		offsets:    offsets,
	}
	if strings.Contains(d.Name, ".") {
		cg.structLayouts[strings.ReplaceAll(d.Name, ".", "_")] = cg.structLayouts[d.Name]
	}
}

// emitEnumDecl emits enum variants as named integer constants.
func (cg *Codegen) emitEnumDecl(d *ast.EnumDecl) {
	for i, v := range d.Variants {
		cg.consts[v] = &ast.IntegerLiteral{Value: int64(i)}
	}
}

// emitFnDecl emits a function definition:
//
//	define <ret-type> @<name>(<param-list>) {
//	entry:
//	  ...
//	}
//
// Parameters are stored into stack slots (alloca) so they can be
// mutated and so that address-of works uniformly with local variables.
func (cg *Codegen) emitFnDecl(fn *ast.FnDecl) {
	// Reset counters per function so IR is deterministic.
	cg.regCounter = 0
	cg.labelCounter = 0
	cg.terminated = false
	cg.deferred = nil
	cg.currentFnBody = fn.Body

	retType := llvmType(fn.ReturnType)
	if retType == "" {
		retType = "void"
	}
	cg.currentFnRetType = retType

	// Determine the LLVM function name. For a parameterless main, we generate
	// a wrapper @main that captures argc/argv and then calls @_skink_main.
	llvmName := fn.Name
	if fn.Name == "main" && len(fn.Params) == 0 {
		cg.hasMainWrapper = true
		cg.mainRetType = retType
		llvmName = "_skink_main"
	}
	cg.currentFnName = llvmName
	cg.scopeVars = []map[string]scopeVar{make(map[string]scopeVar)}

	// Handle [cuda] attribute: emit declaration only, track kernel name.
	for _, attr := range fn.Attributes {
		if attr == "cuda" {
			cg.cudaKernels = append(cg.cudaKernels, llvmName)
			cg.writef("declare %s @%s(", retType, llvmName)
			firstParam := true
			for _, p := range fn.Params {
				if p.Variadic {
					continue
				}
				if !firstParam {
					cg.out.WriteString(", ")
				}
				firstParam = false
				lt := llvmType(p.Type)
				if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
					cg.writef("%s*", lt)
				} else {
					cg.writef("%s", lt)
				}
			}
			cg.writeln(") ; CUDA kernel")
			return
		}
	}

	// Track return types for multi-return support.
	var retTypes []string
	if tup, ok := fn.ReturnType.(*ast.TupleType); ok {
		for _, t := range tup.Types {
			retTypes = append(retTypes, llvmType(t))
		}
	} else if fn.ReturnType != nil {
		lt := llvmType(fn.ReturnType)
		if lt != "" && lt != "void" {
			retTypes = []string{lt}
		}
	} else {
		retTypes = []string{"void"}
	}
	cg.currentFnRetTypes = retTypes
	cg.fnRetTypes[llvmName] = retTypes
	cg.fnRetTypes[fn.Name] = retTypes
	var paramTypes []string
	variadic := false
	for _, p := range fn.Params {
		if p.Variadic {
			variadic = true
		} else {
			lt := llvmType(p.Type)
			if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
				paramTypes = append(paramTypes, lt+"*")
			} else {
				paramTypes = append(paramTypes, lt)
			}
		}
	}
	cg.fnParamTypes[llvmName] = paramTypes
	cg.fnParamTypes[fn.Name] = paramTypes
	cg.fnVariadic[llvmName] = variadic
	cg.fnVariadic[fn.Name] = variadic

	if strings.HasPrefix(fn.Name, "reflect.TypeOf_") {
		cg.writef("define %s @%s(", retType, fn.Name)
		for i, p := range fn.Params {
			if i > 0 {
				cg.out.WriteString(", ")
			}
			lt := llvmType(p.Type)
			if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
				cg.writef("%s* %%%s", lt, p.Name)
			} else {
				cg.writef("%s %%%s", lt, p.Name)
			}
		}
		cg.writeln(") {")
		cg.emitReflectTypeOfBody(fn)
		return
	}

	if fn.Body == nil {
		cg.writef("declare %s @%s(", retType, llvmName)
		firstParam := true
		for _, p := range fn.Params {
			if p.Variadic {
				continue
			}
			if !firstParam {
				cg.out.WriteString(", ")
			}
			firstParam = false
			lt := llvmType(p.Type)
			if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
				cg.writef("%s*", lt)
			} else {
				cg.writef("%s", lt)
			}
		}
		if variadic {
			if !firstParam {
				cg.out.WriteString(", ")
			}
			cg.out.WriteString("...")
		}
		cg.writeln(")")
		return
	}

	// Set up debug scope for this function.
	if cg.debug && cg.debugInfo != nil {
		cg.setDebugLoc(fn.Token.Line, fn.Token.Column)
		cg.debugScope = cg.debugInfo.Subprogram(fn.Name, fn.Token.Line)
	}

	cg.writef("define %s @%s(", retType, llvmName)
	firstParam := true
	for _, p := range fn.Params {
		if p.Variadic {
			continue
		}
		if !firstParam {
			cg.out.WriteString(", ")
		}
		firstParam = false
		lt := llvmType(p.Type)
		if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
			cg.writef("%s* %%%s", lt, p.Name)
		} else {
			cg.writef("%s %%%s", lt, p.Name)
		}
	}
	if variadic {
		if !firstParam {
			cg.out.WriteString(", ")
		}
		cg.out.WriteString("...")
	}
	if cg.debug && cg.debugScope != 0 {
		cg.writef(") !dbg !%d {\n", cg.debugScope)
		// Emit subprogram metadata definition.
		cg.metadata.WriteString(cg.debugInfo.SubprogramDef(cg.debugScope, fn.Name, fn.Token.Line))
		typeID := cg.debugInfo.allocID()
		cg.metadata.WriteString(cg.debugInfo.SubroutineTypeDef(typeID))
		listID := cg.debugInfo.allocID()
		cg.metadata.WriteString(cg.debugInfo.TypeListDef(listID))
	} else {
		cg.writeln(") {")
	}

	// Entry block
	cg.writeln("entry:")

	// Allocate stack slots for parameters.
	for _, p := range fn.Params {
		if p.Variadic {
			continue
		}
		alloca := cg.nextReg()
		lt := llvmType(p.Type)
		if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
			cg.writef("  %s = alloca %s*\n", alloca, lt)
			cg.writef("  store %s* %%%s, %s** %s\n", lt, p.Name, lt, alloca)
			cg.declareVar(p.Name, alloca, lt+"*", isUnsignedType(p.Type), p.Type)
		} else {
			cg.writef("  %s = alloca %s\n", alloca, lt)
			cg.writef("  store %s %%%s, %s* %s\n", lt, p.Name, lt, alloca)
			cg.declareVar(p.Name, alloca, lt, isUnsignedType(p.Type), p.Type)
		}
	}

	cg.emitFuncBody(func() {
		cg.pushScope()
		cg.emitBlockStmt(fn.Body)
		// We run popScope right here, but wait: if '!cg.terminated', we should also make sure all deallocs are run.
		// Since popScope emits block-level deallocations, and this is the outermost block scope, it will deallocate any remaining local candidates before pop.
		// If cg.terminated is already true, popScope will still run the pop logic (though instructions after terminator might be dead, but LLVM handles that or we can guard it).
		cg.popScope()

		// Ensure every function has a terminator.
		if !cg.terminated {
			cg.emitAllDeallocations()
			cg.emitDeferred()
			if retType == "void" {
				cg.writeln("  ret void")
			} else if strings.HasSuffix(retType, "*") || strings.HasPrefix(retType, "%struct.") || strings.HasPrefix(retType, "%map_") || strings.HasPrefix(retType, "%set.") {
				cg.writef("  ret %s null\n", retType)
			} else {
				cg.writef("  ret %s 0\n", retType)
			}
		}
	})
	cg.writeln("}")
	cg.writeln("")
}

// emitFnLiteral emits an anonymous function as a top-level LLVM function and returns its pointer.
func (cg *Codegen) emitFnLiteral(fn *ast.FnLiteral) string {
	name := fmt.Sprintf("anon_fn_%d", cg.anonFnCounter)
	cg.anonFnCounter++

	retType := llvmType(fn.ReturnType)
	if retType == "" {
		retType = "void"
	}

	var retTypes []string
	if tup, ok := fn.ReturnType.(*ast.TupleType); ok {
		for _, t := range tup.Types {
			retTypes = append(retTypes, llvmType(t))
		}
	} else if fn.ReturnType != nil {
		lt := llvmType(fn.ReturnType)
		if lt != "" && lt != "void" {
			retTypes = []string{lt}
		}
	}
	cg.fnRetTypes[name] = retTypes

	var paramTypes []string
	for _, p := range fn.Params {
		paramTypes = append(paramTypes, llvmType(p.Type))
	}
	cg.fnParamTypes[name] = paramTypes

	// Save current output and state.
	oldOut := cg.out
	oldRegCounter := cg.regCounter
	oldLabelCounter := cg.labelCounter
	oldTerminated := cg.terminated
	oldDeferred := cg.deferred
	oldCurrentFnName := cg.currentFnName
	oldCurrentFnRetType := cg.currentFnRetType
	oldCurrentFnRetTypes := cg.currentFnRetTypes
	oldScopeVars := cg.scopeVars
	oldClosureEnv := cg.closureEnv
	oldCurrentFnBody := cg.currentFnBody

	// If this is a closure (has captures), create env struct type and global.
	hasCaptures := len(fn.Captures) > 0
	var envStructName, envGlobalName string
	if hasCaptures {
		envStructName = fmt.Sprintf("struct.anon_env_%d", cg.anonFnCounter-1)
		envGlobalName = fmt.Sprintf("@anon_env_%d", cg.anonFnCounter-1)
		var envFields []string
		cg.closureEnv = make(map[string]closureEnvInfo)
		for i, capName := range fn.Captures {
			capLLType := "i32"
			for j := len(oldScopeVars) - 1; j >= 0; j-- {
				if sv, ok := oldScopeVars[j][capName]; ok {
					capLLType = sv.llType
					break
				}
			}
			if capLLType == "" {
				capLLType = "i32"
			}
			envFields = append(envFields, capLLType)
			cg.closureEnv[capName] = closureEnvInfo{
				envStruct:  envStructName,
				globalName: envGlobalName,
				fieldIndex: i,
				llType:     capLLType,
			}
		}
		cg.moduleHeader.WriteString(fmt.Sprintf("%%%s = type { %s }\n", envStructName, strings.Join(envFields, ", ")))
		cg.moduleHeader.WriteString(fmt.Sprintf("%s = global %%%s zeroinitializer\n\n", envGlobalName, envStructName))
	}

	cg.out = cg.anonFns
	cg.regCounter = 0
	cg.labelCounter = 0
	cg.terminated = false
	cg.deferred = nil
	cg.currentFnName = name
	cg.currentFnRetType = retType
	cg.currentFnRetTypes = retTypes
	cg.currentFnBody = fn.Body
	cg.scopeVars = []map[string]scopeVar{make(map[string]scopeVar)}

	cg.writef("define %s @%s(", retType, name)
	for i, p := range fn.Params {
		if i > 0 {
			cg.out.WriteString(", ")
		}
		lt := llvmType(p.Type)
		if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
			cg.writef("%s* %%%s", lt, p.Name)
		} else {
			cg.writef("%s %%%s", lt, p.Name)
		}
	}
	cg.out.WriteString(") {\n")
	cg.writeln("entry:")

	for _, p := range fn.Params {
		alloca := cg.nextReg()
		lt := llvmType(p.Type)
		if (strings.HasPrefix(lt, "%struct.") || strings.HasPrefix(lt, "%map_") || strings.HasPrefix(lt, "%set.")) && !strings.HasSuffix(lt, "*") {
			cg.writef("  %s = alloca %s*\n", alloca, lt)
			cg.writef("  store %s* %%%s, %s** %s\n", lt, p.Name, lt, alloca)
			cg.declareVar(p.Name, alloca, lt+"*", isUnsignedType(p.Type), p.Type)
		} else {
			cg.writef("  %s = alloca %s\n", alloca, lt)
			cg.writef("  store %s %%%s, %s* %s\n", lt, p.Name, lt, alloca)
			cg.declareVar(p.Name, alloca, lt, isUnsignedType(p.Type), p.Type)
		}
	}

	cg.emitFuncBody(func() {
		cg.pushScope()
		cg.emitBlockStmt(fn.Body)
		cg.popScope()

		if !cg.terminated {
			cg.emitAllDeallocations()
			cg.emitDeferred()
			if retType == "void" {
				cg.writeln("  ret void")
			} else if strings.HasSuffix(retType, "*") || strings.HasPrefix(retType, "%struct.") || strings.HasPrefix(retType, "%map_") || strings.HasPrefix(retType, "%set.") {
				cg.writef("  ret %s null\n", retType)
			} else {
				cg.writef("  ret %s 0\n", retType)
			}
		}
	})
	cg.writeln("}")
	cg.writeln("")

	// Restore state.
	cg.anonFns = cg.out
	cg.out = oldOut
	cg.regCounter = oldRegCounter
	cg.labelCounter = oldLabelCounter
	cg.terminated = oldTerminated
	cg.deferred = oldDeferred
	cg.currentFnName = oldCurrentFnName
	cg.currentFnRetType = oldCurrentFnRetType
	cg.currentFnRetTypes = oldCurrentFnRetTypes
	cg.scopeVars = oldScopeVars
	cg.closureEnv = oldClosureEnv
	cg.currentFnBody = oldCurrentFnBody

	// If this is a closure with captures, store the current values of captured
	// variables into the env struct, and redirect outer function accesses through
	// the env struct so both the closure and the outer function share state.
	if hasCaptures {
		for i, capName := range fn.Captures {
			capLLType := "i32"
			var capAlloca string
			for j := len(oldScopeVars) - 1; j >= 0; j-- {
				if sv, ok := oldScopeVars[j][capName]; ok {
					capLLType = sv.llType
					capAlloca = sv.alloca
					break
				}
			}
			if capLLType == "" {
				capLLType = "i32"
			}
			// Store current value into env struct.
			if capAlloca != "" {
				loadReg := cg.nextReg()
				cg.writef("  %s = load %s, %s* %s\n", loadReg, capLLType, capLLType, capAlloca)
				ptrReg := cg.nextReg()
				cg.writef("  %s = getelementptr %%%s, %%%s* %s, i32 0, i32 %d\n", ptrReg, envStructName, envStructName, envGlobalName, i)
				cg.writef("  store %s %s, %s* %s\n", capLLType, loadReg, capLLType, ptrReg)
			}
			// Add to outer function's closureEnv so subsequent reads/writes go through env.
			if cg.closureEnv == nil {
				cg.closureEnv = make(map[string]closureEnvInfo)
			}
			cg.closureEnv[capName] = closureEnvInfo{
				envStruct:  envStructName,
				globalName: envGlobalName,
				fieldIndex: i,
				llType:     capLLType,
			}
		}
	}

	return "@" + name
}

// emitMainWrapper generates the real @main entry point, globals for argc/argv,
// and helper functions that back the os.Args / os.ArgsCount abstraction.
func (cg *Codegen) emitMainWrapper() {
	cg.writeln("")
	cg.writeln("; Skink runtime: argc/argv globals")
	cg.writeln("@skink_argc = global i32 0")
	cg.writeln("@skink_argv = global i8** null")

	cg.writeln("")
	cg.writeln("define void @_skink_runtime_set_argc(i32 %argc) {")
	cg.writeln("entry:")
	cg.writeln("  store i32 %argc, i32* @skink_argc")
	cg.writeln("  ret void")
	cg.writeln("}")

	cg.writeln("")
	cg.writeln("define void @_skink_runtime_set_argv(i8** %argv) {")
	cg.writeln("entry:")
	cg.writeln("  store i8** %argv, i8*** @skink_argv")
	cg.writeln("  ret void")
	cg.writeln("}")

	cg.writeln("")
	cg.writeln("define i32 @_skink_runtime_get_argc() {")
	cg.writeln("entry:")
	cg.writeln("  %r = load i32, i32* @skink_argc")
	cg.writeln("  ret i32 %r")
	cg.writeln("}")

	cg.writeln("")
	cg.writeln("define i8** @_skink_runtime_get_argv() {")
	cg.writeln("entry:")
	cg.writeln("  %r = load i8**, i8*** @skink_argv")
	cg.writeln("  ret i8** %r")
	cg.writeln("}")

	cg.writeln("")
	cg.writeln("define i32 @main(i32 %argc, i8** %argv) {")
	cg.writeln("entry:")
	cg.writeln("  call void @_skink_runtime_set_argc(i32 %argc)")
	cg.writeln("  call void @_skink_runtime_set_argv(i8** %argv)")

	switch cg.mainRetType {
	case "void":
		cg.writeln("  call void @_skink_main()")
		cg.writeln("  ret i32 0")
	case "i32":
		cg.writeln("  %r = call i32 @_skink_main()")
		cg.writeln("  ret i32 %r")
	case "double":
		cg.writeln("  %r = call double @_skink_main()")
		cg.writeln("  %r_int = fptosi double %r to i32")
		cg.writeln("  ret i32 %r_int")
	case "i1":
		cg.writeln("  %r = call i1 @_skink_main()")
		cg.writeln("  %r_int = zext i1 %r to i32")
		cg.writeln("  ret i32 %r_int")
	default:
		cg.writef("  call %s @_skink_main()\n", cg.mainRetType)
		cg.writeln("  ret i32 0")
	}
	cg.writeln("}")
}

// emitExternFnDecl emits an LLVM function declaration (not definition).
func (cg *Codegen) emitExternFnDecl(fn *ast.ExternFnDecl) {
	retType := llvmType(fn.ReturnType)
	if retType == "" {
		retType = "void"
	}
	// Skip emitting declare statement if the function is already pre-declared in the module header
	isPreDeclared := false
	switch fn.Name {
	case "printf", "malloc", "free", "strlen", "strcpy", "strcat", "strcmp", "exit", "usleep", "calloc", "strncpy",
		"pow", "sin", "cos", "tan", "sqrt",
		"_skink_runtime_set_argc", "_skink_runtime_set_argv", "_skink_runtime_get_argc", "_skink_runtime_get_argv",
		"Skink_chan_make", "Skink_chan_send", "Skink_chan_recv", "Skink_chan_close", "Skink_chan_select",
		"Skink_future_make", "Skink_future_set", "Skink_future_get",
		"Skink_spawn", "Skink_free",
		"reflect_get_int", "reflect_set_int", "reflect_get_string", "reflect_set_string",
		"reflect_get_float", "reflect_set_float", "reflect_hash",
		"reflect_get_bool", "reflect_set_bool", "reflect_get_int64", "reflect_set_int64",
		"reflect_get_ptr", "reflect_set_ptr":
		isPreDeclared = true
	}

	llvmFnName := fn.Name
	if fn.Name == "_close" {
		llvmFnName = "close"
	}

	if !isPreDeclared {
		cg.writef("declare %s @%s(", retType, llvmFnName)
		for i, p := range fn.Params {
			if i > 0 {
				cg.out.WriteString(", ")
			}
			cg.writef("%s", llvmType(p.Type))
		}
		if fn.Varargs {
			if len(fn.Params) > 0 {
				cg.out.WriteString(", ")
			}
			cg.out.WriteString("...")
		}
		cg.writeln(")")
	}
	// Track return and param types for identifier resolution.
	cg.fnRetTypes[fn.Name] = []string{retType}
	var paramTypes []string
	for _, p := range fn.Params {
		paramTypes = append(paramTypes, llvmType(p.Type))
	}
	cg.fnParamTypes[fn.Name] = paramTypes
	cg.fnVariadic[fn.Name] = fn.Varargs
}

// emitBlockStmt emits every statement in a block sequentially.
// Statements after an unreachable terminator (e.g. panic, assert, return)
// are silently skipped to avoid invalid IR.
func (cg *Codegen) emitBlockStmt(block *ast.BlockStmt) {
	for _, stmt := range block.Statements {
		if cg.terminated {
			break
		}
		cg.emitStatement(stmt)
	}
}

// tokenFromStmt extracts the token (and thus line/column) from any statement.
func tokenFromStmt(stmt ast.Statement) token.Token {
	switch s := stmt.(type) {
	case *ast.VarStmt:
		return s.Token
	case *ast.TupleVarStmt:
		return s.Token
	case *ast.VarBlockStmt:
		return s.Token
	case *ast.ExprStmt:
		return s.Token
	case *ast.BlockStmt:
		return s.Token
	case *ast.AssignmentStmt:
		return s.Token
	case *ast.TupleAssignmentStmt:
		return s.Token
	case *ast.ForStmt:
		return s.Token
	case *ast.WhileStmt:
		return s.Token
	case *ast.UntilStmt:
		return s.Token
	case *ast.ReturnStmt:
		return s.Token
	case *ast.BreakStmt:
		return s.Token
	case *ast.ContinueStmt:
		return s.Token
	case *ast.ComptimeStmt:
		return s.Token
	case *ast.DeferStmt:
		return s.Token
	case *ast.UnsafeStmt:
		return s.Token
	case *ast.SpawnStmt:
		return s.Token
	case *ast.SelectStmt:
		return s.Token
	case *ast.WithStmt:
		return s.Token
	case *ast.IfStmt:
		return s.Token
	}
	return token.Token{}
}

// emitStatement dispatches a Skink statement to its specialised emitter.
func (cg *Codegen) emitStatement(stmt ast.Statement) {
	if cg.debug {
		tok := tokenFromStmt(stmt)
		if tok.Line > 0 {
			cg.setDebugLoc(tok.Line, tok.Column)
		}
	}
	switch s := stmt.(type) {
	case *ast.VarStmt:
		cg.emitVarStmt(s)
	case *ast.AssignmentStmt:
		cg.emitAssignmentStmt(s)
	case *ast.TupleAssignmentStmt:
		cg.emitTupleAssignmentStmt(s)
	case *ast.TupleVarStmt:
		cg.emitTupleVarStmt(s)
	case *ast.ReturnStmt:
		cg.emitReturnStmt(s)
	case *ast.BreakStmt:
		cg.emitBreakStmt()
	case *ast.ContinueStmt:
		cg.emitContinueStmt()
	case *ast.ExprStmt:
		cg.emitExprStmt(s)
	case *ast.IfStmt:
		cg.emitIfStmt(s)
	case *ast.WhileStmt:
		cg.emitWhileStmt(s)
	case *ast.UntilStmt:
		cg.emitUntilStmt(s)
	case *ast.DeferStmt:
		cg.emitDeferStmt(s)
	case *ast.UnsafeStmt:
		cg.emitUnsafeStmt(s)
	case *ast.VarBlockStmt:
		cg.emitVarBlockStmt(s)
	case *ast.WithStmt:
		cg.emitWithStmt(s)
	case *ast.ComptimeStmt:
		// Compile-time block: skip emission entirely.
	case *ast.SpawnStmt:
		cg.emitSpawnStmt(s)
	case *ast.SelectStmt:
		cg.emitSelectStmt(s)
	case *ast.SwitchStmt:
		cg.emitSwitchStmt(s)
	case *ast.ForStmt:
		cg.emitForStmt(s)
	case *ast.BlockStmt:
		cg.pushScope()
		cg.emitBlockStmt(s)
		cg.popScope()
	}
}

// emitVarStmt emits a variable declaration:
//
//	<reg> = alloca <type>
//	store <value> <reg>
//
// If the AST does not carry an explicit type (e.g. :=), the type is
// inferred from the initialiser expression via inferLLType.
func (cg *Codegen) emitVarStmt(v *ast.VarStmt) {
	// Handle error propagation: a := foo()?
	if ep, ok := v.Value.(*ast.ErrorPropagationExpr); ok {
		if call, ok2 := ep.Expr.(*ast.CallExpr); ok2 {
			if id, ok3 := call.Function.(*ast.Identifier); ok3 {
				if retTypes, ok4 := cg.fnRetTypes[id.Value]; ok4 && len(retTypes) > 1 {
					aggReg := cg.emitCallExpr(call)
					cg.emitErrorPropagationBranch(aggReg, retTypes)

					lt := retTypes[0]
					alloca := cg.nextReg()
					cg.writef("  %s = alloca %s\n", alloca, lt)

					reg := cg.nextReg()
					aggType := "{ " + strings.Join(retTypes, ", ") + " }"
					cg.writef("  %s = extractvalue %s %s, 0\n", reg, aggType, aggReg)
					if lt == "i32" && retTypes[0] == "i1" {
						zext := cg.nextReg()
						cg.writef("  %s = zext i1 %s to i32\n", zext, reg)
						reg = zext
					}
					cg.writef("  store %s %s, %s* %s\n", lt, reg, lt, alloca)
					cg.declareVar(v.Name, alloca, lt, false, cg.exprASTType(v.Value))
					return
				}
			}
		}
	}

	alloca := cg.nextReg()
	lt := llvmType(v.Type)
	if lt == "" || lt == "void" {
		if v.Value != nil {
			lt = cg.exprLLType(v.Value)
			// For struct init expressions, use the value type, not pointer type.
			if _, ok := v.Value.(*ast.StructInitExpr); ok && strings.HasSuffix(lt, "*") {
				lt = strings.TrimSuffix(lt, "*")
			}
		} else {
			lt = "i32"
		}
	}
	cg.writef("  %s = alloca %s\n", alloca, lt)
	if v.Value != nil {
		valReg := cg.emitExpression(v.Value)
		// If value is i1 but target is i32, zext before storing.
		if lt == "i32" && cg.exprLLType(v.Value) == "i1" {
			zextReg := cg.nextReg()
			cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
			valReg = zextReg
		}
		// If target is struct value but value is a pointer, load the value first.
		if strings.HasPrefix(lt, "%struct.") && !strings.HasSuffix(lt, "*") && strings.HasSuffix(cg.exprLLType(v.Value), "*") {
			loadedReg := cg.nextReg()
			cg.writef("  %s = load %s, %s* %s\n", loadedReg, lt, lt, valReg)
			valReg = loadedReg
		}
		// ARC: retain non-constructor heap values on first assignment.
		if cg.isHeapLLType(lt) && !cg.isConstructorExpr(v.Value) {
			valReg = cg.emitRetain(valReg, lt)
		}
		cg.writef("  store %s %s, %s* %s\n", lt, valReg, lt, alloca)
	} else {
		cg.writef("  store %s %s, %s* %s\n", lt, defaultValue(v.Type), lt, alloca)
	}
	var synType ast.Type
	if v.Type != nil {
		synType = v.Type
	} else {
		synType = cg.exprASTType(v.Value)
	}
	cg.declareVar(v.Name, alloca, lt, isUnsignedType(v.Type) || cg.isUnsignedExpr(v.Value), synType)
	// Record array metadata for for-in loop support.
	if arr, ok := v.Value.(*ast.ArrayLiteral); ok && len(arr.Elements) > 0 {
		cg.arraySizes[cg.currentFnName+":"+alloca] = arrayMeta{
			len:      len(arr.Elements),
			elemType: cg.exprLLType(arr.Elements[0]),
		}
	}
}

// emitAssignmentStmt emits a variable assignment:
//
//	store <value> <alloca>
//
// Supports identifier, field access (obj.field), and index (arr[i]) lvalues.
func (cg *Codegen) emitAssignmentStmt(a *ast.AssignmentStmt) {
	switch lval := a.LValue.(type) {
	case *ast.Identifier:
		cg.emitIdentAssignment(lval, a.Operator, a.Value)
	case *ast.FieldAccessExpr:
		cg.emitFieldAccessAssignment(lval, a.Operator, a.Value)
	case *ast.IndexExpr:
		cg.emitIndexAssignment(lval, a.Operator, a.Value)
	default:
		// Fallback for other expression types
		alloca, lt := cg.resolveVar(a.LValue.String())
		if alloca == "" {
			return
		}
		if lt == "" {
			lt = "i32"
		}
		valReg := cg.emitExpression(a.Value)
		if lt == "i32" && cg.exprLLType(a.Value) == "i1" {
			zextReg := cg.nextReg()
			cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
			valReg = zextReg
		}
		cg.writef("  store %s %s, %s* %s\n", lt, valReg, lt, alloca)
	}
}

// computeCompoundOp loads the current value, applies the operator with the
// new value, and returns the result register.  For non-numeric types or
// unsupported operators it returns the valReg unchanged.
func (cg *Codegen) computeCompoundOp(oldReg, valReg, op, lt string, isUnsigned bool) string {
	if op == "=" {
		return valReg
	}
	isFloat := lt == "double"
	reg := cg.nextReg()
	switch op {
	case "+=":
		if isFloat {
			cg.writef("  %s = fadd double %s, %s\n", reg, oldReg, valReg)
		} else {
			cg.writef("  %s = add %s %s, %s\n", reg, lt, oldReg, valReg)
		}
	case "-=":
		if isFloat {
			cg.writef("  %s = fsub double %s, %s\n", reg, oldReg, valReg)
		} else {
			cg.writef("  %s = sub %s %s, %s\n", reg, lt, oldReg, valReg)
		}
	case "*=":
		if isFloat {
			cg.writef("  %s = fmul double %s, %s\n", reg, oldReg, valReg)
		} else {
			cg.writef("  %s = mul %s %s, %s\n", reg, lt, oldReg, valReg)
		}
	case "/=":
		if isFloat {
			cg.writef("  %s = fdiv double %s, %s\n", reg, oldReg, valReg)
		} else {
			if isUnsigned {
				cg.writef("  %s = udiv %s %s, %s\n", reg, lt, oldReg, valReg)
			} else {
				cg.writef("  %s = sdiv %s %s, %s\n", reg, lt, oldReg, valReg)
			}
		}
	case "%=":
		if isFloat {
			cg.writef("  %s = frem double %s, %s\n", reg, oldReg, valReg)
		} else {
			if isUnsigned {
				cg.writef("  %s = urem %s %s, %s\n", reg, lt, oldReg, valReg)
			} else {
				cg.writef("  %s = srem %s %s, %s\n", reg, lt, oldReg, valReg)
			}
		}
	case "&=":
		cg.writef("  %s = and %s %s, %s\n", reg, lt, oldReg, valReg)
	case "|=":
		cg.writef("  %s = or %s %s, %s\n", reg, lt, oldReg, valReg)
	case "^=":
		cg.writef("  %s = xor %s %s, %s\n", reg, lt, oldReg, valReg)
	case "<<=":
		cg.writef("  %s = shl %s %s, %s\n", reg, lt, oldReg, valReg)
	case ">>=":
		if isUnsigned {
			cg.writef("  %s = lshr %s %s, %s\n", reg, lt, oldReg, valReg)
		} else {
			cg.writef("  %s = ashr %s %s, %s\n", reg, lt, oldReg, valReg)
		}
	default:
		// Unknown compound op — just return the new value.
		return valReg
	}
	return reg
}

// emitIdentAssignment stores a value into a simple variable.
func (cg *Codegen) emitIdentAssignment(id *ast.Identifier, op string, value ast.Expression) {
	// Check if this is a captured variable in a closure.
	if info, ok := cg.closureEnv[id.Value]; ok {
		valReg := cg.emitExpression(value)
		valType := cg.exprLLType(value)
		if valType == "" {
			valType = info.llType
		}
		if op != "=" {
			// Read current value, apply operation, then store.
			ptrReg := cg.nextReg()
			cg.writef("  %s = getelementptr %%%s, %%%s* %s, i32 0, i32 %d\n", ptrReg, info.envStruct, info.envStruct, info.globalName, info.fieldIndex)
			curReg := cg.nextReg()
			cg.writef("  %s = load %s, %s* %s\n", curReg, info.llType, info.llType, ptrReg)
			valReg = cg.computeCompoundOp(curReg, valReg, op, info.llType, false)
		}
		ptrReg := cg.nextReg()
		cg.writef("  %s = getelementptr %%%s, %%%s* %s, i32 0, i32 %d\n", ptrReg, info.envStruct, info.envStruct, info.globalName, info.fieldIndex)
		cg.writef("  store %s %s, %s* %s\n", info.llType, valReg, info.llType, ptrReg)
		return
	}
	alloca, lt := cg.resolveVar(id.Value)
	if lt == "" {
		lt = cg.globalVarTypes[id.Value]
		if lt == "" {
			lt = "i32"
		}
	}

	var dest string
	if alloca == "" {
		// Global variable.
		dest = "@" + id.Value
	} else {
		dest = alloca
	}

	// Handle error propagation: a = foo()?
	if ep, ok := value.(*ast.ErrorPropagationExpr); ok && op == "=" {
		if call, ok2 := ep.Expr.(*ast.CallExpr); ok2 {
			if callId, ok3 := call.Function.(*ast.Identifier); ok3 {
				if retTypes, ok4 := cg.fnRetTypes[callId.Value]; ok4 && len(retTypes) > 1 {
					aggReg := cg.emitCallExpr(call)
					cg.emitErrorPropagationBranch(aggReg, retTypes)

					elemType := retTypes[0]
					reg := cg.nextReg()
					aggType := "{ " + strings.Join(retTypes, ", ") + " }"
					cg.writef("  %s = extractvalue %s %s, 0\n", reg, aggType, aggReg)
					if elemType == "i32" && retTypes[0] == "i1" {
						zext := cg.nextReg()
						cg.writef("  %s = zext i1 %s to i32\n", zext, reg)
						reg = zext
					}
					cg.writef("  store %s %s, %s* %s\n", elemType, reg, elemType, dest)
					return
				}
			}
		}
	}

	valReg := cg.emitExpression(value)
	if lt == "i32" && cg.exprLLType(value) == "i1" {
		zextReg := cg.nextReg()
		cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
		valReg = zextReg
	}
	// If target is struct value but value is a pointer, load the value first.
	if strings.HasPrefix(lt, "%struct.") && !strings.HasSuffix(lt, "*") && strings.HasSuffix(cg.exprLLType(value), "*") {
		loadedReg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", loadedReg, lt, lt, valReg)
		valReg = loadedReg
	}
	if op != "=" {
		oldReg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", oldReg, lt, lt, dest)
		valReg = cg.computeCompoundOp(oldReg, valReg, op, lt, cg.isUnsignedVar(id.Value))
	}
	// ARC: release old heap value and retain new non-constructor heap value.
	if op == "=" && cg.isHeapLLType(lt) {
		oldReg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", oldReg, lt, lt, dest)
		cg.emitRelease(oldReg, lt)
		if !cg.isConstructorExpr(value) {
			valReg = cg.emitRetain(valReg, lt)
		}
	}
	cg.writef("  store %s %s, %s* %s\n", lt, valReg, lt, dest)
}

// emitNestedFieldPtr computes a pointer to a nested field access expression
// by starting from the base variable's alloca and applying GEPs without
// creating intermediate copies.
func (cg *Codegen) emitNestedFieldPtr(e ast.Expression) string {
	// Build chain of field accesses from outermost to innermost.
	var chain []*ast.FieldAccessExpr
	cur := e
	for {
		fa, ok := cur.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		chain = append([]*ast.FieldAccessExpr{fa}, chain...)
		cur = fa.Left
	}

	// cur should be the base identifier.
	id, ok := cur.(*ast.Identifier)
	if !ok {
		// Fall back to regular expression emission.
		return cg.emitExpression(e)
	}

	alloca, _ := cg.resolveVar(id.Value)
	if alloca == "" {
		alloca = "@" + id.Value
	}

	ptr := alloca
	for _, fa := range chain {
		leftType := cg.exprLLType(fa.Left)
		structName := ""
		if strings.HasPrefix(leftType, "%struct.") {
			structName = strings.TrimPrefix(leftType, "%struct.")
			if idx := strings.Index(structName, "*"); idx > 0 {
				structName = structName[:idx]
			}
		}
		if structName == "" {
			return "0"
		}
		layout, ok := cg.structLayouts[structName]
		if !ok {
			return "0"
		}
		fieldIdx := -1
		for i, f := range layout.fields {
			if f == fa.Field {
				fieldIdx = i
				break
			}
		}
		if fieldIdx == -1 {
			return "0"
		}
		structLLType := "%struct." + strings.ReplaceAll(structName, ".", "_")
		ptrReg := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
			ptrReg, structLLType, structLLType, ptr, fieldIdx)
		ptr = ptrReg
	}

	return ptr
}

// emitFieldAccessAssignment stores a value into a struct field.
func (cg *Codegen) emitFieldAccessAssignment(e *ast.FieldAccessExpr, op string, value ast.Expression) {
	leftType := cg.exprLLType(e.Left)
	structName := ""
	if strings.HasPrefix(leftType, "%struct.") {
		structName = strings.TrimPrefix(leftType, "%struct.")
		if idx := strings.Index(structName, "*"); idx > 0 {
			structName = structName[:idx]
		}
	}
	if structName == "" {
		return
	}
	layout, ok := cg.structLayouts[structName]
	if !ok {
		return
	}
	fieldIdx := -1
	for i, f := range layout.fields {
		if f == e.Field {
			fieldIdx = i
			break
		}
	}
	if fieldIdx == -1 {
		return
	}

	var ptrReg string
	origAlloca := ""
	var objReg string
	if _, isNested := e.Left.(*ast.FieldAccessExpr); isNested {
		// For nested field access (e.g. o.inner.x), compute the pointer
		// directly from the base variable's alloca to avoid intermediate
		// copies that would prevent the mutation from propagating.
		ptrReg = cg.emitNestedFieldPtr(e)
	} else {
		structLLType := "%struct." + strings.ReplaceAll(structName, ".", "_")
		// For local struct variables, use the alloca pointer directly to avoid
		// unnecessary copies and ensure mutations propagate correctly.
		if id, ok := e.Left.(*ast.Identifier); ok {
			alloca, lt := cg.resolveVar(id.Value)
			if lt == structLLType {
				origAlloca = alloca
				objReg = alloca
			}
		}
		if objReg == "" {
			objReg = cg.emitExpression(e.Left)
			if !strings.HasSuffix(leftType, "*") {
				tmpReg := cg.nextReg()
				cg.writef("  %s = alloca %s\n", tmpReg, structLLType)
				cg.writef("  store %s %s, %s* %s\n", structLLType, objReg, structLLType, tmpReg)
				objReg = tmpReg
			}
		}
		ptrReg = cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
			ptrReg, structLLType, structLLType, objReg, fieldIdx)
		// Defer copy-back from temp to original alloca until after the field store.
		_ = origAlloca
	}

	fieldType := layout.fieldTypes[fieldIdx]
	valReg := cg.emitExpression(value)
	if fieldType == "i32" && cg.exprLLType(value) == "i1" {
		zextReg := cg.nextReg()
		cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
		valReg = zextReg
	}
	if op != "=" {
		oldReg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", oldReg, fieldType, fieldType, ptrReg)
		valReg = cg.computeCompoundOp(oldReg, valReg, op, fieldType, layout.isUnsigned[fieldIdx])
	}
	cg.writef("  store %s %s, %s* %s\n", fieldType, valReg, fieldType, ptrReg)

	// If we used a temp alloca for a struct value identifier, copy the modified
	// value back to the original variable's alloca.
	if origAlloca != "" && objReg != origAlloca {
		structLLType := "%struct." + strings.ReplaceAll(structName, ".", "_")
		updatedReg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", updatedReg, structLLType, structLLType, objReg)
		cg.writef("  store %s %s, %s* %s\n", structLLType, updatedReg, structLLType, origAlloca)
	}
}

// emitIndexAssignment stores a value into an array element or map entry.
func (cg *Codegen) emitIndexAssignment(e *ast.IndexExpr, op string, value ast.Expression) {
	baseReg := cg.emitExpression(e.Left)
	leftType := cg.exprLLType(e.Left)
	isString := leftType == "i8*"

	// Handle ^n from-end indexing.
	var indexReg string
	if fe, ok := e.Index.(*ast.FromEndIndexExpr); ok {
		operandReg := cg.emitExpression(fe.Operand)
		if isString {
			lenReg := cg.nextReg()
			cg.writef("  %s = call i32 @strlen(i8* %s)\n", lenReg, baseReg)
			subReg := cg.nextReg()
			cg.writef("  %s = sub i32 %s, %s\n", subReg, lenReg, operandReg)
			indexReg = subReg
		} else {
			arrLen, _ := cg.inferArrayInfo(e.Left)
			if arrLen > 0 {
				lenReg := cg.nextReg()
				cg.writef("  %s = add i32 0, %d\n", lenReg, arrLen)
				subReg := cg.nextReg()
				cg.writef("  %s = sub i32 %s, %s\n", subReg, lenReg, operandReg)
				indexReg = subReg
			} else {
				indexReg = operandReg
			}
		}
	} else {
		indexReg = cg.emitExpression(e.Index)
	}

	// Detect map assignment and delegate.
	if keyLL, valLL, isMap := parseMapType(leftType); isMap {
		cg.emitMapIndexAssignment(baseReg, indexReg, keyLL, valLL, value, op)
		return
	}

	elemType := "i32"
	if strings.HasSuffix(leftType, "*") {
		elemType = strings.TrimSuffix(leftType, "*")
	}
	if strings.HasPrefix(elemType, "%struct.") && !strings.HasSuffix(elemType, "*") {
		elemType = elemType + "*"
	}

	actualBase := baseReg
	if leftType != elemType+"*" {
		castReg := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to %s*\n", castReg, leftType, baseReg, elemType)
		actualBase = castReg
	}

	gep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", gep, elemType, elemType, actualBase, indexReg)

	valReg := cg.emitExpression(value)
	valType := cg.exprLLType(value)
	if valType == "i1" {
		zextReg := cg.nextReg()
		cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
		valReg = zextReg
		valType = "i32"
	}
	// Truncate i32 value to smaller element types (i8, i16).
	if valType == "i32" && (elemType == "i8" || elemType == "i16") {
		truncReg := cg.nextReg()
		cg.writef("  %s = trunc i32 %s to %s\n", truncReg, valReg, elemType)
		valReg = truncReg
	}
	if op != "=" {
		oldReg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", oldReg, elemType, elemType, gep)
		valReg = cg.computeCompoundOp(oldReg, valReg, op, elemType, cg.isUnsignedExpr(e.Left))
	}
	cg.writef("  store %s %s, %s* %s\n", elemType, valReg, elemType, gep)
}

// emitMapIndexAssignment emits a linear search to find the matching key and
// store a new value. If the key is not found, a new entry is appended.
func (cg *Codegen) emitMapIndexAssignment(mapReg, keyReg, keyLL, valLL string, value ast.Expression, op string) {
	mapType := "%" + mapTypeName(keyLL, valLL)

	// Load keys, values, and len from map struct.
	keysField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n", keysField, mapType, mapType, mapReg)
	keysPtr := cg.nextReg()
	cg.writef("  %s = load %s*, %s** %s\n", keysPtr, keyLL, keyLL, keysField)

	valsField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n", valsField, mapType, mapType, mapReg)
	valsPtr := cg.nextReg()
	cg.writef("  %s = load %s*, %s** %s\n", valsPtr, valLL, valLL, valsField)

	lenField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 2\n", lenField, mapType, mapType, mapReg)
	lenReg := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", lenReg, lenField)

	// Emit the value to store.
	valReg := cg.emitExpression(value)
	valType := cg.exprLLType(value)
	if valType == "i1" {
		zextReg := cg.nextReg()
		cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
		valReg = zextReg
		valType = "i32"
	}
	if valType == "i32" && valLL == "i8" {
		truncReg := cg.nextReg()
		cg.writef("  %s = trunc i32 %s to i8\n", truncReg, valReg)
		valReg = truncReg
		valType = "i8"
	}
	if op != "=" {
		// Compound assignment on maps is not supported; just store the raw value.
	}

	// Search loop with found flag and index.
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	foundLabel := cg.nextLabel()
	searchEndLabel := cg.nextLabel()
	initLabel := cg.nextLabel()

	foundFlag := cg.nextReg()
	cg.writef("  %s = alloca i1\n", foundFlag)
	cg.writef("  store i1 0, i1* %s\n", foundFlag)
	foundIdx := cg.nextReg()
	cg.writef("  %s = alloca i32\n", foundIdx)
	cg.writef("  store i32 0, i32* %s\n", foundIdx)

	iAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", iAlloca)
	cg.writef("  store i32 0, i32* %s\n", iAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// cond: i < len.
	cg.writeln(condLabel + ":")
	iVal := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", iVal, iAlloca)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, iVal, lenReg)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, searchEndLabel)

	// body: compare keys[i] with keyReg.
	cg.writeln(bodyLabel + ":")
	keyGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", keyGep, keyLL, keyLL, keysPtr, iVal)
	curKey := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", curKey, keyLL, keyLL, keyGep)

	var match string
	if keyLL == "i8*" {
		cmp := cg.nextReg()
		cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmp, curKey, keyReg)
		match = cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, 0\n", match, cmp)
	} else {
		match = cg.nextReg()
		cg.writef("  %s = icmp eq %s %s, %s\n", match, keyLL, curKey, keyReg)
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", match, foundLabel, initLabel)

	// found: record index and jump to searchEnd.
	cg.writeln(foundLabel + ":")
	cg.writef("  store i1 1, i1* %s\n", foundFlag)
	cg.writef("  store i32 %s, i32* %s\n", iVal, foundIdx)
	cg.writef("  br label %%%s\n", searchEndLabel)

	// init: i++.
	cg.writeln(initLabel + ":")
	nextI := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextI, iVal)
	cg.writef("  store i32 %s, i32* %s\n", nextI, iAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// searchEnd: check if found.
	cg.terminated = false
	cg.writeln(searchEndLabel + ":")
	wasFound := cg.nextReg()
	cg.writef("  %s = load i1, i1* %s\n", wasFound, foundFlag)
	updateLabel := cg.nextLabel()
	insertLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", wasFound, updateLabel, insertLabel)

	// update: store values[foundIdx] = valReg.
	cg.writeln(updateLabel + ":")
	idxReg := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxReg, foundIdx)
	valGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", valGep, valLL, valLL, valsPtr, idxReg)
	cg.writef("  store %s %s, %s* %s\n", valLL, valReg, valLL, valGep)
	cg.writef("  br label %%%s\n", endLabel)

	// insert: grow arrays and append new key-value pair.
	cg.writeln(insertLabel + ":")
	newLen := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", newLen, lenReg)

	keyElemSize := cg.emitElemSize(keyLL)
	valElemSize := cg.emitElemSize(valLL)

	// Allocate new keys array.
	newKeysBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %s, %s\n", newKeysBytes, newLen, keyElemSize)
	newKeysBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", newKeysBytes64, newKeysBytes)
	newKeysRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", newKeysRaw, newKeysBytes64)
	newKeysPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", newKeysPtr, newKeysRaw, keyLL)

	// Allocate new values array.
	newValsBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %s, %s\n", newValsBytes, newLen, valElemSize)
	newValsBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", newValsBytes64, newValsBytes)
	newValsRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", newValsRaw, newValsBytes64)
	newValsPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", newValsPtr, newValsRaw, valLL)

	// Copy old keys to new keys array.
	copyCond := cg.nextLabel()
	copyBody := cg.nextLabel()
	copyEnd := cg.nextLabel()
	copyI := cg.nextReg()
	cg.writef("  %s = alloca i32\n", copyI)
	cg.writef("  store i32 0, i32* %s\n", copyI)
	cg.writef("  br label %%%s\n", copyCond)

	cg.writeln(copyCond + ":")
	ciVal := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", ciVal, copyI)
	ciCond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", ciCond, ciVal, lenReg)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", ciCond, copyBody, copyEnd)

	cg.writeln(copyBody + ":")
	oldKeyGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", oldKeyGep, keyLL, keyLL, keysPtr, ciVal)
	oldKeyVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", oldKeyVal, keyLL, keyLL, oldKeyGep)
	newKeyGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", newKeyGep, keyLL, keyLL, newKeysPtr, ciVal)
	cg.writef("  store %s %s, %s* %s\n", keyLL, oldKeyVal, keyLL, newKeyGep)
	oldValGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", oldValGep, valLL, valLL, valsPtr, ciVal)
	oldValVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", oldValVal, valLL, valLL, oldValGep)
	newValGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", newValGep, valLL, valLL, newValsPtr, ciVal)
	cg.writef("  store %s %s, %s* %s\n", valLL, oldValVal, valLL, newValGep)
	ciNext := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", ciNext, ciVal)
	cg.writef("  store i32 %s, i32* %s\n", ciNext, copyI)
	cg.writef("  br label %%%s\n", copyCond)

	cg.writeln(copyEnd + ":")
	// Store new key at index len.
	newKeyGep2 := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", newKeyGep2, keyLL, keyLL, newKeysPtr, lenReg)
	cg.writef("  store %s %s, %s* %s\n", keyLL, keyReg, keyLL, newKeyGep2)
	// Store new value at index len.
	newValGep2 := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", newValGep2, valLL, valLL, newValsPtr, lenReg)
	cg.writef("  store %s %s, %s* %s\n", valLL, valReg, valLL, newValGep2)
	// Update map struct.
	newKeysField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n", newKeysField, mapType, mapType, mapReg)
	cg.writef("  store %s* %s, %s** %s\n", keyLL, newKeysPtr, keyLL, newKeysField)
	newValsField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n", newValsField, mapType, mapType, mapReg)
	cg.writef("  store %s* %s, %s** %s\n", valLL, newValsPtr, valLL, newValsField)
	newLenField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 2\n", newLenField, mapType, mapType, mapReg)
	cg.writef("  store i32 %s, i32* %s\n", newLen, newLenField)
	// Release old arrays.
	oldKeysRaw := cg.nextReg()
	cg.writef("  %s = bitcast %s* %s to i8*\n", oldKeysRaw, keyLL, keysPtr)
	cg.writef("  call void @Skink_rc_release(i8* %s)\n", oldKeysRaw)
	oldValsRaw := cg.nextReg()
	cg.writef("  %s = bitcast %s* %s to i8*\n", oldValsRaw, valLL, valsPtr)
	cg.writef("  call void @Skink_rc_release(i8* %s)\n", oldValsRaw)
	cg.writef("  br label %%%s\n", endLabel)

	// end.
	cg.terminated = false
	cg.writeln(endLabel + ":")
}

// emitTupleAssignmentStmt emits a multi-value assignment: a, b = foo()
func (cg *Codegen) emitTupleAssignmentStmt(s *ast.TupleAssignmentStmt) {
	// Emit the RHS (should be a call returning an aggregate, or an aggregate value).
	aggReg := cg.emitExpression(s.Value)
	var retTypes []string
	// Try to determine the tuple element types from the RHS.
	var isErrorProp bool
	if call, ok := s.Value.(*ast.CallExpr); ok {
		if id, ok2 := call.Function.(*ast.Identifier); ok2 {
			if rt, ok3 := cg.fnRetTypes[id.Value]; ok3 && len(rt) > 1 {
				retTypes = rt
			}
		}
	}
	if ep, ok := s.Value.(*ast.ErrorPropagationExpr); ok {
		isErrorProp = true
		if call, ok2 := ep.Expr.(*ast.CallExpr); ok2 {
			if id, ok3 := call.Function.(*ast.Identifier); ok3 {
				if rt, ok4 := cg.fnRetTypes[id.Value]; ok4 && len(rt) > 1 {
					retTypes = rt
				}
			}
		}
	}
	if len(retTypes) == 0 {
		// Fallback: try to infer from exprLLType if it's a tuple.
		llt := cg.exprLLType(s.Value)
		if strings.HasPrefix(llt, "{") && strings.HasSuffix(llt, "}") {
			inner := strings.TrimSpace(llt[1 : len(llt)-1])
			retTypes = strings.Split(inner, ", ")
		}
	}
	// Emit error propagation branch if needed.
	if isErrorProp && len(retTypes) > 1 {
		cg.emitErrorPropagationBranch(aggReg, retTypes)
	}
	for i, lv := range s.LValues {
		id, ok := lv.(*ast.Identifier)
		if !ok {
			continue
		}
		alloca, lt := cg.resolveVar(id.Value)
		if alloca == "" {
			continue
		}
		var elemType string
		if i < len(retTypes) {
			elemType = retTypes[i]
		} else {
			elemType = lt
		}
		reg := cg.nextReg()
		cg.writef("  %s = extractvalue %s %s, %d\n", reg, cg.exprLLType(s.Value), aggReg, i)
		// zext if needed
		if elemType == "i32" && len(retTypes) > i && retTypes[i] == "i1" {
			zext := cg.nextReg()
			cg.writef("  %s = zext i1 %s to i32\n", zext, reg)
			reg = zext
		}
		cg.writef("  store %s %s, %s* %s\n", elemType, reg, elemType, alloca)
	}
}

// emitTupleVarStmt emits a multi-value variable declaration: a, b := foo()
func (cg *Codegen) emitTupleVarStmt(s *ast.TupleVarStmt) {
	// Emit the RHS (should be a call returning an aggregate).
	aggReg := cg.emitExpression(s.Value)
	var retTypes []string
	var isErrorProp bool
	if call, ok := s.Value.(*ast.CallExpr); ok {
		if id, ok2 := call.Function.(*ast.Identifier); ok2 {
			if rt, ok3 := cg.fnRetTypes[id.Value]; ok3 && len(rt) > 1 {
				retTypes = rt
			}
		}
	}
	if ep, ok := s.Value.(*ast.ErrorPropagationExpr); ok {
		isErrorProp = true
		if call, ok2 := ep.Expr.(*ast.CallExpr); ok2 {
			if id, ok3 := call.Function.(*ast.Identifier); ok3 {
				if rt, ok4 := cg.fnRetTypes[id.Value]; ok4 && len(rt) > 1 {
					retTypes = rt
				}
			}
		}
	}
	if len(retTypes) == 0 {
		llt := cg.exprLLType(s.Value)
		if strings.HasPrefix(llt, "{") && strings.HasSuffix(llt, "}") {
			inner := strings.TrimSpace(llt[1 : len(llt)-1])
			retTypes = strings.Split(inner, ", ")
		}
	}
	// Emit error propagation branch if needed.
	if isErrorProp && len(retTypes) > 1 {
		cg.emitErrorPropagationBranch(aggReg, retTypes)
	}
	for i, name := range s.Names {
		var elemType string
		if i < len(retTypes) {
			elemType = retTypes[i]
		} else {
			elemType = "i32"
		}
		ptrReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", ptrReg, elemType)
		cg.declareVar(name, ptrReg, elemType, false, nil)
		reg := cg.nextReg()
		cg.writef("  %s = extractvalue %s %s, %d\n", reg, cg.exprLLType(s.Value), aggReg, i)
		if elemType == "i32" && len(retTypes) > i && retTypes[i] == "i1" {
			zext := cg.nextReg()
			cg.writef("  %s = zext i1 %s to i32\n", zext, reg)
			reg = zext
		}
		cg.writef("  store %s %s, %s* %s\n", elemType, reg, elemType, ptrReg)
	}
}

// emitErrorPropagationBranch emits the error check and branch for foo()?.
// It assumes the aggregate value has already been computed.
// After this function, the current block is the ok block.
func (cg *Codegen) emitErrorPropagationBranch(aggReg string, retTypes []string) {
	if len(retTypes) < 2 {
		return
	}
	aggType := "{ " + strings.Join(retTypes, ", ") + " }"
	lastIdx := len(retTypes) - 1

	errReg := cg.nextReg()
	cg.writef("  %s = extractvalue %s %s, %d\n", errReg, aggType, aggReg, lastIdx)

	// error is a fat pointer { fn ptr, data ptr }; data ptr null means no error.
	// Extract the data pointer (index 1) from the error fat pointer.
	dataReg := cg.nextReg()
	cg.writef("  %s = extractvalue %%error %s, 1\n", dataReg, errReg)
	isErrReg := cg.nextReg()
	cg.writef("  %s = icmp ne i8* %s, null\n", isErrReg, dataReg)

	okLabel := cg.nextLabel()
	errLabel := cg.nextLabel()

	cg.writef("  br i1 %s, label %%%s, label %%%s\n", isErrReg, errLabel, okLabel)

	cg.writeln(errLabel + ":")
	cg.emitDeferred()
	cg.writef("  ret %s %s\n", aggType, aggReg)
	cg.terminated = true

	cg.writeln(okLabel + ":")
	cg.terminated = false
}

// emitDeferred emits all accumulated deferred statements in LIFO order.
func (cg *Codegen) emitDeferred() {
	for i := len(cg.deferred) - 1; i >= 0; i-- {
		cg.emitStatement(cg.deferred[i])
	}
}

// emitDeferStmt records a statement to be executed at function exit.
func (cg *Codegen) emitDeferStmt(d *ast.DeferStmt) {
	cg.deferred = append(cg.deferred, d.Statement)
}

// emitUnsafeStmt emits an unsafe block (currently just emits the body).
func (cg *Codegen) emitUnsafeStmt(u *ast.UnsafeStmt) {
	cg.pushScope()
	cg.emitBlockStmt(u.Body)
	cg.popScope()
}

// emitVarBlockStmt emits each variable declaration in a var block.
func (cg *Codegen) emitVarBlockStmt(v *ast.VarBlockStmt) {
	for _, decl := range v.Decls {
		cg.emitVarStmt(decl)
	}
}

// emitWithStmt emits a with statement: declare the variable, emit the body.
func (cg *Codegen) emitWithStmt(w *ast.WithStmt) {
	cg.pushScope()
	// Treat as a variable declaration.
	vs := &ast.VarStmt{Token: w.Token, Name: w.Name, Value: w.Value, Implicit: true}
	cg.emitVarStmt(vs)
	cg.emitBlockStmt(w.Body)
	cg.popScope()
}

// emitSpawnStmt emits a spawn statement.
// Supports spawning zero-argument functions and functions with arguments
// via the concurrency runtime.  Functions with arguments are wrapped in a
// thunk that unpacks a heap-allocated arg struct.
func (cg *Codegen) emitSpawnStmt(s *ast.SpawnStmt) {
	call, ok := s.Call.(*ast.CallExpr)
	if !ok {
		cg.emitExpression(s.Call)
		return
	}
	fnName := ""
	switch f := call.Function.(type) {
	case *ast.Identifier:
		fnName = f.Value
	case *ast.FieldAccessExpr:
		fnName = f.Field
	}
	if fnName == "" {
		cg.emitExpression(s.Call)
		return
	}

	if len(call.Arguments) == 0 {
		// Zero-argument fast path.
		retType := "i32"
		if rts, ok := cg.fnRetTypes[fnName]; ok && len(rts) > 0 {
			retType = rts[0]
		}
		castReg := cg.nextReg()
		if retType == "void" {
			cg.writef("  %s = bitcast void ()* @%s to i8*\n", castReg, fnName)
		} else {
			cg.writef("  %s = bitcast %s ()* @%s to i8*\n", castReg, retType, fnName)
		}
		cg.writef("  call void @Skink_spawn(i8* %s, i8* null, i8* null)\n", castReg)
		return
	}

	// --- Argument thunk path ---
	// Determine LLVM types for each argument.
	argTypes := make([]string, len(call.Arguments))
	for i, arg := range call.Arguments {
		argTypes[i] = cg.exprLLType(arg)
	}

	// Determine the real function's return type.
	retType := "void"
	if rts, ok := cg.fnRetTypes[fnName]; ok && len(rts) > 0 {
		retType = rts[0]
	}

	// Build anonymous struct type for packed args.
	var structParts []string
	for _, at := range argTypes {
		structParts = append(structParts, at)
	}
	structType := "{ " + strings.Join(structParts, ", ") + " }"

	// Generate a unique thunk name.
	thunkName := fmt.Sprintf("__spawn_thunk_%d", cg.spawnThunkCounter+1)
	cg.spawnThunkCounter++

	// --- Emit thunk function ---
	// Thunk: i8* thunk(i8* %arg) { unpack struct, call real fn, ret i8* null }
	cg.spawnThunks.WriteString(fmt.Sprintf("define i8* @%s(i8* %%arg) {\n", thunkName))
	tr := cg.nextThunkReg()
	cg.spawnThunks.WriteString(fmt.Sprintf("  %s = bitcast i8* %%arg to %s*\n", tr, structType))
	structPtr := tr
	var loadRegs []string
	for i, at := range argTypes {
		tr = cg.nextThunkReg()
		cg.spawnThunks.WriteString(fmt.Sprintf("  %s = getelementptr %s, %s* %s, i32 0, i32 %d\n", tr, structType, structType, structPtr, i))
		ptrReg := tr
		tr = cg.nextThunkReg()
		cg.spawnThunks.WriteString(fmt.Sprintf("  %s = load %s, %s* %s\n", tr, at, at, ptrReg))
		loadRegs = append(loadRegs, tr)
	}
	var callArgs []string
	for i, at := range argTypes {
		callArgs = append(callArgs, fmt.Sprintf("%s %s", at, loadRegs[i]))
	}
	if retType == "void" {
		cg.spawnThunks.WriteString(fmt.Sprintf("  call void @%s(%s)\n", fnName, strings.Join(callArgs, ", ")))
	} else {
		tr = cg.nextThunkReg()
		cg.spawnThunks.WriteString(fmt.Sprintf("  %s = call %s @%s(%s)\n", tr, retType, fnName, strings.Join(callArgs, ", ")))
	}
	// Free the arg struct (it was malloc'd by the spawner).
	cg.spawnThunks.WriteString(fmt.Sprintf("  call void @free(i8* %%arg)\n"))
	cg.spawnThunks.WriteString("  ret i8* null\n")
	cg.spawnThunks.WriteString("}\n\n")

	// --- In the current function: malloc struct, store args, spawn ---
	// Compute struct size with the GEP-null trick.
	sizePtr := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", sizePtr, structType, structType)
	sizeReg := cg.nextReg()
	cg.writef("  %s = ptrtoint %s* %s to i64\n", sizeReg, structType, sizePtr)
	mallocReg := cg.nextReg()
	cg.writef("  %s = call i8* @malloc(i64 %s)\n", mallocReg, sizeReg)
	structReg := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", structReg, mallocReg, structType)

	for i, arg := range call.Arguments {
		argVal := cg.emitExpression(arg)
		slotReg := cg.nextReg()
		cg.writef("  %s = getelementptr %s, %s* %s, i32 0, i32 %d\n", slotReg, structType, structType, structReg, i)
		at := argTypes[i]
		cg.writef("  store %s %s, %s* %s\n", at, argVal, at, slotReg)
	}

	thunkCast := cg.nextReg()
	cg.writef("  %s = bitcast i8* (i8*)* @%s to i8*\n", thunkCast, thunkName)
	cg.writef("  call void @Skink_spawn(i8* %s, i8* %s, i8* null)\n", thunkCast, mallocReg)
}

// emitAsyncExpr emits an async expression.
func (cg *Codegen) emitAsyncExpr(e *ast.AsyncExpr) string {
	call, ok := e.Expr.(*ast.CallExpr)
	if !ok {
		cg.Errorf("async operand must be a call expression")
		return "0"
	}

	fnName := ""
	switch f := call.Function.(type) {
	case *ast.Identifier:
		fnName = f.Value
	case *ast.FieldAccessExpr:
		fnName = f.Field
	}
	if fnName == "" {
		cg.Errorf("async operand must be a valid function call")
		return "0"
	}

	// Create future
	futureReg := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_future_make()\n", futureReg)

	// Determine LLVM types for each argument.
	argTypes := make([]string, len(call.Arguments))
	for i, arg := range call.Arguments {
		argTypes[i] = cg.exprLLType(arg)
	}

	// Determine the real function's return type.
	retType := "void"
	if rts, ok := cg.fnRetTypes[fnName]; ok && len(rts) > 0 {
		retType = rts[0]
	}

	// Generate a unique thunk name.
	thunkName := fmt.Sprintf("__async_thunk_%d", cg.spawnThunkCounter+1)
	cg.spawnThunkCounter++

	var structType string
	if len(call.Arguments) == 0 {
		// Zero-argument path.
		cg.spawnThunks.WriteString(fmt.Sprintf("define i8* @%s(i8* %%arg) {\n", thunkName))
		if retType == "void" {
			cg.spawnThunks.WriteString(fmt.Sprintf("  call void @%s()\n", fnName))
			cg.spawnThunks.WriteString("  ret i8* null\n")
		} else {
			tr := cg.nextThunkReg()
			cg.spawnThunks.WriteString(fmt.Sprintf("  %s = call %s @%s()\n", tr, retType, fnName))
			// Convert ret value to i8* and return it
			castRet := cg.nextThunkReg()
			if strings.HasSuffix(retType, "*") {
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = bitcast %s %s to i8*\n", castRet, retType, tr))
			} else if retType == "double" {
				i64Ret := cg.nextThunkReg()
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = bitcast double %s to i64\n", i64Ret, tr))
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = inttoptr i64 %s to i8*\n", castRet, i64Ret))
			} else if retType == "i64" {
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = inttoptr i64 %s to i8*\n", castRet, tr))
			} else { // integer types
				i64Ret := cg.nextThunkReg()
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = zext %s %s to i64\n", i64Ret, retType, tr))
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = inttoptr i64 %s to i8*\n", castRet, i64Ret))
			}
			cg.spawnThunks.WriteString(fmt.Sprintf("  ret i8* %s\n", castRet))
		}
		cg.spawnThunks.WriteString("}\n\n")

		thunkCast := cg.nextReg()
		cg.writef("  %s = bitcast i8* (i8*)* @%s to i8*\n", thunkCast, thunkName)
		cg.writef("  call void @Skink_spawn(i8* %s, i8* null, i8* %s)\n", thunkCast, futureReg)
	} else {
		// Build anonymous struct type for packed args.
		var structParts []string
		for _, at := range argTypes {
			structParts = append(structParts, at)
		}
		structType = "{ " + strings.Join(structParts, ", ") + " }"

		// Thunk: i8* thunk(i8* %arg) { unpack struct, call real fn, convert and ret value as i8* }
		cg.spawnThunks.WriteString(fmt.Sprintf("define i8* @%s(i8* %%arg) {\n", thunkName))
		tr := cg.nextThunkReg()
		cg.spawnThunks.WriteString(fmt.Sprintf("  %s = bitcast i8* %%arg to %s*\n", tr, structType))
		structPtr := tr
		var loadRegs []string
		for i, at := range argTypes {
			tr = cg.nextThunkReg()
			cg.spawnThunks.WriteString(fmt.Sprintf("  %s = getelementptr %s, %s* %s, i32 0, i32 %d\n", tr, structType, structType, structPtr, i))
			ptrReg := tr
			tr = cg.nextThunkReg()
			cg.spawnThunks.WriteString(fmt.Sprintf("  %s = load %s, %s* %s\n", tr, at, at, ptrReg))
			loadRegs = append(loadRegs, tr)
		}
		var callArgs []string
		for i, at := range argTypes {
			callArgs = append(callArgs, fmt.Sprintf("%s %s", at, loadRegs[i]))
		}

		var valReg string
		if retType == "void" {
			cg.spawnThunks.WriteString(fmt.Sprintf("  call void @%s(%s)\n", fnName, strings.Join(callArgs, ", ")))
		} else {
			tr = cg.nextThunkReg()
			cg.spawnThunks.WriteString(fmt.Sprintf("  %s = call %s @%s(%s)\n", tr, retType, fnName, strings.Join(callArgs, ", ")))
			valReg = tr
		}
		// Free the arg struct (it was malloc'd by the spawner).
		cg.spawnThunks.WriteString(fmt.Sprintf("  call void @free(i8* %%arg)\n"))

		if retType == "void" {
			cg.spawnThunks.WriteString("  ret i8* null\n")
		} else {
			// Convert ret value to i8* and return it
			castRet := cg.nextThunkReg()
			if strings.HasSuffix(retType, "*") {
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = bitcast %s %s to i8*\n", castRet, retType, valReg))
			} else if retType == "double" {
				i64Ret := cg.nextThunkReg()
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = bitcast double %s to i64\n", i64Ret, valReg))
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = inttoptr i64 %s to i8*\n", castRet, i64Ret))
			} else if retType == "i64" {
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = inttoptr i64 %s to i8*\n", castRet, valReg))
			} else { // integer types
				i64Ret := cg.nextThunkReg()
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = zext %s %s to i64\n", i64Ret, retType, valReg))
				cg.spawnThunks.WriteString(fmt.Sprintf("  %s = inttoptr i64 %s to i8*\n", castRet, i64Ret))
			}
			cg.spawnThunks.WriteString(fmt.Sprintf("  ret i8* %s\n", castRet))
		}
		cg.spawnThunks.WriteString("}\n\n")

		// --- In the current function: malloc struct, store args, spawn ---
		// Compute struct size with the GEP-null trick.
		sizePtr := cg.nextReg()
		cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", sizePtr, structType, structType)
		sizeReg := cg.nextReg()
		cg.writef("  %s = ptrtoint %s* %s to i64\n", sizeReg, structType, sizePtr)
		mallocReg := cg.nextReg()
		cg.writef("  %s = call i8* @malloc(i64 %s)\n", mallocReg, sizeReg)
		structReg := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to %s*\n", structReg, mallocReg, structType)

		for i, arg := range call.Arguments {
			argVal := cg.emitExpression(arg)
			slotReg := cg.nextReg()
			cg.writef("  %s = getelementptr %s, %s* %s, i32 0, i32 %d\n", slotReg, structType, structType, structReg, i)
			at := argTypes[i]
			cg.writef("  store %s %s, %s* %s\n", at, argVal, at, slotReg)
		}

		thunkCast := cg.nextReg()
		cg.writef("  %s = bitcast i8* (i8*)* @%s to i8*\n", thunkCast, thunkName)
		cg.writef("  call void @Skink_spawn(i8* %s, i8* %s, i8* %s)\n", thunkCast, mallocReg, futureReg)
	}

	return futureReg
}

// emitAwaitExpr emits an await expression.
func (cg *Codegen) emitAwaitExpr(e *ast.AwaitExpr) string {
	futureVal := cg.emitExpression(e.Expr)
	rawRet := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_future_get(i8* %s)\n", rawRet, futureVal)

	// Convert back from i8* to expected return type of the future.
	astType := cg.exprASTType(e.Expr)
	var expectedType string
	if astType != nil {
		expectedType = llvmType(astType)
	} else {
		expectedType = "i32"
	}

	if expectedType == "void" {
		return "0"
	}

	reg := cg.nextReg()
	if strings.HasSuffix(expectedType, "*") {
		cg.writef("  %s = bitcast i8* %s to %s\n", reg, rawRet, expectedType)
	} else if expectedType == "double" {
		i64Ret := cg.nextReg()
		cg.writef("  %s = ptrtoint i8* %s to i64\n", i64Ret, rawRet)
		cg.writef("  %s = bitcast i64 %s to double\n", reg, i64Ret)
	} else if expectedType == "i64" {
		cg.writef("  %s = ptrtoint i8* %s to i64\n", reg, rawRet)
	} else { // other integer types (i32, i16, i8, i1)
		i64Ret := cg.nextReg()
		cg.writef("  %s = ptrtoint i8* %s to i64\n", i64Ret, rawRet)
		cg.writef("  %s = trunc i64 %s to %s\n", reg, i64Ret, expectedType)
	}
	return reg
}

// emitSelectStmt emits a select statement.
// Supports boolean cases (case true), channel receive (case <-ch),
// channel send (case ch <- val), and default.
func (cg *Codegen) emitSelectStmt(s *ast.SelectStmt) {
	// If any case is literally `true`, it's always ready.
	for i := range s.Cases {
		if !s.Cases[i].IsDefault {
			if bl, ok := s.Cases[i].Condition.(*ast.BooleanLiteral); ok && bl.Value {
				cg.pushScope()
				cg.emitBlockStmt(s.Cases[i].Body)
				cg.popScope()
				return
			}
		}
	}

	// Separate channel cases from default.
	var chCases []ast.SelectCase
	var defCase *ast.SelectCase
	for i := range s.Cases {
		if s.Cases[i].IsDefault {
			defCase = &s.Cases[i]
		} else {
			chCases = append(chCases, s.Cases[i])
		}
	}

	// If no channel cases, emit default if present.
	if len(chCases) == 0 {
		if defCase != nil && defCase.Body != nil {
			cg.pushScope()
			cg.emitBlockStmt(defCase.Body)
			cg.popScope()
		}
		return
	}

	// Build arrays for Skink_chan_select on the stack.
	n := len(chCases)
	chsArr := cg.nextReg()
	cg.writef("  %s = alloca i8*, i32 %d\n", chsArr, n)
	isSendArr := cg.nextReg()
	cg.writef("  %s = alloca i32, i32 %d\n", isSendArr, n)
	valsArr := cg.nextReg()
	cg.writef("  %s = alloca i8*, i32 %d\n", valsArr, n)

	for i, c := range chCases {
		isSend := 0
		var chExpr ast.Expression
		var valExpr ast.Expression
		switch cond := c.Condition.(type) {
		case *ast.PrefixExpr:
			if cond.Operator == "<-" {
				isSend = 0
				chExpr = cond.Right
			}
		case *ast.InfixExpr:
			if cond.Operator == "<-" {
				isSend = 1
				chExpr = cond.Left
				valExpr = cond.Right
			}
		}
		// Store channel pointer.
		chPtr := cg.emitExpression(chExpr)
		chSlot := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds i8*, i8** %s, i32 %d\n", chSlot, chsArr, i)
		cg.writef("  store i8* %s, i8** %s\n", chPtr, chSlot)
		// Store is_send flag.
		sendSlot := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds i32, i32* %s, i32 %d\n", sendSlot, isSendArr, i)
		cg.writef("  store i32 %d, i32* %s\n", isSend, sendSlot)
		// Store value pointer (for sends).
		valSlot := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds i8*, i8** %s, i32 %d\n", valSlot, valsArr, i)
		if isSend == 1 && valExpr != nil {
			valType := cg.exprLLType(valExpr)
			valReg := cg.emitExpression(valExpr)
			tmpReg := cg.nextReg()
			cg.writef("  %s = alloca %s\n", tmpReg, valType)
			cg.writef("  store %s %s, %s* %s\n", valType, valReg, valType, tmpReg)
			castReg := cg.nextReg()
			cg.writef("  %s = bitcast %s* %s to i8*\n", castReg, valType, tmpReg)
			cg.writef("  store i8* %s, i8** %s\n", castReg, valSlot)
		} else {
			cg.writef("  store i8* null, i8** %s\n", valSlot)
		}
	}

	// Call select.
	outValPtr := cg.nextReg()
	cg.writef("  %s = alloca i8*\n", outValPtr)
	idxReg := cg.nextReg()
	cg.writef("  %s = call i32 @Skink_chan_select(i32 %d, i8** %s, i32* %s, i8** %s, i8** %s, i32 -1)\n",
		idxReg, n, chsArr, isSendArr, valsArr, outValPtr)

	// Create labels for each case and a done label.
	labels := make([]string, n)
	for i := range labels {
		labels[i] = cg.nextLabel()
	}
	doneLabel := cg.nextLabel()
	defLabel := doneLabel
	if defCase != nil {
		defLabel = cg.nextLabel()
	}

	// Switch on the returned index.
	cg.writef("  switch i32 %s, label %%%s [\n", idxReg, defLabel)
	for i := range chCases {
		cg.writef("    i32 %d, label %%%s\n", i, labels[i])
	}
	cg.writeln("  ]")

	// Emit each case body.
	for i, c := range chCases {
		cg.writeln(labels[i] + ":")
		cg.terminated = false
		cg.pushScope()
		// If receive case, extract the received value into a variable if needed.
		if prefix, ok := c.Condition.(*ast.PrefixExpr); ok && prefix.Operator == "<-" {
			// Load the received pointer.
			recvPtr := cg.nextReg()
			cg.writef("  %s = load i8*, i8** %s\n", recvPtr, outValPtr)
			// If RecvVar is set, bind the received value to a local variable.
			if c.RecvVar != "" {
				// Determine the element type from the channel expression.
				chType := cg.exprLLType(prefix.Right)
				elemType := "i8*"
				// Look up the channel variable's AST type to find the element type.
				if ct, ok := prefix.Right.(*ast.Identifier); ok {
					for i := len(cg.scopeVars) - 1; i >= 0; i-- {
						if v, ok := cg.scopeVars[i][ct.Value]; ok {
							if ch, ok := v.synType.(*ast.ChanType); ok {
								elemType = llvmType(ch.Elem)
							}
							break
						}
					}
				}
				if strings.HasPrefix(chType, "%struct.") {
					elemType = chType
				}
				if elemType == "i8*" {
					elemType = "i32"
				}
				valReg := cg.nextReg()
				cg.writef("  %s = load %s, %s* %s\n", valReg, elemType, elemType, recvPtr)
				alloca := cg.nextReg()
				cg.writef("  %s = alloca %s\n", alloca, elemType)
				cg.writef("  store %s %s, %s* %s\n", elemType, valReg, elemType, alloca)
				cg.declareVar(c.RecvVar, alloca, elemType, false, nil)
			} else {
				_ = recvPtr
			}
		}
		cg.emitBlockStmt(c.Body)
		cg.popScope()
		if !cg.terminated {
			cg.writef("  br label %%%s\n", doneLabel)
		}
	}

	// Default case.
	if defCase != nil && defCase.Body != nil {
		cg.writeln(defLabel + ":")
		cg.terminated = false
		cg.pushScope()
		cg.emitBlockStmt(defCase.Body)
		cg.popScope()
		if !cg.terminated {
			cg.writef("  br label %%%s\n", doneLabel)
		}
	}

	cg.writeln(doneLabel + ":")
	cg.terminated = false
}

// emitSwitchStmt emits a C-style switch statement as a chain of comparisons.
func (cg *Codegen) emitSwitchStmt(s *ast.SwitchStmt) {
	subjReg := cg.emitExpression(s.Subject)
	subjType := cg.exprLLType(s.Subject)
	endLabel := cg.nextLabel()
	cg.pushLoopLabels("", endLabel)
	defer cg.popLoopLabels()

	var nonDefault []ast.SwitchCase
	var defCase *ast.SwitchCase
	for i := range s.Cases {
		if s.Cases[i].IsDefault {
			defCase = &s.Cases[i]
		} else {
			nonDefault = append(nonDefault, s.Cases[i])
		}
	}

	for _, c := range nonDefault {
		var condReg string
		for j, v := range c.Values {
			valReg := cg.emitExpression(v)
			cmpReg := cg.nextReg()
			valType := cg.exprLLType(v)
			if subjType == "i8*" || valType == "i8*" {
				cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmpReg, subjReg, valReg)
				eqReg := cg.nextReg()
				cg.writef("  %s = icmp eq i32 %s, 0\n", eqReg, cmpReg)
				cmpReg = eqReg
			} else {
				cg.writef("  %s = icmp eq %s %s, %s\n", cmpReg, subjType, subjReg, valReg)
			}
			if j == 0 {
				condReg = cmpReg
			} else {
				orReg := cg.nextReg()
				cg.writef("  %s = or i1 %s, %s\n", orReg, condReg, cmpReg)
				condReg = orReg
			}
		}

		bodyLabel := cg.nextLabel()
		nextLabel := cg.nextLabel()
		cg.writef("  br i1 %s, label %%%s, label %%%s\n", condReg, bodyLabel, nextLabel)

		cg.writeln(bodyLabel + ":")
		cg.terminated = false
		cg.pushScope()
		cg.emitBlockStmt(c.Body)
		cg.popScope()
		if !cg.terminated {
			cg.writef("  br label %%%s\n", endLabel)
		}

		cg.writeln(nextLabel + ":")
		cg.terminated = false
	}

	if defCase != nil {
		cg.pushScope()
		cg.emitBlockStmt(defCase.Body)
		cg.popScope()
		if !cg.terminated {
			cg.writef("  br label %%%s\n", endLabel)
		}
	}

	cg.writeln(endLabel + ":")
	cg.terminated = false
}

// emitReturnStmt emits a return instruction and marks the current basic
// block as terminated.  Subsequent statements in the same block are
// silently skipped to avoid generating invalid IR with multiple terminators.
func (cg *Codegen) emitReturnStmt(r *ast.ReturnStmt) {
	if cg.terminated {
		return
	}
	if len(r.Values) == 0 {
		cg.emitAllDeallocations()
		cg.emitDeferred()
		cg.writeln("  ret void" + cg.dbgTag())
		cg.terminated = true
		return
	}
	// Multiple return values: build aggregate with insertvalue, then ret aggregate.
	if len(r.Values) > 1 {
		agg := cg.buildAggregateReturn(r.Values)
		cg.emitAllDeallocations()
		cg.emitDeferred()
		cg.writef("  ret %s %s%s\n", cg.currentFnRetType, agg, cg.dbgTag())
		cg.terminated = true
		return
	}
	// Single return.
	valReg := cg.emitExpression(r.Values[0])
	// If the single value is a call to a multi-return function, return the aggregate directly.
	if call, ok := r.Values[0].(*ast.CallExpr); ok {
		if id, ok2 := call.Function.(*ast.Identifier); ok2 {
			if retTypes, ok3 := cg.fnRetTypes[id.Value]; ok3 && len(retTypes) > 1 {
				// ARC: retain heap return value before deallocating locals.
				if cg.isHeapLLType(cg.currentFnRetType) {
					valReg = cg.emitRetain(valReg, cg.currentFnRetType)
				}
				cg.emitAllDeallocations()
				cg.emitDeferred()
				cg.writef("  ret %s %s%s\n", cg.currentFnRetType, valReg, cg.dbgTag())
				cg.terminated = true
				return
			}
		}
	}
	if cg.currentFnRetType == "%error" && (valReg == "0" || valReg == "null") {
		valReg = "zeroinitializer"
	}
	// If returning a boolean/comparison (i1) but function expects i32, zext.
	if cg.currentFnRetType == "i32" && cg.exprLLType(r.Values[0]) == "i1" {
		zextReg := cg.nextReg()
		cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
		valReg = zextReg
	}
	// If returning a double but function expects i32, fptosi.
	if cg.currentFnRetType == "i32" && cg.exprLLType(r.Values[0]) == "double" {
		convReg := cg.nextReg()
		cg.writef("  %s = fptosi double %s to i32\n", convReg, valReg)
		valReg = convReg
	}
	// If returning a struct by value, load the struct from its allocated pointer.
	if strings.HasPrefix(cg.currentFnRetType, "%struct.") && !strings.HasSuffix(cg.currentFnRetType, "*") {
		valLLType := cg.exprLLType(r.Values[0])
		if strings.HasSuffix(valLLType, "*") {
			loadedReg := cg.nextReg()
			cg.writef("  %s = load %s, %s* %s\n", loadedReg, cg.currentFnRetType, cg.currentFnRetType, valReg)
			valReg = loadedReg
		}
	}
	// ARC: retain heap return value before deallocating locals.
	if cg.isHeapLLType(cg.currentFnRetType) {
		valReg = cg.emitRetain(valReg, cg.currentFnRetType)
	}
	cg.emitAllDeallocations()
	cg.emitDeferred()
	cg.writef("  ret %s %s%s\n", cg.currentFnRetType, valReg, cg.dbgTag())
	cg.terminated = true
}

// buildAggregateReturn builds an LLVM aggregate value from multiple
// return expressions using insertvalue instructions.
func (cg *Codegen) buildAggregateReturn(values []ast.Expression) string {
	var parts []string
	for _, t := range cg.currentFnRetTypes {
		parts = append(parts, t)
	}
	aggType := "{ " + strings.Join(parts, ", ") + " }"
	agg := cg.nextReg()

	firstType := cg.currentFnRetTypes[0]
	firstVal := cg.emitExpression(values[0])
	if firstType == "%error" && (firstVal == "0" || firstVal == "null") {
		firstVal = "zeroinitializer"
	} else if (firstVal == "0" || firstVal == "null") && strings.HasPrefix(firstType, "%struct.") {
		firstVal = "zeroinitializer"
	}
	// Box concrete error values into the error interface fat pointer.
	if firstType == "%error" {
		valLLType := cg.exprLLType(values[0])
		if strings.HasPrefix(valLLType, "%struct.") {
			concreteLLType := valLLType
			if !strings.HasSuffix(valLLType, "*") {
				alloca := cg.nextReg()
				cg.writef("  %s = alloca %s\n", alloca, valLLType)
				cg.writef("  store %s %s, %s* %s\n", valLLType, firstVal, valLLType, alloca)
				firstVal = alloca
				concreteLLType = valLLType + "*"
			}
			firstVal = cg.emitBoxError(firstVal, concreteLLType)
		}
	}
	if strings.HasPrefix(firstType, "%struct.") && !strings.HasSuffix(firstType, "*") {
		valLLType := cg.exprLLType(values[0])
		if strings.HasSuffix(valLLType, "*") {
			loadedReg := cg.nextReg()
			cg.writef("  %s = load %s, %s* %s\n", loadedReg, firstType, firstType, firstVal)
			firstVal = loadedReg
		}
	}

	cg.writef("  %s = insertvalue %s undef, %s %s, 0\n", agg, aggType,
		firstType, firstVal)

	for i := 1; i < len(values); i++ {
		nextAgg := cg.nextReg()
		nextType := cg.currentFnRetTypes[i]
		nextVal := cg.emitExpression(values[i])
		if nextType == "%error" && (nextVal == "0" || nextVal == "null") {
			nextVal = "zeroinitializer"
		} else if (nextVal == "0" || nextVal == "null") && strings.HasPrefix(nextType, "%struct.") {
			nextVal = "zeroinitializer"
		}
		// Box concrete error values into the error interface fat pointer.
		if nextType == "%error" {
			valLLType := cg.exprLLType(values[i])
			if strings.HasPrefix(valLLType, "%struct.") {
				concreteLLType := valLLType
				if !strings.HasSuffix(valLLType, "*") {
					// Struct returned by value; allocate on stack to get a pointer.
					alloca := cg.nextReg()
					cg.writef("  %s = alloca %s\n", alloca, valLLType)
					cg.writef("  store %s %s, %s* %s\n", valLLType, nextVal, valLLType, alloca)
					nextVal = alloca
					concreteLLType = valLLType + "*"
				}
				nextVal = cg.emitBoxError(nextVal, concreteLLType)
			}
		}
		if strings.HasPrefix(nextType, "%struct.") && !strings.HasSuffix(nextType, "*") {
			valLLType := cg.exprLLType(values[i])
			if strings.HasSuffix(valLLType, "*") {
				loadedReg := cg.nextReg()
				cg.writef("  %s = load %s, %s* %s\n", loadedReg, nextType, nextType, nextVal)
				nextVal = loadedReg
			}
		}
		cg.writef("  %s = insertvalue %s %s, %s %s, %d\n", nextAgg, aggType, agg,
			nextType, nextVal, i)
		agg = nextAgg
	}
	return agg
}

// emitExprStmt emits an expression whose result is discarded
// (e.g. a standalone function call like print("hello")).
func (cg *Codegen) emitExprStmt(e *ast.ExprStmt) {
	cg.emitExpression(e.Expr)
}

// emitIfStmt emits an if/else as a classic diamond control-flow graph:
//
//	     br i1 %cond, label %then, label %else
//	then:
//	     ...
//	     br label %merge
//	else:
//	     ...
//	     br label %merge
//	merge:
//
// The merge block is only emitted when at least one branch falls through,
// preventing unreachable-block warnings from LLVM.
func (cg *Codegen) emitIfStmt(i *ast.IfStmt) {
	condVal := cg.emitExpression(i.Condition)
	thenLabel := cg.nextLabel()
	elseLabel := cg.nextLabel()
	mergeLabel := cg.nextLabel()

	cg.writef("  br i1 %s, label %%%s, label %%%s\n", condVal, thenLabel, elseLabel)

	cg.writeln(thenLabel + ":")
	cg.terminated = false
	cg.pushScope()
	cg.emitBlockStmt(i.Consequence)
	cg.popScope()
	thenTerminated := cg.terminated
	if !thenTerminated {
		cg.writef("  br label %%%s\n", mergeLabel)
	}

	cg.writeln(elseLabel + ":")
	cg.terminated = false
	elseTerminated := false
	if i.Alternative != nil {
		cg.pushScope()
		cg.emitStatement(i.Alternative)
		cg.popScope()
		elseTerminated = cg.terminated
	}
	if !elseTerminated {
		cg.writef("  br label %%%s\n", mergeLabel)
	}

	cg.terminated = thenTerminated && elseTerminated
	if !cg.terminated {
		cg.writeln(mergeLabel + ":")
	}
}

// emitIfExpr emits an if expression that returns a value.
// Strategy: alloca a result slot, store branch values, load result.
func (cg *Codegen) emitIfExpr(e *ast.IfExpr) string {
	resultType := cg.exprLLType(e)
	if resultType == "" {
		resultType = "i32"
	}
	resultAlloca := cg.nextReg()
	cg.writef("  %s = alloca %s\n", resultAlloca, resultType)

	condVal := cg.emitExpression(e.Condition)
	thenLabel := cg.nextLabel()
	elseLabel := cg.nextLabel()
	mergeLabel := cg.nextLabel()

	cg.writef("  br i1 %s, label %%%s, label %%%s\n", condVal, thenLabel, elseLabel)

	// Then branch: emit all but last statement, then store last expression.
	cg.writeln(thenLabel + ":")
	cg.terminated = false
	cg.pushScope()
	cg.emitIfBranchBlock(e.Consequence, resultAlloca, resultType)
	cg.popScope()
	cg.writef("  br label %%%s\n", mergeLabel)

	// Else branch
	cg.writeln(elseLabel + ":")
	cg.terminated = false
	cg.pushScope()
	cg.emitIfBranchBlock(e.Alternative, resultAlloca, resultType)
	cg.popScope()
	cg.writef("  br label %%%s\n", mergeLabel)

	// Merge
	cg.writeln(mergeLabel + ":")
	cg.terminated = false
	resultReg := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", resultReg, resultType, resultType, resultAlloca)
	return resultReg
}

// emitMatchExpr lowers a match expression to a chain of conditional branches.
func (cg *Codegen) emitMatchExpr(e *ast.MatchExpr) string {
	resultType := cg.exprLLType(e)
	if resultType == "" {
		resultType = "i32"
	}
	resultAlloca := cg.nextReg()
	cg.writef("  %s = alloca %s\n", resultAlloca, resultType)

	subjectReg := cg.emitExpression(e.Subject)
	subjectType := cg.exprLLType(e.Subject)
	endLabel := cg.nextLabel()

	for i, arm := range e.Arms {
		isLast := i == len(e.Arms)-1
		isWildcard := false
		if id, ok := arm.Pattern.(*ast.Identifier); ok && id.Value == "_" {
			isWildcard = true
		}

		// Last arm or wildcard: just emit body.
		if isLast || isWildcard {
			cg.pushScope()
			cg.emitIfBranchBlock(arm.Body, resultAlloca, resultType)
			cg.popScope()
			cg.writef("  br label %%%s\n", endLabel)
			break
		}

		// Emit pattern comparison.
		patternReg := cg.emitExpression(arm.Pattern)
		cmpReg := cg.nextReg()
		if subjectType == "i8*" || cg.exprLLType(arm.Pattern) == "i8*" {
			cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmpReg, subjectReg, patternReg)
			matchReg := cg.nextReg()
			cg.writef("  %s = icmp eq i32 %s, 0\n", matchReg, cmpReg)
			cmpReg = matchReg
		} else {
			cg.writef("  %s = icmp eq i32 %s, %s\n", cmpReg, subjectReg, patternReg)
		}

		// Optional guard.
		condReg := cmpReg
		if arm.Guard != nil {
			guardReg := cg.emitExpression(arm.Guard)
			combined := cg.nextReg()
			cg.writef("  %s = and i1 %s, %s\n", combined, condReg, guardReg)
			condReg = combined
		}

		bodyLabel := cg.nextLabel()
		nextLabel := cg.nextLabel()
		cg.writef("  br i1 %s, label %%%s, label %%%s\n", condReg, bodyLabel, nextLabel)

		cg.writeln(bodyLabel + ":")
		cg.terminated = false
		cg.pushScope()
		cg.emitIfBranchBlock(arm.Body, resultAlloca, resultType)
		cg.popScope()
		cg.writef("  br label %%%s\n", endLabel)

		cg.writeln(nextLabel + ":")
		cg.terminated = false
	}

	cg.writeln(endLabel + ":")
	cg.terminated = false
	resultReg := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", resultReg, resultType, resultType, resultAlloca)
	return resultReg
}

// emitQueryExpr lowers a LINQ-style query into loops and temporary arrays.
// Supports: from x in source [where cond] select expr
func (cg *Codegen) emitQueryExpr(e *ast.QueryExpr) string {
	// Determine element type and result type.
	elemLLType := cg.inferElementType(e.From.Iterable)
	if elemLLType == "" {
		elemLLType = "i32"
	}
	resultLLType := cg.exprLLType(e.Select.Expression)
	if resultLLType == "" {
		resultLLType = "i32"
	}

	// Emit source array.
	sourceReg := cg.emitExpression(e.From.Iterable)

	// Get source length from the 8-byte prefix before the data pointer.
	sourceRaw := cg.nextReg()
	cg.writef("  %s = bitcast %s* %s to i8*\n", sourceRaw, elemLLType, sourceReg)
	sourceLenPtrRaw := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 -8\n", sourceLenPtrRaw, sourceRaw)
	sourceLenPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", sourceLenPtr, sourceLenPtrRaw)
	sourceLenReg := cg.nextReg()
	cg.writef("  %s = load i64, i64* %s\n", sourceLenReg, sourceLenPtr)
	sourceLenI32 := cg.nextReg()
	cg.writef("  %s = trunc i64 %s to i32\n", sourceLenI32, sourceLenReg)

	// Determine if there's a where clause.
	var whereClause *ast.WhereClause
	for _, clause := range e.Clauses {
		if wc, ok := clause.(*ast.WhereClause); ok {
			whereClause = wc
			break
		}
	}

	// Allocate result array (worst-case size = source length).
	resultElemSize := cg.emitElemSize(resultLLType)
	totalBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %s, %s\n", totalBytes, sourceLenI32, resultElemSize)
	totalBytesPrefix := cg.nextReg()
	cg.writef("  %s = add i32 %s, 8\n", totalBytesPrefix, totalBytes)
	totalBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", totalBytes64, totalBytesPrefix)
	rawResult := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawResult, totalBytes64)
	resultLenPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", resultLenPtr, rawResult)
	// Initialize length to 0; we'll update it at the end.
	cg.writef("  store i64 0, i64* %s\n", resultLenPtr)
	resultDataStart := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", resultDataStart, rawResult)
	resultDataPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", resultDataPtr, resultDataStart, resultLLType)

	// Allocate output index counter.
	outIdxAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", outIdxAlloca)
	cg.writef("  store i32 0, i32* %s\n", outIdxAlloca)

	// Allocate loop variable.
	loopVarAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", loopVarAlloca)
	cg.writef("  store i32 0, i32* %s\n", loopVarAlloca)

	// Labels.
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	postLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.writef("  br label %%%s\n", condLabel)

	// cond: i < sourceLen
	cg.writeln(condLabel + ":")
	cg.terminated = false
	iLoad := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", iLoad, loopVarAlloca)
	cmpReg := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cmpReg, iLoad, sourceLenI32)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cmpReg, bodyLabel, endLabel)

	// body: load element, declare range var, check where, evaluate select, store.
	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.pushScope()

	// Load source element.
	elemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		elemPtr, elemLLType, elemLLType, sourceReg, iLoad)
	elemVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", elemVal, elemLLType, elemLLType, elemPtr)

	// Declare range variable.
	rangeVarAlloca := cg.nextReg()
	cg.writef("  %s = alloca %s\n", rangeVarAlloca, elemLLType)
	cg.writef("  store %s %s, %s* %s\n", elemLLType, elemVal, elemLLType, rangeVarAlloca)
	cg.declareVar(e.From.Variable, rangeVarAlloca, elemLLType, false, &ast.NamedType{Name: "int"})

	// Evaluate where condition if present.
	if whereClause != nil {
		condReg := cg.emitExpression(whereClause.Condition)
		skipLabel := cg.nextLabel()
		cg.writef("  br i1 %s, label %%%s, label %%%s\n", condReg, skipLabel, postLabel)
		cg.writeln(skipLabel + ":")
		cg.terminated = false
	}

	// Evaluate select expression.
	selectVal := cg.emitExpression(e.Select.Expression)
	selectLLType := cg.exprLLType(e.Select.Expression)
	if selectLLType == "" {
		selectLLType = "i32"
	}

	// Store in result array.
	outIdxLoad := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", outIdxLoad, outIdxAlloca)
	resultElemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		resultElemPtr, selectLLType, selectLLType, resultDataPtr, outIdxLoad)
	cg.writef("  store %s %s, %s* %s\n", selectLLType, selectVal, selectLLType, resultElemPtr)

	// Increment output index.
	outIdxNext := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", outIdxNext, outIdxLoad)
	cg.writef("  store i32 %s, i32* %s\n", outIdxNext, outIdxAlloca)

	cg.writef("  br label %%%s\n", postLabel)
	cg.popScope()

	// post: i++
	cg.writeln(postLabel + ":")
	cg.terminated = false
	iLoad2 := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", iLoad2, loopVarAlloca)
	nextI := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextI, iLoad2)
	cg.writef("  store i32 %s, i32* %s\n", nextI, loopVarAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// end: update result length and return.
	cg.writeln(endLabel + ":")
	cg.terminated = false
	finalLenReg := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", finalLenReg, outIdxAlloca)
	finalLen64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", finalLen64, finalLenReg)
	cg.writef("  store i64 %s, i64* %s\n", finalLen64, resultLenPtr)

	return resultDataPtr
}

// inferElementType extracts the LLVM element type from an iterable expression.
func (cg *Codegen) inferElementType(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.ArrayLiteral:
		if len(e.Elements) > 0 {
			return cg.exprLLType(e.Elements[0])
		}
		return "i32"
	case *ast.Identifier:
		_, lt := cg.resolveVar(e.Value)
		if lt == "" {
			lt = cg.globalVarTypes[e.Value]
		}
		if lt == "i8*" {
			return "i32" // string element type
		}
		if strings.HasSuffix(lt, "*") {
			return strings.TrimSuffix(lt, "*")
		}
		return lt
	case *ast.MapLiteral:
		if len(e.Pairs) > 0 {
			return cg.exprLLType(e.Pairs[0].Value)
		}
		return "i32"
	case *ast.SetLiteral:
		if len(e.Elements) > 0 {
			return cg.exprLLType(e.Elements[0])
		}
		return "i32"
	}
	return "i32"
}

// emitIfBranchBlock emits all statements in a block except the last,
// then evaluates the last expression and stores it into resultAlloca.
func (cg *Codegen) emitIfBranchBlock(block *ast.BlockStmt, resultAlloca, resultType string) {
	if block == nil || len(block.Statements) == 0 {
		return
	}
	for _, stmt := range block.Statements[:len(block.Statements)-1] {
		cg.emitStatement(stmt)
	}
	last := block.Statements[len(block.Statements)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		valReg := cg.emitExpression(es.Expr)
		if resultType == "i32" && cg.exprLLType(es.Expr) == "i1" {
			zext := cg.nextReg()
			cg.writef("  %s = zext i1 %s to i32\n", zext, valReg)
			valReg = zext
		}
		cg.writef("  store %s %s, %s* %s\n", resultType, valReg, resultType, resultAlloca)
	}
}

// emitWhileStmt emits a while loop as a loop control-flow graph:
//
//	     br label %cond
//	cond:
//	     br i1 %cond, label %body, label %end
//	body:
//	     ...
//	     br label %cond
//	end:
//
// If the loop body contains an early return (e.g. break is not yet
// supported), the back-edge is omitted to keep the IR valid.
func (cg *Codegen) emitWhileStmt(w *ast.WhileStmt) {
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.pushLoopLabels(condLabel, endLabel)
	defer cg.popLoopLabels()

	cg.writef("  br label %%%s\n", condLabel)

	cg.writeln(condLabel + ":")
	condVal := cg.emitExpression(w.Condition)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", condVal, bodyLabel, endLabel)

	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.pushScope()
	cg.emitBlockStmt(w.Body)
	cg.popScope()
	if !cg.terminated {
		cg.writef("  br label %%%s\n", condLabel)
	}

	cg.terminated = false
	cg.writeln(endLabel + ":")
}

// emitUntilStmt emits an until loop (while !condition):
//
//	     br label %cond
//	cond:
//	     %neg = xor i1 %cond, 1
//	     br i1 %neg, label %body, label %end
//	body:
//	     ...
//	     br label %cond
//	end:
func (cg *Codegen) emitUntilStmt(u *ast.UntilStmt) {
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.pushLoopLabels(condLabel, endLabel)
	defer cg.popLoopLabels()

	cg.writef("  br label %%%s\n", condLabel)

	cg.writeln(condLabel + ":")
	condVal := cg.emitExpression(u.Condition)
	// Negate: until condition means loop while !condition
	negReg := cg.nextReg()
	cg.writef("  %s = xor i1 %s, 1\n", negReg, condVal)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", negReg, bodyLabel, endLabel)

	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.pushScope()
	cg.emitBlockStmt(u.Body)
	cg.popScope()
	if !cg.terminated {
		cg.writef("  br label %%%s\n", condLabel)
	}

	cg.terminated = false
	cg.writeln(endLabel + ":")
}

// emitForStmt emits a C-style for loop:
//
//	for init; cond; post { body }
//
// Lowered to explicit init + while-like structure with a dedicated
// post-update block:
//
//	     <init>
//	     br label %cond
//	cond:
//	     br i1 %cond, label %body, label %end
//	body:
//	     ...
//	     br label %post
//	post:
//	     <post>
//	     br label %cond
//	end:
//
// for-in loops (Iterator != nil) are lowered to index-based iteration.
func (cg *Codegen) emitForStmt(f *ast.ForStmt) {
	if f.Iterator != nil {
		cg.emitForInStmt(f.Iterator, f.Body)
		return
	}
	// C-style for: init; cond; post { body }
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	postLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.pushLoopLabels(postLabel, endLabel)
	defer cg.popLoopLabels()

	if f.Init != nil {
		cg.emitStatement(f.Init)
	}

	cg.writef("  br label %%%s\n", condLabel)

	cg.writeln(condLabel + ":")
	if f.Condition != nil {
		condVal := cg.emitExpression(f.Condition)
		cg.writef("  br i1 %s, label %%%s, label %%%s\n", condVal, bodyLabel, endLabel)
	} else {
		cg.writef("  br label %%%s\n", bodyLabel)
	}

	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.pushScope()
	cg.emitBlockStmt(f.Body)
	cg.popScope()
	if !cg.terminated {
		cg.writef("  br label %%%s\n", postLabel)
	}

	cg.writeln(postLabel + ":")
	cg.terminated = false
	if f.Post != nil {
		cg.emitStatement(f.Post)
	}
	cg.writef("  br label %%%s\n", condLabel)

	cg.terminated = false
	cg.writeln(endLabel + ":")
}

// emitForInStmt lowers `for x in arr { body }` to index-based iteration:
//
//	idx = alloca i32; store 0 to idx
//	br label %cond
//
// cond:
//
//	current = load idx
//	cmp = icmp slt current, len(arr)
//	br i1 cmp, label %body, label %end
//
// body:
//
//	current = load idx
//	elem = getelementptr arr, current; load elem
//	store elem to x
//	... body ...
//	br label %post
//
// post:
//
//	current = load idx
//	next = add current, 1
//	store next to idx
//	br label %cond
//
// end:
func (cg *Codegen) emitForInStmt(it *ast.ForInStmt, body *ast.BlockStmt) {
	// Handle channel range loop: for val := range ch { }
	if it.IsRange {
		cg.emitForRangeStmt(it, body)
		return
	}

	// Handle range expression: for i in 0..10 { }
	if rng, ok := it.Iterable.(*ast.RangeExpr); ok {
		cg.emitForInRange(it.Variable, rng, body)
		return
	}

	// Determine array info from the iterable expression.
	arrLen, elemType := cg.inferArrayInfo(it.Iterable)
	iterLLType := cg.exprLLType(it.Iterable)
	if elemType == "" {
		if strings.HasSuffix(iterLLType, "*") {
			elemType = strings.TrimSuffix(iterLLType, "*")
		} else {
			elemType = "i32"
		}
	}

	// Allocate and init index counter.
	idxAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxAlloca)
	cg.writef("  store i32 0, i32* %s\n", idxAlloca)

	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	postLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.pushLoopLabels(condLabel, endLabel)
	defer cg.popLoopLabels()

	cg.writef("  br label %%%s\n", condLabel)

	// cond: check idx < arrLen
	cg.writeln(condLabel + ":")
	idxLoad := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxLoad, idxAlloca)
	cmpReg := cg.nextReg()
	if arrLen > 0 {
		cg.writef("  %s = icmp slt i32 %s, %d\n", cmpReg, idxLoad, arrLen)
	} else {
		iterPtr := cg.emitExpression(it.Iterable)
		lenReg := cg.emitRuntimeArrayLen(iterPtr, iterLLType)
		cg.writef("  %s = icmp slt i32 %s, %s\n", cmpReg, idxLoad, lenReg)
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cmpReg, bodyLabel, endLabel)

	// body: load element, store to loop var, execute body
	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.pushScope()

	// Loop variable alloca
	varAlloca := cg.nextReg()
	cg.writef("  %s = alloca %s\n", varAlloca, elemType)
	var elemAST ast.Type
	if arr, ok := cg.exprASTType(it.Iterable).(*ast.ArrayType); ok {
		elemAST = arr.Elem
	}
	cg.declareVar(it.Variable, varAlloca, elemType, cg.isUnsignedExpr(it.Iterable), elemAST)

	// Load element from array using simple pointer arithmetic.
	// The array variable stores i32* (pointer to first element).
	arrReg := cg.emitExpression(it.Iterable)
	idxLoad2 := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxLoad2, idxAlloca)
	elemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		elemPtr, elemType, elemType, arrReg, idxLoad2)
	elemVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", elemVal, elemType, elemType, elemPtr)
	cg.writef("  store %s %s, %s* %s\n", elemType, elemVal, elemType, varAlloca)

	cg.emitBlockStmt(body)
	cg.popScope()
	if !cg.terminated {
		cg.writef("  br label %%%s\n", postLabel)
	}

	// post: increment index
	cg.writeln(postLabel + ":")
	cg.terminated = false
	idxLoad3 := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", idxLoad3, idxAlloca)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, idxLoad3)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// end
	cg.terminated = false
	cg.writeln(endLabel + ":")
}

// emitForRangeStmt emits `for val := range ch { body }`.
func (cg *Codegen) emitForRangeStmt(it *ast.ForInStmt, body *ast.BlockStmt) {
	chType := cg.exprASTType(it.Iterable)
	var elemAST ast.Type
	if ct, ok := chType.(*ast.ChanType); ok {
		elemAST = ct.Elem
	}
	elemLLType := llvmType(elemAST)
	if elemLLType == "" {
		elemLLType = "i32"
	}

	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.pushLoopLabels(condLabel, endLabel)
	defer cg.popLoopLabels()

	chReg := cg.emitExpression(it.Iterable)
	cg.writef("  br label %%%s\n", condLabel)

	// cond: call Skink_chan_recv
	cg.writeln(condLabel + ":")
	recvPtr := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_chan_recv(i8* %s)\n", recvPtr, chReg)
	cmpReg := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", cmpReg, recvPtr)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cmpReg, endLabel, bodyLabel)

	// body: cast received pointer, store in loop variable, run body
	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.pushScope()

	varAlloca := cg.nextReg()
	cg.writef("  %s = alloca %s\n", varAlloca, elemLLType)
	cg.declareVar(it.Variable, varAlloca, elemLLType, isUnsignedType(elemAST), elemAST)

	castReg := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", castReg, recvPtr, elemLLType)
	valReg := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", valReg, elemLLType, elemLLType, castReg)
	cg.writef("  store %s %s, %s* %s\n", elemLLType, valReg, elemLLType, varAlloca)

	// Free the received node data block.
	cg.writef("  call void @Skink_free(i8* %s)\n", recvPtr)

	cg.emitBlockStmt(body)
	cg.popScope()

	if !cg.terminated {
		cg.writef("  br label %%%s\n", condLabel)
	}

	cg.terminated = false
	cg.writeln(endLabel + ":")
}

// emitForInRange lowers `for i in start..end { body }` to a numeric loop.
func (cg *Codegen) emitForInRange(varName string, rng *ast.RangeExpr, body *ast.BlockStmt) {
	startReg := cg.emitExpression(rng.Start)
	endReg := cg.emitExpression(rng.End)

	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	postLabel := cg.nextLabel()
	endLabel := cg.nextLabel()
	cg.pushLoopLabels(condLabel, endLabel)
	defer cg.popLoopLabels()

	// Push a scope for the loop variable so it shadows outer vars
	// and is properly cleaned up after the loop.
	cg.pushScope()

	// Allocate loop variable and initialize to start.
	varAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", varAlloca)
	cg.writef("  store i32 %s, i32* %s\n", startReg, varAlloca)
	cg.declareVar(varName, varAlloca, "i32", false, &ast.NamedType{Name: "int"})

	cg.writef("  br label %%%s\n", condLabel)

	// cond: check i < end
	cg.writeln(condLabel + ":")
	iLoad := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", iLoad, varAlloca)
	cmpReg := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cmpReg, iLoad, endReg)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cmpReg, bodyLabel, endLabel)

	// body: execute body (emitBlockStmt pushes/pops its own scope)
	cg.writeln(bodyLabel + ":")
	cg.terminated = false
	cg.emitBlockStmt(body)
	if !cg.terminated {
		cg.writef("  br label %%%s\n", postLabel)
	}

	// post: i++
	cg.writeln(postLabel + ":")
	cg.terminated = false
	iLoad2 := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", iLoad2, varAlloca)
	nextI := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextI, iLoad2)
	cg.writef("  store i32 %s, i32* %s\n", nextI, varAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// end: pop the loop variable scope
	cg.terminated = false
	cg.popScope()
	cg.writeln(endLabel + ":")
}

// --- Loop label tracking for break / continue ---

// pushLoopLabels saves continue and end labels for the current loop.
func (cg *Codegen) pushLoopLabels(cont, end string) {
	cg.loopLabels = append(cg.loopLabels, loopLabels{continueLabel: cont, endLabel: end})
}

// popLoopLabels removes the innermost loop labels.
func (cg *Codegen) popLoopLabels() {
	if len(cg.loopLabels) > 0 {
		cg.loopLabels = cg.loopLabels[:len(cg.loopLabels)-1]
	}
}

// currentLoopLabels returns the active loop labels, or false if none.
func (cg *Codegen) currentLoopLabels() (continueLabel, endLabel string, ok bool) {
	if len(cg.loopLabels) == 0 {
		return "", "", false
	}
	ll := cg.loopLabels[len(cg.loopLabels)-1]
	return ll.continueLabel, ll.endLabel, true
}

// emitBreakStmt emits a branch to the nearest enclosing loop's end label.
func (cg *Codegen) emitBreakStmt() {
	if cg.terminated {
		return
	}
	_, endLabel, ok := cg.currentLoopLabels()
	if !ok {
		cg.Errorf("break outside loop")
		return
	}
	cg.writef("  br label %%%s\n", endLabel)
	cg.terminated = true
}

// emitContinueStmt emits a branch to the nearest enclosing loop's continue label.
func (cg *Codegen) emitContinueStmt() {
	if cg.terminated {
		return
	}
	continueLabel, _, ok := cg.currentLoopLabels()
	if !ok || continueLabel == "" {
		cg.Errorf("continue outside loop")
		return
	}
	cg.writef("  br label %%%s\n", continueLabel)
	cg.terminated = true
}

// inferArrayInfo extracts the array length and element type from an expression.
// Returns (0, "") if the length cannot be determined.
func (cg *Codegen) inferArrayInfo(expr ast.Expression) (int, string) {
	switch e := expr.(type) {
	case *ast.ArrayLiteral:
		if len(e.Elements) > 0 {
			return len(e.Elements), cg.exprLLType(e.Elements[0])
		}
		return 0, "i32"
	case *ast.Identifier:
		// First check the arraySizes map (populated when a var is declared
		// with an array literal).
		allocaKey := e.Value
		if allocReg, _ := cg.resolveVar(e.Value); allocReg != "" {
			allocaKey = allocReg
		}
		if meta, ok := cg.arraySizes[cg.currentFnName+":"+allocaKey]; ok {
			if meta.heapAlloc {
				return 0, meta.elemType
			}
			return meta.len, meta.elemType
		}
		// Fallback: try to parse array type from scope.
		_, lt := cg.resolveVar(e.Value)
		if strings.HasPrefix(lt, "[") {
			rest := lt[1:] // remove leading [
			idxX := strings.Index(rest, " x ")
			if idxX > 0 {
				countStr := rest[:idxX]
				if n, err := strconv.Atoi(countStr); err == nil {
					elemStart := idxX + 3
					elemEnd := strings.LastIndex(rest, "]")
					if elemEnd > elemStart {
						return n, rest[elemStart:elemEnd]
					}
				}
			}
		}
	}
	return 0, ""
}

// arrayLLType formats an array type string "[len x elemType]".
func (cg *Codegen) arrayLLType(len int, elemType string) string {
	return fmt.Sprintf("[%d x %s]", len, elemType)
}

// emitExpression recursively evaluates a Skink expression and returns the
// LLVM SSA virtual register (or inline constant) that holds its value.
//
// For literals the result is the literal string (e.g. "42").
// For identifiers the result is a fresh register loaded from the alloca.
// For compound expressions the result is a fresh register from the operation.
func (cg *Codegen) emitExpression(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return fmt.Sprintf("%d", e.Value)
	case *ast.FloatLiteral:
		return fmt.Sprintf("%f", e.Value)
	case *ast.BooleanLiteral:
		if e.Value {
			return "1"
		}
		return "0"
	case *ast.NilLiteral:
		return "null"
	case *ast.StringLiteral:
		return cg.emitStringLiteral(e)
	case *ast.Identifier:
		// Check if this identifier is a captured variable in a closure.
		if info, ok := cg.closureEnv[e.Value]; ok {
			// Load from the global env struct.
			reg := cg.nextReg()
			cg.writef("  %s = getelementptr %%%s, %%%s* %s, i32 0, i32 %d\n", reg, info.envStruct, info.envStruct, info.globalName, info.fieldIndex)
			loadReg := cg.nextReg()
			cg.writef("  %s = load %s, %s* %s\n", loadReg, info.llType, info.llType, reg)
			return loadReg
		}
		// Check if this is a const — inline the literal value directly.
		if val, ok := cg.consts[e.Value]; ok {
			return cg.emitExpression(val)
		}
		alloca, lt := cg.resolveVar(e.Value)
		if lt == "" {
			lt = cg.globalVarTypes[e.Value]
			if lt == "" {
				lt = "i32"
			}
		}
		if alloca == "" {
			// Could be a global function or a global variable.
			if _, isFn := cg.fnRetTypes[e.Value]; isFn {
				return "@" + e.Value
			}
			// Global variable: load from global address.
			reg := cg.nextReg()
			cg.writef("  %s = load %s, %s* @%s\n", reg, lt, lt, e.Value)
			return reg
		}
		reg := cg.nextReg()
		cg.writef("  %s = load %s, %s* %s\n", reg, lt, lt, alloca)
		return reg
	case *ast.PrefixExpr:
		return cg.emitPrefixExpr(e)
	case *ast.InfixExpr:
		return cg.emitInfixExpr(e)
	case *ast.CallExpr:
		return cg.emitCallExpr(e)
	case *ast.ArrayLiteral:
		return cg.emitArrayLiteral(e)
	case *ast.MapLiteral:
		return cg.emitMapLiteral(e)
	case *ast.SetLiteral:
		return cg.emitSetLiteral(e)
	case *ast.SpreadExpr:
		return cg.emitExpression(e.Operand)
	case *ast.IndexExpr:
		return cg.emitIndexExpr(e)
	case *ast.FromEndIndexExpr:
		return cg.emitExpression(e.Operand)
	case *ast.SliceExpr:
		return cg.emitSliceExpr(e)
	case *ast.RangeExpr:
		return cg.emitRangeExpr(e)
	case *ast.FieldAccessExpr:
		return cg.emitFieldAccessExpr(e)
	case *ast.StructInitExpr:
		return cg.emitStructInitExpr(e)
	case *ast.ErrorPropagationExpr:
		return cg.emitExpression(e.Expr)
	case *ast.AsyncExpr:
		return cg.emitAsyncExpr(e)
	case *ast.AwaitExpr:
		return cg.emitAwaitExpr(e)
	case *ast.SizeofExpr:
		return cg.emitSizeofExpr(e)
	case *ast.AlignofExpr:
		return cg.emitAlignofExpr(e)
	case *ast.MinExpr:
		return cg.emitMinExpr(e)
	case *ast.MaxExpr:
		return cg.emitMaxExpr(e)
	case *ast.MakeExpr:
		return cg.emitMakeExpr(e)
	case *ast.IfExpr:
		return cg.emitIfExpr(e)
	case *ast.MatchExpr:
		return cg.emitMatchExpr(e)
	case *ast.QueryExpr:
		return cg.emitQueryExpr(e)
	case *ast.FnLiteral:
		startCounter := cg.anonFnCounter
		fnPtr := cg.emitFnLiteral(e)
		if len(e.Captures) > 0 {
			envNum := startCounter
			envGlobal := fmt.Sprintf("@anon_env_%d", envNum)
			envStruct := fmt.Sprintf("struct.anon_env_%d", envNum)
			for i, capName := range e.Captures {
				// Look up the captured var's type and alloca in the current scope.
				capLLType := "i32"
				alloca, lt := cg.resolveVar(capName)
				if lt == "" {
					lt = cg.globalVarTypes[capName]
				}
				if lt != "" {
					capLLType = lt
				}
				// Load the captured value.
				var valReg string
				if alloca != "" {
					valReg = cg.nextReg()
					cg.writef("  %s = load %s, %s* %s\n", valReg, capLLType, capLLType, alloca)
				} else {
					// Global variable.
					valReg = cg.nextReg()
					cg.writef("  %s = load %s, %s* @%s\n", valReg, capLLType, capLLType, capName)
				}
				// Store into env struct.
				ptrReg := cg.nextReg()
				cg.writef("  %s = getelementptr %%%s, %%%s* %s, i32 0, i32 %d\n", ptrReg, envStruct, envStruct, envGlobal, i)
				cg.writef("  store %s %s, %s* %s\n", capLLType, valReg, capLLType, ptrReg)
			}
		}
		return fnPtr
	}
	return "0"
}

// emitGlobalStringConst emits a global string constant for reflection.
func (cg *Codegen) emitGlobalStringConst(val string) string {
	cg.strCounter++
	globalName := fmt.Sprintf("@str.reflect.%d", cg.strCounter)
	escaped, length := escapeLLVMString(val)
	cg.writeStringGlobal("%s = private constant [%d x i8] c\"%s\\00\"\n", globalName, length, escaped)
	ptrReg := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds [%d x i8], [%d x i8]* %s, i64 0, i64 0\n",
		ptrReg, length, length, globalName)
	return ptrReg
}

// emitReflectTypeOfBody generates the body of the reflect.TypeOf runtime function.
func (cg *Codegen) emitReflectTypeOfBody(fn *ast.FnDecl) {
	// We are already inside the curly braces of define Types
	cg.writeln("entry:")

	suffix := strings.TrimPrefix(fn.Name, "reflect.TypeOf_")

	// Derive the human-readable type name. If we have a struct declaration,
	// use its short name (last component after any module qualifier).
	typeName := suffix

	// 1. Check if suffix is a struct and compile layout/metadata
	d, hasDecl := cg.structDecls[suffix]
	var fieldsPtr string
	var numFields int
	var size int

	if hasDecl {
		numFields = len(d.Fields)
		size, _ = cg.computeStructLayout(suffix)
		if idx := strings.LastIndex(d.Name, "."); idx >= 0 {
			typeName = d.Name[idx+1:]
		} else {
			typeName = d.Name
		}

		// 2. Allocate the Fields dynamic array block: 8 + numFields * 8 (array of pointers)
		if numFields > 0 {
			bytesNeeded := 8 + numFields*8

			rawFields := cg.nextReg()
			cg.writef("  %s = call i8* @malloc(i64 %d)\n", rawFields, bytesNeeded)

			// Store length as i64 in the prefix
			lenPtr := cg.nextReg()
			cg.writef("  %s = bitcast i8* %s to i64*\n", lenPtr, rawFields)
			cg.writef("  store i64 %d, i64* %s\n", numFields, lenPtr)

			// Elements start at offset 8
			elemStart := cg.nextReg()
			cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", elemStart, rawFields)
			typedElemStart := cg.nextReg()
			cg.writef("  %s = bitcast i8* %s to %%struct.reflect_Field**\n", typedElemStart, elemStart)

			layout := cg.structLayouts[suffix]

			// Store each field's metadata
			for i, field := range d.Fields {
				fieldNameGlobal := cg.emitGlobalStringConst(field.Name)
				fieldTypeGlobal := cg.emitGlobalStringConst(field.Type.String())
				fieldOffset := layout.offsets[i]

				// Allocate a single reflect.Field struct via RC runtime
				fieldRaw := cg.nextReg()
				cg.writef("  %s = call i8* @Skink_rc_alloc(i64 24)\n", fieldRaw)
				fieldPtr := cg.nextReg()
				cg.writef("  %s = bitcast i8* %s to %%struct.reflect_Field*\n", fieldPtr, fieldRaw)

				// Store name into fieldPtr.0
				nameField := cg.nextReg()
				cg.writef("  %s = getelementptr inbounds %%struct.reflect_Field, %%struct.reflect_Field* %s, i32 0, i32 0\n",
					nameField, fieldPtr)
				cg.writef("  store i8* %s, i8** %s\n", fieldNameGlobal, nameField)

				// Store type_name into fieldPtr.1
				typeField := cg.nextReg()
				cg.writef("  %s = getelementptr inbounds %%struct.reflect_Field, %%struct.reflect_Field* %s, i32 0, i32 1\n",
					typeField, fieldPtr)
				cg.writef("  store i8* %s, i8** %s\n", fieldTypeGlobal, typeField)

				// Store offset into fieldPtr.2
				offsetField := cg.nextReg()
				cg.writef("  %s = getelementptr inbounds %%struct.reflect_Field, %%struct.reflect_Field* %s, i32 0, i32 2\n",
					offsetField, fieldPtr)
				cg.writef("  store i32 %d, i32* %s\n", fieldOffset, offsetField)

				// Store the pointer into the array
				arrSlot := cg.nextReg()
				cg.writef("  %s = getelementptr inbounds %%struct.reflect_Field*, %%struct.reflect_Field** %s, i32 %d\n",
					arrSlot, typedElemStart, i)
				cg.writef("  store %%struct.reflect_Field* %s, %%struct.reflect_Field** %s\n", fieldPtr, arrSlot)
			}

			fieldsPtr = typedElemStart
		} else {
			fieldsPtr = "null"
		}
	} else {
		// Non-struct primitive type suffix (like "int" or generic parameter types)
		numFields = 0
		size = 4
		if suffix == "int64" || suffix == "float" || suffix == "string" {
			size = 8
		}
		fieldsPtr = "null"
	}

	// 3. Allocate the Type struct: 24 bytes (sizeof(reflect.Type))
	rawType := cg.nextReg()
	cg.writef("  %s = call i8* @malloc(i64 24)\n", rawType)
	typedType := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %%struct.reflect_Type*\n", typedType, rawType)

	// Set fields in the allocated Type struct
	typeNameGlobal := cg.emitGlobalStringConst(typeName)

	// name field
	namePtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %%struct.reflect_Type, %%struct.reflect_Type* %s, i32 0, i32 0\n",
		namePtr, typedType)
	cg.writef("  store i8* %s, i8** %s\n", typeNameGlobal, namePtr)

	// size field
	sizePtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %%struct.reflect_Type, %%struct.reflect_Type* %s, i32 0, i32 1\n",
		sizePtr, typedType)
	cg.writef("  store i32 %d, i32* %s\n", size, sizePtr)

	// numFields field
	numPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %%struct.reflect_Type, %%struct.reflect_Type* %s, i32 0, i32 2\n",
		numPtr, typedType)
	cg.writef("  store i32 %d, i32* %s\n", numFields, numPtr)

	// fields field
	fieldsGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %%struct.reflect_Type, %%struct.reflect_Type* %s, i32 0, i32 3\n",
		fieldsGep, typedType)
	cg.writef("  store %%struct.reflect_Field** %s, %%struct.reflect_Field*** %s\n", fieldsPtr, fieldsGep)

	// Return the constructed Type value
	retVal := cg.nextReg()
	cg.writef("  %s = load %%struct.reflect_Type, %%struct.reflect_Type* %s\n", retVal, typedType)
	cg.writef("  ret %%struct.reflect_Type %s\n", retVal)
	cg.writeln("}")
}

// emitCast emits an LLVM conversion instruction for a type cast.
// Supports numeric casts, bool<->int, and pointer casts.
func (cg *Codegen) emitCast(expr ast.Expression, targetType string) string {
	operand := cg.emitExpression(expr)
	srcType := cg.exprLLType(expr)
	reg := cg.nextReg()

	// Same type: no-op.
	if srcType == targetType {
		return operand
	}

	// Bool to float (must come before integer-to-float since i1 starts with "i").
	if srcType == "i1" && targetType == "double" {
		cg.writef("  %s = uitofp i1 %s to double\n", reg, operand)
		return reg
	}
	// Float to bool (must come before float-to-integer since i1 starts with "i").
	if srcType == "double" && targetType == "i1" {
		cg.writef("  %s = fcmp one double %s, 0.0\n", reg, operand)
		return reg
	}
	// Integer to float.
	if strings.HasPrefix(srcType, "i") && targetType == "double" {
		cg.writef("  %s = sitofp %s %s to double\n", reg, srcType, operand)
		return reg
	}
	// Float to integer.
	if srcType == "double" && strings.HasPrefix(targetType, "i") {
		cg.writef("  %s = fptosi double %s to %s\n", reg, operand, targetType)
		return reg
	}
	// Bool (i1) to integer.
	if srcType == "i1" && targetType == "i32" {
		cg.writef("  %s = zext i1 %s to i32\n", reg, operand)
		return reg
	}
	// Integer to bool.
	if srcType == "i32" && targetType == "i1" {
		cg.writef("  %s = icmp ne i32 %s, 0\n", reg, operand)
		return reg
	}
	// Pointer to pointer (bitcast).
	if strings.HasSuffix(srcType, "*") && strings.HasSuffix(targetType, "*") {
		cg.writef("  %s = bitcast %s %s to %s\n", reg, srcType, operand, targetType)
		return reg
	}
	// Integer to pointer.
	if (srcType == "i32" || srcType == "i64" || srcType == "u64" || srcType == "uint64") && strings.HasSuffix(targetType, "*") {
		cg.writef("  %s = inttoptr %s %s to %s\n", reg, srcType, operand, targetType)
		return reg
	}
	// Pointer to integer.
	if strings.HasSuffix(srcType, "*") && (targetType == "i32" || targetType == "i64" || targetType == "u64" || targetType == "uint64") {
		cg.writef("  %s = ptrtoint %s %s to %s\n", reg, srcType, operand, targetType)
		return reg
	}
	// Integer truncation / extension between sizes.
	if strings.HasPrefix(srcType, "i") && strings.HasPrefix(targetType, "i") {
		srcBits := strings.TrimPrefix(srcType, "i")
		targetBits := strings.TrimPrefix(targetType, "i")
		if srcBits != "" && targetBits != "" {
			if srcBits == targetBits {
				return operand
			}
			sb, _ := strconv.Atoi(srcBits)
			tb, _ := strconv.Atoi(targetBits)
			if sb > tb {
				cg.writef("  %s = trunc %s %s to %s\n", reg, srcType, operand, targetType)
			} else {
				cg.writef("  %s = zext %s %s to %s\n", reg, srcType, operand, targetType)
			}
			return reg
		}
	}
	// Fallback: just return operand.
	return operand
}

// emitArrayLiteral emits a fixed-size array allocated on the stack.
//
// Strategy:
//  1. alloca [N x elemLLType]     -- allocate array storage
//  2. store each element via getelementptr
//  3. return pointer to element 0 (elemLLType*)
//
// This gives a C-compatible array pointer that works with index expressions.
func (cg *Codegen) emitArrayLiteral(e *ast.ArrayLiteral) string {
	// Check for spread elements.
	hasSpread := false
	for _, el := range e.Elements {
		if _, ok := el.(*ast.SpreadExpr); ok {
			hasSpread = true
			break
		}
	}

	if !hasSpread {
		lenVal := len(e.Elements)
		if lenVal == 0 {
			return "null"
		}
		origElemLLType := cg.exprLLType(e.Elements[0])
		elemLLType := origElemLLType
		storePtr := false
		// Skink arrays of structs store pointers to the struct.
		if strings.HasPrefix(origElemLLType, "%struct.") && !strings.HasSuffix(origElemLLType, "*") {
			elemLLType = origElemLLType + "*"
			storePtr = true
		}
		elemSize := cg.emitElemSize(elemLLType)
		totalBytesReg := cg.nextReg()
		cg.writef("  %s = mul i32 %d, %s\n", totalBytesReg, lenVal, elemSize)
		totalBytesWithPrefix := cg.nextReg()
		cg.writef("  %s = add i32 %s, 8\n", totalBytesWithPrefix, totalBytesReg)
		totalBytes64 := cg.nextReg()
		cg.writef("  %s = zext i32 %s to i64\n", totalBytes64, totalBytesWithPrefix)
		rawReg := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawReg, totalBytes64)
		lenPtr := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to i64*\n", lenPtr, rawReg)
		cg.writef("  store i64 %d, i64* %s\n", lenVal, lenPtr)
		elemStart := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", elemStart, rawReg)
		typedPtr := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to %s*\n", typedPtr, elemStart, elemLLType)
		for i, elem := range e.Elements {
			elemReg := cg.emitExpression(elem)
			if cg.exprLLType(elem) == "i1" && elemLLType == "i32" {
				zextReg := cg.nextReg()
				cg.writef("  %s = zext i1 %s to i32\n", zextReg, elemReg)
				elemReg = zextReg
			}
			// For struct values, heap-allocate a copy so the array stores a pointer.
			if storePtr && !strings.HasSuffix(cg.exprLLType(elem), "*") {
				sizeReg := cg.nextReg()
				cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", sizeReg, origElemLLType, origElemLLType)
				sizeReg2 := cg.nextReg()
				cg.writef("  %s = ptrtoint %s* %s to i64\n", sizeReg2, origElemLLType, sizeReg)
				rawReg2 := cg.nextReg()
				cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawReg2, sizeReg2)
				ptrReg := cg.nextReg()
				cg.writef("  %s = bitcast i8* %s to %s*\n", ptrReg, rawReg2, origElemLLType)
				cg.writef("  store %s %s, %s* %s\n", origElemLLType, elemReg, origElemLLType, ptrReg)
				elemReg = ptrReg
			}
			gep := cg.nextReg()
			cg.writef("  %s = getelementptr inbounds %s, %s* %s, i64 %d\n",
				gep, elemLLType, elemLLType, typedPtr, i)
			cg.writef("  store %s %s, %s* %s\n", elemLLType, elemReg, elemLLType, gep)
		}
		return typedPtr
	}

	// Spread path: compute total size, allocate, copy fixed and spread elements.
	elemLLType := "i32"
	for _, el := range e.Elements {
		if _, ok := el.(*ast.SpreadExpr); !ok {
			elemLLType = cg.exprLLType(el)
			break
		}
	}

	// Compute total length.
	fixedCount := 0
	spreadInfos := []struct {
		expr ast.Expression
		len  int
	}{}
	for _, el := range e.Elements {
		if spread, ok := el.(*ast.SpreadExpr); ok {
			arrLen, _ := cg.inferArrayInfo(spread.Operand)
			if arrLen <= 0 {
				cg.Errorf("cannot determine length of spread array")
				return "null"
			}
			spreadInfos = append(spreadInfos, struct {
				expr ast.Expression
				len  int
			}{spread.Operand, arrLen})
		} else {
			fixedCount++
		}
	}
	totalLen := fixedCount
	for _, si := range spreadInfos {
		totalLen += si.len
	}

	// Allocate result array on heap with length prefix.
	elemSize := cg.emitElemSize(elemLLType)
	totalBytesReg := cg.nextReg()
	cg.writef("  %s = mul i32 %d, %s\n", totalBytesReg, totalLen, elemSize)
	totalBytesWithPrefix := cg.nextReg()
	cg.writef("  %s = add i32 %s, 8\n", totalBytesWithPrefix, totalBytesReg)
	totalBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", totalBytes64, totalBytesWithPrefix)
	rawReg := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawReg, totalBytes64)
	lenPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", lenPtr, rawReg)
	cg.writef("  store i64 %d, i64* %s\n", totalLen, lenPtr)
	elemStart := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", elemStart, rawReg)
	typedPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", typedPtr, elemStart, elemLLType)

	// Copy elements.
	offset := 0
	spreadIdx := 0
	for _, el := range e.Elements {
		if spread, ok := el.(*ast.SpreadExpr); ok {
			si := spreadInfos[spreadIdx]
			spreadIdx++
			srcReg := cg.emitExpression(spread.Operand)
			// Emit copy loop: for i = 0 to si.len
			loopVar := cg.nextReg()
			cg.writef("  %s = alloca i32\n", loopVar)
			cg.writef("  store i32 0, i32* %s\n", loopVar)
			condLabel := cg.nextLabel()
			bodyLabel := cg.nextLabel()
			postLabel := cg.nextLabel()
			endLabel := cg.nextLabel()
			cg.writef("  br label %%%s\n", condLabel)
			// cond
			cg.writeln(condLabel + ":")
			iLoad := cg.nextReg()
			cg.writef("  %s = load i32, i32* %s\n", iLoad, loopVar)
			cmpReg := cg.nextReg()
			cg.writef("  %s = icmp slt i32 %s, %d\n", cmpReg, iLoad, si.len)
			cg.writef("  br i1 %s, label %%%s, label %%%s\n", cmpReg, bodyLabel, endLabel)
			// body
			cg.writeln(bodyLabel + ":")
			cg.terminated = false
			srcGep := cg.nextReg()
			cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
				srcGep, elemLLType, elemLLType, srcReg, iLoad)
			valReg := cg.nextReg()
			cg.writef("  %s = load %s, %s* %s\n", valReg, elemLLType, elemLLType, srcGep)
			dstGep := cg.nextReg()
			dstIdx := cg.nextReg()
			cg.writef("  %s = add i32 %d, %s\n", dstIdx, offset, iLoad)
			cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
				dstGep, elemLLType, elemLLType, typedPtr, dstIdx)
			cg.writef("  store %s %s, %s* %s\n", elemLLType, valReg, elemLLType, dstGep)
			cg.writef("  br label %%%s\n", postLabel)
			// post
			cg.writeln(postLabel + ":")
			cg.terminated = false
			iLoad2 := cg.nextReg()
			cg.writef("  %s = load i32, i32* %s\n", iLoad2, loopVar)
			nextI := cg.nextReg()
			cg.writef("  %s = add i32 %s, 1\n", nextI, iLoad2)
			cg.writef("  store i32 %s, i32* %s\n", nextI, loopVar)
			cg.writef("  br label %%%s\n", condLabel)
			// end
			cg.writeln(endLabel + ":")
			cg.terminated = false
			offset += si.len
		} else {
			elemReg := cg.emitExpression(el)
			if cg.exprLLType(el) == "i1" && elemLLType == "i32" {
				zextReg := cg.nextReg()
				cg.writef("  %s = zext i1 %s to i32\n", zextReg, elemReg)
				elemReg = zextReg
			}
			gep := cg.nextReg()
			cg.writef("  %s = getelementptr inbounds %s, %s* %s, i64 %d\n",
				gep, elemLLType, elemLLType, typedPtr, offset)
			cg.writef("  store %s %s, %s* %s\n", elemLLType, elemReg, elemLLType, gep)
			offset++
		}
	}

	return typedPtr
}

// emitMapTypeDecl emits a map struct type declaration on demand.
func (cg *Codegen) emitMapTypeDecl(name, keyLL, valLL string) {
	if cg.mapTypes[name] {
		return
	}
	cg.mapTypes[name] = true
	cg.moduleHeader.WriteString(fmt.Sprintf("%%%s = type { %s*, %s*, i32 }\n", name, keyLL, valLL))
}

// emitMapLiteral emits a map literal as an RC-managed struct.
func (cg *Codegen) emitMapLiteral(e *ast.MapLiteral) string {
	if len(e.Pairs) == 0 {
		return "null"
	}
	keyLL := cg.exprLLType(e.Pairs[0].Key)
	valLL := cg.exprLLType(e.Pairs[0].Value)
	mtName := mapTypeName(keyLL, valLL)
	cg.emitMapTypeDecl(mtName, keyLL, valLL)
	mapStructType := fmt.Sprintf("%%%s", mtName)

	n := len(e.Pairs)
	keyElemSize := cg.emitElemSize(keyLL)
	valElemSize := cg.emitElemSize(valLL)

	// Allocate keys array via RC.
	keysBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %d, %s\n", keysBytes, n, keyElemSize)
	keysBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", keysBytes64, keysBytes)
	keysRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", keysRaw, keysBytes64)
	keysTyped := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", keysTyped, keysRaw, keyLL)

	// Allocate values array via RC.
	valsBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %d, %s\n", valsBytes, n, valElemSize)
	valsBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", valsBytes64, valsBytes)
	valsRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", valsRaw, valsBytes64)
	valsTyped := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", valsTyped, valsRaw, valLL)

	for i, pair := range e.Pairs {
		keyReg := cg.emitExpression(pair.Key)
		valReg := cg.emitExpression(pair.Value)

		keyGep := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i64 %d\n",
			keyGep, keyLL, keyLL, keysTyped, i)
		cg.writef("  store %s %s, %s* %s\n", keyLL, keyReg, keyLL, keyGep)

		valGep := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i64 %d\n",
			valGep, valLL, valLL, valsTyped, i)
		cg.writef("  store %s %s, %s* %s\n", valLL, valReg, valLL, valGep)
	}

	// Allocate map struct via RC.
	structSizePtr := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", structSizePtr, mapStructType, mapStructType)
	structSize := cg.nextReg()
	cg.writef("  %s = ptrtoint %s* %s to i64\n", structSize, mapStructType, structSizePtr)
	mapRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", mapRaw, structSize)
	mapPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", mapPtr, mapRaw, mapStructType)

	// Store keys pointer.
	keysField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n",
		keysField, mapStructType, mapStructType, mapPtr)
	cg.writef("  store %s* %s, %s** %s\n", keyLL, keysTyped, keyLL, keysField)

	// Store values pointer.
	valsField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n",
		valsField, mapStructType, mapStructType, mapPtr)
	cg.writef("  store %s* %s, %s** %s\n", valLL, valsTyped, valLL, valsField)

	// Store length.
	lenField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 2\n",
		lenField, mapStructType, mapStructType, mapPtr)
	cg.writef("  store i32 %d, i32* %s\n", n, lenField)

	return mapPtr
}

// emitSetLiteral emits a set literal as an RC-managed struct.
// For now, only integer and string sets are supported.
func (cg *Codegen) emitSetLiteral(e *ast.SetLiteral) string {
	if len(e.Elements) == 0 {
		return "null"
	}

	elemType := cg.exprLLType(e.Elements[0])
	n := len(e.Elements)

	var elemRegs []string
	for _, el := range e.Elements {
		elemRegs = append(elemRegs, cg.emitExpression(el))
	}

	var setTypeName string
	var elemLLType string
	if elemType == "i32" {
		setTypeName = "%set.Int"
		elemLLType = "i32"
	} else if elemType == "i8*" {
		setTypeName = "%set.Str"
		elemLLType = "i8*"
	} else {
		setTypeName = "%set.Int"
		elemLLType = "i32"
	}

	// Allocate elements array via RC.
	elemSize := cg.emitElemSize(elemLLType)
	arrBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %d, %s\n", arrBytes, n, elemSize)
	arrBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", arrBytes64, arrBytes)
	arrRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", arrRaw, arrBytes64)
	arrTyped := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", arrTyped, arrRaw, elemLLType)

	for i, reg := range elemRegs {
		gep := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i64 %d\n",
			gep, elemLLType, elemLLType, arrTyped, i)
		cg.writef("  store %s %s, %s* %s\n", elemLLType, reg, elemLLType, gep)
	}

	// Allocate set struct via RC.
	structSizePtr := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", structSizePtr, setTypeName, setTypeName)
	structSize := cg.nextReg()
	cg.writef("  %s = ptrtoint %s* %s to i64\n", structSize, setTypeName, structSizePtr)
	setRaw := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", setRaw, structSize)
	setPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", setPtr, setRaw, setTypeName)

	// Store data pointer.
	dataField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n",
		dataField, setTypeName, setTypeName, setPtr)
	cg.writef("  store %s* %s, %s** %s\n", elemLLType, arrTyped, elemLLType, dataField)

	// Store length.
	lenField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n",
		lenField, setTypeName, setTypeName, setPtr)
	cg.writef("  store i32 %d, i32* %s\n", n, lenField)

	return setPtr
}

// emitSizeofExpr returns the compile-time size of a type in bytes.
func (cg *Codegen) emitSizeofExpr(e *ast.SizeofExpr) string {
	sz, _ := cg.typeSizeAndAlign(e.Type)
	return fmt.Sprintf("%d", sz)
}

// emitAlignofExpr returns the compile-time alignment of a type in bytes.
func (cg *Codegen) emitAlignofExpr(e *ast.AlignofExpr) string {
	_, al := cg.typeSizeAndAlign(e.Type)
	return fmt.Sprintf("%d", al)
}

// alignUp rounds offset up to the nearest multiple of align.
func alignUp(offset, align int) int {
	if align == 0 {
		return offset
	}
	return (offset + align - 1) & ^(align - 1)
}

// typeSizeAndAlign returns the size and alignment in bytes for an AST type.
func (cg *Codegen) typeSizeAndAlign(t ast.Type) (size int, align int) {
	if t == nil {
		return 0, 1
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		switch tt.Name {
		case "int", "uint", "int32", "uint32":
			return 4, 4
		case "int8", "uint8":
			return 1, 1
		case "int16", "uint16":
			return 2, 2
		case "int64", "uint64":
			return 8, 8
		case "float":
			return 8, 8
		case "bool":
			return 1, 1
		case "string", "bytes":
			return 8, 8
		case "void":
			return 0, 1
		case "error":
			return 16, 8
		default:
			// Struct lookup
			if _, ok := cg.structLayouts[tt.Name]; ok {
				size, align := cg.computeStructLayout(tt.Name)
				return size, align
			}
			return 4, 4 // fallback if not found
		}
	case *ast.PointerType:
		return 8, 8
	case *ast.ArrayType:
		return 8, 8
	case *ast.SetType, *ast.ChanType, *ast.TensorType:
		return 8, 8
	case *ast.FunctionType:
		return 8, 8
	case *ast.TupleType:
		totalSize := 0
		maxAlign := 1
		for _, subtype := range tt.Types {
			s, a := cg.typeSizeAndAlign(subtype)
			if a > maxAlign {
				maxAlign = a
			}
			totalSize = alignUp(totalSize, a)
			totalSize += s
		}
		totalSize = alignUp(totalSize, maxAlign)
		return totalSize, maxAlign
	}
	return 4, 4
}

// computeStructLayout returns the size and alignment of a struct type.
func (cg *Codegen) computeStructLayout(structName string) (int, int) {
	layout, ok := cg.structLayouts[structName]
	if !ok {
		return 4, 4 // fallback if unknown
	}
	if layout.size > 0 {
		return layout.size, layout.alignment
	}

	d, hasDecl := cg.structDecls[structName]
	if !hasDecl {
		return 4, 4
	}

	isPacked := false
	customAlign := 0
	for _, attr := range d.Attributes {
		if attr == "packed" {
			isPacked = true
		} else if strings.HasPrefix(attr, "align(") && strings.HasSuffix(attr, ")") {
			inside := attr[len("align(") : len(attr)-1]
			if val, err := strconv.Atoi(inside); err == nil {
				customAlign = val
			}
		}
	}

	var offsets []int
	var structAlign int = 1
	var size int = 0

	if isPacked {
		structAlign = 1
		for _, f := range d.Fields {
			offsets = append(offsets, size)
			var fs int
			if f.BitWidth != nil {
				fs = (*f.BitWidth + 7) / 8
			} else {
				fs, _ = cg.typeSizeAndAlign(f.Type)
			}
			size += fs
		}
		if customAlign > 0 {
			structAlign = customAlign
		}
		size = alignUp(size, structAlign)
	} else {
		maxAlign := 1
		for _, f := range d.Fields {
			var fs, fa int
			if f.BitWidth != nil {
				fs = (*f.BitWidth + 7) / 8
				fa = 1
			} else {
				fs, fa = cg.typeSizeAndAlign(f.Type)
			}
			if fa > maxAlign {
				maxAlign = fa
			}
			size = alignUp(size, fa)
			offsets = append(offsets, size)
			size += fs
		}
		if customAlign > 0 {
			structAlign = customAlign
		} else {
			structAlign = maxAlign
		}
		size = alignUp(size, structAlign)
	}

	layout.size = size
	layout.alignment = structAlign
	layout.offsets = offsets
	cg.structLayouts[structName] = layout

	return size, structAlign
}

// typeName returns the string name of an AST type.
func typeName(t ast.Type) string {
	if nt, ok := t.(*ast.NamedType); ok {
		return nt.Name
	}
	return ""
}

// emitMinExpr returns the compile-time minimum value for a numeric type.
func (cg *Codegen) emitMinExpr(e *ast.MinExpr) string {
	switch typeName(e.Type) {
	case "bool":
		return "0"
	case "int8":
		return "-128"
	case "int16":
		return "-32768"
	case "int32":
		return "-2147483648"
	case "int64":
		return "-9223372036854775808"
	case "int":
		return "-2147483648"
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return "0"
	case "float":
		return "0xFFF0000000000000" // -inf
	}
	return "0"
}

// emitMaxExpr returns the compile-time maximum value for a numeric type.
func (cg *Codegen) emitMaxExpr(e *ast.MaxExpr) string {
	switch typeName(e.Type) {
	case "bool":
		return "1"
	case "int8":
		return "127"
	case "int16":
		return "32767"
	case "int32":
		return "2147483647"
	case "int64":
		return "9223372036854775807"
	case "int":
		return "2147483647"
	case "uint", "uint32":
		return "4294967295"
	case "uint8":
		return "255"
	case "uint16":
		return "65535"
	case "uint64":
		return "18446744073709551615"
	case "float":
		return "0x7FF0000000000000" // +inf
	}
	return "0"
}

// emitMakeExpr emits make(Type) — allocates an empty set, map, or chan.
func (cg *Codegen) emitMakeExpr(e *ast.MakeExpr) string {
	lt := llvmType(e.Type)
	if strings.HasPrefix(lt, "%set.") {
		// Empty set: data=null, len=0.
		var setTypeName string
		if lt == "%set.Str*" {
			setTypeName = "%set.Str"
		} else {
			setTypeName = "%set.Int"
		}
		setReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", setReg, setTypeName)
		// Store null data pointer.
		dataField := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n",
			dataField, setTypeName, setTypeName, setReg)
		if setTypeName == "%set.Str" {
			cg.writef("  store i8** null, i8*** %s\n", dataField)
		} else {
			cg.writef("  store i32* null, i32** %s\n", dataField)
		}
		// Store zero length.
		lenField := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n",
			lenField, setTypeName, setTypeName, setReg)
		cg.writef("  store i32 0, i32* %s\n", lenField)
		return setReg
	}
	if strings.HasPrefix(lt, "%map_") && strings.HasSuffix(lt, "*") {
		// Empty map: keys=null, vals=null, len=0.
		mapType := strings.TrimSuffix(lt, "*")
		keyLL, valLL, _ := parseMapType(lt)
		cg.emitMapTypeDecl(mapTypeName(keyLL, valLL), keyLL, valLL)
		mapReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", mapReg, mapType)
		// keys
		kf := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n", kf, mapType, mapType, mapReg)
		cg.writef("  store %s* null, %s** %s\n", keyLL, keyLL, kf)
		// vals
		vf := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n", vf, mapType, mapType, mapReg)
		cg.writef("  store %s* null, %s** %s\n", valLL, valLL, vf)
		// len
		lf := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 2\n", lf, mapType, mapType, mapReg)
		cg.writef("  store i32 0, i32* %s\n", lf)
		return mapReg
	}
	// Channel: create via runtime.
	if chType, ok := e.Type.(*ast.ChanType); ok {
		elemLL := llvmType(chType.Elem)
		elemSize := 4
		switch elemLL {
		case "i1", "i8":
			elemSize = 1
		case "i16":
			elemSize = 2
		case "i64", "double", "i8*":
			elemSize = 8
		}
		capReg := "0"
		if e.Capacity != nil {
			capReg = cg.emitExpression(e.Capacity)
		}
		reg := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_chan_make(i32 %d, i32 %s)\n", reg, elemSize, capReg)
		return reg
	}
	// Array: make([]T, capacity) — allocate backing array via runtime.
	if arrType, ok := e.Type.(*ast.ArrayType); ok {
		elemLL := llvmType(arrType.Elem)
		if strings.HasPrefix(elemLL, "%struct.") && !strings.HasSuffix(elemLL, "*") {
			elemLL = elemLL + "*"
		}
		capReg := "0"
		if e.Capacity != nil {
			capReg = cg.emitExpression(e.Capacity)
		}
		elemSize := cg.emitElemSize(elemLL)
		totalElemBytes := cg.nextReg()
		cg.writef("  %s = mul i32 %s, %s\n", totalElemBytes, capReg, elemSize)
		totalBytesWithPrefix := cg.nextReg()
		cg.writef("  %s = add i32 %s, 8\n", totalBytesWithPrefix, totalElemBytes)
		totalBytes64 := cg.nextReg()
		cg.writef("  %s = zext i32 %s to i64\n", totalBytes64, totalBytesWithPrefix)
		rawReg := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawReg, totalBytes64)
		lenPtr := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to i64*\n", lenPtr, rawReg)
		capReg64 := cg.nextReg()
		cg.writef("  %s = zext i32 %s to i64\n", capReg64, capReg)
		cg.writef("  store i64 %s, i64* %s\n", capReg64, lenPtr)
		elemStart := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", elemStart, rawReg)
		typedPtr := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to %s*\n", typedPtr, elemStart, elemLL)
		return typedPtr
	}
	// Generic pointer alloca fallback.
	if strings.HasSuffix(lt, "*") && !strings.HasPrefix(lt, "%set.") && !strings.HasPrefix(lt, "%map.") && !strings.HasPrefix(lt, "%struct.") {
		chanReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", chanReg, lt)
		return chanReg
	}
	// Fallback: return null for unknown make types.
	cg.Errorf("make() unsupported for type %s", lt)
	return "null"
}

// parseMapType extracts key and value LLVM type names from a map type string like "%map_i8Ptr__i32*".
func parseMapType(llType string) (keyLL, valLL string, ok bool) {
	if !strings.HasPrefix(llType, "%map_") || !strings.HasSuffix(llType, "*") {
		return "", "", false
	}
	inner := strings.TrimPrefix(llType, "%map_")
	inner = strings.TrimSuffix(inner, "*")
	parts := strings.Split(inner, "__")
	if len(parts) != 2 {
		return "", "", false
	}
	desanitize := func(s string) string {
		// Reverse the Ptr -> * sanitization used by mapTypeName.
		// Common pointer type patterns: i8Ptr, i32Ptr, i64Ptr, i1Ptr, floatPtr, doublePtr, %struct.*Ptr
		if strings.HasSuffix(s, "Ptr") {
			s = s[:len(s)-3] + "*"
		}
		return s
	}
	return desanitize(parts[0]), desanitize(parts[1]), true
}

// emitIndexExpr emits an array subscript, map lookup, or string indexing operation.
//
//	Array:   gep = getelementptr i32, i32* <base>, i32 <index>
//	         val = load i32, i32* gep
//	String:  gep = getelementptr i8, i8* <base>, i32 <index>
//	         val = load i8, i8* gep
//	         res = zext i8 val to i32
//	Map:     linear search through keys using strcmp or icmp eq
func (cg *Codegen) emitIndexExpr(e *ast.IndexExpr) string {
	baseReg := cg.emitExpression(e.Left)
	isString := cg.exprLLType(e.Left) == "i8*"
	leftLL := cg.exprLLType(e.Left)
	keyLL, valLL, isMap := parseMapType(leftLL)

	// Handle ^n from-end indexing.
	var indexReg string
	if fe, ok := e.Index.(*ast.FromEndIndexExpr); ok {
		operandReg := cg.emitExpression(fe.Operand)
		if isString {
			lenReg := cg.nextReg()
			cg.writef("  %s = call i32 @strlen(i8* %s)\n", lenReg, baseReg)
			subReg := cg.nextReg()
			cg.writef("  %s = sub i32 %s, %s\n", subReg, lenReg, operandReg)
			indexReg = subReg
		} else {
			arrLen, _ := cg.inferArrayInfo(e.Left)
			if arrLen > 0 {
				lenReg := cg.nextReg()
				cg.writef("  %s = add i32 0, %d\n", lenReg, arrLen)
				subReg := cg.nextReg()
				cg.writef("  %s = sub i32 %s, %s\n", subReg, lenReg, operandReg)
				indexReg = subReg
			} else {
				// Fallback: just use operand (not correct for dynamic arrays).
				indexReg = operandReg
			}
		}
	} else {
		indexReg = cg.emitExpression(e.Index)
	}

	if isMap {
		return cg.emitMapIndex(baseReg, indexReg, keyLL, valLL)
	}

	if isString {
		gep := cg.nextReg()
		cg.writef("  %s = getelementptr inbounds i8, i8* %s, i32 %s\n", gep, baseReg, indexReg)
		reg := cg.nextReg()
		cg.writef("  %s = load i8, i8* %s\n", reg, gep)
		zext := cg.nextReg()
		cg.writef("  %s = zext i8 %s to i32\n", zext, reg)
		return zext
	}

	leftType := cg.exprLLType(e.Left)
	elemType := "i32"
	if strings.HasSuffix(leftType, "*") {
		elemType = strings.TrimSuffix(leftType, "*")
	}
	// Skink arrays of structs store pointers to the struct, not inline struct values.
	if strings.HasPrefix(elemType, "%struct.") && !strings.HasSuffix(elemType, "*") {
		elemType = elemType + "*"
	}

	actualBase := baseReg
	if leftType != elemType+"*" {
		castReg := cg.nextReg()
		cg.writef("  %s = bitcast %s %s to %s*\n", castReg, leftType, baseReg, elemType)
		actualBase = castReg
	}

	gep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", gep, elemType, elemType, actualBase, indexReg)
	reg := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", reg, elemType, elemType, gep)
	return reg
}

// emitMapIndex emits a linear search through a map with the given key/value LLVM types.
func (cg *Codegen) emitMapIndex(mapReg, keyReg, keyLL, valLL string) string {
	mapType := "%" + mapTypeName(keyLL, valLL)
	resultAlloca := cg.nextReg()
	cg.writef("  %s = alloca %s\n", resultAlloca, valLL)
	if valLL == "i8*" {
		// Missing string keys should return empty string, not null.
		emptyStr := cg.emitStringLiteral(&ast.StringLiteral{Value: ""})
		cg.writef("  store i8* %s, i8** %s\n", emptyStr, resultAlloca)
	} else if strings.HasSuffix(valLL, "*") || strings.HasPrefix(valLL, "%struct.") || strings.HasPrefix(valLL, "%map_") || strings.HasPrefix(valLL, "%set.") {
		cg.writef("  store %s null, %s* %s\n", valLL, valLL, resultAlloca)
	} else {
		cg.writef("  store %s 0, %s* %s\n", valLL, valLL, resultAlloca)
	}

	// Load keys, values, and len from map struct
	keysField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n", keysField, mapType, mapType, mapReg)
	keysPtr := cg.nextReg()
	cg.writef("  %s = load %s*, %s** %s\n", keysPtr, keyLL, keyLL, keysField)

	valsField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n", valsField, mapType, mapType, mapReg)
	valsPtr := cg.nextReg()
	cg.writef("  %s = load %s*, %s** %s\n", valsPtr, valLL, valLL, valsField)

	lenField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 2\n", lenField, mapType, mapType, mapReg)
	lenReg := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", lenReg, lenField)

	// Loop labels
	initLabel := cg.nextLabel()
	condLabel := cg.nextLabel()
	bodyLabel := cg.nextLabel()
	foundLabel := cg.nextLabel()
	endLabel := cg.nextLabel()

	// Initialize i = 0
	iAlloca := cg.nextReg()
	cg.writef("  %s = alloca i32\n", iAlloca)
	cg.writef("  store i32 0, i32* %s\n", iAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// cond: i < len
	cg.writeln(condLabel + ":")
	iVal := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", iVal, iAlloca)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, iVal, lenReg)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)

	// body: compare keys[i] with keyReg
	cg.writeln(bodyLabel + ":")
	keyGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", keyGep, keyLL, keyLL, keysPtr, iVal)
	curKey := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", curKey, keyLL, keyLL, keyGep)

	var match string
	if keyLL == "i8*" {
		cmp := cg.nextReg()
		cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmp, curKey, keyReg)
		match = cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, 0\n", match, cmp)
	} else {
		match = cg.nextReg()
		cg.writef("  %s = icmp eq %s %s, %s\n", match, keyLL, curKey, keyReg)
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", match, foundLabel, initLabel)

	// found: store values[i] into result, then jump to end
	cg.writeln(foundLabel + ":")
	valGep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", valGep, valLL, valLL, valsPtr, iVal)
	valReg := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", valReg, valLL, valLL, valGep)
	cg.writef("  store %s %s, %s* %s\n", valLL, valReg, valLL, resultAlloca)
	cg.writef("  br label %%%s\n", endLabel)

	// init: i++
	cg.writeln(initLabel + ":")
	nextI := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextI, iVal)
	cg.writef("  store i32 %s, i32* %s\n", nextI, iAlloca)
	cg.writef("  br label %%%s\n", condLabel)

	// end: load result
	cg.terminated = false
	cg.writeln(endLabel + ":")
	result := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", result, valLL, valLL, resultAlloca)
	return result
}

// emitSliceExpr emits a slice expression arr[start:end], arr[..end], arr[start..], or arr[..].
// Returns a pointer to the start element.
func (cg *Codegen) emitSliceExpr(e *ast.SliceExpr) string {
	baseReg := cg.emitExpression(e.Left)
	leftType := cg.exprLLType(e.Left)

	// Determine element type from left type.
	elemType := "i32"
	if leftType == "i8*" {
		elemType = "i8"
	} else if strings.HasSuffix(leftType, "*") {
		elemType = strings.TrimSuffix(leftType, "*")
	}

	var startReg string
	if e.Start != nil {
		startReg = cg.emitExpression(e.Start)
	} else {
		startReg = "0"
	}

	// For now, end is only checked for type validity; the slice is just a pointer offset.
	if e.End != nil {
		_ = cg.emitExpression(e.End)
	}

	gep := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n", gep, elemType, elemType, baseReg, startReg)
	return gep
}

// emitRangeExpr emits a range expression start..end as a { i32, i32 } aggregate.
func (cg *Codegen) emitRangeExpr(e *ast.RangeExpr) string {
	startReg := cg.emitExpression(e.Start)
	endReg := cg.emitExpression(e.End)
	// Build aggregate { i32, i32 } using insertvalue.
	agg := cg.nextReg()
	cg.writef("  %s = insertvalue { i32, i32 } undef, i32 %s, 0\n", agg, startReg)
	agg2 := cg.nextReg()
	cg.writef("  %s = insertvalue { i32, i32 } %s, i32 %s, 1\n", agg2, agg, endReg)
	return agg2
}

// emitFieldAccessPtr emits the GEP pointer for a struct field access
// without loading the value. It returns the pointer register and the
// LLVM type of the field. The ok return is false for non-struct accesses.
func (cg *Codegen) emitFieldAccessPtr(e *ast.FieldAccessExpr) (string, string, bool) {
	if leftASTType := cg.exprASTType(e.Left); leftASTType != nil {
		if nt, ok := leftASTType.(*ast.NamedType); ok {
			if nt.Name == "Reader" || nt.Name == "Writer" || nt.Name == "reader.Reader" || nt.Name == "writer.Writer" {
				return "null", "i8*", true
			}
		}
	}
	// Module-qualified constant or variable access: not addressable.
	if id, ok := e.Left.(*ast.Identifier); ok {
		alloca, _ := cg.resolveVar(id.Value)
		_, isGlobal := cg.globalVarTypes[id.Value]
		if alloca == "" && !isGlobal {
			return "", "", false
		}
	}

	leftType := cg.exprLLType(e.Left)

	// Error type field access: not addressable.
	if leftType == "{ i32, i8* }" {
		return "", "", false
	}

	// Tensor transpose: not addressable.
	if leftType == "i8*" && e.Field == "T" {
		return "", "", false
	}

	structName := ""
	if strings.HasPrefix(leftType, "%struct.") {
		structName = strings.TrimPrefix(leftType, "%struct.")
		if idx := strings.Index(structName, "*"); idx > 0 {
			structName = structName[:idx]
		}
	}
	if structName == "" {
		cg.Errorf("field access on non-struct type")
		return "", "", false
	}
	layout, ok := cg.structLayouts[structName]
	if !ok {
		cg.Errorf("unknown struct type %q in field access", structName)
		return "", "", false
	}
	fieldIdx := -1
	for i, f := range layout.fields {
		if f == e.Field {
			fieldIdx = i
			break
		}
	}
	// Handle promoted fields via embedded structs.
	if fieldIdx == -1 {
		decl, ok := cg.structDecls[structName]
		if !ok {
			cg.Errorf("struct %q has no field %q", structName, e.Field)
			return "", "", false
		}
		for i, f := range decl.Fields {
			if !f.Embedded {
				continue
			}
			embeddedName := ""
			if nt, ok := f.Type.(*ast.NamedType); ok {
				embeddedName = nt.Name
			}
			if embeddedName == "" {
				continue
			}
			embeddedDecl, ok := cg.structDecls[embeddedName]
			if !ok {
				continue
			}
			for _, ef := range embeddedDecl.Fields {
				if ef.Name == e.Field {
					objReg := cg.emitExpression(e.Left)
					structLLType := "%struct." + strings.ReplaceAll(structName, ".", "_")
					if !strings.HasSuffix(leftType, "*") {
						tmpReg := cg.nextReg()
						cg.writef("  %s = alloca %s\n", tmpReg, structLLType)
						cg.writef("  store %s %s, %s* %s\n", structLLType, objReg, structLLType, tmpReg)
						objReg = tmpReg
					}
					embedPtr := cg.nextReg()
					cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
						embedPtr, structLLType, structLLType, objReg, i)
					embeddedLL := "%struct." + strings.ReplaceAll(embeddedName, ".", "_")
					for j, ef2 := range embeddedDecl.Fields {
						if ef2.Name == e.Field {
							fieldPtr := cg.nextReg()
							cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
								fieldPtr, embeddedLL, embeddedLL, embedPtr, j)
							fieldType := llvmType(ef2.Type)
							return fieldPtr, fieldType, true
						}
					}
				}
			}
		}
		cg.Errorf("struct %q has no field %q", structName, e.Field)
		return "", "", false
	}
	objReg := cg.emitExpression(e.Left)
	structLLType := "%struct." + strings.ReplaceAll(structName, ".", "_")
	if !strings.HasSuffix(leftType, "*") {
		tmpReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", tmpReg, structLLType)
		cg.writef("  store %s %s, %s* %s\n", structLLType, objReg, structLLType, tmpReg)
		objReg = tmpReg
	}
	ptrReg := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
		ptrReg, structLLType, structLLType, objReg, fieldIdx)
	fieldType := layout.fieldTypes[fieldIdx]
	return ptrReg, fieldType, true
}

// emitFieldAccessExpr emits a struct field access.
//
//	%field_ptr = getelementptr %struct.T, %struct.T* %obj, i32 0, i32 <idx>
//	%field_val = load <type>, <type>* %field_ptr   (for primitive fields)
//	return %field_ptr                               (for struct fields)
func (cg *Codegen) emitFieldAccessExpr(e *ast.FieldAccessExpr) string {
	if leftASTType := cg.exprASTType(e.Left); leftASTType != nil {
		if nt, ok := leftASTType.(*ast.NamedType); ok {
			if nt.Name == "Reader" || nt.Name == "Writer" || nt.Name == "reader.Reader" || nt.Name == "writer.Writer" {
				return "null"
			}
		}
	}
	// Module-qualified constant or variable access: module.constName
	if id, ok := e.Left.(*ast.Identifier); ok {
		alloca, _ := cg.resolveVar(id.Value)
		_, isGlobal := cg.globalVarTypes[id.Value]
		if alloca == "" && !isGlobal {
			realModuleName := cg.resolveImportAlias(id.Value)
			candidates := []string{realModuleName + "." + e.Field, id.Value + "." + e.Field}
			for _, fqName := range candidates {
				if val, ok := cg.consts[fqName]; ok {
					return cg.emitExpression(val)
				}
				if lt, ok := cg.globalVarTypes[fqName]; ok && lt != "" {
					reg := cg.nextReg()
					cg.writef("  %s = load %s, %s* @%s\n", reg, lt, lt, fqName)
					return reg
				}
			}
		}
	}

	// Tensor transpose: A.T
	if leftType := cg.exprLLType(e.Left); leftType == "i8*" && e.Field == "T" {
		tensorReg := cg.emitExpression(e.Left)
		reg := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_tensor_transpose(i8* %s)\n", reg, tensorReg)
		return reg
	}

	ptrReg, fieldType, ok := cg.emitFieldAccessPtr(e)
	if !ok {
		return "0"
	}
	if strings.HasPrefix(fieldType, "%struct.") && !strings.HasSuffix(fieldType, "*") {
		// For struct fields, return the pointer (struct is passed by reference).
		return ptrReg
	}
	// For primitive fields, load the value.
	loadReg := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", loadReg, fieldType, fieldType, ptrReg)
	// Zero-extend small integer types (bitfields like i4, i8, i16) to i32 for arithmetic.
	// i1 (bool) is kept as-is since br expects i1.
	if fieldType != "i1" && fieldType != "i32" && fieldType != "i64" && strings.HasPrefix(fieldType, "i") && !strings.HasSuffix(fieldType, "*") {
		zextReg := cg.nextReg()
		cg.writef("  %s = zext %s %s to i32\n", zextReg, fieldType, loadReg)
		return zextReg
	}
	return loadReg
}

// emitStructInitExpr emits a struct instantiation on the stack.
//
//	%ptr = alloca %struct.T
//	store each field via getelementptr
//	return %struct.T* (or %ptr)
func (cg *Codegen) emitStructInitExpr(e *ast.StructInitExpr) string {
	layout, ok := cg.structLayouts[e.Type]
	if !ok {
		cg.Errorf("unknown struct type %q in init", e.Type)
		return "0"
	}
	structName := strings.ReplaceAll(e.Type, ".", "_")
	structLLType := "%struct." + structName
	sizePtr := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", sizePtr, structLLType, structLLType)
	sizeReg := cg.nextReg()
	cg.writef("  %s = ptrtoint %s* %s to i64\n", sizeReg, structLLType, sizePtr)
	rawPtr := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawPtr, sizeReg)
	ptrReg := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", ptrReg, rawPtr, structLLType)
	for i, fieldName := range layout.fields {
		if val, ok := e.Fields[fieldName]; ok {
			valReg := cg.emitExpression(val)
			fieldType := layout.fieldTypes[i]
			fieldPtr := cg.nextReg()
			cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
				fieldPtr, structLLType, structLLType, ptrReg, i)
			if strings.HasPrefix(fieldType, "%struct.") && !strings.HasSuffix(fieldType, "*") {
				valLLType := cg.exprLLType(val)
				if strings.HasSuffix(valLLType, "*") {
					// valReg is a pointer, load the value first.
					loadedReg := cg.nextReg()
					cg.writef("  %s = load %s, %s* %s\n", loadedReg, fieldType, fieldType, valReg)
					valReg = loadedReg
				}
				cg.writef("  store %s %s, %s* %s\n", fieldType, valReg, fieldType, fieldPtr)
			} else {
				valType := cg.exprLLType(val)
				if fieldType == "i64" && valType == "i32" {
					zextReg := cg.nextReg()
					cg.writef("  %s = zext i32 %s to i64\n", zextReg, valReg)
					valReg = zextReg
					valType = "i64"
				}
				cg.writef("  store %s %s, %s* %s\n", fieldType, valReg, fieldType, fieldPtr)
			}
		}
	}
	// Handle promoted fields from embedded structs.
	for name, val := range e.Fields {
		found := false
		for _, fn := range layout.fields {
			if fn == name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		decl := cg.structDecls[e.Type]
		if decl == nil {
			continue
		}
		for embedIdx, f := range decl.Fields {
			if !f.Embedded {
				continue
			}
			nt, ok := f.Type.(*ast.NamedType)
			if !ok {
				continue
			}
			embedDecl := cg.structDecls[nt.Name]
			if embedDecl == nil {
				continue
			}
			for promIdx, ef := range embedDecl.Fields {
				if ef.Name != name {
					continue
				}
				valReg := cg.emitExpression(val)
				fieldType := llvmType(ef.Type)
				// GEP to embedded field.
				embedPtr := cg.nextReg()
				cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
					embedPtr, structLLType, structLLType, ptrReg, embedIdx)
				// GEP to promoted field within embedded struct.
				fieldPtr := cg.nextReg()
				embedLL := "%struct." + strings.ReplaceAll(nt.Name, ".", "_")
				cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 %d\n",
					fieldPtr, embedLL, embedLL, embedPtr, promIdx)
				if strings.HasPrefix(fieldType, "%struct.") && !strings.HasSuffix(fieldType, "*") {
					valLLType := cg.exprLLType(val)
					if strings.HasSuffix(valLLType, "*") {
						loadedReg := cg.nextReg()
						cg.writef("  %s = load %s, %s* %s\n", loadedReg, fieldType, fieldType, valReg)
						valReg = loadedReg
					}
				}
				cg.writef("  store %s %s, %s* %s\n", fieldType, valReg, fieldType, fieldPtr)
				break
			}
		}
	}
	return ptrReg
}

// emitStringLiteral emits a global string constant and returns a pointer
// to its first character (i8*).
func escapeLLVMString(strVal string) (string, int) {
	var resolvedBytes []byte
	for i := 0; i < len(strVal); i++ {
		if strVal[i] == '\\' && i+1 < len(strVal) {
			escapedChar := strVal[i+1]
			switch escapedChar {
			case 'n':
				resolvedBytes = append(resolvedBytes, '\n')
			case 't':
				resolvedBytes = append(resolvedBytes, '\t')
			case 'r':
				resolvedBytes = append(resolvedBytes, '\r')
			case '\\':
				resolvedBytes = append(resolvedBytes, '\\')
			case '"':
				resolvedBytes = append(resolvedBytes, '"')
			default:
				resolvedBytes = append(resolvedBytes, strVal[i], escapedChar)
			}
			i++
		} else {
			resolvedBytes = append(resolvedBytes, strVal[i])
		}
	}
	var escapedBuilder strings.Builder
	for _, b := range resolvedBytes {
		if b >= 32 && b <= 126 && b != '\\' && b != '"' {
			escapedBuilder.WriteByte(b)
		} else {
			escapedBuilder.WriteString(fmt.Sprintf("\\%02X", b))
		}
	}
	return escapedBuilder.String(), len(resolvedBytes) + 1
}

// safeStringPtr ensures a string pointer is safe for strcmp by replacing a
// runtime null pointer with a pointer to an empty string constant.
func (cg *Codegen) safeStringPtr(reg string) string {
	if reg == "null" {
		return cg.emitStringLiteral(&ast.StringLiteral{Value: ""})
	}
	isNull := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", isNull, reg)
	empty := cg.emitStringLiteral(&ast.StringLiteral{Value: ""})
	safe := cg.nextReg()
	cg.writef("  %s = select i1 %s, i8* %s, i8* %s\n", safe, isNull, empty, reg)
	return safe
}

// Strategy:
//
//	@str.N = private constant [L x i8] c"...\00"
//	%r     = getelementptr inbounds [L x i8], [L x i8]* @str.N, i64 0, i64 0
//
// The string is null-terminated so it is compatible with C library
// functions like printf.
func (cg *Codegen) emitStringLiteral(s *ast.StringLiteral) string {
	// For now, emit as a global string constant and return pointer.
	// We need a unique global name.
	cg.strCounter++
	globalName := fmt.Sprintf("@str.%d", cg.strCounter)
	// Build escaped C string (backslash, double-quote, newline)
	escaped, length := escapeLLVMString(s.Value)
	// Emit the global constant at module level, not inside a function.
	cg.writeStringGlobal("%s = private constant [%d x i8] c\"%s\\00\"\n", globalName, length, escaped)
	reg := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds [%d x i8], [%d x i8]* %s, i64 0, i64 0\n",
		reg, length, length, globalName)
	return reg
}

// emitStringConcat emits a string concatenation using libc functions:
//
//	total = strlen(left) + strlen(right) + 1
//	buf   = Skink_rc_alloc(total)
//	strcpy(buf, left)
//	strcat(buf, right)
//	return buf
func (cg *Codegen) emitStringConcat(left, right string) string {
	safeLeft := cg.safeStringPtr(left)
	safeRight := cg.safeStringPtr(right)
	len1 := cg.nextReg()
	cg.writef("  %s = call i64 @strlen(i8* %s)\n", len1, safeLeft)
	len2 := cg.nextReg()
	cg.writef("  %s = call i64 @strlen(i8* %s)\n", len2, safeRight)
	total := cg.nextReg()
	cg.writef("  %s = add i64 %s, %s\n", total, len1, len2)
	totalPlus1 := cg.nextReg()
	cg.writef("  %s = add i64 %s, 1\n", totalPlus1, total)
	buf := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", buf, totalPlus1)
	cg.writef("  call i8* @strcpy(i8* %s, i8* %s)\n", buf, safeLeft)
	cg.writef("  call i8* @strcat(i8* %s, i8* %s)\n", buf, safeRight)
	return buf
}

// emitPrefixExpr emits a unary prefix operation.
// Supported operators: - (negation), ! (logical not), ~ (bitwise not).
func (cg *Codegen) emitPrefixExpr(e *ast.PrefixExpr) string {
	operand := cg.emitExpression(e.Right)
	reg := cg.nextReg()
	switch e.Operator {
	case "-":
		if cg.exprLLType(e.Right) == "double" {
			cg.writef("  %s = fsub double 0.0, %s\n", reg, operand)
		} else {
			cg.writef("  %s = sub i32 0, %s\n", reg, operand)
		}
	case "!":
		cg.writef("  %s = xor i1 %s, 1\n", reg, operand)
	case "~":
		cg.writef("  %s = xor i32 %s, -1\n", reg, operand)
	case "&":
		// Address-of: for identifiers, return their alloca directly.
		if id, ok := e.Right.(*ast.Identifier); ok {
			alloca, lt := cg.resolveVar(id.Value)
			if alloca != "" {
				if strings.HasPrefix(lt, "%struct.") && strings.HasSuffix(lt, "*") && !strings.HasSuffix(lt, "**") {
					// For struct variables, the alloca holds the pointer, so we load it to get the struct address
					reg := cg.nextReg()
					cg.writef("  %s = load %s, %s* %s\n", reg, lt, lt, alloca)
					return reg
				}
				return alloca
			}
		}
		// For index expressions, emit the GEP pointer without loading.
		if idx, ok := e.Right.(*ast.IndexExpr); ok {
			baseReg := cg.emitExpression(idx.Left)
			indexReg := cg.emitExpression(idx.Index)
			gep := cg.nextReg()
			isString := cg.exprLLType(idx.Left) == "i8*"
			if isString {
				cg.writef("  %s = getelementptr inbounds i8, i8* %s, i32 %s\n", gep, baseReg, indexReg)
			} else {
				cg.writef("  %s = getelementptr inbounds i32, i32* %s, i32 %s\n", gep, baseReg, indexReg)
			}
			return gep
		}
		// For field access, emit the GEP pointer without loading.
		if fa, ok := e.Right.(*ast.FieldAccessExpr); ok {
			ptrReg, _, ok2 := cg.emitFieldAccessPtr(fa)
			if ok2 {
				return ptrReg
			}
		}
		// For other expressions, the emitted value is already an address.
		return operand
	case "*":
		// Dereference: operand is a pointer; load from it.
		// Infer pointee type from operand's LLVM type.
		operandType := cg.exprLLType(e.Right)
		pointeeType := "i32"
		if strings.HasSuffix(operandType, "*") {
			pointeeType = strings.TrimSuffix(operandType, "*")
		}
		cg.writef("  %s = load %s, %s* %s\n", reg, pointeeType, pointeeType, operand)
	case "<-":
		// Channel receive: call Skink_chan_recv, then load and cast.
		recvPtr := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_chan_recv(i8* %s)\n", recvPtr, operand)
		// Track correct element type from channel type.
		chType := cg.exprASTType(e.Right)
		elemType := "i32"
		if ct, ok := chType.(*ast.ChanType); ok {
			elemType = llvmType(ct.Elem)
		}
		castReg := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to %s*\n", castReg, recvPtr, elemType)
		cg.writef("  %s = load %s, %s* %s\n", reg, elemType, elemType, castReg)
		// Free the returned buffer.
		cg.writef("  call void @Skink_free(i8* %s)\n", recvPtr)
		return reg
	default:
		cg.writef("  %s = add i32 0, %s\n", reg, operand)
	}
	return reg
}

// emitInfixExpr emits a binary infix operation.
//
// Supported operators:
//
//	Arithmetic:  +  -  *  /  %
//	Comparison: == != <  >  <= >=
//	Logical:    && ||
//
// Float operands use fadd/fsub/fmul/fdiv/frem/fcmp; integer operands use
// add/sub/mul/sdiv/srem/icmp.  Mixed-type operations are not yet handled.
func (cg *Codegen) emitInfixExpr(e *ast.InfixExpr) string {
	left := cg.emitExpression(e.Left)
	right := cg.emitExpression(e.Right)
	reg := cg.nextReg()

	// String concatenation: str1 + str2
	if e.Operator == "+" && cg.exprLLType(e.Left) == "i8*" && cg.exprLLType(e.Right) == "i8*" {
		return cg.emitStringConcat(left, right)
	}

	// Set membership: x in set
	if e.Operator == "in" {
		return cg.emitInExpr(left, right, e.Left, e.Right)
	}

	// Set operations: union, intersection, difference.
	if e.Operator == "|" || e.Operator == "&" || e.Operator == "-" {
		lt := cg.exprLLType(e.Left)
		rt := cg.exprLLType(e.Right)
		// Both sides must be set types (set literals now return %set.* after fixes).
		isSetType := func(t string) bool {
			return strings.HasPrefix(t, "%set.")
		}
		if isSetType(lt) && isSetType(rt) {
			return cg.emitSetOperation(left, right, e.Operator, e.Left)
		}
	}

	// Tensor matrix multiplication: A @ B
	if e.Operator == "@" {
		reg := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_tensor_matmul(i8* %s, i8* %s)\n", reg, left, right)
		return reg
	}

	// Channel send: ch <- val
	if e.Operator == "<-" {
		valType := cg.exprLLType(e.Right)
		// Allocate temp, store value, bitcast to i8*, send.
		tmpReg := cg.nextReg()
		cg.writef("  %s = alloca %s\n", tmpReg, valType)
		cg.writef("  store %s %s, %s* %s\n", valType, right, valType, tmpReg)
		castReg := cg.nextReg()
		cg.writef("  %s = bitcast %s* %s to i8*\n", castReg, valType, tmpReg)
		cg.writef("  call void @Skink_chan_send(i8* %s, i8* %s)\n", left, castReg)
		return "0"
	}

	// Assignment expression: a = b
	if e.Operator == "=" {
		switch lval := e.Left.(type) {
		case *ast.Identifier:
			cg.emitIdentAssignment(lval, "=", e.Right)
		case *ast.FieldAccessExpr:
			cg.emitFieldAccessAssignment(lval, "=", e.Right)
		case *ast.IndexExpr:
			cg.emitIndexAssignment(lval, "=", e.Right)
		default:
			// Fallback: try to resolve variable and store.
			alloca, lt := cg.resolveVar(e.Left.String())
			if alloca != "" {
				if lt == "" {
					lt = "i32"
				}
				valReg := cg.emitExpression(e.Right)
				if lt == "i32" && cg.exprLLType(e.Right) == "i1" {
					zextReg := cg.nextReg()
					cg.writef("  %s = zext i1 %s to i32\n", zextReg, valReg)
					valReg = zextReg
				}
				cg.writef("  store %s %s, %s* %s\n", lt, valReg, lt, alloca)
			}
		}
		return right
	}

	// Determine whether operands are float (double).
	isFloat := cg.exprLLType(e.Left) == "double" || cg.exprLLType(e.Right) == "double"

	// Pointer arithmetic: ptr +/- int
	leftType := cg.exprLLType(e.Left)
	rightType := cg.exprLLType(e.Right)
	isLeftPtr := strings.HasSuffix(leftType, "*")
	isRightPtr := strings.HasSuffix(rightType, "*")
	if isLeftPtr && e.Operator == "+" && !isRightPtr {
		pointeeType := strings.TrimSuffix(leftType, "*")
		cg.writef("  %s = getelementptr %s, %s %s, i32 %s\n", reg, pointeeType, leftType, left, right)
		return reg
	}
	if isLeftPtr && e.Operator == "-" && !isRightPtr {
		pointeeType := strings.TrimSuffix(leftType, "*")
		negReg := cg.nextReg()
		cg.writef("  %s = sub i32 0, %s\n", negReg, right)
		cg.writef("  %s = getelementptr %s, %s %s, i32 %s\n", reg, pointeeType, leftType, left, negReg)
		return reg
	}
	if isRightPtr && e.Operator == "+" && !isLeftPtr {
		pointeeType := strings.TrimSuffix(rightType, "*")
		cg.writef("  %s = getelementptr %s, %s %s, i32 %s\n", reg, pointeeType, rightType, right, left)
		return reg
	}

	if isFloat {
		// Promote int operand(s) to double for mixed-type operations.
		if cg.exprLLType(e.Left) == "i32" {
			conv := cg.nextReg()
			cg.writef("  %s = sitofp i32 %s to double\n", conv, left)
			left = conv
		}
		if cg.exprLLType(e.Right) == "i32" {
			conv := cg.nextReg()
			cg.writef("  %s = sitofp i32 %s to double\n", conv, right)
			right = conv
		}
		switch e.Operator {
		case "+":
			cg.writef("  %s = fadd double %s, %s\n", reg, left, right)
		case "-":
			cg.writef("  %s = fsub double %s, %s\n", reg, left, right)
		case "*":
			cg.writef("  %s = fmul double %s, %s\n", reg, left, right)
		case "/":
			cg.writef("  %s = fdiv double %s, %s\n", reg, left, right)
		case "%":
			cg.writef("  %s = frem double %s, %s\n", reg, left, right)
		case "**":
			cg.writef("  %s = call double @pow(double %s, double %s)\n", reg, left, right)
		case "==":
			cg.writef("  %s = fcmp oeq double %s, %s\n", reg, left, right)
		case "!=":
			cg.writef("  %s = fcmp one double %s, %s\n", reg, left, right)
		case "<":
			cg.writef("  %s = fcmp olt double %s, %s\n", reg, left, right)
		case ">":
			cg.writef("  %s = fcmp ogt double %s, %s\n", reg, left, right)
		case "<=":
			cg.writef("  %s = fcmp ole double %s, %s\n", reg, left, right)
		case ">=":
			cg.writef("  %s = fcmp oge double %s, %s\n", reg, left, right)
		case "&&":
			cg.writef("  %s = and i1 %s, %s\n", reg, left, right)
		case "||":
			cg.writef("  %s = or i1 %s, %s\n", reg, left, right)
		default:
			cg.writef("  %s = fadd double %s, %s\n", reg, left, right)
		}
	} else {
		// String comparison using strcmp, but use pointer icmp for nil comparisons
		isStr := cg.exprLLType(e.Left) == "i8*" && cg.exprLLType(e.Right) == "i8*"
		if isStr {
			switch e.Operator {
			case "==", "!=":
				// If either side is a literal null, use pointer comparison.
				if left == "null" || right == "null" {
					pred := "eq"
					if e.Operator == "!=" {
						pred = "ne"
					}
					cg.writef("  %s = icmp %s i8* %s, %s\n", reg, pred, left, right)
					return reg
				}
				// Treat runtime null pointers as empty strings before strcmp.
				safeLeft := cg.safeStringPtr(left)
				safeRight := cg.safeStringPtr(right)
				cmpReg := cg.nextReg()
				cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmpReg, safeLeft, safeRight)
				pred := "eq"
				if e.Operator == "!=" {
					pred = "ne"
				}
				cg.writef("  %s = icmp %s i32 %s, 0\n", reg, pred, cmpReg)
				return reg
			case "<", ">", "<=", ">=":
				// Treat runtime null pointers as empty strings before strcmp.
				safeLeft := cg.safeStringPtr(left)
				safeRight := cg.safeStringPtr(right)
				cmpReg := cg.nextReg()
				cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmpReg, safeLeft, safeRight)
				pred := "eq"
				switch e.Operator {
				case "!=":
					pred = "ne"
				case "<":
					pred = "slt"
				case ">":
					pred = "sgt"
				case "<=":
					pred = "sle"
				case ">=":
					pred = "sge"
				}
				cg.writef("  %s = icmp %s i32 %s, 0\n", reg, pred, cmpReg)
				return reg
			}
		}
		lt := cg.exprLLType(e.Left)
		rt := cg.exprLLType(e.Right)
		if (lt == "%error" || rt == "%error") && (e.Operator == "==" || e.Operator == "!=") {
			var errVal string
			if lt == "%error" {
				errVal = left
			} else {
				errVal = right
			}
			// Extract data pointer and compare to null
			dataReg := cg.nextReg()
			cg.writef("  %s = extractvalue %%error %s, 1\n", dataReg, errVal)
			if e.Operator == "==" {
				cg.writef("  %s = icmp eq i8* %s, null\n", reg, dataReg)
			} else {
				cg.writef("  %s = icmp ne i8* %s, null\n", reg, dataReg)
			}
			return reg
		}
		if lt == "" {
			lt = "i32"
		}
		// If either side is a pointer, use that pointer type for the comparison.
		if strings.HasSuffix(rt, "*") && !strings.HasSuffix(lt, "*") {
			lt = rt
		}
		isUnsigned := cg.isUnsignedExpr(e.Left)
		switch e.Operator {
		case "+":
			cg.writef("  %s = add %s %s, %s\n", reg, lt, left, right)
		case "-":
			cg.writef("  %s = sub %s %s, %s\n", reg, lt, left, right)
		case "*":
			cg.writef("  %s = mul %s %s, %s\n", reg, lt, left, right)
		case "/":
			if isUnsigned {
				cg.writef("  %s = udiv %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = sdiv %s %s, %s\n", reg, lt, left, right)
			}
		case "%":
			if isUnsigned {
				cg.writef("  %s = urem %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = srem %s %s, %s\n", reg, lt, left, right)
			}
		case "**":
			ld := cg.nextReg()
			rd := cg.nextReg()
			pd := cg.nextReg()
			cg.writef("  %s = sitofp %s %s to double\n", ld, lt, left)
			cg.writef("  %s = sitofp %s %s to double\n", rd, lt, right)
			cg.writef("  %s = call double @pow(double %s, double %s)\n", pd, ld, rd)
			cg.writef("  %s = fptosi double %s to %s\n", reg, pd, lt)
		case "==":
			cg.writef("  %s = icmp eq %s %s, %s\n", reg, lt, left, right)
		case "!=":
			cg.writef("  %s = icmp ne %s %s, %s\n", reg, lt, left, right)
		case "<":
			if isUnsigned {
				cg.writef("  %s = icmp ult %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = icmp slt %s %s, %s\n", reg, lt, left, right)
			}
		case ">":
			if isUnsigned {
				cg.writef("  %s = icmp ugt %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = icmp sgt %s %s, %s\n", reg, lt, left, right)
			}
		case "<=":
			if isUnsigned {
				cg.writef("  %s = icmp ule %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = icmp sle %s %s, %s\n", reg, lt, left, right)
			}
		case ">=":
			if isUnsigned {
				cg.writef("  %s = icmp uge %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = icmp sge %s %s, %s\n", reg, lt, left, right)
			}
		case "&&":
			cg.writef("  %s = and i1 %s, %s\n", reg, left, right)
		case "||":
			cg.writef("  %s = or i1 %s, %s\n", reg, left, right)
		case "&":
			cg.writef("  %s = and %s %s, %s\n", reg, lt, left, right)
		case "|":
			cg.writef("  %s = or %s %s, %s\n", reg, lt, left, right)
		case "^":
			cg.writef("  %s = xor %s %s, %s\n", reg, lt, left, right)
		case "<<":
			cg.writef("  %s = shl %s %s, %s\n", reg, lt, left, right)
		case ">>":
			if isUnsigned {
				cg.writef("  %s = lshr %s %s, %s\n", reg, lt, left, right)
			} else {
				cg.writef("  %s = ashr %s %s, %s\n", reg, lt, left, right)
			}
		default:
			cg.writef("  %s = add %s %s, %s\n", reg, lt, left, right)
		}
	}
	return reg
}

// emitInExpr emits set membership test: value in set.
// The set is a pointer to { elemType*, i32 }.
func (cg *Codegen) emitInExpr(valReg, setReg string, valExpr, setExpr ast.Expression) string {
	// Determine element type from value expression.
	elemType := cg.exprLLType(valExpr)
	var setTypeName string
	if elemType == "i8*" {
		setTypeName = "%set.Str"
	} else {
		setTypeName = "%set.Int"
	}

	// Load set length.
	lenPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n",
		lenPtr, setTypeName, setTypeName, setReg)
	setLen := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", setLen, lenPtr)

	// Load data pointer.
	dataPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n",
		dataPtr, setTypeName, setTypeName, setReg)
	dataArr := cg.nextReg()
	cg.writef("  %s = load %s*, %s** %s\n", dataArr, elemType, elemType, dataPtr)

	// Set up loop labels.
	loopStart := cg.nextLabel()
	loopBody := cg.nextLabel()
	foundLabel := cg.nextLabel()
	notFoundLabel := cg.nextLabel()
	endLabel := cg.nextLabel()

	// Initialize loop index.
	idxReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxReg)
	cg.writef("  store i32 0, i32* %s\n", idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	// Loop start: check if idx < len.
	cg.writef("%s:\n", loopStart)
	curIdx := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curIdx, idxReg)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, curIdx, setLen)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, loopBody, notFoundLabel)

	// Loop body: load element and compare.
	cg.writef("%s:\n", loopBody)
	elemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		elemPtr, elemType, elemType, dataArr, curIdx)
	elemVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", elemVal, elemType, elemType, elemPtr)

	var match string
	if elemType == "i8*" {
		cmp := cg.nextReg()
		cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmp, elemVal, valReg)
		m := cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, 0\n", m, cmp)
		match = m
	} else {
		m := cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, %s\n", m, elemVal, valReg)
		match = m
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", match, foundLabel, loopStart+".next")

	// Increment index.
	cg.writef("%s.next:\n", loopStart)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, curIdx)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	// Found block.
	cg.writef("%s:\n", foundLabel)
	cg.writef("  br label %%%s\n", endLabel)

	// Not found block.
	cg.writef("%s:\n", notFoundLabel)
	cg.writef("  br label %%%s\n", endLabel)

	// End: phi node.
	cg.writef("%s:\n", endLabel)
	result := cg.nextReg()
	cg.writef("  %s = phi i1 [ true, %%%s ], [ false, %%%s ]\n", result, foundLabel, notFoundLabel)
	return result
}

// emitSetOperation emits union, intersection, or difference of two sets.
// Sets are pointers to { elemType*, i32 }.
func (cg *Codegen) emitSetOperation(leftReg, rightReg, op string, leftExpr ast.Expression) string {
	elemType := cg.exprLLType(leftExpr)
	// Strip trailing * from set types (exprLLType for identifiers preserves the *).
	if strings.HasSuffix(elemType, "*") {
		elemType = strings.TrimSuffix(elemType, "*")
	}
	var setTypeName string
	var elemLLType string
	switch elemType {
	case "%set.Str":
		setTypeName = "%set.Str"
		elemLLType = "i8*"
	case "%set.Int":
		setTypeName = "%set.Int"
		elemLLType = "i32"
	case "i8*":
		setTypeName = "%set.Str"
		elemLLType = "i8*"
	default:
		setTypeName = "%set.Int"
		elemLLType = "i32"
	}
	elemType = elemLLType

	// Load left and right lengths.
	llen := cg.emitLoadSetLen(leftReg, setTypeName)
	rlen := cg.emitLoadSetLen(rightReg, setTypeName)
	ldata := cg.emitLoadSetData(leftReg, setTypeName, elemType)
	rdata := cg.emitLoadSetData(rightReg, setTypeName, elemType)

	// Allocate result array via malloc (worst case: left + right).
	maxReg := cg.nextReg()
	cg.writef("  %s = add i32 %s, %s\n", maxReg, llen, rlen)
	elemSize := cg.emitElemSize(elemType)
	totalBytes := cg.nextReg()
	cg.writef("  %s = mul i32 %s, %s\n", totalBytes, maxReg, elemSize)
	totalBytes64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", totalBytes64, totalBytes)
	rawPtr := cg.nextReg()
	cg.writef("  %s = call i8* @malloc(i64 %s)\n", rawPtr, totalBytes64)
	arrReg := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", arrReg, rawPtr, elemType)

	// Count register to track how many elements were written.
	countReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", countReg)
	cg.writef("  store i32 0, i32* %s\n", countReg)

	// For union, copy all left elements first, then unique right elements.
	// For intersection and difference, loop over left and filter.
	if op == "|" {
		// Copy all left elements unconditionally.
		cg.emitSetCopyAll(ldata, llen, arrReg, countReg, elemType)
		// Then append unique right elements.
		cg.emitSetAppendUnique(rdata, rlen, arrReg, countReg, elemType)
	} else {
		cg.emitSetFilterLoop(ldata, llen, rdata, rlen, arrReg, countReg, elemType, op)
	}

	finalCount := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", finalCount, countReg)
	return cg.emitBuildSet(arrReg, finalCount, setTypeName, elemType)
}

// emitSetCopyAll copies all elements from src to dst, updating count.
func (cg *Codegen) emitSetCopyAll(srcData, srcLen, dstArr, countReg, elemType string) {
	loopStart := cg.nextLabel()
	loopBody := cg.nextLabel()
	loopNext := loopStart + ".next"
	loopEnd := cg.nextLabel()

	idxReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxReg)
	cg.writef("  store i32 0, i32* %s\n", idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopStart)
	curIdx := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curIdx, idxReg)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, curIdx, srcLen)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, loopBody, loopEnd)

	cg.writef("%s:\n", loopBody)
	srcElemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		srcElemPtr, elemType, elemType, srcData, curIdx)
	srcElem := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", srcElem, elemType, elemType, srcElemPtr)
	curCount := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curCount, countReg)
	dstPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		dstPtr, elemType, elemType, dstArr, curCount)
	cg.writef("  store %s %s, %s* %s\n", elemType, srcElem, elemType, dstPtr)
	newCount := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", newCount, curCount)
	cg.writef("  store i32 %s, i32* %s\n", newCount, countReg)
	cg.writef("  br label %%%s\n", loopNext)

	cg.writef("%s:\n", loopNext)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, curIdx)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopEnd)
}

// emitSetAppendUnique appends elements from src to dst only if not already in dst.
func (cg *Codegen) emitSetAppendUnique(srcData, srcLen, dstArr, countReg, elemType string) {
	loopStart := cg.nextLabel()
	loopBody := cg.nextLabel()
	loopNext := loopStart + ".next"
	copyLabel := cg.nextLabel()
	loopEnd := cg.nextLabel()

	idxReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxReg)
	cg.writef("  store i32 0, i32* %s\n", idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopStart)
	curIdx := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curIdx, idxReg)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, curIdx, srcLen)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, loopBody, loopEnd)

	cg.writef("%s:\n", loopBody)
	srcElemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		srcElemPtr, elemType, elemType, srcData, curIdx)
	srcElem := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", srcElem, elemType, elemType, srcElemPtr)
	shouldCopy := cg.emitSetNotContainsInArr(srcElem, dstArr, countReg, elemType)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", shouldCopy, copyLabel, loopNext)

	cg.writef("%s:\n", copyLabel)
	curCount := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curCount, countReg)
	dstPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		dstPtr, elemType, elemType, dstArr, curCount)
	cg.writef("  store %s %s, %s* %s\n", elemType, srcElem, elemType, dstPtr)
	newCount := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", newCount, curCount)
	cg.writef("  store i32 %s, i32* %s\n", newCount, countReg)
	cg.writef("  br label %%%s\n", loopNext)

	cg.writef("%s:\n", loopNext)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, curIdx)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopEnd)
}

// emitSetFilterLoop copies elements from srcData to dstArr based on condition against other set.
func (cg *Codegen) emitSetFilterLoop(srcData, srcLen, otherData, otherLen, dstArr, countReg, elemType, op string) {
	loopStart := cg.nextLabel()
	loopBody := cg.nextLabel()
	loopNext := loopStart + ".next"
	copyLabel := cg.nextLabel()
	loopEnd := cg.nextLabel()

	idxReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxReg)
	cg.writef("  store i32 0, i32* %s\n", idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopStart)
	curIdx := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curIdx, idxReg)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, curIdx, srcLen)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, loopBody, loopEnd)

	cg.writef("%s:\n", loopBody)
	srcElemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		srcElemPtr, elemType, elemType, srcData, curIdx)
	srcElem := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", srcElem, elemType, elemType, srcElemPtr)

	var shouldCopy string
	if op == "&" {
		shouldCopy = cg.emitSetContains(srcElem, otherData, otherLen, elemType)
	} else {
		shouldCopy = cg.emitSetNotContains(srcElem, otherData, otherLen, elemType)
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", shouldCopy, copyLabel, loopNext)

	cg.writef("%s:\n", copyLabel)
	curCount := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curCount, countReg)
	dstPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		dstPtr, elemType, elemType, dstArr, curCount)
	cg.writef("  store %s %s, %s* %s\n", elemType, srcElem, elemType, dstPtr)
	newCount := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", newCount, curCount)
	cg.writef("  store i32 %s, i32* %s\n", newCount, countReg)
	cg.writef("  br label %%%s\n", loopNext)

	cg.writef("%s:\n", loopNext)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, curIdx)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopEnd)
}

// emitLoadSetLen loads the length field of a set struct.
func (cg *Codegen) emitLoadSetLen(setReg, setTypeName string) string {
	lenPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n",
		lenPtr, setTypeName, setTypeName, setReg)
	setLen := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", setLen, lenPtr)
	return setLen
}

// emitLoadSetData loads the data pointer field of a set struct.
func (cg *Codegen) emitLoadSetData(setReg, setTypeName, elemType string) string {
	dataPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n",
		dataPtr, setTypeName, setTypeName, setReg)
	dataArr := cg.nextReg()
	cg.writef("  %s = load %s*, %s** %s\n", dataArr, elemType, elemType, dataPtr)
	return dataArr
}

// emitSetContains returns i1 whether val is in the set data array of given length.
func (cg *Codegen) emitSetContains(valReg, dataArr, dataLen, elemType string) string {
	loopStart := cg.nextLabel()
	loopBody := cg.nextLabel()
	foundLabel := cg.nextLabel()
	notFoundLabel := cg.nextLabel()
	endLabel := cg.nextLabel()

	idxReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxReg)
	cg.writef("  store i32 0, i32* %s\n", idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopStart)
	curIdx := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curIdx, idxReg)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, curIdx, dataLen)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, loopBody, notFoundLabel)

	cg.writef("%s:\n", loopBody)
	elemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		elemPtr, elemType, elemType, dataArr, curIdx)
	elemVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", elemVal, elemType, elemType, elemPtr)
	var match string
	if elemType == "i8*" {
		cmp := cg.nextReg()
		cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmp, elemVal, valReg)
		m := cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, 0\n", m, cmp)
		match = m
	} else {
		m := cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, %s\n", m, elemVal, valReg)
		match = m
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", match, foundLabel, loopStart+".next")

	cg.writef("%s.next:\n", loopStart)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, curIdx)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", foundLabel)
	cg.writef("  br label %%%s\n", endLabel)

	cg.writef("%s:\n", notFoundLabel)
	cg.writef("  br label %%%s\n", endLabel)

	cg.writef("%s:\n", endLabel)
	result := cg.nextReg()
	cg.writef("  %s = phi i1 [ true, %%%s ], [ false, %%%s ]\n", result, foundLabel, notFoundLabel)
	return result
}

// emitSetNotContains returns i1 whether val is NOT in the set data array.
func (cg *Codegen) emitSetNotContains(valReg, dataArr, dataLen, elemType string) string {
	found := cg.emitSetContains(valReg, dataArr, dataLen, elemType)
	notFound := cg.nextReg()
	cg.writef("  %s = xor i1 %s, 1\n", notFound, found)
	return notFound
}

// emitElemSize returns the byte size of an LLVM element type.
func (cg *Codegen) emitElemSize(elemType string) string {
	sz := cg.nextReg()
	if elemType == "i8*" {
		cg.writef("  %s = ptrtoint i8** getelementptr (i8*, i8** null, i64 1) to i64\n", sz)
		// Need i32, so trunc.
		sz32 := cg.nextReg()
		cg.writef("  %s = trunc i64 %s to i32\n", sz32, sz)
		return sz32
	}
	// i32, i1, i8, i16, i64 sizes.
	var size int
	switch elemType {
	case "i1":
		size = 1
	case "i8":
		size = 1
	case "i16":
		size = 2
	case "i32":
		size = 4
	case "i64":
		size = 8
	case "double":
		size = 8
	default:
		if strings.HasPrefix(elemType, "%struct.") {
			// Compute actual struct size via LLVM getelementptr.
			cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", sz, elemType, elemType)
			sz2 := cg.nextReg()
			cg.writef("  %s = ptrtoint %s* %s to i64\n", sz2, elemType, sz)
			sz32 := cg.nextReg()
			cg.writef("  %s = trunc i64 %s to i32\n", sz32, sz2)
			return sz32
		}
		size = 8 // pointer or other
	}
	cg.writef("  %s = add i32 0, %d\n", sz, size)
	return sz
}

// emitSetNotContainsInArr checks whether val is NOT in the first 'count' elements of arr.
func (cg *Codegen) emitSetNotContainsInArr(valReg, arrReg, countReg, elemType string) string {
	loopStart := cg.nextLabel()
	loopBody := cg.nextLabel()
	foundLabel := cg.nextLabel()
	notFoundLabel := cg.nextLabel()
	endLabel := cg.nextLabel()

	idxReg := cg.nextReg()
	cg.writef("  %s = alloca i32\n", idxReg)
	cg.writef("  store i32 0, i32* %s\n", idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", loopStart)
	curIdx := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", curIdx, idxReg)
	dataLen := cg.nextReg()
	cg.writef("  %s = load i32, i32* %s\n", dataLen, countReg)
	cond := cg.nextReg()
	cg.writef("  %s = icmp slt i32 %s, %s\n", cond, curIdx, dataLen)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", cond, loopBody, notFoundLabel)

	cg.writef("%s:\n", loopBody)
	elemPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 %s\n",
		elemPtr, elemType, elemType, arrReg, curIdx)
	elemVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", elemVal, elemType, elemType, elemPtr)
	var match string
	if elemType == "i8*" {
		cmp := cg.nextReg()
		cg.writef("  %s = call i32 @strcmp(i8* %s, i8* %s)\n", cmp, elemVal, valReg)
		m := cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, 0\n", m, cmp)
		match = m
	} else {
		m := cg.nextReg()
		cg.writef("  %s = icmp eq i32 %s, %s\n", m, elemVal, valReg)
		match = m
	}
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", match, foundLabel, loopStart+".next")

	cg.writef("%s.next:\n", loopStart)
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i32 %s, 1\n", nextIdx, curIdx)
	cg.writef("  store i32 %s, i32* %s\n", nextIdx, idxReg)
	cg.writef("  br label %%%s\n", loopStart)

	cg.writef("%s:\n", foundLabel)
	cg.writef("  br label %%%s\n", endLabel)

	cg.writef("%s:\n", notFoundLabel)
	cg.writef("  br label %%%s\n", endLabel)

	cg.writef("%s:\n", endLabel)
	result := cg.nextReg()
	cg.writef("  %s = phi i1 [ false, %%%s ], [ true, %%%s ]\n", result, foundLabel, notFoundLabel)
	return result
}

// emitBuildSet creates a set struct from a data pointer and count.
func (cg *Codegen) emitBuildSet(dataPtr, countReg, setTypeName, elemType string) string {
	setReg := cg.nextReg()
	cg.writef("  %s = alloca %s\n", setReg, setTypeName)

	dataField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 0\n",
		dataField, setTypeName, setTypeName, setReg)
	cg.writef("  store %s* %s, %s** %s\n", elemType, dataPtr, elemType, dataField)

	lenField := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds %s, %s* %s, i32 0, i32 1\n",
		lenField, setTypeName, setTypeName, setReg)
	cg.writef("  store i32 %s, i32* %s\n", countReg, lenField)

	return setReg
}

// emitCallExpr emits a function call instruction.
//
// Built-in mapping:
//
//	print, println  ->  call i32 (i8*, ...) @printf(...)
//	All other calls  ->  call i32 @<name>(...)
//
// Argument types default to i32 for regular calls and i8* for printf.
// A future enhancement should look up the callee's signature for precise types.
func (cg *Codegen) emitCallExpr(e *ast.CallExpr) string {
	fnName := ""
	var recvReg string
	var recvType string
	if id, ok := e.Function.(*ast.Identifier); ok {
		fnName = id.Value
	}
	// Method call syntax: obj.method(args)
	if fa, ok := e.Function.(*ast.FieldAccessExpr); ok {
		fnName = fa.Field
		// Service method call: ServiceName.method(args) — no receiver.
		if id, ok := fa.Left.(*ast.Identifier); ok && cg.services[id.Value] {
			recvReg = ""
			recvType = ""
		} else if id, ok := fa.Left.(*ast.Identifier); ok && cg.rulesets[id.Value] {
			// Ruleset method call: RulesetName.method(args) — no receiver.
			recvReg = ""
			recvType = ""
			fnName = id.Value + "." + fnName
			// Special handling for registerRule and registerRules.
			if fnName == id.Value+".registerRule" && len(e.Arguments) == 1 {
				cg.emitRulesetRegisterRule(id.Value, e.Arguments[0])
				return "0"
			}
			if fnName == id.Value+".registerRules" && len(e.Arguments) == 1 {
				cg.emitRulesetRegisterRules(id.Value, e.Arguments[0])
				return "0"
			}
		} else if id, ok := fa.Left.(*ast.Identifier); ok && (func() bool {
			alloca, _ := cg.resolveVar(id.Value)
			_, isGlobal := cg.globalVarTypes[id.Value]
			return alloca == "" && !isGlobal
		}()) {
			// Module-qualified function call: module.func(args) — no receiver.
			recvReg = ""
			recvType = ""
			realModuleName := cg.resolveImportAlias(id.Value)
			if _, ok := cg.fnRetTypes[fa.Field]; ok {
				fnName = fa.Field
			} else if _, ok := cg.fnRetTypes[realModuleName+"."+fa.Field]; ok {
				fnName = realModuleName + "." + fa.Field
			} else if _, ok := cg.fnRetTypes[id.Value+"."+fa.Field]; ok {
				fnName = id.Value + "." + fa.Field
			} else {
				fnName = fa.Field
			}
		} else {
			if leftASTType := cg.exprASTType(fa.Left); leftASTType != nil {
				if nt, ok := leftASTType.(*ast.NamedType); ok {
					if nt.Name == "Reader" || nt.Name == "Writer" || nt.Name == "reader.Reader" || nt.Name == "writer.Writer" {
						return "0"
					}
				}
			}
			recvReg = cg.emitExpression(fa.Left)
			recvType = cg.exprLLType(fa.Left)

			// Template method dispatch: opaque pointer (i8*) receiver with Read/Write.
			if recvType == "i8*" && (fnName == "Read" || fnName == "Write") {
				return "0"
			}

			// Interface method dispatch: error.String()
			if recvType == "%error" {
				fnPtr := cg.nextReg()
				cg.writef("  %s = extractvalue %%error %s, 0\n", fnPtr, recvReg)
				dataPtr := cg.nextReg()
				cg.writef("  %s = extractvalue %%error %s, 1\n", dataPtr, recvReg)
				callReg := cg.nextReg()
				cg.writef("  %s = call i8* %s(i8* %s)\n", callReg, fnPtr, dataPtr)
				return callReg
			}

			// Fully qualify the method name if calling on a struct.
			structName := ""
			t := recvType
			if strings.HasSuffix(t, "*") {
				t = strings.TrimSuffix(t, "*")
			}
			if strings.HasPrefix(t, "%struct.") {
				structName = strings.TrimPrefix(t, "%struct.")
			}
			if structName != "" {
				// Use original dotted name from structDecls if available.
				if decl, ok := cg.structDecls[structName]; ok {
					structName = decl.Name
				}
				candidate := structName + "." + fnName
				if _, ok := cg.fnRetTypes[candidate]; ok {
					fnName = candidate
				} else {
					// Fallback: try module-qualified function name (e.g. json.Method for standalone functions)
					parts := strings.Split(structName, ".")
					if len(parts) >= 2 {
						moduleName := strings.Join(parts[:len(parts)-1], ".")
						candidate = moduleName + "." + fnName
						if _, ok := cg.fnRetTypes[candidate]; ok {
							fnName = candidate
						}
					}
				}
			}

			// Automatic pointer vs value receiver conversion.
			if params, ok := cg.fnParamTypes[fnName]; ok && len(params) > 0 {
				expected := params[0]
				// Case A: method expects value (Struct), but receiver is pointer (Struct*)
				if strings.HasSuffix(recvType, "*") && !strings.HasSuffix(expected, "*") {
					if strings.HasPrefix(recvType, "%struct.") && strings.HasPrefix(expected, "%struct.") {
						valReg := cg.nextReg()
						cg.writef("  %s = load %s, %s* %s\n", valReg, expected, expected, recvReg)
						recvReg = valReg
						recvType = expected
					}
				}
				// Case B: method expects pointer (Struct*), but receiver is value (Struct)
				if !strings.HasSuffix(recvType, "*") && strings.HasSuffix(expected, "*") {
					if strings.HasPrefix(expected, "%struct.") && strings.HasPrefix(recvType, "%struct.") {
						if id, ok := fa.Left.(*ast.Identifier); ok {
							allocReg, _ := cg.resolveVar(id.Value)
							if allocReg != "" {
								recvReg = allocReg
								recvType = expected
							}
						} else {
							tmpReg := cg.nextReg()
							cg.writef("  %s = alloca %s\n", tmpReg, recvType)
							cg.writef("  store %s %s, %s* %s\n", recvType, recvReg, recvType, tmpReg)
							recvReg = tmpReg
							recvType = expected
						}
					}
				}
			}
		}
	}

	// Handle built-ins when called by identifier.
	if fnName != "" {
		// Type cast: Type(expr) — e.g., int(3.14), float(x)
		if len(e.Arguments) == 1 && fnName != "print" && fnName != "println" && fnName != "len" && fnName != "assert" && fnName != "append" && fnName != "panic" && fnName != "error" {
			// Check if it's actually a known type name.
			isType := false
			switch fnName {
			case "int", "float", "bool", "string", "void",
				"int8", "int16", "int32", "int64",
				"uint", "uint8", "uint16", "uint32", "uint64",
				"bytes":
				isType = true
			default:
				// Could be a struct type.
				if _, ok := cg.structLayouts[fnName]; ok {
					isType = true
				}
			}
			if isType {
				castType := llvmType(&ast.NamedType{Name: fnName})
				return cg.emitCast(e.Arguments[0], castType)
			}
		}

		// Handle assert(cond) — runtime assertion.
		if fnName == "assert" && len(e.Arguments) == 1 {
			condReg := cg.emitExpression(e.Arguments[0])
			passLabel := cg.nextLabel()
			failLabel := cg.nextLabel()
			cg.writef("  br i1 %s, label %%%s, label %%%s\n", condReg, passLabel, failLabel)
			cg.writeln(failLabel + ":")
			cg.writeln("  call void @exit(i32 1)")
			cg.writeln("  unreachable")
			cg.terminated = true
			cg.writeln(passLabel + ":")
			cg.terminated = false
			reg := cg.nextReg()
			cg.writef("  %s = add i32 0, 0\n", reg)
			return reg
		}

		// Handle panic(msg) — print message and abort.
		if fnName == "panic" && len(e.Arguments) == 1 {
			msgReg := cg.emitExpression(e.Arguments[0])
			cg.writef("  call i32 (i8*, ...) @printf(i8* %s)\n", msgReg)
			cg.writeln("  call void @exit(i32 1)")
			cg.writeln("  unreachable")
			cg.terminated = true
			return "0"
		}

		// Handle math functions (sin, cos, tan, sqrt) returning double.
		if (fnName == "sin" || fnName == "cos" || fnName == "tan" || fnName == "sqrt") && len(e.Arguments) == 1 {
			valReg := cg.emitExpression(e.Arguments[0])
			reg := cg.nextReg()
			cg.writef("  %s = call double @%s(double %s)\n", reg, fnName, valReg)
			return reg
		}
		if fnName == "pow" && len(e.Arguments) == 2 {
			xReg := cg.emitExpression(e.Arguments[0])
			yReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call double @pow(double %s, double %s)\n", reg, xReg, yReg)
			return reg
		}

		// Handle det(A) -> double
		if fnName == "det" && len(e.Arguments) == 1 {
			tReg := cg.emitExpression(e.Arguments[0])
			reg := cg.nextReg()
			cg.writef("  %s = call double @Skink_tensor_det(i8* %s)\n", reg, tReg)
			return reg
		}

		// Handle inv(A) -> i8*
		if fnName == "inv" && len(e.Arguments) == 1 {
			tReg := cg.emitExpression(e.Arguments[0])
			reg := cg.nextReg()
			cg.writef("  %s = call i8* @Skink_tensor_inv(i8* %s)\n", reg, tReg)
			return reg
		}

		// Handle diff(f, x) -> double
		if fnName == "diff" && len(e.Arguments) == 2 {
			fReg := cg.emitExpression(e.Arguments[0])
			xReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call double @Skink_math_diff(double (double)* %s, double %s)\n", reg, fReg, xReg)
			return reg
		}

		// Handle integrate(f, a, b) -> double
		if fnName == "integrate" && len(e.Arguments) == 3 {
			fReg := cg.emitExpression(e.Arguments[0])
			aReg := cg.emitExpression(e.Arguments[1])
			bReg := cg.emitExpression(e.Arguments[2])
			reg := cg.nextReg()
			cg.writef("  %s = call double @Skink_math_integrate(double (double)* %s, double %s, double %s)\n", reg, fReg, aReg, bReg)
			return reg
		}

		// Handle gradient(f, x) -> i8*
		if fnName == "gradient" && len(e.Arguments) == 2 {
			fReg := cg.emitExpression(e.Arguments[0])
			xReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call i8* @Skink_tensor_gradient(double (double*)* %s, i8* %s)\n", reg, fReg, xReg)
			return reg
		}

		// Handle dot(v, w) -> double
		if fnName == "dot" && len(e.Arguments) == 2 {
			vReg := cg.emitExpression(e.Arguments[0])
			wReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call double @Skink_tensor_dot(i8* %s, i8* %s)\n", reg, vReg, wReg)
			return reg
		}

		// Handle cross(v, w) -> i8*
		if fnName == "cross" && len(e.Arguments) == 2 {
			vReg := cg.emitExpression(e.Arguments[0])
			wReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call i8* @Skink_tensor_cross(i8* %s, i8* %s)\n", reg, vReg, wReg)
			return reg
		}

		// Handle norm(v) -> double
		if fnName == "norm" && len(e.Arguments) == 1 {
			vReg := cg.emitExpression(e.Arguments[0])
			reg := cg.nextReg()
			cg.writef("  %s = call double @Skink_tensor_norm(i8* %s)\n", reg, vReg)
			return reg
		}

		// Handle eigenvalues(A) -> i8*
		if fnName == "eigenvalues" && len(e.Arguments) == 1 {
			tReg := cg.emitExpression(e.Arguments[0])
			reg := cg.nextReg()
			cg.writef("  %s = call i8* @Skink_tensor_eigenvalues(i8* %s)\n", reg, tReg)
			return reg
		}

		// Handle len(arr) — compile-time for known arrays, runtime for strings and dynamic arrays.
		if fnName == "len" && len(e.Arguments) == 1 {
			arg := e.Arguments[0]
			switch a := arg.(type) {
			case *ast.ArrayLiteral:
				return fmt.Sprintf("%d", len(a.Elements))
			case *ast.Identifier:
				allocaKey := a.Value
				if allocReg, _ := cg.resolveVar(a.Value); allocReg != "" {
					allocaKey = allocReg
				}
				if meta, ok := cg.arraySizes[cg.currentFnName+":"+allocaKey]; ok && !meta.heapAlloc {
					return fmt.Sprintf("%d", meta.len)
				}
				// No compile-time info: compute string/array length at runtime.
				argReg := cg.emitExpression(arg)
				argType := cg.exprLLType(arg)
				if argType == "i8*" {
					// String: use strlen.
					lenReg := cg.nextReg()
					cg.writef("  %s = call i64 @strlen(i8* %s)\n", lenReg, argReg)
					truncReg := cg.nextReg()
					cg.writef("  %s = trunc i64 %s to i32\n", truncReg, lenReg)
					return truncReg
				}
				return cg.emitRuntimeArrayLen(argReg, argType)
			case *ast.StringLiteral:
				return fmt.Sprintf("%d", len(a.Value))
			}
			// Fallback: try runtime length for other expression types.
			argReg := cg.emitExpression(arg)
			argType := cg.exprLLType(arg)
			if argType == "i8*" {
				lenReg := cg.nextReg()
				cg.writef("  %s = call i64 @strlen(i8* %s)\n", lenReg, argReg)
				truncReg := cg.nextReg()
				cg.writef("  %s = trunc i64 %s to i32\n", truncReg, lenReg)
				return truncReg
			}
			if strings.HasSuffix(argType, "*") {
				return cg.emitRuntimeArrayLen(argReg, argType)
			}
			cg.writeln("  ; len: unknown length at compile time")
			return "0"
		}

		// Handle append(arr, val) — runtime array append.
		if fnName == "append" && len(e.Arguments) == 2 {
			return cg.emitAppend(e.Arguments[0], e.Arguments[1])
		}
		// Handle close(ch) — close a channel.
		if fnName == "close" && len(e.Arguments) == 1 {
			chReg := cg.emitExpression(e.Arguments[0])
			cg.writef("  call void @Skink_chan_close(i8* %s)\n", chReg)
			return "0"
		}

		// Handle tensor_ones(rows, cols) — returns i8* tensor.
		if fnName == "tensor_ones" && len(e.Arguments) == 2 {
			rowReg := cg.emitExpression(e.Arguments[0])
			colReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call i8* @Skink_tensor_ones(i32 %s, i32 %s)\n", reg, rowReg, colReg)
			return reg
		}
		// Handle tensor_zeros(rows, cols) — returns i8* tensor.
		if fnName == "tensor_zeros" && len(e.Arguments) == 2 {
			rowReg := cg.emitExpression(e.Arguments[0])
			colReg := cg.emitExpression(e.Arguments[1])
			reg := cg.nextReg()
			cg.writef("  %s = call i8* @Skink_tensor_zeros(i32 %s, i32 %s)\n", reg, rowReg, colReg)
			return reg
		}
		// Handle tensor_get(t, row, col) — returns double.
		if fnName == "tensor_get" && len(e.Arguments) == 3 {
			tReg := cg.emitExpression(e.Arguments[0])
			rowReg := cg.emitExpression(e.Arguments[1])
			colReg := cg.emitExpression(e.Arguments[2])
			reg := cg.nextReg()
			cg.writef("  %s = call double @Skink_tensor_get(i8* %s, i32 %s, i32 %s)\n", reg, tReg, rowReg, colReg)
			return reg
		}
	}

	// Map built-in print -> printf, and main -> _skink_main when wrapped.
	isPrintf := fnName == "print" || fnName == "println"
	llvmFn := fnName
	if isPrintf {
		llvmFn = "printf"
	}
	if fnName == "main" && cg.hasMainWrapper {
		llvmFn = "_skink_main"
	}
	if fnName == "_close" {
		llvmFn = "close"
	}

	// Handle string interpolation for print("Hello, {name}!")
	if isPrintf && len(e.Arguments) > 0 {
		if strLit, ok := e.Arguments[0].(*ast.StringLiteral); ok && strings.Contains(strLit.Value, "{") {
			return cg.emitInterpolatedPrint(strLit.Value, e.Arguments[1:], fnName == "println")
		}
	}

	// Build argument string for call
	var argStrs []string
	// Prepend receiver for method calls.
	if recvReg != "" {
		argStrs = append(argStrs, fmt.Sprintf("%s %s", recvType, recvReg))
	}
	expectedParamTypes := cg.fnParamTypes[fnName]
	_, fnIsLocalVar := cg.resolveVar(fnName)
	if fnName == "" || fnIsLocalVar != "" {
		// Function pointer call: parse expected parameter types from the pointer type.
		fnType := cg.exprLLType(e.Function)
		// Resolve named function alias types back to inline form.
		for aliasName, namedLL := range cg.fnAliasLLTypes {
			if fnType == namedLL {
				fnType = llvmType(cg.aliases[aliasName])
				break
			}
		}
		expectedParamTypes = parseFunctionPointerParams(fnType)
	}
	// If a receiver is prepended, skip its slot in expectedParamTypes so the
	// remaining argument indices align with e.Arguments.
	if recvReg != "" && len(expectedParamTypes) > 0 {
		expectedParamTypes = expectedParamTypes[1:]
	}
	for i, arg := range e.Arguments {
		argReg := cg.emitExpression(arg)
		var argType string
		if isPrintf {
			argType = "i8*"
		} else {
			argType = cg.exprLLType(arg)
			if len(expectedParamTypes) > i {
				expected := expectedParamTypes[i]
				if _, ok := arg.(*ast.NilLiteral); ok {
					if strings.HasSuffix(expected, "*") {
						argType = expected
					}
				}
				if strings.HasPrefix(expected, "%struct.") && !strings.HasSuffix(expected, "*") {
					if strings.HasPrefix(argType, "%struct.") && strings.HasSuffix(argType, "*") {
						loadedReg := cg.nextReg()
						cg.writef("  %s = load %s, %s* %s\n", loadedReg, expected, expected, argReg)
						argReg = loadedReg
						argType = expected
					}
				}
				if strings.HasPrefix(expected, "%struct.") && strings.HasSuffix(expected, "*") {
					if strings.HasPrefix(argType, "%struct.") && !strings.HasSuffix(argType, "*") {
						if id, ok := arg.(*ast.Identifier); ok {
							allocReg, _ := cg.resolveVar(id.Value)
							if allocReg != "" {
								argReg = allocReg
								argType = expected
							}
						} else {
							// Allocate a temporary to hold the struct value and pass its address.
							tmpReg := cg.nextReg()
							structType := strings.TrimSuffix(expected, "*")
							cg.writef("  %s = alloca %s\n", tmpReg, structType)
							cg.writef("  store %s %s, %s* %s\n", argType, argReg, structType, tmpReg)
							argReg = tmpReg
							argType = expected
						}
					}
				}
			}
		}
		argStrs = append(argStrs, fmt.Sprintf("%s %s", argType, argReg))
	}

	if isPrintf {
		reg := cg.nextReg()
		cg.writef("  %s = call i32 (i8*, ...) @%s(%s)%s\n", reg, llvmFn, strings.Join(argStrs, ", "), cg.dbgTag())
		// Append a newline after every print/println call.
		nlReg := cg.nextReg()
		cg.writef("  %s = call i32 (i8*, ...) @printf(i8* getelementptr inbounds ([2 x i8], [2 x i8]* @str.newline, i64 0, i64 0))\n", nlReg)
		return reg
	}

	// Determine if this is a direct global call or a function pointer call.
	isGlobalCall := false
	callType := "i32"
	if fnName != "" {
		_, lt := cg.resolveVar(fnName)
		if lt == "" {
			// Not a local variable — global function.
			isGlobalCall = true
			retTypes := cg.fnRetTypes[fnName]
			if len(retTypes) > 1 {
				callType = "{ " + strings.Join(retTypes, ", ") + " }"
			} else if len(retTypes) == 1 {
				callType = retTypes[0]
			}
		}
	}

	isVoid := callType == "void"
	var reg string
	if !isVoid {
		reg = cg.nextReg()
	}

	if isGlobalCall {
		if cg.fnVariadic[fnName] {
			if isVoid {
				cg.writef("  call void (%s, ...) @%s(%s)%s\n", strings.Join(cg.fnParamTypes[fnName], ", "), llvmFn, strings.Join(argStrs, ", "), cg.dbgTag())
			} else {
				cg.writef("  %s = call %s (%s, ...) @%s(%s)%s\n", reg, callType, strings.Join(cg.fnParamTypes[fnName], ", "), llvmFn, strings.Join(argStrs, ", "), cg.dbgTag())
			}
		} else {
			if isVoid {
				cg.writef("  call %s @%s(%s)%s\n", callType, llvmFn, strings.Join(argStrs, ", "), cg.dbgTag())
			} else {
				cg.writef("  %s = call %s @%s(%s)%s\n", reg, callType, llvmFn, strings.Join(argStrs, ", "), cg.dbgTag())
			}
		}
	} else {
		// Function pointer call: emit the function expression, then call through the pointer.
		fnReg := cg.emitExpression(e.Function)
		fnType := cg.exprLLType(e.Function)
		// Resolve named function alias types (e.g. %fp_IntFn) back to inline form.
		for aliasName, namedLL := range cg.fnAliasLLTypes {
			if fnType == namedLL {
				fnType = llvmType(cg.aliases[aliasName])
				break
			}
		}
		// fnType is something like "i32 (i32, i32)*"
		// For call, we need to strip the trailing * to get the function type.
		funcType := strings.TrimSuffix(fnType, "*")
		isVoidCall := strings.HasPrefix(funcType, "void")
		if isVoid || isVoidCall {
			cg.writef("  call %s %s(%s)%s\n", funcType, fnReg, strings.Join(argStrs, ", "), cg.dbgTag())
		} else {
			cg.writef("  %s = call %s %s(%s)%s\n", reg, funcType, fnReg, strings.Join(argStrs, ", "), cg.dbgTag())
		}
		if isVoidCall {
			return "0"
		}
		return reg
	}
	if isVoid {
		return "0"
	}
	return reg
}

// interpolatedArg holds an AST expression and the original raw id string
// for an interpolation placeholder inside a print string.
type interpolatedArg struct {
	rawID string
	expr  ast.Expression
}

// buildInterpolatedExpr converts a raw interpolation id like "r1.device_id"
// into the appropriate AST expression (FieldAccessExpr or Identifier).
func buildInterpolatedExpr(id string) ast.Expression {
	if dotIdx := strings.Index(id, "."); dotIdx >= 0 {
		base := id[:dotIdx]
		field := id[dotIdx+1:]
		return &ast.FieldAccessExpr{
			Left:  &ast.Identifier{Value: base},
			Field: field,
		}
	}
	return &ast.Identifier{Value: id}
}

// emitRuntimeArrayLen loads the dynamic length header that precedes a heap
// array allocation. The header stores the length as an i64 located 8 bytes
// before the element pointer. Strings (i8*) are handled separately via strlen,
// so this helper assumes arrReg refers to an array payload pointer.
func (cg *Codegen) emitRuntimeArrayLen(arrReg, arrType string) string {
	if arrType == "" {
		arrType = "i8*"
	}
	rawPtr := cg.nextReg()
	cg.writef("  %s = bitcast %s %s to i8*\n", rawPtr, arrType, arrReg)
	isNull := cg.nextReg()
	cg.writef("  %s = icmp eq i8* %s, null\n", isNull, rawPtr)
	lenPtr := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 -8\n", lenPtr, rawPtr)
	typedLenPtr := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", typedLenPtr, lenPtr)
	loadedLen := cg.nextReg()
	cg.writef("  %s = load i64, i64* %s\n", loadedLen, typedLenPtr)
	lenReg64 := cg.nextReg()
	cg.writef("  %s = select i1 %s, i64 0, i64 %s\n", lenReg64, isNull, loadedLen)
	truncReg := cg.nextReg()
	cg.writef("  %s = trunc i64 %s to i32\n", truncReg, lenReg64)
	return truncReg
}

// emitInterpolatedPrint handles print("literal {var} literal") by building a
// printf format string and passing the interpolated variables as arguments.
//
// Supports simple identifiers (e.g. {name}) and dotted field access
// (e.g. {obj.field}). Each {id} is replaced with the appropriate format
// specifier and the expression is evaluated and passed to printf.
func (cg *Codegen) emitInterpolatedPrint(raw string, extraArgs []ast.Expression, addNewline bool) string {
	var formatParts []string
	var interpolatedArgs []interpolatedArg

	i := 0
	for i < len(raw) {
		if raw[i] == '{' {
			// Find closing brace
			j := i + 1
			for j < len(raw) && raw[j] != '}' {
				j++
			}
			if j < len(raw) {
				id := raw[i+1 : j]
				expr := buildInterpolatedExpr(id)
				interpolatedArgs = append(interpolatedArgs, interpolatedArg{rawID: id, expr: expr})

				// Determine specifier based on the type of the expression
				lt := cg.exprLLType(expr)
				// For unsigned check, use the base variable name
				baseName := id
				if dotIdx := strings.Index(id, "."); dotIdx >= 0 {
					baseName = id[:dotIdx]
				}
				isUnsigned := cg.isUnsignedVar(baseName)
				spec := "%s"
				switch lt {
				case "i1", "i8", "i16", "i32":
					if isUnsigned {
						spec = "%u"
					} else {
						spec = "%d"
					}
				case "i64":
					if isUnsigned {
						spec = "%llu"
					} else {
						spec = "%lld"
					}
				case "double":
					spec = "%f"
				default:
					if strings.HasSuffix(lt, "*") && lt != "i8*" {
						spec = "%p"
					} else {
						spec = "%s"
					}
				}

				formatParts = append(formatParts, spec)
				i = j + 1
				continue
			}
		}
		// Append literal character
		if len(formatParts) == 0 || strings.HasPrefix(formatParts[len(formatParts)-1], "%") {
			formatParts = append(formatParts, string(raw[i]))
		} else {
			formatParts[len(formatParts)-1] += string(raw[i])
		}
		i++
	}

	fmtStr := strings.Join(formatParts, "")
	if addNewline {
		fmtStr += "\n"
	}

	// Emit format string global
	cg.strCounter++
	globalName := fmt.Sprintf("@str.%d", cg.strCounter)
	escaped, length := escapeLLVMString(fmtStr)
	cg.writeStringGlobal("%s = private constant [%d x i8] c\"%s\\00\"\n", globalName, length, escaped)
	fmtReg := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds [%d x i8], [%d x i8]* %s, i64 0, i64 0\n",
		fmtReg, length, length, globalName)

	// Build argument list for printf: format string + interpolated vars + extra args
	var argStrs []string
	argStrs = append(argStrs, fmt.Sprintf("i8* %s", fmtReg))

	for _, ia := range interpolatedArgs {
		valReg := cg.emitExpression(ia.expr)
		lt := cg.exprLLType(ia.expr)
		baseName := ia.rawID
		if dotIdx := strings.Index(ia.rawID, "."); dotIdx >= 0 {
			baseName = ia.rawID[:dotIdx]
		}
		isUnsigned := cg.isUnsignedVar(baseName)

		argType := lt
		argReg := valReg

		switch lt {
		case "i1":
			zext := cg.nextReg()
			cg.writef("  %s = zext i1 %s to i32\n", zext, valReg)
			argReg = zext
			argType = "i32"
		case "i8":
			if isUnsigned {
				zext := cg.nextReg()
				cg.writef("  %s = zext i8 %s to i32\n", zext, valReg)
				argReg = zext
			} else {
				sext := cg.nextReg()
				cg.writef("  %s = sext i8 %s to i32\n", sext, valReg)
				argReg = sext
			}
			argType = "i32"
		case "i16":
			if isUnsigned {
				zext := cg.nextReg()
				cg.writef("  %s = zext i16 %s to i32\n", zext, valReg)
				argReg = zext
			} else {
				sext := cg.nextReg()
				cg.writef("  %s = sext i16 %s to i32\n", sext, valReg)
				argReg = sext
			}
			argType = "i32"
		}
		argStrs = append(argStrs, fmt.Sprintf("%s %s", argType, argReg))
	}

	for _, extra := range extraArgs {
		reg := cg.emitExpression(extra)
		argStrs = append(argStrs, fmt.Sprintf("i8* %s", reg))
	}

	reg := cg.nextReg()
	cg.writef("  %s = call i32 (i8*, ...) @printf(%s)\n", reg, strings.Join(argStrs, ", "))
	return reg
}

// emitAppend emits runtime array append: append(arr, value).
// It allocates a new buffer, copies old elements, stores the new value,
// and returns a pointer to the new array.
func (cg *Codegen) emitAppend(arrExpr, valExpr ast.Expression) string {
	oldArr := cg.emitExpression(arrExpr)
	valReg := cg.emitExpression(valExpr)
	valType := cg.exprLLType(valExpr)

	// For struct values, arrays store pointers to the struct, not the struct itself.
	// Heap-allocate a copy so the array element points to valid persistent memory.
	if strings.HasPrefix(valType, "%struct.") && !strings.HasSuffix(valType, "*") {
		sizeReg := cg.nextReg()
		cg.writef("  %s = getelementptr %s, %s* null, i32 1\n", sizeReg, valType, valType)
		sizeReg2 := cg.nextReg()
		cg.writef("  %s = ptrtoint %s* %s to i64\n", sizeReg2, valType, sizeReg)
		rawReg := cg.nextReg()
		cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawReg, sizeReg2)
		ptrReg := cg.nextReg()
		cg.writef("  %s = bitcast i8* %s to %s*\n", ptrReg, rawReg, valType)
		cg.writef("  store %s %s, %s* %s\n", valType, valReg, valType, ptrReg)
		valReg = ptrReg
		valType = valType + "*"
	}

	elemSizeI32 := cg.emitElemSize(valType)
	elemSizeI64 := cg.nextReg()
	cg.writef("  %s = zext i32 %s to i64\n", elemSizeI64, elemSizeI32)

	// --- Dynamic Heap Array Branch (check if null at runtime) ---
	isNull := cg.nextReg()
	// Cast oldArr to i8* first to check for null uniformly
	oldArrRaw := cg.nextReg()
	arrType := cg.exprLLType(arrExpr)
	cg.writef("  %s = bitcast %s %s to i8*\n", oldArrRaw, arrType, oldArr)
	cg.writef("  %s = icmp eq i8* %s, null\n", isNull, oldArrRaw)

	nullLabel := cg.nextLabel()
	notNullLabel := cg.nextLabel()
	mergeLabel := cg.nextLabel()

	cg.writef("  br i1 %s, label %%%s, label %%%s\n", isNull, nullLabel, notNullLabel)

	// --- NULL Sub-Branch (Create new array) ---
	cg.writeln(nullLabel + ":")
	bytesNullReg := cg.nextReg()
	cg.writef("  %s = add i64 %s, 8\n", bytesNullReg, elemSizeI64)
	rawNull := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawNull, bytesNullReg)
	// Store length 1 as i64 at offset 0
	lenPtrNull := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", lenPtrNull, rawNull)
	cg.writef("  store i64 1, i64* %s\n", lenPtrNull)
	// Elements start at rawNull + 8
	elemStartNull := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", elemStartNull, rawNull)
	typedElemStartNull := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", typedElemStartNull, elemStartNull, valType)
	cg.writef("  store %s %s, %s* %s\n", valType, valReg, valType, typedElemStartNull)
	cg.writef("  br label %%%s\n", mergeLabel)

	// --- NOT NULL Sub-Branch (Resize and append) ---
	cg.writeln(notNullLabel + ":")
	// Load current length from oldArrRaw - 8
	lenPtrOld := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 -8\n", lenPtrOld, oldArrRaw)
	typedLenPtrOld := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", typedLenPtrOld, lenPtrOld)
	oldLenReg := cg.nextReg()
	cg.writef("  %s = load i64, i64* %s\n", oldLenReg, typedLenPtrOld)

	// newLen = oldLen + 1
	newLenReg := cg.nextReg()
	cg.writef("  %s = add i64 %s, 1\n", newLenReg, oldLenReg)

	// Compute bytes needed: 8 + newLen * elemSize
	bytesNeededElem := cg.nextReg()
	cg.writef("  %s = mul i64 %s, %s\n", bytesNeededElem, newLenReg, elemSizeI64)
	bytesNeeded := cg.nextReg()
	cg.writef("  %s = add i64 %s, 8\n", bytesNeeded, bytesNeededElem)

	// Allocate new buffer
	rawNew := cg.nextReg()
	cg.writef("  %s = call i8* @Skink_rc_alloc(i64 %s)\n", rawNew, bytesNeeded)

	// Store new length as i64 in the new block
	lenPtrNew := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to i64*\n", lenPtrNew, rawNew)
	cg.writef("  store i64 %s, i64* %s\n", newLenReg, lenPtrNew)

	// Get element pointer for new block start offset 8
	elemStartNew := cg.nextReg()
	cg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 8\n", elemStartNew, rawNew)
	typedElemStartNew := cg.nextReg()
	cg.writef("  %s = bitcast i8* %s to %s*\n", typedElemStartNew, elemStartNew, valType)

	// Copy old elements from oldArr to typedElemStartNew
	// Loop over i from 0 to oldLen - 1
	loopCondLabel := cg.nextLabel()
	loopBodyLabel := cg.nextLabel()
	loopEndLabel := cg.nextLabel()

	copyIdxAlloca := cg.nextReg()
	cg.writef("  %s = alloca i64\n", copyIdxAlloca)
	cg.writef("  store i64 0, i64* %s\n", copyIdxAlloca)
	cg.writef("  br label %%%s\n", loopCondLabel)

	cg.writeln(loopCondLabel + ":")
	currIdx := cg.nextReg()
	cg.writef("  %s = load i64, i64* %s\n", currIdx, copyIdxAlloca)
	comp := cg.nextReg()
	cg.writef("  %s = icmp slt i64 %s, %s\n", comp, currIdx, oldLenReg)
	cg.writef("  br i1 %s, label %%%s, label %%%s\n", comp, loopBodyLabel, loopEndLabel)

	cg.writeln(loopBodyLabel + ":")
	// Load from oldArr at currIdx
	srcGep := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* %s, i64 %s\n", srcGep, valType, valType, oldArr, currIdx)
	srcVal := cg.nextReg()
	cg.writef("  %s = load %s, %s* %s\n", srcVal, valType, valType, srcGep)

	// Store to typedElemStartNew at currIdx
	dstGep := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* %s, i64 %s\n", dstGep, valType, valType, typedElemStartNew, currIdx)
	cg.writef("  store %s %s, %s* %s\n", valType, srcVal, valType, dstGep)

	// Increment idx
	nextIdx := cg.nextReg()
	cg.writef("  %s = add i64 %s, 1\n", nextIdx, currIdx)
	cg.writef("  store i64 %s, i64* %s\n", nextIdx, copyIdxAlloca)
	cg.writef("  br label %%%s\n", loopCondLabel)

	cg.writeln(loopEndLabel + ":")

	// Store new element at position typedElemStartNew[oldLenReg]
	endGep := cg.nextReg()
	cg.writef("  %s = getelementptr %s, %s* %s, i64 %s\n", endGep, valType, valType, typedElemStartNew, oldLenReg)
	cg.writef("  store %s %s, %s* %s\n", valType, valReg, valType, endGep)

	cg.writef("  br label %%%s\n", mergeLabel)

	// --- Merge ---
	cg.writeln(mergeLabel + ":")
	resPtr := cg.nextReg()
	cg.writef("  %s = phi %s* [ %s, %%%s ], [ %s, %%%s ]\n",
		resPtr, valType, typedElemStartNull, nullLabel, typedElemStartNew, loopEndLabel)

	// Update compile-time tracking to make it a heap-allocated array now!
	if id, ok := arrExpr.(*ast.Identifier); ok {
		allocaKey := id.Value
		if allocReg, _ := cg.resolveVar(id.Value); allocReg != "" {
			allocaKey = allocReg
		}
		cg.arraySizes[cg.currentFnName+":"+allocaKey] = arrayMeta{len: 0, elemType: valType, heapAlloc: true}
	}

	return resPtr
}

// exprLLType returns the LLVM IR type string for an expression.
// Unlike the standalone inferLLType, it can look up identifier types from
// the current scope stack.
func (cg *Codegen) exprLLType(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return "i32"
	case *ast.FloatLiteral:
		return "double"
	case *ast.BooleanLiteral:
		return "i1"
	case *ast.NilLiteral:
		return "i8*"
	case *ast.StringLiteral:
		return "i8*"
	case *ast.ArrayLiteral:
		if len(e.Elements) > 0 {
			return cg.exprLLType(e.Elements[0]) + "*"
		}
		if e.Type != nil {
			return llvmType(e.Type) + "*"
		}
		return "i32*"
	case *ast.MapLiteral:
		if len(e.Pairs) == 0 {
			return "i8*"
		}
		keyLL := cg.exprLLType(e.Pairs[0].Key)
		valLL := cg.exprLLType(e.Pairs[0].Value)
		return "%" + mapTypeName(keyLL, valLL) + "*"
	case *ast.MakeExpr:
		return llvmType(e.Type)
	case *ast.SpreadExpr:
		// Spread represents elements of an array, so return the element type.
		operandType := cg.exprLLType(e.Operand)
		if operandType == "i8*" {
			return "i32"
		}
		if strings.HasSuffix(operandType, "*") {
			return strings.TrimSuffix(operandType, "*")
		}
		return operandType
	case *ast.SetLiteral:
		if len(e.Elements) > 0 {
			et := cg.exprLLType(e.Elements[0])
			if et == "i8*" {
				return "%set.Str*"
			}
		}
		return "%set.Int*"
	case *ast.QueryExpr:
		selectLL := cg.exprLLType(e.Select.Expression)
		if selectLL == "" {
			return "i32*"
		}
		return selectLL + "*"
	case *ast.Identifier:
		// Check if this is a const — return the literal's type.
		if val, ok := cg.consts[e.Value]; ok {
			return cg.exprLLType(val)
		}
		_, lt := cg.resolveVar(e.Value)
		if lt == "" {
			lt = cg.globalVarTypes[e.Value]
			if lt == "" {
				// Could be a global function.
				if retTypes, ok := cg.fnRetTypes[e.Value]; ok {
					var ret string
					if len(retTypes) > 1 {
						ret = "{ " + strings.Join(retTypes, ", ") + " }"
					} else if len(retTypes) == 1 {
						ret = retTypes[0]
					} else {
						ret = "i32"
					}
					params := cg.fnParamTypes[e.Value]
					return ret + " (" + strings.Join(params, ", ") + ")*"
				}
				return "i32"
			}
		}
		return lt
	case *ast.InfixExpr:
		// Comparisons always produce i1.
		if isComparisonOp(e.Operator) {
			return "i1"
		}
		// Arithmetic: promote to double if either operand is float.
		if cg.exprLLType(e.Left) == "double" || cg.exprLLType(e.Right) == "double" {
			return "double"
		}
		return cg.exprLLType(e.Left)
	case *ast.PrefixExpr:
		switch e.Operator {
		case "!":
			return "i1"
		case "&":
			rt := cg.exprLLType(e.Right)
			if strings.HasPrefix(rt, "%struct.") && strings.HasSuffix(rt, "*") && !strings.HasSuffix(rt, "**") {
				return rt
			}
			return rt + "*"
		case "*":
			// Dereference: if operand is T*, return T
			operandType := cg.exprLLType(e.Right)
			if strings.HasSuffix(operandType, "*") {
				return strings.TrimSuffix(operandType, "*")
			}
			return "i32"
		case "<-":
			// Channel receive: returns the element type.
			chType := cg.exprASTType(e.Right)
			if ct, ok := chType.(*ast.ChanType); ok {
				return llvmType(ct.Elem)
			}
			return "i32"
		default:
			return cg.exprLLType(e.Right)
		}
	case *ast.CallExpr:
		fnName := ""
		if id, ok := e.Function.(*ast.Identifier); ok {
			fnName = id.Value
			// Type casts: int(expr), float(expr), bool(expr), string(expr)
			switch fnName {
			case "int", "uint":
				return "i32"
			case "int8", "uint8":
				return "i8"
			case "int16", "uint16":
				return "i16"
			case "int32", "uint32":
				return "i32"
			case "int64", "uint64":
				return "i64"
			case "float":
				return "double"
			case "bool":
				return "i1"
			case "string":
				return "i8*"
			case "error":
				return "{ i32, i8* }"
			}
		} else if fa, ok := e.Function.(*ast.FieldAccessExpr); ok {
			if id, ok2 := fa.Left.(*ast.Identifier); ok2 {
				alloca, lt := cg.resolveVar(id.Value)
				_, isGlobal := cg.globalVarTypes[id.Value]
				isService := cg.services[id.Value]
				isRuleset := cg.rulesets[id.Value]
				if alloca == "" && !isGlobal && !isService && !isRuleset {
					realModuleName := cg.resolveImportAlias(id.Value)
					if _, ok := cg.fnRetTypes[fa.Field]; ok {
						fnName = fa.Field
					} else if _, ok3 := cg.fnRetTypes[realModuleName+"."+fa.Field]; ok3 {
						fnName = realModuleName + "." + fa.Field
					} else if _, ok3 := cg.fnRetTypes[id.Value+"."+fa.Field]; ok3 {
						fnName = id.Value + "." + fa.Field
					}
				} else {
					if lt == "" {
						lt = cg.globalVarTypes[id.Value]
					}
					// Interface method dispatch: error.String() -> i8*
					if lt == "%error" && fa.Field == "String" {
						return "i8*"
					}
					structName := ""
					if strings.HasPrefix(lt, "%struct.") {
						structName = strings.TrimPrefix(lt, "%struct.")
						if idx := strings.Index(structName, "*"); idx > 0 {
							structName = structName[:idx]
						}
					}
					if structName != "" {
						if decl, ok := cg.structDecls[structName]; ok {
							structName = decl.Name
						}
						if _, ok := cg.fnRetTypes[structName+"."+fa.Field]; ok {
							fnName = structName + "." + fa.Field
						} else {
							parts := strings.Split(structName, ".")
							if len(parts) >= 2 {
								moduleName := strings.Join(parts[:len(parts)-1], ".")
								candidate := moduleName + "." + fa.Field
								if _, ok := cg.fnRetTypes[candidate]; ok {
									fnName = candidate
								}
							}
						}
					}
				}
			}
			// Handle method calls on non-identifier receivers (e.g., arr[i].Method()).
			if fnName == "" {
				lt := cg.exprLLType(fa.Left)
				// Interface method dispatch: error.String() -> i8*
				if lt == "%error" && fa.Field == "String" {
					return "i8*"
				}
				structName := ""
				if strings.HasPrefix(lt, "%struct.") {
					structName = strings.TrimPrefix(lt, "%struct.")
					if idx := strings.Index(structName, "*"); idx > 0 {
						structName = structName[:idx]
					}
				}
				if structName != "" {
					if decl, ok := cg.structDecls[structName]; ok {
						structName = decl.Name
					}
					if _, ok := cg.fnRetTypes[structName+"."+fa.Field]; ok {
						fnName = structName + "." + fa.Field
					} else {
						parts := strings.Split(structName, ".")
						if len(parts) >= 2 {
							moduleName := strings.Join(parts[:len(parts)-1], ".")
							candidate := moduleName + "." + fa.Field
							if _, ok := cg.fnRetTypes[candidate]; ok {
								fnName = candidate
							}
						}
					}
				}
			}
		}

		if fnName != "" {
			if retTypes, ok2 := cg.fnRetTypes[fnName]; ok2 && len(retTypes) > 1 {
				return "{ " + strings.Join(retTypes, ", ") + " }"
			} else if len(retTypes) == 1 {
				return retTypes[0]
			}
			// Tensor and Math builtins.
			switch fnName {
			case "tensor_ones", "tensor_zeros", "inv", "gradient", "cross", "eigenvalues":
				return "i8*"
			case "tensor_get", "sin", "cos", "tan", "sqrt", "pow", "det", "diff", "integrate", "dot", "norm":
				return "double"
			}
		}
		return "i32"
	case *ast.ErrorPropagationExpr:
		if call, ok := e.Expr.(*ast.CallExpr); ok {
			if id, ok2 := call.Function.(*ast.Identifier); ok2 {
				if retTypes, ok3 := cg.fnRetTypes[id.Value]; ok3 && len(retTypes) > 1 {
					return retTypes[0]
				} else if len(retTypes) == 1 {
					return retTypes[0]
				}
			}
		}
		return cg.exprLLType(e.Expr)
	case *ast.AsyncExpr:
		return "i8*"
	case *ast.AwaitExpr:
		astType := cg.exprASTType(e.Expr)
		if astType != nil {
			return llvmType(astType)
		}
		return cg.exprLLType(e.Expr)
	case *ast.IfExpr:
		return cg.ifExprLLType(e)
	case *ast.MatchExpr:
		return cg.matchExprLLType(e)
	case *ast.SliceExpr:
		arrType := cg.exprLLType(e.Left)
		if strings.HasSuffix(arrType, "*") {
			return arrType
		}
		return "i32*"
	case *ast.RangeExpr:
		return "{ i32, i32 }"
	case *ast.StructInitExpr:
		return "%struct." + strings.ReplaceAll(e.Type, ".", "_") + "*"
	case *ast.FieldAccessExpr:
		if astType := cg.exprASTType(e); astType != nil {
			if _, isFn := astType.(*ast.FunctionType); isFn {
				return "i8*"
			}
		}
		// Module-qualified constant or variable: module.constName
		if id, ok := e.Left.(*ast.Identifier); ok {
			alloca, _ := cg.resolveVar(id.Value)
			_, isGlobal := cg.globalVarTypes[id.Value]
			if alloca == "" && !isGlobal {
				realModuleName := cg.resolveImportAlias(id.Value)
				candidates := []string{realModuleName + "." + e.Field, id.Value + "." + e.Field}
				for _, fqName := range candidates {
					if val, ok := cg.consts[fqName]; ok {
						return cg.exprLLType(val)
					}
					if lt, ok := cg.globalVarTypes[fqName]; ok && lt != "" {
						return lt
					}
				}
			}
		}
		leftType := cg.exprLLType(e.Left)
		if leftType == "{ i32, i8* }" {
			if e.Field == "code" {
				return "i32"
			} else if e.Field == "message" {
				return "i8*"
			}
		}
		if leftType == "i8*" && e.Field == "T" {
			return "i8*"
		}
		if strings.HasPrefix(leftType, "%struct.") {
			structName := strings.TrimPrefix(leftType, "%struct.")
			if idx := strings.Index(structName, "*"); idx > 0 {
				structName = structName[:idx]
			}
			if layout, ok := cg.structLayouts[structName]; ok {
				for i, f := range layout.fields {
					if f == e.Field {
						ft := layout.fieldTypes[i]
						if ft != "i1" && ft != "i32" && ft != "i64" && strings.HasPrefix(ft, "i") && !strings.HasSuffix(ft, "*") {
							return "i32"
						}
						if strings.HasPrefix(ft, "%struct.") && !strings.HasSuffix(ft, "*") {
							return ft + "*"
						}
						return ft
					}
				}
			}
		}
		return "i32"
	case *ast.IndexExpr:
		// Array element type: if arr is T*, element is T.
		arrType := cg.exprLLType(e.Left)
		if arrType == "i8*" {
			return "i32"
		}
		// Map lookup: return the value type.
		if strings.HasPrefix(arrType, "%map_") && strings.HasSuffix(arrType, "*") {
			_, valLL, _ := parseMapType(arrType)
			if valLL != "" {
				return valLL
			}
		}
		elemType := "i32"
		if strings.HasSuffix(arrType, "*") {
			elemType = strings.TrimSuffix(arrType, "*")
		}
		// Skink arrays of structs store pointers to the struct.
		if strings.HasPrefix(elemType, "%struct.") && !strings.HasSuffix(elemType, "*") {
			elemType = elemType + "*"
		}
		return elemType
	case *ast.FnLiteral:
		var paramTypes []ast.Type
		for _, p := range e.Params {
			paramTypes = append(paramTypes, p.Type)
		}
		return llvmType(&ast.FunctionType{ParamTypes: paramTypes, ReturnType: e.ReturnType})
	}
	return "i32"
}

// ifExprLLType infers the LLVM type of an if expression by looking at
// the last expression in each branch.
func (cg *Codegen) ifExprLLType(e *ast.IfExpr) string {
	if e.Consequence == nil || len(e.Consequence.Statements) == 0 {
		return "i32"
	}
	last := e.Consequence.Statements[len(e.Consequence.Statements)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		return cg.exprLLType(es.Expr)
	}
	return "i32"
}

// matchExprLLType infers the LLVM type of a match expression by looking at
// the last expression in the first arm.
func (cg *Codegen) matchExprLLType(e *ast.MatchExpr) string {
	if len(e.Arms) == 0 {
		return "i32"
	}
	arm := e.Arms[0]
	if arm.Body == nil || len(arm.Body.Statements) == 0 {
		return "i32"
	}
	last := arm.Body.Statements[len(arm.Body.Statements)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		return cg.exprLLType(es.Expr)
	}
	return "i32"
}

// isComparisonOp returns true for operators that produce an i1 result.
func isComparisonOp(op string) bool {
	switch op {
	case "==", "!=", "<", ">", "<=", ">=", "in":
		return true
	}
	return false
}

// inferLLType performs best-effort type inference on an expression AST node.
// It is used when a variable declaration has no explicit type (:= syntax).
//
// Limitations:
//   - Assumes all identifiers are i32.
//   - Does not resolve function return types from symbol tables.
//   - Array element type is inferred from the first element.
func inferLLType(expr ast.Expression) string {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return "i32"
	case *ast.FloatLiteral:
		return "double"
	case *ast.BooleanLiteral:
		return "i1"
	case *ast.StringLiteral:
		return "i8*"
	case *ast.ArrayLiteral:
		if len(e.Elements) > 0 {
			return inferLLType(e.Elements[0]) + "*"
		}
		if e.Type != nil {
			return llvmType(e.Type) + "*"
		}
		return "i32*"
	case *ast.FromEndIndexExpr:
		return "i32"
	case *ast.SpreadExpr:
		operandType := inferLLType(e.Operand)
		if operandType == "i8*" {
			return "i32"
		}
		if strings.HasSuffix(operandType, "*") {
			return strings.TrimSuffix(operandType, "*")
		}
		return operandType
	case *ast.RangeExpr:
		return "{ i32, i32 }"
	case *ast.MapLiteral:
		if len(e.Pairs) == 0 {
			return "i8*"
		}
		keyLL := inferLLType(e.Pairs[0].Key)
		valLL := inferLLType(e.Pairs[0].Value)
		return "%" + mapTypeName(keyLL, valLL) + "*"
	case *ast.InfixExpr:
		if isComparisonOp(e.Operator) {
			return "i1"
		}
		return inferLLType(e.Left)
	case *ast.CallExpr:
		// Type casts return the target type; otherwise default to i32.
		if id, ok := e.Function.(*ast.Identifier); ok {
			switch id.Value {
			case "int", "uint":
				return "i32"
			case "int8", "uint8":
				return "i8"
			case "int16", "uint16":
				return "i16"
			case "int32", "uint32":
				return "i32"
			case "int64", "uint64":
				return "i64"
			case "float":
				return "double"
			case "bool":
				return "i1"
			case "string":
				return "i8*"
			case "error":
				return "%error"
			}
		} else if fa, ok := e.Function.(*ast.FieldAccessExpr); ok {
			if id, ok := fa.Left.(*ast.Identifier); ok && id.Value == "error" && fa.Field == "String" {
				return "i8*"
			}
		}
		return "i32"
	case *ast.Identifier:
		return "i32"
	case *ast.StructInitExpr:
		return "%struct." + strings.ReplaceAll(e.Type, ".", "_") + "*"
	case *ast.PrefixExpr:
		switch e.Operator {
		case "!":
			return "i1"
		case "&":
			return inferLLType(e.Right) + "*"
		case "*":
			operandType := inferLLType(e.Right)
			if strings.HasSuffix(operandType, "*") {
				return strings.TrimSuffix(operandType, "*")
			}
			return "i32"
		default:
			return inferLLType(e.Right)
		}
	case *ast.FnLiteral:
		var paramTypes []ast.Type
		for _, p := range e.Params {
			paramTypes = append(paramTypes, p.Type)
		}
		return llvmType(&ast.FunctionType{ParamTypes: paramTypes, ReturnType: e.ReturnType})
	}
	return "i32"
}

// inferType is a placeholder for better type inference.
func inferType(expr ast.Expression) ast.Type {
	return &ast.NamedType{Name: "int"}
}
