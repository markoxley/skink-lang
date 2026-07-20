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

// mono_collect.go implements the collection phase of generic monomorphization:
// scanning the AST to discover all generic instantiations that need concrete
// specializations.
package ast

import "strings"

// collectInDecl scans a single declaration for generic instantiations.
func (m *monomorphizer) collectInDecl(decl Declaration) {
	originalModule := m.currentModule
	originalParams := m.currentParams
	m.currentParams = make(map[string]bool)
	for k, v := range originalParams {
		m.currentParams[k] = v
	}

	var declName string
	switch d := decl.(type) {
	case *FnDecl:
		declName = d.Name
		for _, tp := range d.TypeParams {
			m.currentParams[tp] = true
		}
	case *StructDecl:
		declName = d.Name
		for _, tp := range d.TypeParams {
			m.currentParams[tp] = true
		}
	case *VarDecl:
		declName = d.Name
	case *ConstDecl:
		declName = d.Name
	}
	if idx := strings.Index(declName, "."); idx > 0 {
		m.currentModule = declName[:idx]
	}
	defer func() {
		m.currentModule = originalModule
		m.currentParams = originalParams
	}()

	switch d := decl.(type) {
	case *FnDecl:
		for _, p := range d.Params {
			m.collectInType(p.Type)
		}
		m.collectInType(d.ReturnType)
		m.collectInBlock(d.Body)
	case *StructDecl:
		for _, f := range d.Fields {
			m.collectInType(f.Type)
		}
		for _, md := range d.Methods {
			m.collectInDecl(md)
		}
	case *VarDecl:
		m.collectInType(d.Type)
		if d.Value != nil {
			m.collectInExpr(d.Value)
		}
	case *ConstDecl:
		if d.Value != nil {
			m.collectInExpr(d.Value)
		}
	}
}

// collectInBlock scans a statement block for generic instantiations.
func (m *monomorphizer) collectInBlock(block *BlockStmt) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		m.collectInStmt(stmt)
	}
}

// collectInStmt scans a single statement for generic instantiations.
func (m *monomorphizer) collectInStmt(stmt Statement) {
	switch s := stmt.(type) {
	case *VarStmt:
		m.collectInType(s.Type)
		if s.Value != nil {
			m.collectInExpr(s.Value)
		}
	case *TupleVarStmt:
		if s.Value != nil {
			m.collectInExpr(s.Value)
		}
	case *AssignmentStmt:
		m.collectInExpr(s.Value)
	case *ReturnStmt:
		for _, v := range s.Values {
			m.collectInExpr(v)
		}
	case *IfStmt:
		m.collectInExpr(s.Condition)
		m.collectInBlock(s.Consequence)
		if alt, ok := s.Alternative.(*BlockStmt); ok {
			m.collectInBlock(alt)
		}
	case *WhileStmt:
		m.collectInExpr(s.Condition)
		m.collectInBlock(s.Body)
	case *ForStmt:
		if s.Init != nil {
			m.collectInStmt(s.Init)
		}
		if s.Condition != nil {
			m.collectInExpr(s.Condition)
		}
		if s.Post != nil {
			m.collectInStmt(s.Post)
		}
		if s.Iterator != nil {
			m.collectInExpr(s.Iterator.Iterable)
		}
		m.collectInBlock(s.Body)
	case *BlockStmt:
		m.collectInBlock(s)
	case *ExprStmt:
		m.collectInExpr(s.Expr)
	case *DeferStmt:
		m.collectInStmt(s.Statement)
	case *SpawnStmt:
		m.collectInExpr(s.Call)
	case *ComptimeStmt:
		m.collectInBlock(s.Body)
	case *WithStmt:
		m.collectInExpr(s.Value)
		m.collectInBlock(s.Body)
	case *SelectStmt:
		for i := range s.Cases {
			m.collectInBlock(s.Cases[i].Body)
		}
	case *SwitchStmt:
		m.collectInExpr(s.Subject)
		for i := range s.Cases {
			for _, v := range s.Cases[i].Values {
				m.collectInExpr(v)
			}
			m.collectInBlock(s.Cases[i].Body)
		}
	}
}

// collectInExpr scans an expression for generic instantiations.
func (m *monomorphizer) collectInExpr(expr Expression) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *NamedType:
		m.collectInType(e)
	case *CallExpr:
		m.collectInExpr(e.Function)
		if id, ok := e.Function.(*Identifier); ok {
			m.tryCollectMangled(id.Value)
		} else if fa, ok := e.Function.(*FieldAccessExpr); ok {
			if lid, isId := fa.Left.(*Identifier); isId {
				m.tryCollectMangled(lid.Value + "." + fa.Field)
			}
		}
		for _, a := range e.Arguments {
			m.collectInExpr(a)
		}
	case *InfixExpr:
		m.collectInExpr(e.Left)
		m.collectInExpr(e.Right)
	case *PrefixExpr:
		m.collectInExpr(e.Right)
	case *IndexExpr:
		m.collectInExpr(e.Left)
		m.collectInExpr(e.Index)
	case *FieldAccessExpr:
		m.collectInExpr(e.Left)
	case *StructInitExpr:
		m.tryCollectMangled(e.Type)
		for _, v := range e.Fields {
			m.collectInExpr(v)
		}
	case *ArrayLiteral:
		for _, el := range e.Elements {
			m.collectInExpr(el)
		}
	case *MapLiteral:
		for _, pair := range e.Pairs {
			m.collectInExpr(pair.Key)
			m.collectInExpr(pair.Value)
		}
	case *SetLiteral:
		for _, el := range e.Elements {
			m.collectInExpr(el)
		}
	case *IfExpr:
		m.collectInExpr(e.Condition)
		m.collectInBlock(e.Consequence)
		if e.Alternative != nil {
			m.collectInBlock(e.Alternative)
		}
	case *MatchExpr:
		m.collectInExpr(e.Subject)
		for _, arm := range e.Arms {
			m.collectInExpr(arm.Pattern)
			m.collectInExpr(arm.Guard)
			m.collectInBlock(arm.Body)
		}
	case *RangeExpr:
		m.collectInExpr(e.Start)
		m.collectInExpr(e.End)
	case *SpreadExpr:
		m.collectInExpr(e.Operand)
	case *AsyncExpr:
		m.collectInExpr(e.Expr)
	case *AwaitExpr:
		m.collectInExpr(e.Expr)
	case *ErrorPropagationExpr:
		m.collectInExpr(e.Expr)
	case *SizeofExpr:
		m.collectInType(e.Type)
	case *AlignofExpr:
		m.collectInType(e.Type)
	case *MakeExpr:
		m.collectInType(e.Type)
	case *MinExpr:
		m.collectInType(e.Type)
	case *MaxExpr:
		m.collectInType(e.Type)
	case *FromEndIndexExpr:
		m.collectInExpr(e.Operand)
	case *FnLiteral:
		for _, p := range e.Params {
			m.collectInType(p.Type)
		}
		m.collectInType(e.ReturnType)
		m.collectInBlock(e.Body)
	}
}

// collectInType scans a type for generic instantiations and records them.
func (m *monomorphizer) collectInType(t Type) {
	if t == nil {
		return
	}
	switch tt := t.(type) {
	case *NamedType:
		if len(tt.Args) > 0 {
			isGeneric := false
			if _, ok := m.genericStructs[tt.Name]; ok {
				isGeneric = true
			} else if _, ok := m.genericFns[tt.Name]; ok {
				isGeneric = true
			}

			if isGeneric {
				hasGenericParam := false
				for _, arg := range tt.Args {
					if nt, ok := arg.(*NamedType); ok {
						if m.currentParams[nt.Name] {
							hasGenericParam = true
							break
						}
					}
				}
				if !hasGenericParam {
					m.instantiations[MangleName(tt.Name, tt.Args)] = true
				}
			}
			for _, a := range tt.Args {
				m.collectInType(a)
			}
		}
	case *PointerType:
		m.collectInType(tt.Elem)
	case *ArrayType:
		m.collectInType(tt.Elem)
	case *SetType:
		m.collectInType(tt.Elem)
	case *MapType:
		m.collectInType(tt.Key)
		m.collectInType(tt.Elem)
	case *ChanType:
		m.collectInType(tt.Elem)
	case *TupleType:
		for _, ty := range tt.Types {
			m.collectInType(ty)
		}
	case *FunctionType:
		for _, pt := range tt.ParamTypes {
			m.collectInType(pt)
		}
		m.collectInType(tt.ReturnType)
	}
}
