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

package ast

import (
	"testing"

	"github.com/skink-lang/compiler/token"
)

func TestProgramTokenLiteralEmpty(t *testing.T) {
	p := &Program{}
	if p.TokenLiteral() != "" {
		t.Errorf("empty Program.TokenLiteral() = %q, want empty", p.TokenLiteral())
	}
}

func TestProgramTokenLiteralNonEmpty(t *testing.T) {
	p := &Program{
		Declarations: []Declaration{
			&FnDecl{Token: token.Token{Literal: "fn"}, Name: "main"},
		},
	}
	if p.TokenLiteral() != "fn" {
		t.Errorf("Program.TokenLiteral() = %q, want %q", p.TokenLiteral(), "fn")
	}
}

func TestProgramString(t *testing.T) {
	p := &Program{
		Declarations: []Declaration{
			&FnDecl{Token: token.Token{Literal: "fn"}, Name: "main"},
		},
	}
	if p.String() != "fn main" {
		t.Errorf("Program.String() = %q, want %q", p.String(), "fn main")
	}
}

func TestFnDeclNodeMethods(t *testing.T) {
	fd := &FnDecl{
		Token: token.Token{Literal: "fn"},
		Name:  "foo",
	}
	if fd.TokenLiteral() != "fn" {
		t.Errorf("FnDecl.TokenLiteral() = %q, want %q", fd.TokenLiteral(), "fn")
	}
	if fd.String() != "fn foo" {
		t.Errorf("FnDecl.String() = %q, want %q", fd.String(), "fn foo")
	}
}

func TestExternFnDeclNodeMethods(t *testing.T) {
	ef := &ExternFnDecl{
		Token: token.Token{Literal: "extern"},
		Name:  "printf",
	}
	if ef.TokenLiteral() != "extern" {
		t.Errorf("ExternFnDecl.TokenLiteral() = %q, want %q", ef.TokenLiteral(), "extern")
	}
	if ef.String() != "extern printf" {
		t.Errorf("ExternFnDecl.String() = %q, want %q", ef.String(), "extern printf")
	}
}

func TestVarStmtNodeMethods(t *testing.T) {
	vs := &VarStmt{
		Token: token.Token{Literal: "var"},
		Name:  "x",
	}
	if vs.TokenLiteral() != "var" {
		t.Errorf("VarStmt.TokenLiteral() = %q, want %q", vs.TokenLiteral(), "var")
	}
	if vs.String() != "var x" {
		t.Errorf("VarStmt.String() = %q, want %q", vs.String(), "var x")
	}
}

func TestTupleVarStmtString(t *testing.T) {
	tvs := &TupleVarStmt{
		Token: token.Token{Literal: "var"},
		Names: []string{"a", "b"},
	}
	if tvs.String() != "var a, b" {
		t.Errorf("TupleVarStmt.String() = %q, want %q", tvs.String(), "var a, b")
	}
}

func TestVarBlockStmtString(t *testing.T) {
	vb := &VarBlockStmt{Token: token.Token{Literal: "var"}}
	if vb.String() != "var {}" {
		t.Errorf("VarBlockStmt.String() = %q, want %q", vb.String(), "var {}")
	}
}

func TestExprStmtString(t *testing.T) {
	es := &ExprStmt{
		Expr: &Identifier{Value: "x"},
	}
	if es.String() != "x" {
		t.Errorf("ExprStmt.String() = %q, want %q", es.String(), "x")
	}
}

func TestBlockStmtString(t *testing.T) {
	bs := &BlockStmt{
		Statements: []Statement{
			&ExprStmt{Expr: &Identifier{Value: "a"}},
			&ExprStmt{Expr: &Identifier{Value: "b"}},
		},
	}
	if bs.String() != "ab" {
		t.Errorf("BlockStmt.String() = %q, want %q", bs.String(), "ab")
	}
}

func TestAssignmentStmtString(t *testing.T) {
	as := &AssignmentStmt{
		LValue:   &Identifier{Value: "x"},
		Operator: "=",
		Value:    &IntegerLiteral{Token: token.Token{Literal: "1"}, Value: 1},
	}
	if as.String() != "x = 1" {
		t.Errorf("AssignmentStmt.String() = %q, want %q", as.String(), "x = 1")
	}
}

func TestTupleAssignmentStmtString(t *testing.T) {
	tas := &TupleAssignmentStmt{
		LValues:  []Expression{&Identifier{Value: "a"}, &Identifier{Value: "b"}},
		Operator: "=",
		Value:    &Identifier{Value: "foo"},
	}
	if tas.String() != "a, b = foo" {
		t.Errorf("TupleAssignmentStmt.String() = %q, want %q", tas.String(), "a, b = foo")
	}
}

func TestConstDeclNodeMethods(t *testing.T) {
	cd := &ConstDecl{
		Token: token.Token{Literal: "const"},
		Name:  "MAX",
	}
	if cd.String() != "const MAX" {
		t.Errorf("ConstDecl.String() = %q, want %q", cd.String(), "const MAX")
	}
}

func TestConstBlockDeclString(t *testing.T) {
	cb := &ConstBlockDecl{Token: token.Token{Literal: "const"}}
	if cb.String() != "const { ... }" {
		t.Errorf("ConstBlockDecl.String() = %q, want %q", cb.String(), "const { ... }")
	}
}

func TestVarDeclNodeMethods(t *testing.T) {
	vd := &VarDecl{
		Token: token.Token{Literal: "var"},
		Name:  "x",
	}
	if vd.String() != "var x" {
		t.Errorf("VarDecl.String() = %q, want %q", vd.String(), "var x")
	}
}

func TestStructDeclNodeMethods(t *testing.T) {
	sd := &StructDecl{
		Token: token.Token{Literal: "struct"},
		Name:  "Point",
	}
	if sd.String() != "struct Point" {
		t.Errorf("StructDecl.String() = %q, want %q", sd.String(), "struct Point")
	}
}

func TestEnumDeclNodeMethods(t *testing.T) {
	ed := &EnumDecl{
		Token: token.Token{Literal: "enum"},
		Name:  "Color",
	}
	if ed.String() != "enum Color" {
		t.Errorf("EnumDecl.String() = %q, want %q", ed.String(), "enum Color")
	}
}

func TestModuleDeclNodeMethods(t *testing.T) {
	md := &ModuleDecl{
		Token: token.Token{Literal: "module"},
		Name:  "foo",
	}
	if md.String() != "module foo" {
		t.Errorf("ModuleDecl.String() = %q, want %q", md.String(), "module foo")
	}
}

func TestImportDeclNodeMethods(t *testing.T) {
	id := &ImportDecl{
		Token: token.Token{Literal: "import"},
		Path:  "std/io",
	}
	if id.String() != "import std/io" {
		t.Errorf("ImportDecl.String() = %q, want %q", id.String(), "import std/io")
	}
}

func TestImportBlockDeclString(t *testing.T) {
	ib := &ImportBlockDecl{Token: token.Token{Literal: "import"}}
	if ib.String() != "import { ... }" {
		t.Errorf("ImportBlockDecl.String() = %q, want %q", ib.String(), "import { ... }")
	}
}

func TestServiceDeclNodeMethods(t *testing.T) {
	sd := &ServiceDecl{
		Token: token.Token{Literal: "service"},
		Name:  "Calc",
	}
	if sd.String() != "service Calc" {
		t.Errorf("ServiceDecl.String() = %q, want %q", sd.String(), "service Calc")
	}
}

func TestRuleDeclNodeMethods(t *testing.T) {
	rd := &RuleDecl{
		Token: token.Token{Literal: "rule"},
		Name:  "r1",
	}
	if rd.String() != "rule r1" {
		t.Errorf("RuleDecl.String() = %q, want %q", rd.String(), "rule r1")
	}
}

func TestRulesetDeclNodeMethods(t *testing.T) {
	rsd := &RulesetDecl{
		Token: token.Token{Literal: "ruleset"},
		Name:  "rs",
	}
	if rsd.String() != "ruleset rs" {
		t.Errorf("RulesetDecl.String() = %q, want %q", rsd.String(), "ruleset rs")
	}
}

func TestTemplateDeclNodeMethods(t *testing.T) {
	td := &TemplateDecl{
		Token: token.Token{Literal: "template"},
		Name:  "Addable",
	}
	if td.String() != "template Addable" {
		t.Errorf("TemplateDecl.String() = %q, want %q", td.String(), "template Addable")
	}
}

func TestForStmtNodeMethods(t *testing.T) {
	fs := &ForStmt{Token: token.Token{Literal: "for"}}
	if fs.String() != "for" {
		t.Errorf("ForStmt.String() = %q, want %q", fs.String(), "for")
	}
}

func TestWhileStmtNodeMethods(t *testing.T) {
	ws := &WhileStmt{Token: token.Token{Literal: "while"}}
	if ws.String() != "while" {
		t.Errorf("WhileStmt.String() = %q, want %q", ws.String(), "while")
	}
}

func TestUntilStmtNodeMethods(t *testing.T) {
	us := &UntilStmt{Token: token.Token{Literal: "until"}}
	if us.String() != "until" {
		t.Errorf("UntilStmt.String() = %q, want %q", us.String(), "until")
	}
}

func TestReturnStmtNodeMethods(t *testing.T) {
	rs := &ReturnStmt{Token: token.Token{Literal: "return"}}
	if rs.String() != "return" {
		t.Errorf("ReturnStmt.String() = %q, want %q", rs.String(), "return")
	}
}

func TestBreakStmtNodeMethods(t *testing.T) {
	bs := &BreakStmt{Token: token.Token{Literal: "break"}}
	if bs.String() != "break" {
		t.Errorf("BreakStmt.String() = %q, want %q", bs.String(), "break")
	}
}

func TestContinueStmtNodeMethods(t *testing.T) {
	cs := &ContinueStmt{Token: token.Token{Literal: "continue"}}
	if cs.String() != "continue" {
		t.Errorf("ContinueStmt.String() = %q, want %q", cs.String(), "continue")
	}
}

func TestComptimeStmtNodeMethods(t *testing.T) {
	cs := &ComptimeStmt{Token: token.Token{Literal: "comptime"}}
	if cs.String() != "comptime" {
		t.Errorf("ComptimeStmt.String() = %q, want %q", cs.String(), "comptime")
	}
}

func TestDeferStmtNodeMethods(t *testing.T) {
	ds := &DeferStmt{Token: token.Token{Literal: "defer"}}
	if ds.String() != "defer" {
		t.Errorf("DeferStmt.String() = %q, want %q", ds.String(), "defer")
	}
}

func TestUnsafeStmtNodeMethods(t *testing.T) {
	us := &UnsafeStmt{Token: token.Token{Literal: "unsafe"}}
	if us.String() != "unsafe" {
		t.Errorf("UnsafeStmt.String() = %q, want %q", us.String(), "unsafe")
	}
}

func TestSpawnStmtNodeMethods(t *testing.T) {
	ss := &SpawnStmt{Token: token.Token{Literal: "spawn"}}
	if ss.String() != "spawn" {
		t.Errorf("SpawnStmt.String() = %q, want %q", ss.String(), "spawn")
	}
}

func TestSelectStmtNodeMethods(t *testing.T) {
	ss := &SelectStmt{Token: token.Token{Literal: "select"}}
	if ss.String() != "select" {
		t.Errorf("SelectStmt.String() = %q, want %q", ss.String(), "select")
	}
}

func TestSwitchStmtNodeMethods(t *testing.T) {
	ss := &SwitchStmt{Token: token.Token{Literal: "switch"}}
	if ss.String() != "switch" {
		t.Errorf("SwitchStmt.String() = %q, want %q", ss.String(), "switch")
	}
}

func TestWithStmtNodeMethods(t *testing.T) {
	ws := &WithStmt{Token: token.Token{Literal: "with"}}
	if ws.String() != "with" {
		t.Errorf("WithStmt.String() = %q, want %q", ws.String(), "with")
	}
}

func TestIfStmtNodeMethods(t *testing.T) {
	is := &IfStmt{Token: token.Token{Literal: "if"}}
	if is.String() != "if" {
		t.Errorf("IfStmt.String() = %q, want %q", is.String(), "if")
	}
}

func TestIfExprNodeMethods(t *testing.T) {
	ie := &IfExpr{Token: token.Token{Literal: "if"}}
	if ie.String() != "if" {
		t.Errorf("IfExpr.String() = %q, want %q", ie.String(), "if")
	}
}

func TestMatchExprNodeMethods(t *testing.T) {
	me := &MatchExpr{Token: token.Token{Literal: "match"}}
	if me.String() != "match" {
		t.Errorf("MatchExpr.String() = %q, want %q", me.String(), "match")
	}
}

func TestIdentifierNodeMethods(t *testing.T) {
	id := &Identifier{Token: token.Token{Literal: "x"}, Value: "x"}
	if id.String() != "x" {
		t.Errorf("Identifier.String() = %q, want %q", id.String(), "x")
	}
}

func TestIntegerLiteralNodeMethods(t *testing.T) {
	il := &IntegerLiteral{Token: token.Token{Literal: "42"}, Value: 42}
	if il.String() != "42" {
		t.Errorf("IntegerLiteral.String() = %q, want %q", il.String(), "42")
	}
}

func TestFloatLiteralNodeMethods(t *testing.T) {
	fl := &FloatLiteral{Token: token.Token{Literal: "3.14"}, Value: 3.14}
	if fl.String() != "3.14" {
		t.Errorf("FloatLiteral.String() = %q, want %q", fl.String(), "3.14")
	}
}

func TestStringLiteralNodeMethods(t *testing.T) {
	sl := &StringLiteral{Token: token.Token{Literal: "hello"}, Value: "hello"}
	if sl.String() != "hello" {
		t.Errorf("StringLiteral.String() = %q, want %q", sl.String(), "hello")
	}
}

func TestBooleanLiteralNodeMethods(t *testing.T) {
	bl := &BooleanLiteral{Token: token.Token{Literal: "true"}, Value: true}
	if bl.String() != "true" {
		t.Errorf("BooleanLiteral.String() = %q, want %q", bl.String(), "true")
	}
}

func TestNilLiteralNodeMethods(t *testing.T) {
	nl := &NilLiteral{Token: token.Token{Literal: "nil"}}
	if nl.String() != "nil" {
		t.Errorf("NilLiteral.String() = %q, want %q", nl.String(), "nil")
	}
}

func TestPrefixExprString(t *testing.T) {
	pe := &PrefixExpr{
		Operator: "-",
		Right:    &IntegerLiteral{Token: token.Token{Literal: "5"}, Value: 5},
	}
	if pe.String() != "(-5)" {
		t.Errorf("PrefixExpr.String() = %q, want %q", pe.String(), "(-5)")
	}
}

func TestInfixExprString(t *testing.T) {
	ie := &InfixExpr{
		Left:     &IntegerLiteral{Token: token.Token{Literal: "1"}, Value: 1},
		Operator: "+",
		Right:    &IntegerLiteral{Token: token.Token{Literal: "2"}, Value: 2},
	}
	if ie.String() != "(1 + 2)" {
		t.Errorf("InfixExpr.String() = %q, want %q", ie.String(), "(1 + 2)")
	}
}

func TestArrayLiteralString(t *testing.T) {
	al := &ArrayLiteral{}
	if al.String() != "[...]" {
		t.Errorf("ArrayLiteral.String() = %q, want %q", al.String(), "[...]")
	}
}

func TestSetLiteralString(t *testing.T) {
	sl := &SetLiteral{}
	if sl.String() != "set{...}" {
		t.Errorf("SetLiteral.String() = %q, want %q", sl.String(), "set{...}")
	}
}

func TestMapLiteralString(t *testing.T) {
	ml := &MapLiteral{}
	if ml.String() != "{...}" {
		t.Errorf("MapLiteral.String() = %q, want %q", ml.String(), "{...}")
	}
}

func TestAsyncExprString(t *testing.T) {
	ae := &AsyncExpr{Token: token.Token{Literal: "async"}}
	if ae.String() != "async" {
		t.Errorf("AsyncExpr.String() = %q, want %q", ae.String(), "async")
	}
}

func TestAwaitExprString(t *testing.T) {
	ae := &AwaitExpr{Token: token.Token{Literal: "await"}}
	if ae.String() != "await" {
		t.Errorf("AwaitExpr.String() = %q, want %q", ae.String(), "await")
	}
}

func TestSizeofExprString(t *testing.T) {
	se := &SizeofExpr{Token: token.Token{Literal: "sizeof"}}
	if se.String() != "sizeof" {
		t.Errorf("SizeofExpr.String() = %q, want %q", se.String(), "sizeof")
	}
}

func TestAlignofExprString(t *testing.T) {
	ae := &AlignofExpr{Token: token.Token{Literal: "alignof"}}
	if ae.String() != "alignof" {
		t.Errorf("AlignofExpr.String() = %q, want %q", ae.String(), "alignof")
	}
}

func TestMakeExprString(t *testing.T) {
	me := &MakeExpr{Token: token.Token{Literal: "make"}}
	if me.String() != "make" {
		t.Errorf("MakeExpr.String() = %q, want %q", me.String(), "make")
	}
}

func TestMinExprString(t *testing.T) {
	me := &MinExpr{Token: token.Token{Literal: "min"}}
	if me.String() != "min" {
		t.Errorf("MinExpr.String() = %q, want %q", me.String(), "min")
	}
}

func TestMaxExprString(t *testing.T) {
	me := &MaxExpr{Token: token.Token{Literal: "max"}}
	if me.String() != "max" {
		t.Errorf("MaxExpr.String() = %q, want %q", me.String(), "max")
	}
}

func TestIndexExprString(t *testing.T) {
	ie := &IndexExpr{Left: &Identifier{Value: "arr"}}
	if ie.String() != "arr[...]" {
		t.Errorf("IndexExpr.String() = %q, want %q", ie.String(), "arr[...]")
	}
}

func TestFromEndIndexExprString(t *testing.T) {
	fe := &FromEndIndexExpr{Operand: &IntegerLiteral{Token: token.Token{Literal: "1"}, Value: 1}}
	if fe.String() != "^1" {
		t.Errorf("FromEndIndexExpr.String() = %q, want %q", fe.String(), "^1")
	}
}

func TestSliceExprString(t *testing.T) {
	se := &SliceExpr{Left: &Identifier{Value: "arr"}}
	if se.String() != "arr[...]" {
		t.Errorf("SliceExpr.String() = %q, want %q", se.String(), "arr[...]")
	}
}

func TestRangeExprString(t *testing.T) {
	re := &RangeExpr{Token: token.Token{Literal: ".."}}
	if re.String() != "range" {
		t.Errorf("RangeExpr.String() = %q, want %q", re.String(), "range")
	}
}

func TestSpreadExprString(t *testing.T) {
	sp := &SpreadExpr{Operand: &Identifier{Value: "x"}}
	if sp.String() != "..x" {
		t.Errorf("SpreadExpr.String() = %q, want %q", sp.String(), "..x")
	}
}

func TestFieldAccessExprString(t *testing.T) {
	fa := &FieldAccessExpr{
		Left:  &Identifier{Value: "obj"},
		Field: "field",
	}
	if fa.String() != "obj.field" {
		t.Errorf("FieldAccessExpr.String() = %q, want %q", fa.String(), "obj.field")
	}
}

func TestStructInitExprString(t *testing.T) {
	si := &StructInitExpr{Type: "Point"}
	if si.String() != "Point{...}" {
		t.Errorf("StructInitExpr.String() = %q, want %q", si.String(), "Point{...}")
	}
}

func TestFnLiteralString(t *testing.T) {
	fl := &FnLiteral{}
	if fl.String() != "fn(...) { ... }" {
		t.Errorf("FnLiteral.String() = %q, want %q", fl.String(), "fn(...) { ... }")
	}
}

func TestCallExprString(t *testing.T) {
	ce := &CallExpr{Function: &Identifier{Value: "foo"}}
	if ce.String() != "foo(...)" {
		t.Errorf("CallExpr.String() = %q, want %q", ce.String(), "foo(...)")
	}
}

func TestErrorPropagationExprString(t *testing.T) {
	ep := &ErrorPropagationExpr{Expr: &Identifier{Value: "x"}}
	if ep.String() != "x?" {
		t.Errorf("ErrorPropagationExpr.String() = %q, want %q", ep.String(), "x?")
	}
}

func TestNamedTypeString(t *testing.T) {
	nt := &NamedType{Name: "int"}
	if nt.String() != "int" {
		t.Errorf("NamedType.String() = %q, want %q", nt.String(), "int")
	}
	nt2 := &NamedType{Name: "Box", Args: []Type{&NamedType{Name: "int"}}}
	if nt2.String() != "Box<int>" {
		t.Errorf("NamedType.String() = %q, want %q", nt2.String(), "Box<int>")
	}
}

func TestPointerTypeString(t *testing.T) {
	pt := &PointerType{Elem: &NamedType{Name: "int"}}
	if pt.String() != "*int" {
		t.Errorf("PointerType.String() = %q, want %q", pt.String(), "*int")
	}
}

func TestArrayTypeString(t *testing.T) {
	at := &ArrayType{Elem: &NamedType{Name: "int"}}
	if at.String() != "[]int" {
		t.Errorf("ArrayType.String() = %q, want %q", at.String(), "[]int")
	}
}

func TestMapTypeString(t *testing.T) {
	mt := &MapType{Key: &NamedType{Name: "string"}, Elem: &NamedType{Name: "int"}}
	if mt.String() != "map[string]int" {
		t.Errorf("MapType.String() = %q, want %q", mt.String(), "map[string]int")
	}
}

func TestFunctionTypeString(t *testing.T) {
	ft := &FunctionType{
		ParamTypes: []Type{&NamedType{Name: "int"}},
		ReturnType: &NamedType{Name: "bool"},
	}
	if ft.String() != "fn(int) -> bool" {
		t.Errorf("FunctionType.String() = %q, want %q", ft.String(), "fn(int) -> bool")
	}
}

func TestQueryExprString(t *testing.T) {
	qe := &QueryExpr{Token: token.Token{Literal: "from"}}
	if qe.String() != "query" {
		t.Errorf("QueryExpr.String() = %q, want %q", qe.String(), "query")
	}
}

func TestClauseNodes(t *testing.T) {
	// These just need to compile (clauseNode is a marker method)
	_ = []QueryClause{
		&FromClause{},
		&WhereClause{},
		&OrderByClause{},
		&GroupByClause{},
		&JoinClause{},
	}
}
