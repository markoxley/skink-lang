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

// mono_specialize.go implements the specialization phase of generic
// monomorphization: creating concrete copies of generic structs and
// functions with type arguments substituted.
package ast

import "strings"

// specializeStruct creates a concrete copy of a generic struct by replacing
// type parameters with the provided type arguments.
func (m *monomorphizer) specializeStruct(decl *StructDecl, args []Type) *StructDecl {
	mapping := make(map[string]Type)
	for i, param := range decl.TypeParams {
		if i < len(args) {
			mapping[param] = args[i]
		}
	}
	newDecl := &StructDecl{
		Token:      decl.Token,
		Pub:        decl.Pub,
		Name:       MangleName(decl.Name, args),
		Attributes: decl.Attributes,
		Doc:        decl.Doc,
	}
	for _, f := range decl.Fields {
		newDecl.Fields = append(newDecl.Fields, &FieldDecl{
			Token: f.Token,
			Name:  f.Name,
			Type:  SubstituteType(f.Type, mapping),
		})
	}
	for _, md := range decl.Methods {
		newMethod := &FnDecl{
			Token:      md.Token,
			Pub:        md.Pub,
			Name:       md.Name,
			ReturnType: SubstituteType(md.ReturnType, mapping),
			Body:       m.substituteBlock(md.Body, mapping),
			Doc:        md.Doc,
		}
		for _, p := range md.Params {
			newMethod.Params = append(newMethod.Params, &Param{
				Name: p.Name,
				Type: SubstituteType(p.Type, mapping),
			})
		}
		newDecl.Methods = append(newDecl.Methods, newMethod)
	}
	return newDecl
}

// specializeFn creates a concrete copy of a generic function by replacing
// type parameters with the provided type arguments.
func (m *monomorphizer) specializeFn(decl *FnDecl, args []Type) *FnDecl {
	mapping := make(map[string]Type)
	for i, param := range decl.TypeParams {
		if i < len(args) {
			mapping[param] = args[i]
		}
	}
	newDecl := &FnDecl{
		Token:      decl.Token,
		Pub:        decl.Pub,
		Name:       MangleName(decl.Name, args),
		ReturnType: SubstituteType(decl.ReturnType, mapping),
		Body:       m.substituteBlock(decl.Body, mapping),
		Attributes: decl.Attributes,
		Doc:        decl.Doc,
	}
	for _, p := range decl.Params {
		newDecl.Params = append(newDecl.Params, &Param{
			Name: p.Name,
			Type: SubstituteType(p.Type, mapping),
		})
	}
	return newDecl
}

// substituteBlock returns a new block with types substituted according to mapping.
func (m *monomorphizer) substituteBlock(block *BlockStmt, mapping map[string]Type) *BlockStmt {
	if block == nil {
		return nil
	}
	newBlock := &BlockStmt{Token: block.Token}
	for _, stmt := range block.Statements {
		newBlock.Statements = append(newBlock.Statements, m.substituteStmt(stmt, mapping))
	}
	return newBlock
}

// substituteStmt returns a statement copy with types substituted according to mapping.
func (m *monomorphizer) substituteStmt(stmt Statement, mapping map[string]Type) Statement {
	switch s := stmt.(type) {
	case *VarStmt:
		return &VarStmt{
			Token:    s.Token,
			Name:     s.Name,
			Type:     SubstituteType(s.Type, mapping),
			Value:    m.substituteExpr(s.Value, mapping),
			Implicit: s.Implicit,
		}
	case *TupleVarStmt:
		return &TupleVarStmt{
			Token: s.Token,
			Names: append([]string(nil), s.Names...),
			Value: m.substituteExpr(s.Value, mapping),
		}
	case *AssignmentStmt:
		return &AssignmentStmt{
			Token:    s.Token,
			LValue:   s.LValue,
			Operator: s.Operator,
			Value:    m.substituteExpr(s.Value, mapping),
		}
	case *ReturnStmt:
		newRet := &ReturnStmt{Token: s.Token}
		for _, v := range s.Values {
			newRet.Values = append(newRet.Values, m.substituteExpr(v, mapping))
		}
		return newRet
	case *IfStmt:
		newIf := &IfStmt{
			Token:       s.Token,
			Condition:   m.substituteExpr(s.Condition, mapping),
			Consequence: m.substituteBlock(s.Consequence, mapping),
		}
		if s.Alternative != nil {
			newIf.Alternative = m.substituteStmt(s.Alternative, mapping)
		}
		return newIf
	case *WhileStmt:
		return &WhileStmt{
			Token:     s.Token,
			Condition: m.substituteExpr(s.Condition, mapping),
			Body:      m.substituteBlock(s.Body, mapping),
		}
	case *ForStmt:
		newFor := &ForStmt{Token: s.Token, Body: m.substituteBlock(s.Body, mapping)}
		if s.Init != nil {
			newFor.Init = m.substituteStmt(s.Init, mapping)
		}
		if s.Condition != nil {
			newFor.Condition = m.substituteExpr(s.Condition, mapping)
		}
		if s.Post != nil {
			newFor.Post = m.substituteStmt(s.Post, mapping)
		}
		if s.Iterator != nil {
			newFor.Iterator = &ForInStmt{
				Variable: s.Iterator.Variable,
				Iterable: m.substituteExpr(s.Iterator.Iterable, mapping),
				IsRange:  s.Iterator.IsRange,
			}
		}
		return newFor
	case *BlockStmt:
		return m.substituteBlock(s, mapping)
	case *ExprStmt:
		return &ExprStmt{Token: s.Token, Expr: m.substituteExpr(s.Expr, mapping)}
	case *BreakStmt:
		return s
	case *ContinueStmt:
		return s
	case *DeferStmt:
		return &DeferStmt{Token: s.Token, Statement: m.substituteStmt(s.Statement, mapping)}
	case *SpawnStmt:
		return &SpawnStmt{Token: s.Token, Call: m.substituteExpr(s.Call, mapping)}
	case *ComptimeStmt:
		return &ComptimeStmt{Token: s.Token, Body: m.substituteBlock(s.Body, mapping)}
	case *WithStmt:
		return &WithStmt{
			Token: s.Token,
			Name:  s.Name,
			Value: m.substituteExpr(s.Value, mapping),
			Body:  m.substituteBlock(s.Body, mapping),
		}
	case *SelectStmt:
		newSel := &SelectStmt{Token: s.Token}
		for i := range s.Cases {
			newSel.Cases = append(newSel.Cases, SelectCase{
				Condition: m.substituteExpr(s.Cases[i].Condition, mapping),
				IsDefault: s.Cases[i].IsDefault,
				Body:      m.substituteBlock(s.Cases[i].Body, mapping),
			})
		}
		return newSel
	case *SwitchStmt:
		newSw := &SwitchStmt{Token: s.Token, Subject: m.substituteExpr(s.Subject, mapping)}
		for i := range s.Cases {
			var newVals []Expression
			for _, v := range s.Cases[i].Values {
				newVals = append(newVals, m.substituteExpr(v, mapping))
			}
			newSw.Cases = append(newSw.Cases, SwitchCase{
				Token:     s.Cases[i].Token,
				Values:    newVals,
				IsDefault: s.Cases[i].IsDefault,
				Body:      m.substituteBlock(s.Cases[i].Body, mapping),
			})
		}
		return newSw
	default:
		return s
	}
}

// substituteExpr returns an expression copy with types substituted according to mapping.
func (m *monomorphizer) substituteExpr(expr Expression, mapping map[string]Type) Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral,
		*BooleanLiteral, *NilLiteral:
		return e
	case *ArrayLiteral:
		newArr := &ArrayLiteral{Token: e.Token}
		for _, el := range e.Elements {
			newArr.Elements = append(newArr.Elements, m.substituteExpr(el, mapping))
		}
		return newArr
	case *MapLiteral:
		newMap := &MapLiteral{Token: e.Token}
		for _, pair := range e.Pairs {
			newMap.Pairs = append(newMap.Pairs, MapPair{
				Key:   m.substituteExpr(pair.Key, mapping),
				Value: m.substituteExpr(pair.Value, mapping),
			})
		}
		return newMap
	case *SetLiteral:
		newSet := &SetLiteral{Token: e.Token}
		for _, el := range e.Elements {
			newSet.Elements = append(newSet.Elements, m.substituteExpr(el, mapping))
		}
		return newSet
	case *StructInitExpr:
		newInit := &StructInitExpr{Token: e.Token, Type: m.substituteMangledName(e.Type, mapping)}
		newInit.Fields = make(map[string]Expression)
		for k, v := range e.Fields {
			newInit.Fields[k] = m.substituteExpr(v, mapping)
		}
		return newInit
	case *CallExpr:
		newCall := &CallExpr{Token: e.Token, Function: m.substituteExpr(e.Function, mapping)}
		for _, a := range e.Arguments {
			newCall.Arguments = append(newCall.Arguments, m.substituteExpr(a, mapping))
		}
		return newCall
	case *InfixExpr:
		return &InfixExpr{
			Token:    e.Token,
			Left:     m.substituteExpr(e.Left, mapping),
			Operator: e.Operator,
			Right:    m.substituteExpr(e.Right, mapping),
		}
	case *PrefixExpr:
		return &PrefixExpr{Token: e.Token, Operator: e.Operator, Right: m.substituteExpr(e.Right, mapping)}
	case *IndexExpr:
		return &IndexExpr{Token: e.Token, Left: m.substituteExpr(e.Left, mapping), Index: m.substituteExpr(e.Index, mapping)}
	case *FieldAccessExpr:
		return &FieldAccessExpr{
			Token: e.Token,
			Left:  m.substituteExpr(e.Left, mapping),
			Field: m.substituteMangledName(e.Field, mapping),
		}
	case *IfExpr:
		return &IfExpr{
			Token:       e.Token,
			Condition:   m.substituteExpr(e.Condition, mapping),
			Consequence: m.substituteBlock(e.Consequence, mapping),
			Alternative: m.substituteBlock(e.Alternative, mapping),
		}
	case *MatchExpr:
		newMatch := &MatchExpr{Token: e.Token, Subject: m.substituteExpr(e.Subject, mapping)}
		for _, arm := range e.Arms {
			newMatch.Arms = append(newMatch.Arms, &MatchArm{
				Token:   arm.Token,
				Pattern: m.substituteExpr(arm.Pattern, mapping),
				Guard:   m.substituteExpr(arm.Guard, mapping),
				Body:    m.substituteBlock(arm.Body, mapping),
			})
		}
		return newMatch
	case *RangeExpr:
		return &RangeExpr{Token: e.Token, Start: m.substituteExpr(e.Start, mapping), End: m.substituteExpr(e.End, mapping)}
	case *SpreadExpr:
		return &SpreadExpr{Token: e.Token, Operand: m.substituteExpr(e.Operand, mapping)}
	case *AsyncExpr:
		return &AsyncExpr{Token: e.Token, Expr: m.substituteExpr(e.Expr, mapping)}
	case *AwaitExpr:
		return &AwaitExpr{Token: e.Token, Expr: m.substituteExpr(e.Expr, mapping)}
	case *ErrorPropagationExpr:
		return &ErrorPropagationExpr{Token: e.Token, Expr: m.substituteExpr(e.Expr, mapping)}
	case *SizeofExpr:
		return &SizeofExpr{Token: e.Token, Type: SubstituteType(e.Type, mapping)}
	case *AlignofExpr:
		return &AlignofExpr{Token: e.Token, Type: SubstituteType(e.Type, mapping)}
	case *MakeExpr:
		return &MakeExpr{Token: e.Token, Type: SubstituteType(e.Type, mapping), Capacity: e.Capacity}
	case *MinExpr:
		return &MinExpr{Token: e.Token, Type: SubstituteType(e.Type, mapping)}
	case *MaxExpr:
		return &MaxExpr{Token: e.Token, Type: SubstituteType(e.Type, mapping)}
	case *FromEndIndexExpr:
		return &FromEndIndexExpr{Token: e.Token, Operand: m.substituteExpr(e.Operand, mapping)}
	default:
		return e
	}
}

// substituteMangledName rewrites a mangled generic name with concrete types from mapping.
func (m *monomorphizer) substituteMangledName(name string, mapping map[string]Type) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if t, ok := mapping[part]; ok {
			parts[i] = typeMangleKey(t)
		} else if i > 0 && !strings.Contains(part, ".") && m.currentModule != "" {
			// Qualify the type argument if it's not a primitive.
			isPrimitive := false
			switch part {
			case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float", "bool", "string", "bytes", "void", "any":
				isPrimitive = true
			}
			if !isPrimitive {
				qualified := m.currentModule + "." + part
				if m.moduleSymbols[qualified] {
					parts[i] = qualified
				}
			}
		}
	}
	return strings.Join(parts, "_")
}
