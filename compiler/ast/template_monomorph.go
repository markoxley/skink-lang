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

// Package ast (template_monomorph.go) implements template (duck-typed)
// monomorphization: replacing template declarations and instantiations
// with concrete specialized copies before code generation.
package ast

import (
	"fmt"
	"sort"
	"strings"
)

// TemplateMonomorphize replaces template-parameterized functions with
// concrete specialized copies and rewrites template method calls.
func TemplateMonomorphize(program *Program) *Program {
	tm := &tmplMono{program: program}
	tm.run()
	return tm.program
}

type tmplMono struct {
	program     *Program
	templates   map[string]*TemplateDecl
	structs     map[string]*StructDecl
	fnDecls     map[string]*FnDecl
	insts       map[string][]map[string]Type // fnName -> [mapping]
	specialized map[string]*FnDecl
	currentFn   *FnDecl
}

// mappingEqual reports whether two type parameter mappings are equivalent.
func mappingEqual(a, b map[string]Type) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if !typeEqual(v, bv) {
			return false
		}
	}
	return true
}

// typeEqual reports whether two types are structurally equal.
func typeEqual(a, b Type) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	at, aok := a.(*NamedType)
	bt, bok := b.(*NamedType)
	if aok && bok {
		return at.Name == bt.Name
	}
	ap, aok := a.(*PointerType)
	bp, bok := b.(*PointerType)
	if aok && bok {
		return typeEqual(ap.Elem, bp.Elem)
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// addInst records a new template instantiation for fnName if it is unique.
func (tm *tmplMono) addInst(fnName string, mapping map[string]Type) {
	for _, existing := range tm.insts[fnName] {
		if mappingEqual(existing, mapping) {
			return
		}
	}
	tm.insts[fnName] = append(tm.insts[fnName], mapping)
}

// run executes the template monomorphization pipeline.
func (tm *tmplMono) run() {
	tm.templates = make(map[string]*TemplateDecl)
	tm.structs = make(map[string]*StructDecl)
	tm.fnDecls = make(map[string]*FnDecl)
	tm.insts = make(map[string][]map[string]Type)
	tm.specialized = make(map[string]*FnDecl)

	for _, decl := range tm.program.Declarations {
		switch d := decl.(type) {
		case *TemplateDecl:
			tm.templates[d.Name] = d
		case *StructDecl:
			tm.structs[d.Name] = d
		case *FnDecl:
			tm.fnDecls[d.Name] = d
		}
	}

	changed := true
	for changed {
		changed = false
		for _, decl := range tm.program.Declarations {
			if fn, ok := decl.(*FnDecl); ok {
				tm.findInstsInFn(fn)
			}
		}

		for fnName, mappings := range tm.insts {
			for _, mapping := range mappings {
				mangled := mangleTmplFn(fnName, mapping)
				if _, done := tm.specialized[mangled]; done {
					continue
				}
				orig := tm.fnDecls[fnName]
				if orig == nil {
					continue
				}
				tm.specialized[mangled] = tm.specializeFn(orig, mapping)
				tm.program.Declarations = append(tm.program.Declarations, tm.specialized[mangled])
				changed = true
			}
		}
	}

	if len(tm.specialized) == 0 {
		return
	}
	tm.replaceCalls()
	tm.removeOriginals()
}

// findInstsInFn discovers template instantiations inside a single function.
func (tm *tmplMono) findInstsInFn(fn *FnDecl) {
	tm.currentFn = fn
	scopes := []map[string]Type{{}}
	for _, p := range fn.Params {
		if p.Type != nil {
			scopes[0][p.Name] = p.Type
		}
	}
	tm.findCallsInBlock(fn.Body, scopes, nil)
}

// findCallsInBlock discovers template calls inside a block.
func (tm *tmplMono) findCallsInBlock(block *BlockStmt, scopes []map[string]Type, tmplParams map[int]string) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		tm.findCallsInStmt(stmt, scopes, tmplParams)
	}
}

// findCallsInStmt discovers template calls inside a single statement.
func (tm *tmplMono) findCallsInStmt(stmt Statement, scopes []map[string]Type, tmplParams map[int]string) {
	switch s := stmt.(type) {
	case *VarStmt:
		if s.Type != nil {
			scopes[len(scopes)-1][s.Name] = s.Type
		} else if s.Value != nil {
			scopes[len(scopes)-1][s.Name] = tm.infer(s.Value, scopes)
		}
		if s.Value != nil {
			tm.findCallsInExpr(s.Value, scopes, tmplParams)
		}
	case *TupleVarStmt:
		if s.Value != nil {
			inferred := tm.infer(s.Value, scopes)
			if tuple, ok := inferred.(*TupleType); ok && len(tuple.Types) == len(s.Names) {
				for i, name := range s.Names {
					scopes[len(scopes)-1][name] = tuple.Types[i]
				}
			} else {
				for _, name := range s.Names {
					scopes[len(scopes)-1][name] = inferred
				}
			}
			tm.findCallsInExpr(s.Value, scopes, tmplParams)
		}
	case *AssignmentStmt:
		tm.findCallsInExpr(s.LValue, scopes, tmplParams)
		tm.findCallsInExpr(s.Value, scopes, tmplParams)
	case *ExprStmt:
		tm.findCallsInExpr(s.Expr, scopes, tmplParams)
	case *ReturnStmt:
		for _, v := range s.Values {
			tm.findCallsInExpr(v, scopes, tmplParams)
		}
	case *IfStmt:
		tm.findCallsInExpr(s.Condition, scopes, tmplParams)
		tm.findCallsInBlock(s.Consequence, scopes, tmplParams)
		if s.Alternative != nil {
			tm.findCallsInStmt(s.Alternative, scopes, tmplParams)
		}
	case *WhileStmt:
		tm.findCallsInExpr(s.Condition, scopes, tmplParams)
		tm.findCallsInBlock(s.Body, scopes, tmplParams)
	case *ForStmt:
		child := append(scopes, map[string]Type{})
		if s.Init != nil {
			tm.findCallsInStmt(s.Init, child, tmplParams)
		}
		if s.Condition != nil {
			tm.findCallsInExpr(s.Condition, child, tmplParams)
		}
		if s.Post != nil {
			tm.findCallsInStmt(s.Post, child, tmplParams)
		}
		if s.Iterator != nil {
			child[len(child)-1][s.Iterator.Variable] = tm.infer(s.Iterator.Iterable, scopes)
			tm.findCallsInExpr(s.Iterator.Iterable, child, tmplParams)
		}
		tm.findCallsInBlock(s.Body, child, tmplParams)
	case *BlockStmt:
		child := append(scopes, map[string]Type{})
		tm.findCallsInBlock(s, child, tmplParams)
	case *SwitchStmt:
		tm.findCallsInExpr(s.Subject, scopes, tmplParams)
		for _, c := range s.Cases {
			for _, v := range c.Values {
				tm.findCallsInExpr(v, scopes, tmplParams)
			}
			tm.findCallsInBlock(c.Body, append(scopes, map[string]Type{}), tmplParams)
		}
	case *SelectStmt:
		for _, c := range s.Cases {
			tm.findCallsInBlock(c.Body, append(scopes, map[string]Type{}), tmplParams)
		}
	case *DeferStmt:
		tm.findCallsInStmt(s.Statement, scopes, tmplParams)
	case *SpawnStmt:
		tm.findCallsInExpr(s.Call, scopes, tmplParams)
	case *WithStmt:
		child := append(scopes, map[string]Type{})
		child[len(child)-1][s.Name] = tm.infer(s.Value, scopes)
		tm.findCallsInExpr(s.Value, scopes, tmplParams)
		tm.findCallsInBlock(s.Body, child, tmplParams)
	case *ComptimeStmt:
		tm.findCallsInBlock(s.Body, append(scopes, map[string]Type{}), tmplParams)
	}
}

// findCallsInExpr discovers template calls inside an expression.
func (tm *tmplMono) findCallsInExpr(expr Expression, scopes []map[string]Type, tmplParams map[int]string) {
	switch e := expr.(type) {
	case *CallExpr:
		fnName := ""
		if id, ok := e.Function.(*Identifier); ok {
			fnName = id.Value
		} else if fa, ok := e.Function.(*FieldAccessExpr); ok {
			if id2, ok2 := fa.Left.(*Identifier); ok2 {
				fnName = id2.Value + "." + fa.Field
			}
		}
		if fnName != "" {
			if callee, exists := tm.fnDecls[fnName]; exists {
				calleeTmpl := make(map[int]string)
				for i, p := range callee.Params {
					if nt, ok := p.Type.(*NamedType); ok {
						if _, isTmpl := tm.templates[nt.Name]; isTmpl {
							calleeTmpl[i] = nt.Name
						} else if strings.Contains(nt.Name, ".") {
							parts := strings.SplitN(nt.Name, ".", 2)
							if len(parts) == 2 {
								if _, isTmpl := tm.templates[parts[1]]; isTmpl {
									calleeTmpl[i] = parts[1]
								}
							}
						}
					}
				}
				if len(calleeTmpl) > 0 {
					mapping := make(map[string]Type)
					for i, tmplName := range calleeTmpl {
						if i < len(e.Arguments) {
							concrete := tm.infer(e.Arguments[i], scopes)
							if concrete != nil {
								mapping[tmplName] = concrete
							}
						}
					}
					if len(mapping) > 0 {
						tm.addInst(fnName, mapping)
					}
				}
			}
		}
		for _, a := range e.Arguments {
			tm.findCallsInExpr(a, scopes, tmplParams)
		}
		if e.Function != nil {
			tm.findCallsInExpr(e.Function, scopes, tmplParams)
		}
	case *InfixExpr:
		tm.findCallsInExpr(e.Left, scopes, tmplParams)
		tm.findCallsInExpr(e.Right, scopes, tmplParams)
	case *PrefixExpr:
		tm.findCallsInExpr(e.Right, scopes, tmplParams)
	case *IndexExpr:
		tm.findCallsInExpr(e.Left, scopes, tmplParams)
		tm.findCallsInExpr(e.Index, scopes, tmplParams)
	case *SliceExpr:
		tm.findCallsInExpr(e.Left, scopes, tmplParams)
		if e.Start != nil {
			tm.findCallsInExpr(e.Start, scopes, tmplParams)
		}
		if e.End != nil {
			tm.findCallsInExpr(e.End, scopes, tmplParams)
		}
	case *FieldAccessExpr:
		tm.findCallsInExpr(e.Left, scopes, tmplParams)
	case *ArrayLiteral:
		for _, el := range e.Elements {
			tm.findCallsInExpr(el, scopes, tmplParams)
		}
	case *MapLiteral:
		for _, p := range e.Pairs {
			tm.findCallsInExpr(p.Key, scopes, tmplParams)
			tm.findCallsInExpr(p.Value, scopes, tmplParams)
		}
	case *SetLiteral:
		for _, el := range e.Elements {
			tm.findCallsInExpr(el, scopes, tmplParams)
		}
	case *StructInitExpr:
		for _, v := range e.Fields {
			tm.findCallsInExpr(v, scopes, tmplParams)
		}
	case *IfExpr:
		tm.findCallsInExpr(e.Condition, scopes, tmplParams)
		tm.findCallsInBlock(e.Consequence, scopes, tmplParams)
		if e.Alternative != nil {
			tm.findCallsInBlock(e.Alternative, scopes, tmplParams)
		}
	case *MatchExpr:
		tm.findCallsInExpr(e.Subject, scopes, tmplParams)
		for _, arm := range e.Arms {
			tm.findCallsInBlock(arm.Body, scopes, tmplParams)
		}
	case *RangeExpr:
		tm.findCallsInExpr(e.Start, scopes, tmplParams)
		tm.findCallsInExpr(e.End, scopes, tmplParams)
	case *SpreadExpr:
		tm.findCallsInExpr(e.Operand, scopes, tmplParams)
	case *AsyncExpr:
		tm.findCallsInExpr(e.Expr, scopes, tmplParams)
	case *AwaitExpr:
		tm.findCallsInExpr(e.Expr, scopes, tmplParams)
	case *ErrorPropagationExpr:
		tm.findCallsInExpr(e.Expr, scopes, tmplParams)
	}
}

// infer performs best-effort type inference for an expression given scopes.
func (tm *tmplMono) infer(expr Expression, scopes []map[string]Type) Type {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *Identifier:
		for i := len(scopes) - 1; i >= 0; i-- {
			if t, ok := scopes[i][e.Value]; ok {
				return t
			}
		}
		return nil
	case *StructInitExpr:
		return &NamedType{Name: e.Type}
	case *IntegerLiteral:
		return &NamedType{Name: "int"}
	case *BooleanLiteral:
		return &NamedType{Name: "bool"}
	case *StringLiteral:
		return &NamedType{Name: "string"}
	case *NilLiteral:
		return nil
	case *CallExpr:
		fnName := ""
		if id, ok := e.Function.(*Identifier); ok {
			fnName = id.Value
		} else if fa, ok := e.Function.(*FieldAccessExpr); ok {
			if id2, ok2 := fa.Left.(*Identifier); ok2 {
				fnName = id2.Value + "." + fa.Field
			}
		}
		if fnName != "" {
			if fn, exists := tm.fnDecls[fnName]; exists && fn.ReturnType != nil {
				return fn.ReturnType
			}
		}
		return nil
	case *InfixExpr:
		t := tm.infer(e.Left, scopes)
		if t != nil {
			return t
		}
		return tm.infer(e.Right, scopes)
	case *PrefixExpr:
		if e.Operator == "&" {
			t := tm.infer(e.Right, scopes)
			if t != nil {
				return &PointerType{Elem: t}
			}
			return nil
		}
		return tm.infer(e.Right, scopes)
	case *FieldAccessExpr:
		lt := tm.infer(e.Left, scopes)
		if lt == nil {
			return nil
		}
		if nt, ok := lt.(*NamedType); ok {
			if decl, exists := tm.structs[nt.Name]; exists {
				for _, f := range decl.Fields {
					if f.Name == e.Field {
						return f.Type
					}
				}
			}
		}
		return nil
	case *IndexExpr:
		lt := tm.infer(e.Left, scopes)
		if lt == nil {
			return nil
		}
		if arr, ok := lt.(*ArrayType); ok {
			return arr.Elem
		}
		if m, ok := lt.(*MapType); ok {
			return m.Elem
		}
		return nil
	}
	return nil
}

// specializeFn creates a concrete copy of a template function.
func (tm *tmplMono) specializeFn(decl *FnDecl, mapping map[string]Type) *FnDecl {
	typeMap := make(map[string]Type)
	for k, v := range mapping {
		typeMap[k] = v
	}
	newDecl := &FnDecl{
		Token:      decl.Token,
		Pub:        decl.Pub,
		Name:       mangleTmplFn(decl.Name, mapping),
		ReturnType: substituteType(decl.ReturnType, typeMap),
		Body:       tm.subBlock(decl.Body, typeMap),
		Doc:        decl.Doc,
	}
	for _, p := range decl.Params {
		newDecl.Params = append(newDecl.Params, &Param{
			Name: p.Name,
			Type: substituteType(p.Type, typeMap),
		})
	}
	return newDecl
}

// subBlock returns a block copy with template types substituted.
func (tm *tmplMono) subBlock(block *BlockStmt, mapping map[string]Type) *BlockStmt {
	if block == nil {
		return nil
	}
	newBlock := &BlockStmt{Token: block.Token}
	for _, stmt := range block.Statements {
		newBlock.Statements = append(newBlock.Statements, tm.subStmt(stmt, mapping))
	}
	return newBlock
}

// subStmt returns a statement copy with template types substituted.
func (tm *tmplMono) subStmt(stmt Statement, mapping map[string]Type) Statement {
	switch s := stmt.(type) {
	case *VarStmt:
		return &VarStmt{Token: s.Token, Name: s.Name, Type: substituteType(s.Type, mapping), Value: tm.subExpr(s.Value, mapping), Implicit: s.Implicit}
	case *TupleVarStmt:
		return &TupleVarStmt{Token: s.Token, Names: append([]string(nil), s.Names...), Value: tm.subExpr(s.Value, mapping)}
	case *AssignmentStmt:
		return &AssignmentStmt{Token: s.Token, LValue: s.LValue, Operator: s.Operator, Value: tm.subExpr(s.Value, mapping)}
	case *ReturnStmt:
		newRet := &ReturnStmt{Token: s.Token}
		for _, v := range s.Values {
			newRet.Values = append(newRet.Values, tm.subExpr(v, mapping))
		}
		return newRet
	case *IfStmt:
		return &IfStmt{Token: s.Token, Condition: tm.subExpr(s.Condition, mapping), Consequence: tm.subBlock(s.Consequence, mapping), Alternative: tm.subStmt(s.Alternative, mapping)}
	case *WhileStmt:
		return &WhileStmt{Token: s.Token, Condition: tm.subExpr(s.Condition, mapping), Body: tm.subBlock(s.Body, mapping)}
	case *ForStmt:
		newFor := &ForStmt{Token: s.Token, Body: tm.subBlock(s.Body, mapping)}
		if s.Init != nil {
			newFor.Init = tm.subStmt(s.Init, mapping)
		}
		if s.Condition != nil {
			newFor.Condition = tm.subExpr(s.Condition, mapping)
		}
		if s.Post != nil {
			newFor.Post = tm.subStmt(s.Post, mapping)
		}
		if s.Iterator != nil {
			newFor.Iterator = &ForInStmt{Variable: s.Iterator.Variable, Iterable: tm.subExpr(s.Iterator.Iterable, mapping), IsRange: s.Iterator.IsRange}
		}
		return newFor
	case *BlockStmt:
		return tm.subBlock(s, mapping)
	case *ExprStmt:
		return &ExprStmt{Token: s.Token, Expr: tm.subExpr(s.Expr, mapping)}
	case *BreakStmt, *ContinueStmt:
		return s
	case *DeferStmt:
		return &DeferStmt{Token: s.Token, Statement: tm.subStmt(s.Statement, mapping)}
	case *SpawnStmt:
		return &SpawnStmt{Token: s.Token, Call: tm.subExpr(s.Call, mapping)}
	case *ComptimeStmt:
		return &ComptimeStmt{Token: s.Token, Body: tm.subBlock(s.Body, mapping)}
	case *WithStmt:
		return &WithStmt{Token: s.Token, Name: s.Name, Value: tm.subExpr(s.Value, mapping), Body: tm.subBlock(s.Body, mapping)}
	case *SelectStmt:
		newSel := &SelectStmt{Token: s.Token}
		for i := range s.Cases {
			newSel.Cases = append(newSel.Cases, SelectCase{Condition: tm.subExpr(s.Cases[i].Condition, mapping), IsDefault: s.Cases[i].IsDefault, Body: tm.subBlock(s.Cases[i].Body, mapping)})
		}
		return newSel
	case *SwitchStmt:
		newSw := &SwitchStmt{Token: s.Token, Subject: tm.subExpr(s.Subject, mapping)}
		for i := range s.Cases {
			var newVals []Expression
			for _, v := range s.Cases[i].Values {
				newVals = append(newVals, tm.subExpr(v, mapping))
			}
			newSw.Cases = append(newSw.Cases, SwitchCase{Token: s.Cases[i].Token, Values: newVals, IsDefault: s.Cases[i].IsDefault, Body: tm.subBlock(s.Cases[i].Body, mapping)})
		}
		return newSw
	default:
		return s
	}
}

// subExpr returns an expression copy with template types substituted.
func (tm *tmplMono) subExpr(expr Expression, mapping map[string]Type) Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BooleanLiteral, *NilLiteral:
		return e
	case *ArrayLiteral:
		newArr := &ArrayLiteral{Token: e.Token}
		for _, el := range e.Elements {
			newArr.Elements = append(newArr.Elements, tm.subExpr(el, mapping))
		}
		return newArr
	case *MapLiteral:
		newMap := &MapLiteral{Token: e.Token}
		for _, pair := range e.Pairs {
			newMap.Pairs = append(newMap.Pairs, MapPair{Key: tm.subExpr(pair.Key, mapping), Value: tm.subExpr(pair.Value, mapping)})
		}
		return newMap
	case *SetLiteral:
		newSet := &SetLiteral{Token: e.Token}
		for _, el := range e.Elements {
			newSet.Elements = append(newSet.Elements, tm.subExpr(el, mapping))
		}
		return newSet
	case *StructInitExpr:
		newInit := &StructInitExpr{Token: e.Token, Type: e.Type}
		newInit.Fields = make(map[string]Expression)
		for k, v := range e.Fields {
			newInit.Fields[k] = tm.subExpr(v, mapping)
		}
		return newInit
	case *CallExpr:
		newCall := &CallExpr{Token: e.Token, Function: tm.subExpr(e.Function, mapping)}
		for _, a := range e.Arguments {
			newCall.Arguments = append(newCall.Arguments, tm.subExpr(a, mapping))
		}
		return newCall
	case *InfixExpr:
		return &InfixExpr{Token: e.Token, Left: tm.subExpr(e.Left, mapping), Operator: e.Operator, Right: tm.subExpr(e.Right, mapping)}
	case *PrefixExpr:
		return &PrefixExpr{Token: e.Token, Operator: e.Operator, Right: tm.subExpr(e.Right, mapping)}
	case *IndexExpr:
		return &IndexExpr{Token: e.Token, Left: tm.subExpr(e.Left, mapping), Index: tm.subExpr(e.Index, mapping)}
	case *SliceExpr:
		newSlice := &SliceExpr{Token: e.Token, Left: tm.subExpr(e.Left, mapping)}
		if e.Start != nil {
			newSlice.Start = tm.subExpr(e.Start, mapping)
		}
		if e.End != nil {
			newSlice.End = tm.subExpr(e.End, mapping)
		}
		return newSlice
	case *FieldAccessExpr:
		newLeft := tm.subExpr(e.Left, mapping)
		// Rewrite template method calls to concrete method calls.
		if _, ok := e.Left.(*Identifier); ok {
			leftType := tm.exprType(e.Left)
			if lnt, ok := leftType.(*NamedType); ok {
				if _, isTmpl := tm.templates[lnt.Name]; isTmpl {
					if concrete, ok := mapping[lnt.Name]; ok {
						var nt *NamedType
						if c, ok := concrete.(*NamedType); ok {
							nt = c
						} else if ptr, ok := concrete.(*PointerType); ok {
							if c, ok := ptr.Elem.(*NamedType); ok {
								nt = c
							}
						}
						if nt != nil {
							return &FieldAccessExpr{Token: e.Token, Left: newLeft, Field: nt.Name + "." + e.Field}
						}
					}
				}
			}
		}
		return &FieldAccessExpr{Token: e.Token, Left: newLeft, Field: e.Field}
	case *IfExpr:
		return &IfExpr{Token: e.Token, Condition: tm.subExpr(e.Condition, mapping), Consequence: tm.subBlock(e.Consequence, mapping), Alternative: tm.subBlock(e.Alternative, mapping)}
	case *MatchExpr:
		newMatch := &MatchExpr{Token: e.Token, Subject: tm.subExpr(e.Subject, mapping)}
		for _, arm := range e.Arms {
			newMatch.Arms = append(newMatch.Arms, &MatchArm{Token: arm.Token, Pattern: tm.subExpr(arm.Pattern, mapping), Guard: tm.subExpr(arm.Guard, mapping), Body: tm.subBlock(arm.Body, mapping)})
		}
		return newMatch
	case *RangeExpr:
		return &RangeExpr{Token: e.Token, Start: tm.subExpr(e.Start, mapping), End: tm.subExpr(e.End, mapping)}
	case *SpreadExpr:
		return &SpreadExpr{Token: e.Token, Operand: tm.subExpr(e.Operand, mapping)}
	case *AsyncExpr:
		return &AsyncExpr{Token: e.Token, Expr: tm.subExpr(e.Expr, mapping)}
	case *AwaitExpr:
		return &AwaitExpr{Token: e.Token, Expr: tm.subExpr(e.Expr, mapping)}
	case *ErrorPropagationExpr:
		return &ErrorPropagationExpr{Token: e.Token, Expr: tm.subExpr(e.Expr, mapping)}
	case *FromEndIndexExpr:
		return &FromEndIndexExpr{Token: e.Token, Operand: tm.subExpr(e.Operand, mapping)}
	case *FnLiteral:
		return e
	default:
		return e
	}
}

// exprType returns the type of an expression for template dispatch.
func (tm *tmplMono) exprType(expr Expression) Type {
	switch e := expr.(type) {
	case *Identifier:
		if _, isStruct := tm.structs[e.Value]; isStruct {
			return &NamedType{Name: e.Value}
		}
		return nil
	case *StructInitExpr:
		return &NamedType{Name: e.Type}
	}
	return nil
}

// substituteType replaces template parameters with concrete types in a Type.
func substituteType(t Type, mapping map[string]Type) Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *NamedType:
		if concrete, ok := mapping[tt.Name]; ok {
			return concrete
		}
		// If name is module-qualified, also check unqualified name.
		if strings.Contains(tt.Name, ".") {
			parts := strings.SplitN(tt.Name, ".", 2)
			if len(parts) == 2 {
				if concrete, ok := mapping[parts[1]]; ok {
					return concrete
				}
			}
		}
		return t
	case *PointerType:
		return &PointerType{Elem: substituteType(tt.Elem, mapping)}
	case *ArrayType:
		return &ArrayType{Elem: substituteType(tt.Elem, mapping)}
	case *SetType:
		return &SetType{Elem: substituteType(tt.Elem, mapping)}
	case *MapType:
		return &MapType{Key: substituteType(tt.Key, mapping), Elem: substituteType(tt.Elem, mapping)}
	case *ChanType:
		return &ChanType{Elem: substituteType(tt.Elem, mapping)}
	case *TensorType:
		return &TensorType{Elem: substituteType(tt.Elem, mapping)}
	case *TupleType:
		types := make([]Type, len(tt.Types))
		for i, ty := range tt.Types {
			types[i] = substituteType(ty, mapping)
		}
		return &TupleType{Types: types}
	case *FunctionType:
		params := make([]Type, len(tt.ParamTypes))
		for i, pt := range tt.ParamTypes {
			params[i] = substituteType(pt, mapping)
		}
		var ret Type
		if tt.ReturnType != nil {
			ret = substituteType(tt.ReturnType, mapping)
		}
		return &FunctionType{ParamTypes: params, ReturnType: ret}
	default:
		return t
	}
}

// mangleTmplFn produces a concrete name from a template function and mapping.
func mangleTmplFn(name string, mapping map[string]Type) string {
	var keys []string
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := name
	for _, k := range keys {
		result += "_" + typeKey(mapping[k])
	}
	return result
}

// typeKey returns a stable string key for a type.
func typeKey(t Type) string {
	if t == nil {
		return "nil"
	}
	switch tt := t.(type) {
	case *NamedType:
		return tt.Name
	case *PointerType:
		return "ptr_" + typeKey(tt.Elem)
	case *ArrayType:
		return "arr_" + typeKey(tt.Elem)
	case *SetType:
		return "set_" + typeKey(tt.Elem)
	case *MapType:
		return "map_" + typeKey(tt.Key) + "_" + typeKey(tt.Elem)
	case *ChanType:
		return "chan_" + typeKey(tt.Elem)
	case *TensorType:
		return "tensor_" + typeKey(tt.Elem)
	case *TupleType:
		var parts []string
		for _, ty := range tt.Types {
			parts = append(parts, typeKey(ty))
		}
		return "tup_" + strings.Join(parts, "_")
	default:
		return fmt.Sprintf("%v", t)
	}
}

// removeOriginals strips un-specialized template-param functions from the program.
func (tm *tmplMono) removeOriginals() {
	var newDecls []Declaration
	for _, decl := range tm.program.Declarations {
		if fn, ok := decl.(*FnDecl); ok {
			tmplParams := make(map[int]string)
			for i, p := range fn.Params {
				if p.Type == nil {
					continue
				}
				if nt, ok := p.Type.(*NamedType); ok {
					if _, isTmpl := tm.templates[nt.Name]; isTmpl {
						tmplParams[i] = nt.Name
					} else if strings.Contains(nt.Name, ".") {
						parts := strings.SplitN(nt.Name, ".", 2)
						if len(parts) == 2 {
							if _, isTmpl := tm.templates[parts[1]]; isTmpl {
								tmplParams[i] = parts[1]
							}
						}
					}
				}
			}
			if len(tmplParams) > 0 {
				continue // skip original template-param function
			}
		}
		newDecls = append(newDecls, decl)
	}
	tm.program.Declarations = newDecls
}

// resolveFnName resolves a possibly unqualified function name to its fully-qualified form.
func (tm *tmplMono) resolveFnName(fnName string) string {
	if fnName == "" {
		return ""
	}
	if _, exists := tm.fnDecls[fnName]; exists {
		return fnName
	}
	if !strings.Contains(fnName, ".") && tm.currentFn != nil {
		parts := strings.SplitN(tm.currentFn.Name, ".", 2)
		if len(parts) == 2 {
			fqName := parts[0] + "." + fnName
			if _, exists := tm.fnDecls[fqName]; exists {
				return fqName
			}
		}
	}
	return fnName
}

// replaceCalls rewrites template method calls to concrete specialized calls.
func (tm *tmplMono) replaceCalls() {
	for _, decl := range tm.program.Declarations {
		if fn, ok := decl.(*FnDecl); ok {
			tm.currentFn = fn
			scopes := []map[string]Type{{}}
			for _, p := range fn.Params {
				if p.Type != nil {
					scopes[0][p.Name] = p.Type
				}
			}
			tm.replaceInBlock(fn.Body, scopes)
		}
	}
}

// replaceInBlock rewrites template calls inside a block.
func (tm *tmplMono) replaceInBlock(block *BlockStmt, scopes []map[string]Type) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		tm.replaceInStmt(stmt, scopes)
	}
}

// replaceInStmt rewrites template calls inside a single statement.
func (tm *tmplMono) replaceInStmt(stmt Statement, scopes []map[string]Type) {
	switch s := stmt.(type) {
	case *VarStmt:
		if s.Type != nil {
			scopes[len(scopes)-1][s.Name] = s.Type
		} else if s.Value != nil {
			scopes[len(scopes)-1][s.Name] = tm.infer(s.Value, scopes)
		}
		if s.Value != nil {
			s.Value = tm.replaceInExpr(s.Value, scopes)
		}
	case *TupleVarStmt:
		if s.Value != nil {
			inferred := tm.infer(s.Value, scopes)
			if tuple, ok := inferred.(*TupleType); ok && len(tuple.Types) == len(s.Names) {
				for i, name := range s.Names {
					scopes[len(scopes)-1][name] = tuple.Types[i]
				}
			} else {
				for _, name := range s.Names {
					scopes[len(scopes)-1][name] = inferred
				}
			}
			s.Value = tm.replaceInExpr(s.Value, scopes)
		}
	case *AssignmentStmt:
		s.LValue = tm.replaceInExpr(s.LValue, scopes)
		s.Value = tm.replaceInExpr(s.Value, scopes)
	case *ExprStmt:
		s.Expr = tm.replaceInExpr(s.Expr, scopes)
	case *ReturnStmt:
		for i, v := range s.Values {
			s.Values[i] = tm.replaceInExpr(v, scopes)
		}
	case *IfStmt:
		s.Condition = tm.replaceInExpr(s.Condition, scopes)
		tm.replaceInBlock(s.Consequence, scopes)
		if s.Alternative != nil {
			tm.replaceInStmt(s.Alternative, scopes)
		}
	case *WhileStmt:
		s.Condition = tm.replaceInExpr(s.Condition, scopes)
		tm.replaceInBlock(s.Body, scopes)
	case *ForStmt:
		child := append(scopes, map[string]Type{})
		if s.Init != nil {
			tm.replaceInStmt(s.Init, child)
		}
		if s.Condition != nil {
			s.Condition = tm.replaceInExpr(s.Condition, child)
		}
		if s.Post != nil {
			tm.replaceInStmt(s.Post, child)
		}
		if s.Iterator != nil {
			child[len(child)-1][s.Iterator.Variable] = tm.infer(s.Iterator.Iterable, scopes)
			s.Iterator.Iterable = tm.replaceInExpr(s.Iterator.Iterable, child)
		}
		tm.replaceInBlock(s.Body, child)
	case *BlockStmt:
		child := append(scopes, map[string]Type{})
		tm.replaceInBlock(s, child)
	case *SwitchStmt:
		s.Subject = tm.replaceInExpr(s.Subject, scopes)
		for i := range s.Cases {
			for j, v := range s.Cases[i].Values {
				s.Cases[i].Values[j] = tm.replaceInExpr(v, scopes)
			}
			tm.replaceInBlock(s.Cases[i].Body, append(scopes, map[string]Type{}))
		}
	case *SelectStmt:
		for i := range s.Cases {
			tm.replaceInBlock(s.Cases[i].Body, append(scopes, map[string]Type{}))
		}
	case *DeferStmt:
		tm.replaceInStmt(s.Statement, scopes)
	case *SpawnStmt:
		s.Call = tm.replaceInExpr(s.Call, scopes)
	case *WithStmt:
		child := append(scopes, map[string]Type{})
		child[len(child)-1][s.Name] = tm.infer(s.Value, scopes)
		s.Value = tm.replaceInExpr(s.Value, scopes)
		tm.replaceInBlock(s.Body, child)
	case *ComptimeStmt:
		tm.replaceInBlock(s.Body, append(scopes, map[string]Type{}))
	}
}

// replaceInExpr rewrites template calls inside an expression.
func (tm *tmplMono) replaceInExpr(expr Expression, scopes []map[string]Type) Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *CallExpr:
		fnName := ""
		if id, ok := e.Function.(*Identifier); ok {
			fnName = id.Value
		} else if fa, ok := e.Function.(*FieldAccessExpr); ok {
			if id2, ok2 := fa.Left.(*Identifier); ok2 {
				fnName = id2.Value + "." + fa.Field
			}
		}
		if fnName != "" {
			fqName := tm.resolveFnName(fnName)
			if callee, exists := tm.fnDecls[fqName]; exists {
				calleeTmpl := make(map[int]string)
				for i, p := range callee.Params {
					if nt, ok := p.Type.(*NamedType); ok {
						if _, isTmpl := tm.templates[nt.Name]; isTmpl {
							calleeTmpl[i] = nt.Name
						} else if strings.Contains(nt.Name, ".") {
							parts := strings.SplitN(nt.Name, ".", 2)
							if len(parts) == 2 {
								if _, isTmpl := tm.templates[parts[1]]; isTmpl {
									calleeTmpl[i] = parts[1]
								}
							}
						}
					}
				}
				if len(calleeTmpl) > 0 {
					mapping := make(map[string]Type)
					for i, tmplName := range calleeTmpl {
						if i < len(e.Arguments) {
							concrete := tm.infer(e.Arguments[i], scopes)
							if concrete != nil {
								mapping[tmplName] = concrete
							}
						}
					}
					if len(mapping) > 0 {
						mangled := mangleTmplFn(fqName, mapping)
						e.Function = &Identifier{Token: e.Token, Value: mangled}
					}
				}
			}
		}
		newCall := &CallExpr{Token: e.Token, Function: tm.replaceInExpr(e.Function, scopes)}
		for _, a := range e.Arguments {
			newCall.Arguments = append(newCall.Arguments, tm.replaceInExpr(a, scopes))
		}
		return newCall
	case *InfixExpr:
		return &InfixExpr{Token: e.Token, Left: tm.replaceInExpr(e.Left, scopes), Operator: e.Operator, Right: tm.replaceInExpr(e.Right, scopes)}
	case *PrefixExpr:
		return &PrefixExpr{Token: e.Token, Operator: e.Operator, Right: tm.replaceInExpr(e.Right, scopes)}
	case *IndexExpr:
		return &IndexExpr{Token: e.Token, Left: tm.replaceInExpr(e.Left, scopes), Index: tm.replaceInExpr(e.Index, scopes)}
	case *SliceExpr:
		newSlice := &SliceExpr{Token: e.Token, Left: tm.replaceInExpr(e.Left, scopes)}
		if e.Start != nil {
			newSlice.Start = tm.replaceInExpr(e.Start, scopes)
		}
		if e.End != nil {
			newSlice.End = tm.replaceInExpr(e.End, scopes)
		}
		return newSlice
	case *FieldAccessExpr:
		return &FieldAccessExpr{Token: e.Token, Left: tm.replaceInExpr(e.Left, scopes), Field: e.Field}
	case *ArrayLiteral:
		newArr := &ArrayLiteral{Token: e.Token}
		for _, el := range e.Elements {
			newArr.Elements = append(newArr.Elements, tm.replaceInExpr(el, scopes))
		}
		return newArr
	case *MapLiteral:
		newMap := &MapLiteral{Token: e.Token}
		for _, pair := range e.Pairs {
			newMap.Pairs = append(newMap.Pairs, MapPair{Key: tm.replaceInExpr(pair.Key, scopes), Value: tm.replaceInExpr(pair.Value, scopes)})
		}
		return newMap
	case *SetLiteral:
		newSet := &SetLiteral{Token: e.Token}
		for _, el := range e.Elements {
			newSet.Elements = append(newSet.Elements, tm.replaceInExpr(el, scopes))
		}
		return newSet
	case *StructInitExpr:
		newInit := &StructInitExpr{Token: e.Token, Type: e.Type}
		newInit.Fields = make(map[string]Expression)
		for k, v := range e.Fields {
			newInit.Fields[k] = tm.replaceInExpr(v, scopes)
		}
		return newInit
	case *IfExpr:
		return &IfExpr{Token: e.Token, Condition: tm.replaceInExpr(e.Condition, scopes), Consequence: tm.subBlock(e.Consequence, nil), Alternative: tm.subBlock(e.Alternative, nil)}
	case *MatchExpr:
		newMatch := &MatchExpr{Token: e.Token, Subject: tm.replaceInExpr(e.Subject, scopes)}
		for _, arm := range e.Arms {
			newMatch.Arms = append(newMatch.Arms, &MatchArm{Token: arm.Token, Pattern: tm.replaceInExpr(arm.Pattern, scopes), Guard: tm.replaceInExpr(arm.Guard, scopes), Body: tm.subBlock(arm.Body, nil)})
		}
		return newMatch
	case *RangeExpr:
		return &RangeExpr{Token: e.Token, Start: tm.replaceInExpr(e.Start, scopes), End: tm.replaceInExpr(e.End, scopes)}
	case *SpreadExpr:
		return &SpreadExpr{Token: e.Token, Operand: tm.replaceInExpr(e.Operand, scopes)}
	case *AsyncExpr:
		return &AsyncExpr{Token: e.Token, Expr: tm.replaceInExpr(e.Expr, scopes)}
	case *AwaitExpr:
		return &AwaitExpr{Token: e.Token, Expr: tm.replaceInExpr(e.Expr, scopes)}
	case *ErrorPropagationExpr:
		return &ErrorPropagationExpr{Token: e.Token, Expr: tm.replaceInExpr(e.Expr, scopes)}
	case *FromEndIndexExpr:
		return &FromEndIndexExpr{Token: e.Token, Operand: tm.replaceInExpr(e.Operand, scopes)}
	case *FnLiteral:
		return e
	default:
		return e
	}
}
