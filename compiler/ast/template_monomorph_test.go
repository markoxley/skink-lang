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

func TestTemplateMonomorphizeNoTemplates(t *testing.T) {
	prog := &Program{
		Declarations: []Declaration{
			&FnDecl{Name: "main", Body: &BlockStmt{}},
		},
	}
	result := TemplateMonomorphize(prog)
	if len(result.Declarations) != 1 {
		t.Errorf("expected 1 decl, got %d", len(result.Declarations))
	}
}

func TestTemplateMonomorphizeSimple(t *testing.T) {
	prog := &Program{
		Declarations: []Declaration{
			&TemplateDecl{
				Name: "Addable",
				Methods: []*FnDecl{
					{
						Name: "add",
						Params: []*Param{
							{Name: "self", Type: &NamedType{Name: "Addable"}},
							{Name: "other", Type: &NamedType{Name: "Addable"}},
						},
						ReturnType: &NamedType{Name: "Addable"},
					},
				},
			},
			&StructDecl{
				Name: "IntBox",
				Fields: []*FieldDecl{
					{Name: "val", Type: &NamedType{Name: "int"}},
				},
			},
			&FnDecl{
				Name: "sum",
				Params: []*Param{
					{Name: "a", Type: &NamedType{Name: "Addable"}},
					{Name: "b", Type: &NamedType{Name: "Addable"}},
				},
				ReturnType: &NamedType{Name: "Addable"},
				Body: &BlockStmt{
					Statements: []Statement{
						&ReturnStmt{
							Values: []Expression{
								&CallExpr{
									Function: &FieldAccessExpr{
										Left:  &Identifier{Value: "a"},
										Field: "add",
									},
									Arguments: []Expression{&Identifier{Value: "b"}},
								},
							},
						},
					},
				},
			},
			&FnDecl{
				Name: "main",
				Body: &BlockStmt{
					Statements: []Statement{
						&ExprStmt{
							Expr: &CallExpr{
								Function:  &Identifier{Value: "sum"},
								Arguments: []Expression{&StructInitExpr{Type: "IntBox"}},
							},
						},
					},
				},
			},
		},
	}
	result := TemplateMonomorphize(prog)
	// Should still have declarations (specialized versions may be added)
	if len(result.Declarations) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestMappingEqual(t *testing.T) {
	m1 := map[string]Type{"T": &NamedType{Name: "int"}}
	m2 := map[string]Type{"T": &NamedType{Name: "int"}}
	m3 := map[string]Type{"T": &NamedType{Name: "bool"}}
	if !mappingEqual(m1, m2) {
		t.Error("mappingEqual with same mappings should be true")
	}
	if mappingEqual(m1, m3) {
		t.Error("mappingEqual with different types should be false")
	}
	if mappingEqual(m1, map[string]Type{}) {
		t.Error("mappingEqual with different lengths should be false")
	}
}

func TestTypeEqual(t *testing.T) {
	if !typeEqual(nil, nil) {
		t.Error("typeEqual(nil, nil) should be true")
	}
	if typeEqual(nil, &NamedType{Name: "int"}) {
		t.Error("typeEqual(nil, int) should be false")
	}
	if !typeEqual(&NamedType{Name: "int"}, &NamedType{Name: "int"}) {
		t.Error("typeEqual(int, int) should be true")
	}
	if typeEqual(&NamedType{Name: "int"}, &NamedType{Name: "bool"}) {
		t.Error("typeEqual(int, bool) should be false")
	}
	if !typeEqual(&PointerType{Elem: &NamedType{Name: "int"}}, &PointerType{Elem: &NamedType{Name: "int"}}) {
		t.Error("typeEqual(*int, *int) should be true")
	}
}

func TestMangleTmplFn(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	got := mangleTmplFn("foo", mapping)
	if got != "foo_int" {
		t.Errorf("mangleTmplFn(foo, T=int) = %q, want %q", got, "foo_int")
	}
}

func TestTypeKey(t *testing.T) {
	if typeKey(nil) != "nil" {
		t.Errorf("typeKey(nil) = %q, want %q", typeKey(nil), "nil")
	}
	if typeKey(&NamedType{Name: "int"}) != "int" {
		t.Errorf("typeKey(int) = %q, want %q", typeKey(&NamedType{Name: "int"}), "int")
	}
	if typeKey(&PointerType{Elem: &NamedType{Name: "int"}}) != "ptr_int" {
		t.Errorf("typeKey(*int) = %q, want %q", typeKey(&PointerType{Elem: &NamedType{Name: "int"}}), "ptr_int")
	}
}

func TestSubstituteTypeTmpl(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := substituteType(&NamedType{Name: "T"}, mapping)
	if nt, ok := result.(*NamedType); !ok || nt.Name != "int" {
		t.Errorf("substituteType(T->int) = %v, want int", result)
	}
}
