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

package codegen

import (
	"testing"

	"github.com/skink-lang/compiler/ast"
)

func TestIsChanType(t *testing.T) {
	if isChanType(nil) {
		t.Error("isChanType(nil) should be false")
	}
	if !isChanType(&ast.ChanType{Elem: &ast.NamedType{Name: "int"}}) {
		t.Error("isChanType(chan<int>) should be true")
	}
	if isChanType(&ast.NamedType{Name: "int"}) {
		t.Error("isChanType(int) should be false")
	}
}

func TestHasConcurrencyConstructs(t *testing.T) {
	// spawn
	stmts := []ast.Statement{
		&ast.SpawnStmt{Call: &ast.CallExpr{Function: &ast.Identifier{Value: "foo"}}},
	}
	if !hasConcurrencyConstructs(stmts) {
		t.Error("should detect spawn")
	}
	// select
	stmts = []ast.Statement{
		&ast.SelectStmt{},
	}
	if !hasConcurrencyConstructs(stmts) {
		t.Error("should detect select")
	}
	// nested in block
	stmts = []ast.Statement{
		&ast.BlockStmt{
			Statements: []ast.Statement{
				&ast.SpawnStmt{Call: &ast.CallExpr{Function: &ast.Identifier{Value: "foo"}}},
			},
		},
	}
	if !hasConcurrencyConstructs(stmts) {
		t.Error("should detect spawn in block")
	}
	// no concurrency
	stmts = []ast.Statement{
		&ast.ExprStmt{Expr: &ast.IntegerLiteral{Value: 1}},
	}
	if hasConcurrencyConstructs(stmts) {
		t.Error("should not detect concurrency in simple expr")
	}
}

func TestExprUsesConcurrency(t *testing.T) {
	if exprUsesConcurrency(nil) {
		t.Error("exprUsesConcurrency(nil) should be false")
	}
	// async
	if !exprUsesConcurrency(&ast.AsyncExpr{Expr: &ast.Identifier{Value: "foo"}}) {
		t.Error("should detect async")
	}
	// await
	if !exprUsesConcurrency(&ast.AwaitExpr{Expr: &ast.Identifier{Value: "foo"}}) {
		t.Error("should detect await")
	}
	// make(chan)
	if !exprUsesConcurrency(&ast.MakeExpr{Type: &ast.ChanType{Elem: &ast.NamedType{Name: "int"}}}) {
		t.Error("should detect make(chan)")
	}
	// make(int) - not concurrency
	if exprUsesConcurrency(&ast.MakeExpr{Type: &ast.NamedType{Name: "int"}}) {
		t.Error("should not detect make(int)")
	}
	// close call
	if !exprUsesConcurrency(&ast.CallExpr{
		Function: &ast.Identifier{Value: "close"},
	}) {
		t.Error("should detect close call")
	}
	// simple call
	if exprUsesConcurrency(&ast.CallExpr{
		Function: &ast.Identifier{Value: "foo"},
	}) {
		t.Error("should not detect simple call")
	}
}

func TestDetectNeededRuntimeFiles(t *testing.T) {
	prog := &ast.Program{
		Declarations: []ast.Declaration{
			&ast.ImportDecl{Path: "std/sync"},
		},
	}
	needed := detectNeededRuntimeFiles(prog)
	if !needed["sync_rt.c"] {
		t.Error("std/sync should need sync_rt.c")
	}

	prog = &ast.Program{
		Declarations: []ast.Declaration{
			&ast.ImportDecl{Path: "std/db"},
		},
	}
	needed = detectNeededRuntimeFiles(prog)
	if !needed["db_rt.c"] {
		t.Error("std/db should need db_rt.c")
	}

	prog = &ast.Program{
		Declarations: []ast.Declaration{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Statements: []ast.Statement{
						&ast.SpawnStmt{Call: &ast.CallExpr{Function: &ast.Identifier{Value: "foo"}}},
					},
				},
			},
		},
	}
	needed = detectNeededRuntimeFiles(prog)
	if !needed["conc_rt.c"] {
		t.Error("spawn should need conc_rt.c")
	}
}

func TestPickLinker(t *testing.T) {
	// Just ensure it doesn't panic and returns a string
	_ = pickLinker()
}

func TestResolveLibPath(t *testing.T) {
	// Just ensure it doesn't panic and returns a string
	_ = resolveLibPath("c")
}
