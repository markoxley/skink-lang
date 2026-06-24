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

package parser

import (
	"testing"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/lexer"
)

func TestParseFnDecl(t *testing.T) {
	input := `fn add(a: int, b: int) -> int {
		return a + b
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()

	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	fn, ok := program.Declarations[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("expected *ast.FnDecl, got %T", program.Declarations[0])
	}

	if fn.Name != "add" {
		t.Errorf("expected name 'add', got %q", fn.Name)
	}

	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}

	if fn.Params[0].Name != "a" {
		t.Errorf("expected param[0].Name='a', got %q", fn.Params[0].Name)
	}

	if fn.ReturnType == nil {
		t.Fatalf("expected return type, got nil")
	}
}

func TestParseVarStmt(t *testing.T) {
	input := `fn main() {
		x := 10
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	if len(fn.Body.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(fn.Body.Statements))
	}

	varStmt, ok := fn.Body.Statements[0].(*ast.VarStmt)
	if !ok {
		t.Fatalf("expected *ast.VarStmt, got %T", fn.Body.Statements[0])
	}

	if varStmt.Name != "x" {
		t.Errorf("expected name 'x', got %q", varStmt.Name)
	}

	if !varStmt.Implicit {
		t.Errorf("expected Implicit=true for :=")
	}
}

func TestParseIfStmt(t *testing.T) {
	input := `fn main() {
		if x > 5 {
			return x
		} else {
			return 0
		}
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	ifStmt, ok := fn.Body.Statements[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected *ast.IfStmt, got %T", fn.Body.Statements[0])
	}

	if ifStmt.Consequence == nil {
		t.Errorf("expected consequence block")
	}
	if ifStmt.Alternative == nil {
		t.Errorf("expected alternative block")
	}
}

func TestParseReturnStmt(t *testing.T) {
	input := `fn foo() -> int {
		return 42
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	ret, ok := fn.Body.Statements[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected *ast.ReturnStmt, got %T", fn.Body.Statements[0])
	}

	if len(ret.Values) != 1 {
		t.Fatalf("expected 1 return value, got %d", len(ret.Values))
	}

	lit, ok := ret.Values[0].(*ast.IntegerLiteral)
	if !ok {
		t.Fatalf("expected IntegerLiteral, got %T", ret.Values[0])
	}
	if lit.Value != 42 {
		t.Errorf("expected 42, got %d", lit.Value)
	}
}

func TestParsePrefixExpression(t *testing.T) {
	input := `fn main() {
		return -5
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	ret := fn.Body.Statements[0].(*ast.ReturnStmt)
	prefix, ok := ret.Values[0].(*ast.PrefixExpr)
	if !ok {
		t.Fatalf("expected *ast.PrefixExpr, got %T", ret.Values[0])
	}
	if prefix.Operator != "-" {
		t.Errorf("expected operator '-', got %q", prefix.Operator)
	}
}

func TestParseInfixExpression(t *testing.T) {
	input := `fn main() {
		return 1 + 2 * 3
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	ret := fn.Body.Statements[0].(*ast.ReturnStmt)
	infix, ok := ret.Values[0].(*ast.InfixExpr)
	if !ok {
		t.Fatalf("expected *ast.InfixExpr, got %T", ret.Values[0])
	}
	if infix.Operator != "+" {
		t.Errorf("expected top-level operator '+', got %q", infix.Operator)
	}
}

func TestParseCallExpression(t *testing.T) {
	input := `fn main() {
		print("hello")
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	exprStmt, ok := fn.Body.Statements[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected *ast.ExprStmt, got %T", fn.Body.Statements[0])
	}

	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected *ast.CallExpr, got %T", exprStmt.Expr)
	}

	ident, ok := call.Function.(*ast.Identifier)
	if !ok {
		t.Fatalf("expected Identifier function, got %T", call.Function)
	}
	if ident.Value != "print" {
		t.Errorf("expected function name 'print', got %q", ident.Value)
	}

	if len(call.Arguments) != 1 {
		t.Errorf("expected 1 arg, got %d", len(call.Arguments))
	}
}

func TestParsePubFnDecl(t *testing.T) {
	input := `pub fn add(a: int, b: int) -> int {
		return a + b
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	fn, ok := program.Declarations[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("expected *ast.FnDecl, got %T", program.Declarations[0])
	}

	if !fn.Pub {
		t.Errorf("expected pub=true")
	}
	if fn.Name != "add" {
		t.Errorf("expected name 'add', got %q", fn.Name)
	}
}

func TestParseConstDecl(t *testing.T) {
	input := `const MAX = 100`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	decl, ok := program.Declarations[0].(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected *ast.ConstDecl, got %T", program.Declarations[0])
	}

	if decl.Name != "MAX" {
		t.Errorf("expected name 'MAX', got %q", decl.Name)
	}
}

func TestParseStructDecl(t *testing.T) {
	input := `pub struct Point {
		x: int,
		y: int,
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	decl, ok := program.Declarations[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected *ast.StructDecl, got %T", program.Declarations[0])
	}

	if !decl.Pub {
		t.Errorf("expected pub=true")
	}
	if decl.Name != "Point" {
		t.Errorf("expected name 'Point', got %q", decl.Name)
	}
	if len(decl.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(decl.Fields))
	}
}

func TestParseAssignmentStmt(t *testing.T) {
	input := `fn main() {
		x = 10
		x += 1
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	if len(fn.Body.Statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(fn.Body.Statements))
	}

	assign, ok := fn.Body.Statements[0].(*ast.AssignmentStmt)
	if !ok {
		t.Fatalf("expected *ast.AssignmentStmt, got %T", fn.Body.Statements[0])
	}
	if assign.Operator != "=" {
		t.Errorf("expected operator '=', got %q", assign.Operator)
	}

	addAssign, ok := fn.Body.Statements[1].(*ast.AssignmentStmt)
	if !ok {
		t.Fatalf("expected *ast.AssignmentStmt, got %T", fn.Body.Statements[1])
	}
	if addAssign.Operator != "+=" {
		t.Errorf("expected operator '+=', got %q", addAssign.Operator)
	}
}

func TestParseWhileStmt(t *testing.T) {
	input := `fn main() {
		while x < 10 {
			x = x + 1
		}
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	whileStmt, ok := fn.Body.Statements[0].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected *ast.WhileStmt, got %T", fn.Body.Statements[0])
	}
	if whileStmt.Condition == nil {
		t.Errorf("expected condition")
	}
	if whileStmt.Body == nil {
		t.Errorf("expected body")
	}
}

func TestParseForInStmt(t *testing.T) {
	input := `fn main() {
		for i in items {
			print(i)
		}
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	forStmt, ok := fn.Body.Statements[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected *ast.ForStmt, got %T", fn.Body.Statements[0])
	}
	if forStmt.Iterator == nil {
		t.Fatalf("expected iterator")
	}
	if forStmt.Iterator.Variable != "i" {
		t.Errorf("expected variable 'i', got %q", forStmt.Iterator.Variable)
	}
}

func TestParseArrayLiteral(t *testing.T) {
	input := `fn main() {
		items := [1, 2, 3]
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	varStmt, ok := fn.Body.Statements[0].(*ast.VarStmt)
	if !ok {
		t.Fatalf("expected *ast.VarStmt, got %T", fn.Body.Statements[0])
	}

	arrayLit, ok := varStmt.Value.(*ast.ArrayLiteral)
	if !ok {
		t.Fatalf("expected *ast.ArrayLiteral, got %T", varStmt.Value)
	}
	if len(arrayLit.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arrayLit.Elements))
	}
}

func TestParseForCStyleStmt(t *testing.T) {
	input := `fn main() {
		for i := 0; i < 10; i = i + 1 {
			print(i)
		}
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	forStmt, ok := fn.Body.Statements[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected *ast.ForStmt, got %T", fn.Body.Statements[0])
	}
	if forStmt.Init == nil {
		t.Errorf("expected init")
	}
	if forStmt.Condition == nil {
		t.Errorf("expected condition")
	}
	if forStmt.Post == nil {
		t.Errorf("expected post")
	}
}

// checkParserErrors fails the test immediately if the parser produced any errors.
func checkParserErrors(t *testing.T, p *Parser) {
	errors := p.Errors()
	if len(errors) == 0 {
		return
	}
	t.Errorf("parser has %d errors:", len(errors))
	for _, err := range errors {
		t.Errorf("  parser error: %s", err)
	}
	t.FailNow()
}

func TestParseDocComment(t *testing.T) {
	input := `/// Adds two numbers.
fn add(a: int, b: int) -> int {
		return a + b
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	fn, ok := program.Declarations[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("expected *ast.FnDecl, got %T", program.Declarations[0])
	}

	if fn.Name != "add" {
		t.Errorf("expected fn name 'add', got %q", fn.Name)
	}

	if fn.Doc != "Adds two numbers." {
		t.Errorf("expected doc 'Adds two numbers.', got %q", fn.Doc)
	}
}

func TestParseGenericStruct(t *testing.T) {
	input := `struct Box<T> {
		value: T
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	s, ok := program.Declarations[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected *ast.StructDecl, got %T", program.Declarations[0])
	}

	if s.Name != "Box" {
		t.Errorf("expected struct name 'Box', got %q", s.Name)
	}

	if len(s.TypeParams) != 1 || s.TypeParams[0] != "T" {
		t.Errorf("expected type params [T], got %v", s.TypeParams)
	}
}

func TestParseGenericFn(t *testing.T) {
	input := `fn identity<T>(x: T) -> T {
		return x
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	fn, ok := program.Declarations[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("expected *ast.FnDecl, got %T", program.Declarations[0])
	}

	if fn.Name != "identity" {
		t.Errorf("expected fn name 'identity', got %q", fn.Name)
	}

	if len(fn.TypeParams) != 1 || fn.TypeParams[0] != "T" {
		t.Errorf("expected type params [T], got %v", fn.TypeParams)
	}

	if len(fn.Params) != 1 || fn.Params[0].Name != "x" {
		t.Errorf("expected 1 param 'x', got %v", fn.Params)
	}
}

func TestParseImportBlock(t *testing.T) {
	input := `import { "fmt", "math" }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 2 {
		t.Fatalf("expected 2 import declarations, got %d", len(program.Declarations))
	}

	imp1, ok1 := program.Declarations[0].(*ast.ImportDecl)
	if !ok1 {
		t.Fatalf("expected *ast.ImportDecl, got %T", program.Declarations[0])
	}
	if imp1.Path != "fmt" || imp1.Alias != "fmt" {
		t.Errorf("expected import fmt as fmt, got path=%q alias=%q", imp1.Path, imp1.Alias)
	}

	imp2, ok2 := program.Declarations[1].(*ast.ImportDecl)
	if !ok2 {
		t.Fatalf("expected *ast.ImportDecl, got %T", program.Declarations[1])
	}
	if imp2.Path != "math" || imp2.Alias != "math" {
		t.Errorf("expected import math as math, got path=%q alias=%q", imp2.Path, imp2.Alias)
	}
}

func TestParseImportAsAlias(t *testing.T) {
	input := `import "std/str" as s`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 import declaration, got %d", len(program.Declarations))
	}

	imp, ok := program.Declarations[0].(*ast.ImportDecl)
	if !ok {
		t.Fatalf("expected *ast.ImportDecl, got %T", program.Declarations[0])
	}
	if imp.Path != "std/str" || imp.Alias != "s" {
		t.Errorf("expected import std/str as s, got path=%q alias=%q", imp.Path, imp.Alias)
	}
	if imp.String() != "import std/str as s" {
		t.Errorf("expected String() = \"import std/str as s\", got %q", imp.String())
	}
}

func TestParseImportBlockAsAlias(t *testing.T) {
	input := `import { "std/str" as s, "std/json" as j }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 2 {
		t.Fatalf("expected 2 import declarations, got %d", len(program.Declarations))
	}

	imp1, ok1 := program.Declarations[0].(*ast.ImportDecl)
	if !ok1 {
		t.Fatalf("expected *ast.ImportDecl, got %T", program.Declarations[0])
	}
	if imp1.Path != "std/str" || imp1.Alias != "s" {
		t.Errorf("expected import std/str as s, got path=%q alias=%q", imp1.Path, imp1.Alias)
	}

	imp2, ok2 := program.Declarations[1].(*ast.ImportDecl)
	if !ok2 {
		t.Fatalf("expected *ast.ImportDecl, got %T", program.Declarations[1])
	}
	if imp2.Path != "std/json" || imp2.Alias != "j" {
		t.Errorf("expected import std/json as j, got path=%q alias=%q", imp2.Path, imp2.Alias)
	}
}

func TestParseImportLegacyAlias(t *testing.T) {
	input := `import s "std/str"`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	imp, ok := program.Declarations[0].(*ast.ImportDecl)
	if !ok {
		t.Fatalf("expected *ast.ImportDecl, got %T", program.Declarations[0])
	}
	if imp.Path != "std/str" || imp.Alias != "s" {
		t.Errorf("expected import s std/str, got path=%q alias=%q", imp.Path, imp.Alias)
	}
}

func TestParseTemplateDecl(t *testing.T) {
	input := `template Writer {
		fn Write(self: Writer, b: []byte)
		fn Flush(self: Writer)
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	td, ok := program.Declarations[0].(*ast.TemplateDecl)
	if !ok {
		t.Fatalf("expected *ast.TemplateDecl, got %T", program.Declarations[0])
	}
	if td.Name != "Writer" {
		t.Errorf("expected template name 'Writer', got %q", td.Name)
	}
	if len(td.Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(td.Methods))
	}
	if td.Methods[0].Name != "Write" {
		t.Errorf("expected method 'Write', got %q", td.Methods[0].Name)
	}
	if td.Methods[1].Name != "Flush" {
		t.Errorf("expected method 'Flush', got %q", td.Methods[1].Name)
	}
}

func TestParseAnonymousFunction(t *testing.T) {
	input := `fn main() {
		add := fn(a: int, b: int) -> int { return a + b }
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	fn, ok := program.Declarations[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("expected *ast.FnDecl, got %T", program.Declarations[0])
	}
	body := fn.Body
	if len(body.Statements) != 1 {
		t.Fatalf("expected 1 statement in body, got %d", len(body.Statements))
	}
	varStmt, ok := body.Statements[0].(*ast.VarStmt)
	if !ok {
		t.Fatalf("expected *ast.VarStmt, got %T", body.Statements[0])
	}
	fnLit, ok := varStmt.Value.(*ast.FnLiteral)
	if !ok {
		t.Fatalf("expected *ast.FnLiteral, got %T", varStmt.Value)
	}
	if len(fnLit.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(fnLit.Params))
	}
	if fnLit.Params[0].Name != "a" || fnLit.Params[1].Name != "b" {
		t.Errorf("expected params a and b, got %s and %s", fnLit.Params[0].Name, fnLit.Params[1].Name)
	}
}

func TestParseStructEmbedding(t *testing.T) {
	input := `struct Point {
		x: int
		y: int
	}
	struct LabelledPoint {
		Point
		label: string
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 2 {
		t.Fatalf("expected 2 declarations, got %d", len(program.Declarations))
	}

	lp, ok := program.Declarations[1].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected *ast.StructDecl, got %T", program.Declarations[1])
	}
	if len(lp.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(lp.Fields))
	}
	if !lp.Fields[0].Embedded {
		t.Errorf("expected first field to be embedded")
	}
	if lp.Fields[0].Name != "Point" {
		t.Errorf("expected embedded field name 'Point', got %q", lp.Fields[0].Name)
	}
	if lp.Fields[1].Name != "label" {
		t.Errorf("expected field name 'label', got %q", lp.Fields[1].Name)
	}
}

func TestParseVariadicFunction(t *testing.T) {
	input := `fn sum(first: int, rest: ...int) -> int {
		return first
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(program.Declarations))
	}

	fn, ok := program.Declarations[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("expected *ast.FnDecl, got %T", program.Declarations[0])
	}
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}
	if fn.Params[0].Name != "first" || fn.Params[0].Variadic {
		t.Errorf("expected first param non-variadic")
	}
	if fn.Params[1].Name != "rest" || !fn.Params[1].Variadic {
		t.Errorf("expected second param to be variadic")
	}
}

func TestParseSwitchStmt(t *testing.T) {
	input := `fn main() {
		x := 2
		switch x {
		case 1:
			print("one")
		case 2, 3:
			print("two or three")
		default:
			print("other")
		}
	}`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	sw, ok := fn.Body.Statements[1].(*ast.SwitchStmt)
	if !ok {
		t.Fatalf("expected *ast.SwitchStmt, got %T", fn.Body.Statements[1])
	}
	if len(sw.Cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(sw.Cases))
	}
	if !sw.Cases[0].IsDefault && len(sw.Cases[0].Values) != 1 {
		t.Errorf("expected 1 value in first case, got %d", len(sw.Cases[0].Values))
	}
	if sw.Cases[1].IsDefault {
		t.Errorf("expected second case to be non-default")
	}
	if len(sw.Cases[1].Values) != 2 {
		t.Errorf("expected 2 values in second case, got %d", len(sw.Cases[1].Values))
	}
	if !sw.Cases[2].IsDefault {
		t.Errorf("expected third case to be default")
	}
}

func TestParseOmittedSliceBounds(t *testing.T) {
	input := `fn main() {
		a := arr[..]
		b := arr[..5]
		c := arr[2..]
	}`
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	if len(fn.Body.Statements) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(fn.Body.Statements))
	}

	s1 := fn.Body.Statements[0].(*ast.VarStmt).Value.(*ast.SliceExpr)
	if s1.Start != nil || s1.End != nil {
		t.Errorf("expected both bounds omitted for arr[..]")
	}

	s2 := fn.Body.Statements[1].(*ast.VarStmt).Value.(*ast.SliceExpr)
	if s2.Start != nil {
		t.Errorf("expected start omitted for arr[..5]")
	}
	if s2.End == nil {
		t.Errorf("expected end present for arr[..5]")
	}

	s3 := fn.Body.Statements[2].(*ast.VarStmt).Value.(*ast.SliceExpr)
	if s3.Start == nil {
		t.Errorf("expected start present for arr[2..]")
	}
	if s3.End != nil {
		t.Errorf("expected end omitted for arr[2..]")
	}
}

func TestParseFromEndIndex(t *testing.T) {
	input := `fn main() {
		last := arr[^1]
	}`
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	idx := fn.Body.Statements[0].(*ast.VarStmt).Value.(*ast.IndexExpr)
	fe, ok := idx.Index.(*ast.FromEndIndexExpr)
	if !ok {
		t.Fatalf("expected *ast.FromEndIndexExpr, got %T", idx.Index)
	}
	if fe.Operand.(*ast.IntegerLiteral).Value != 1 {
		t.Errorf("expected operand 1, got %d", fe.Operand.(*ast.IntegerLiteral).Value)
	}
}

func TestParseSpreadExpr(t *testing.T) {
	input := `fn main() {
		b := [..a, 1, ..c]
	}`
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	arr := fn.Body.Statements[0].(*ast.VarStmt).Value.(*ast.ArrayLiteral)
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
	if _, ok := arr.Elements[0].(*ast.SpreadExpr); !ok {
		t.Errorf("expected first element to be SpreadExpr, got %T", arr.Elements[0])
	}
	if _, ok := arr.Elements[1].(*ast.IntegerLiteral); !ok {
		t.Errorf("expected second element to be IntegerLiteral, got %T", arr.Elements[1])
	}
	if _, ok := arr.Elements[2].(*ast.SpreadExpr); !ok {
		t.Errorf("expected third element to be SpreadExpr, got %T", arr.Elements[2])
	}
}

func TestParseBracketMapLiteral(t *testing.T) {
	input := `fn main() {
		m := ["a": 1, "b": 2]
	}`
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	ml := fn.Body.Statements[0].(*ast.VarStmt).Value.(*ast.MapLiteral)
	if len(ml.Pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(ml.Pairs))
	}
	if ml.Pairs[0].Key.(*ast.StringLiteral).Value != "a" {
		t.Errorf("expected key 'a', got %q", ml.Pairs[0].Key.(*ast.StringLiteral).Value)
	}
	if ml.Pairs[1].Key.(*ast.StringLiteral).Value != "b" {
		t.Errorf("expected key 'b', got %q", ml.Pairs[1].Key.(*ast.StringLiteral).Value)
	}
}

func TestParseLinqQuery(t *testing.T) {
	input := `fn main() {
		result := from x in arr where x > 2 select x * 10
	}`
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	query := fn.Body.Statements[0].(*ast.VarStmt).Value.(*ast.QueryExpr)
	if query.From.Variable != "x" {
		t.Errorf("expected variable 'x', got %q", query.From.Variable)
	}
	if len(query.Clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(query.Clauses))
	}
	_, ok := query.Clauses[0].(*ast.WhereClause)
	if !ok {
		t.Fatalf("expected WhereClause, got %T", query.Clauses[0])
	}
}

func TestParseLinqQuerySimple(t *testing.T) {
	input := `fn main() {
		result := from x in arr select x * 2
	}`
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn := program.Declarations[0].(*ast.FnDecl)
	query := fn.Body.Statements[0].(*ast.VarStmt).Value.(*ast.QueryExpr)
	if query.From.Variable != "x" {
		t.Errorf("expected variable 'x', got %q", query.From.Variable)
	}
	if len(query.Clauses) != 0 {
		t.Fatalf("expected 0 clauses, got %d", len(query.Clauses))
	}
}
