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

// Package ast (mono_substitute.go) implements the substitution phase of generic
// monomorphization: replacing type parameter references with concrete types
// throughout the AST.
package ast

import (
	"strings"
)

// replaceInDecl replaces generic type parameter references with concrete
// types inside a single declaration.
func (m *monomorphizer) replaceInDecl(decl Declaration) {
	originalModule := m.currentModule
	var declName string
	switch d := decl.(type) {
	case *FnDecl:
		declName = d.Name
	case *StructDecl:
		declName = d.Name
	case *VarDecl:
		declName = d.Name
	case *ConstDecl:
		declName = d.Name
	}
	if idx := strings.Index(declName, "."); idx > 0 {
		m.currentModule = declName[:idx]
	}
	defer func() { m.currentModule = originalModule }()

	switch d := decl.(type) {
	case *FnDecl:
		for _, p := range d.Params {
			p.Type = m.replaceType(p.Type)
		}
		d.ReturnType = m.replaceType(d.ReturnType)
		m.replaceInBlock(d.Body)
	case *StructDecl:
		for _, f := range d.Fields {
			f.Type = m.replaceType(f.Type)
		}
		for _, md := range d.Methods {
			m.replaceInDecl(md)
		}
	case *VarDecl:
		d.Type = m.replaceType(d.Type)
		if d.Value != nil {
			d.Value = m.replaceInExpr(d.Value)
		}
	case *ConstDecl:
		if d.Value != nil {
			d.Value = m.replaceInExpr(d.Value)
		}
	}
}

// replaceInBlock replaces generic type references inside a block statement.
func (m *monomorphizer) replaceInBlock(block *BlockStmt) {
	if block == nil {
		return
	}
	for i, stmt := range block.Statements {
		m.replaceInStmt(stmt)
		if exprStmt, ok := stmt.(*ExprStmt); ok {
			exprStmt.Expr = m.replaceInExpr(exprStmt.Expr)
		}
		_ = i
	}
}

// replaceInStmt replaces generic type references inside a single statement.
func (m *monomorphizer) replaceInStmt(stmt Statement) {
	switch s := stmt.(type) {
	case *VarStmt:
		s.Type = m.replaceType(s.Type)
		if s.Value != nil {
			s.Value = m.replaceInExpr(s.Value)
		}
	case *TupleVarStmt:
		if s.Value != nil {
			s.Value = m.replaceInExpr(s.Value)
		}
	case *AssignmentStmt:
		s.LValue = m.replaceInExpr(s.LValue)
		s.Value = m.replaceInExpr(s.Value)
	case *ReturnStmt:
		for i, v := range s.Values {
			s.Values[i] = m.replaceInExpr(v)
		}
	case *IfStmt:
		s.Condition = m.replaceInExpr(s.Condition)
		m.replaceInBlock(s.Consequence)
		if altBlock, ok := s.Alternative.(*BlockStmt); ok {
			m.replaceInBlock(altBlock)
		} else if altStmt, ok := s.Alternative.(Statement); ok {
			m.replaceInStmt(altStmt)
		}
	case *WhileStmt:
		s.Condition = m.replaceInExpr(s.Condition)
		m.replaceInBlock(s.Body)
	case *ForStmt:
		if s.Init != nil {
			m.replaceInStmt(s.Init)
		}
		if s.Condition != nil {
			s.Condition = m.replaceInExpr(s.Condition)
		}
		if s.Post != nil {
			m.replaceInStmt(s.Post)
		}
		if s.Iterator != nil {
			s.Iterator.Iterable = m.replaceInExpr(s.Iterator.Iterable)
		}
		m.replaceInBlock(s.Body)
	case *BlockStmt:
		m.replaceInBlock(s)
	case *ExprStmt:
		s.Expr = m.replaceInExpr(s.Expr)
	case *DeferStmt:
		m.replaceInStmt(s.Statement)
	case *SpawnStmt:
		s.Call = m.replaceInExpr(s.Call)
	case *ComptimeStmt:
		m.replaceInBlock(s.Body)
	case *WithStmt:
		s.Value = m.replaceInExpr(s.Value)
		m.replaceInBlock(s.Body)
	case *SelectStmt:
		for i := range s.Cases {
			s.Cases[i].Condition = m.replaceInExpr(s.Cases[i].Condition)
			m.replaceInBlock(s.Cases[i].Body)
		}
	case *SwitchStmt:
		s.Subject = m.replaceInExpr(s.Subject)
		for i := range s.Cases {
			for j, v := range s.Cases[i].Values {
				s.Cases[i].Values[j] = m.replaceInExpr(v)
			}
			m.replaceInBlock(s.Cases[i].Body)
		}
	}
}

// replaceInExpr replaces generic type references inside an expression.
func (m *monomorphizer) replaceInExpr(expr Expression) Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *NamedType:
		return m.replaceInExprNamedType(e)
	case *CallExpr:
		// Rewrite module-qualified generic function calls.
		if fa, ok := e.Function.(*FieldAccessExpr); ok {
			if id, ok := fa.Left.(*Identifier); ok {
				mangled := m.qualifyMangledName(fa.Field, false, id.Value)
				if m.generatedSpecializations[mangled] || m.instantiations[mangled] {
					if strings.Contains(mangled, ".") {
						parts := strings.SplitN(mangled, ".", 2)
						fa.Left = &Identifier{Token: fa.Token, Value: parts[0]}
						fa.Field = parts[1]
					} else {
						e.Function = &Identifier{Token: fa.Token, Value: mangled}
					}
				}
			}
		}
		e.Function = m.replaceInExpr(e.Function)
		for i, a := range e.Arguments {
			e.Arguments[i] = m.replaceInExpr(a)
		}
		return e
	case *InfixExpr:
		e.Left = m.replaceInExpr(e.Left)
		e.Right = m.replaceInExpr(e.Right)
		return e
	case *PrefixExpr:
		e.Right = m.replaceInExpr(e.Right)
		return e
	case *IndexExpr:
		e.Left = m.replaceInExpr(e.Left)
		e.Index = m.replaceInExpr(e.Index)
		return e
	case *FieldAccessExpr:
		module := ""
		if id, ok := e.Left.(*Identifier); ok {
			module = id.Value
		}
		e.Left = m.replaceInExpr(e.Left)
		e.Field = m.qualifyMangledName(e.Field, false, module)
		return e
	case *StructInitExpr:
		e.Type = m.qualifyMangledName(e.Type, true, "")
		for k, v := range e.Fields {
			e.Fields[k] = m.replaceInExpr(v)
		}
		return e
	case *ArrayLiteral:
		for i, el := range e.Elements {
			e.Elements[i] = m.replaceInExpr(el)
		}
		return e
	case *MapLiteral:
		for i, pair := range e.Pairs {
			e.Pairs[i].Key = m.replaceInExpr(pair.Key)
			e.Pairs[i].Value = m.replaceInExpr(pair.Value)
		}
		return e
	case *SetLiteral:
		for i, el := range e.Elements {
			e.Elements[i] = m.replaceInExpr(el)
		}
		return e
	case *IfExpr:
		e.Condition = m.replaceInExpr(e.Condition)
		m.replaceInBlock(e.Consequence)
		if e.Alternative != nil {
			m.replaceInBlock(e.Alternative)
		}
		return e
	case *MatchExpr:
		e.Subject = m.replaceInExpr(e.Subject)
		for _, arm := range e.Arms {
			arm.Pattern = m.replaceInExpr(arm.Pattern)
			arm.Guard = m.replaceInExpr(arm.Guard)
			m.replaceInBlock(arm.Body)
		}
		return e
	case *RangeExpr:
		e.Start = m.replaceInExpr(e.Start)
		e.End = m.replaceInExpr(e.End)
		return e
	case *SpreadExpr:
		e.Operand = m.replaceInExpr(e.Operand)
		return e
	case *AsyncExpr:
		e.Expr = m.replaceInExpr(e.Expr)
		return e
	case *AwaitExpr:
		e.Expr = m.replaceInExpr(e.Expr)
		return e
	case *ErrorPropagationExpr:
		e.Expr = m.replaceInExpr(e.Expr)
		return e
	case *SizeofExpr:
		e.Type = m.replaceType(e.Type)
		return e
	case *AlignofExpr:
		e.Type = m.replaceType(e.Type)
		return e
	case *MakeExpr:
		e.Type = m.replaceType(e.Type)
		if e.Capacity != nil {
			e.Capacity = m.replaceInExpr(e.Capacity)
		}
		return e
	case *MinExpr:
		e.Type = m.replaceType(e.Type)
		return e
	case *MaxExpr:
		e.Type = m.replaceType(e.Type)
		return e
	case *FromEndIndexExpr:
		e.Operand = m.replaceInExpr(e.Operand)
		return e
	case *FnLiteral:
		for _, p := range e.Params {
			p.Type = m.replaceType(p.Type)
		}
		e.ReturnType = m.replaceType(e.ReturnType)
		m.replaceInBlock(e.Body)
		return e
	}
	return expr
}

// qualifyMangledName resolves a mangled generic name with module qualification.
func (m *monomorphizer) qualifyMangledName(name string, isStruct bool, module string) string {
	if !strings.Contains(name, "_") {
		return name
	}
	var generics map[string]*StructDecl
	var fns map[string]*FnDecl
	if isStruct {
		generics = m.genericStructs
	} else {
		fns = m.genericFns
	}
	namesToTry := []string{name}
	if module != "" {
		namesToTry = append(namesToTry, module+"."+name)
	}
	if isStruct {
		for _, tryName := range namesToTry {
			for _, genName := range sortedGenericStructNames(generics) {
				decl := generics[genName]
				if args, ok := demangleName(tryName, genName, decl.TypeParams); ok {
					res := MangleName(genName, m.qualifyTypes(args))
					return res
				}
			}
		}
	} else {
		for _, tryName := range namesToTry {
			for _, genName := range sortedGenericFnNames(fns) {
				decl := fns[genName]
				if args, ok := demangleName(tryName, genName, decl.TypeParams); ok {
					res := MangleName(genName, m.qualifyTypes(args))
					return res
				}
			}
		}
	}
	return name
}

// qualifyTypes adds module prefixes to named types when needed.
func (m *monomorphizer) qualifyTypes(types []Type) []Type {
	var qualified []Type
	for _, t := range types {
		if nt, ok := t.(*NamedType); ok {
			name := nt.Name
			if !strings.Contains(name, ".") && m.currentModule != "" {
				q := m.currentModule + "." + name
				if m.moduleSymbols[q] {
					name = q
				}
			}
			qualified = append(qualified, &NamedType{Token: nt.Token, Name: name, Args: m.qualifyTypes(nt.Args)})
		} else {
			qualified = append(qualified, t)
		}
	}
	return qualified
}

// replaceInExprNamedType replaces a NamedType inside an expression with its mangled concrete form.
func (m *monomorphizer) replaceInExprNamedType(e *NamedType) Expression {
	if len(e.Args) > 0 {
		name := e.Name
		if !strings.Contains(name, ".") && m.currentModule != "" && m.currentModule != "main" {
			qualified := m.currentModule + "." + name
			if m.moduleSymbols[qualified] {
				name = qualified
			}
		}
		var qualifiedArgs []Type
		for _, arg := range e.Args {
			if nt, ok := arg.(*NamedType); ok {
				argName := nt.Name
				if !strings.Contains(argName, ".") && m.currentModule != "" {
					qualified := m.currentModule + "." + argName
					if m.moduleSymbols[qualified] {
						argName = qualified
					}
				}
				qualifiedArgs = append(qualifiedArgs, &NamedType{Token: nt.Token, Name: argName, Args: nt.Args})
			} else {
				qualifiedArgs = append(qualifiedArgs, arg)
			}
		}
		mangledName := MangleName(name, qualifiedArgs)
		if strings.Contains(mangledName, ".") {
			parts := strings.SplitN(mangledName, ".", 2)
			return &FieldAccessExpr{
				Token: e.Token,
				Left:  &Identifier{Token: e.Token, Value: parts[0]},
				Field: parts[1],
			}
		}
		return &Identifier{Token: e.Token, Value: mangledName}
	}
	return e
}

// replaceType replaces generic type parameter references with concrete types in a Type.
func (m *monomorphizer) replaceType(t Type) Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *NamedType:
		name := tt.Name
		if !strings.Contains(name, ".") && m.currentModule != "" && m.currentModule != "main" {
			isPrimitive := false
			switch name {
			case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float", "bool", "string", "bytes", "void", "any":
				isPrimitive = true
			}
			if !isPrimitive {
				basePart := strings.Split(name, "_")[0]
				qualifiedBase := m.currentModule + "." + basePart
				if m.moduleSymbols[qualifiedBase] {
					name = m.currentModule + "." + name
				}
			}
		}
		if len(tt.Args) > 0 {
			namesToTry := []string{tt.Name}
			if !strings.Contains(tt.Name, ".") && m.currentModule != "" && m.currentModule != "main" {
				namesToTry = append(namesToTry, m.currentModule+"."+tt.Name)
			}
			for _, n := range namesToTry {
				if _, ok := m.genericStructs[n]; ok {
					return &NamedType{Token: tt.Token, Name: MangleName(name, tt.Args)}
				}
				if _, ok := m.genericFns[n]; ok {
					return &NamedType{Token: tt.Token, Name: MangleName(name, tt.Args)}
				}
			}
			var newArgs []Type
			for _, a := range tt.Args {
				newArgs = append(newArgs, m.replaceType(a))
			}
			return &NamedType{Token: tt.Token, Name: name, Args: newArgs}
		}
		return &NamedType{Token: tt.Token, Name: name}
	case *PointerType:
		return &PointerType{Token: tt.Token, Elem: m.replaceType(tt.Elem)}
	case *ArrayType:
		return &ArrayType{Token: tt.Token, Elem: m.replaceType(tt.Elem)}
	case *SetType:
		return &SetType{Token: tt.Token, Elem: m.replaceType(tt.Elem)}
	case *MapType:
		return &MapType{Token: tt.Token, Key: m.replaceType(tt.Key), Elem: m.replaceType(tt.Elem)}
	case *ChanType:
		return &ChanType{Token: tt.Token, Elem: m.replaceType(tt.Elem)}
	case *TupleType:
		var newTypes []Type
		for _, ty := range tt.Types {
			newTypes = append(newTypes, m.replaceType(ty))
		}
		return &TupleType{Token: tt.Token, Types: newTypes}
	case *FunctionType:
		var newParams []Type
		for _, pt := range tt.ParamTypes {
			newParams = append(newParams, m.replaceType(pt))
		}
		return &FunctionType{
			Token:      tt.Token,
			ParamTypes: newParams,
			ReturnType: m.replaceType(tt.ReturnType),
		}
	}
	return t
}
