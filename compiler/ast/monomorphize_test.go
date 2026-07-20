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
)

func TestMonomorphizeNoGenerics(t *testing.T) {
	prog := &Program{
		Declarations: []Declaration{
			&FnDecl{Name: "main", Body: &BlockStmt{}},
		},
	}
	result := Monomorphize(prog)
	if len(result.Declarations) != 1 {
		t.Errorf("expected 1 decl, got %d", len(result.Declarations))
	}
}

func TestMonomorphizeGenericStruct(t *testing.T) {
	prog := &Program{
		Declarations: []Declaration{
			&StructDecl{
				Name:       "Box",
				TypeParams: []string{"T"},
				Fields: []*FieldDecl{
					{Name: "value", Type: &NamedType{Name: "T"}},
				},
			},
			&FnDecl{
				Name: "main",
				Body: &BlockStmt{
					Statements: []Statement{
						&VarStmt{
							Name:  "b",
							Type:  &NamedType{Name: "Box", Args: []Type{&NamedType{Name: "int"}}},
							Value: &StructInitExpr{Type: "Box_int"},
						},
					},
				},
			},
		},
	}
	result := Monomorphize(prog)
	// Should have Box_int specialization plus main
	found := false
	for _, decl := range result.Declarations {
		if sd, ok := decl.(*StructDecl); ok && sd.Name == "Box_int" {
			found = true
			if len(sd.Fields) != 1 {
				t.Errorf("Box_int should have 1 field, got %d", len(sd.Fields))
			}
		}
	}
	if !found {
		t.Error("Box_int specialization not found")
	}
}

func TestMonomorphizeGenericFn(t *testing.T) {
	prog := &Program{
		Declarations: []Declaration{
			&FnDecl{
				Name:       "identity",
				TypeParams: []string{"T"},
				Params:     []*Param{{Name: "x", Type: &NamedType{Name: "T"}}},
				ReturnType: &NamedType{Name: "T"},
				Body: &BlockStmt{
					Statements: []Statement{
						&ReturnStmt{Values: []Expression{&Identifier{Value: "x"}}},
					},
				},
			},
			&FnDecl{
				Name: "main",
				Body: &BlockStmt{
					Statements: []Statement{
						&ExprStmt{
							Expr: &CallExpr{
								Function: &Identifier{Value: "identity_int"},
								Arguments: []Expression{&IntegerLiteral{Value: 42}},
							},
						},
					},
				},
			},
		},
	}
	result := Monomorphize(prog)
	found := false
	for _, decl := range result.Declarations {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "identity_int" {
			found = true
			if len(fd.Params) != 1 {
				t.Errorf("identity_int should have 1 param, got %d", len(fd.Params))
			}
		}
	}
	if !found {
		t.Error("identity_int specialization not found")
	}
}
