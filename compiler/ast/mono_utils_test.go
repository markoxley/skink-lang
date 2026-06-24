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

func TestMangleNameNoArgs(t *testing.T) {
	if got := MangleName("Stack", nil); got != "Stack" {
		t.Errorf("MangleName(Stack, nil) = %q, want %q", got, "Stack")
	}
	if got := MangleName("Stack", []Type{}); got != "Stack" {
		t.Errorf("MangleName(Stack, []) = %q, want %q", got, "Stack")
	}
}

func TestMangleNameWithArgs(t *testing.T) {
	args := []Type{&NamedType{Name: "int"}}
	if got := MangleName("Stack", args); got != "Stack_int" {
		t.Errorf("MangleName(Stack, [int]) = %q, want %q", got, "Stack_int")
	}
	args2 := []Type{&NamedType{Name: "int"}, &NamedType{Name: "string"}}
	if got := MangleName("Map", args2); got != "Map_int_string" {
		t.Errorf("MangleName(Map, [int, string]) = %q, want %q", got, "Map_int_string")
	}
}

func TestTypeMangleKey(t *testing.T) {
	tests := []struct {
		name string
		typ  Type
		want string
	}{
		{"nil", nil, "void"},
		{"int", &NamedType{Name: "int"}, "int"},
		{"ptr", &PointerType{Elem: &NamedType{Name: "int"}}, "ptr_int"},
		{"arr", &ArrayType{Elem: &NamedType{Name: "int"}}, "arr_int"},
		{"set", &SetType{Elem: &NamedType{Name: "int"}}, "set_int"},
		{"map", &MapType{Key: &NamedType{Name: "string"}, Elem: &NamedType{Name: "int"}}, "map_string_int"},
		{"chan", &ChanType{Elem: &NamedType{Name: "int"}}, "chan_int"},
		{"tup", &TupleType{Types: []Type{&NamedType{Name: "int"}, &NamedType{Name: "bool"}}}, "tup_int_bool"},
		{"fn", &FunctionType{}, "fn"},
	}
	for _, tt := range tests {
		got := typeMangleKey(tt.typ)
		if got != tt.want {
			t.Errorf("typeMangleKey(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestTypeFromMangleKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"int", "int"},
		{"bool", "bool"},
		{"string", "string"},
		{"ptr_int", "*int"},
		{"arr_int", "[]int"},
		{"set_int", "set<int>"},
	}
	for _, tt := range tests {
		got := typeFromMangleKey(tt.key)
		if got == nil {
			t.Errorf("typeFromMangleKey(%q) = nil, want non-nil", tt.key)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("typeFromMangleKey(%q).String() = %q, want %q", tt.key, got.String(), tt.want)
		}
	}
}

func TestDemangleName(t *testing.T) {
	// Single arg
	args, ok := demangleName("Stack_int", "Stack", []string{"T"})
	if !ok {
		t.Error("demangleName(Stack_int, Stack, [T]) should succeed")
	}
	if len(args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(args))
	}
	// Multi arg
	args2, ok2 := demangleName("Map_int_string", "Map", []string{"K", "V"})
	if !ok2 {
		t.Error("demangleName(Map_int_string, Map, [K,V]) should succeed")
	}
	if len(args2) != 2 {
		t.Errorf("expected 2 args, got %d", len(args2))
	}
	// No match
	_, ok3 := demangleName("Queue_int", "Stack", []string{"T"})
	if ok3 {
		t.Error("demangleName(Queue_int, Stack, [T]) should fail")
	}
}

func TestSubstituteTypeNamed(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := SubstituteType(&NamedType{Name: "T"}, mapping)
	if nt, ok := result.(*NamedType); !ok || nt.Name != "int" {
		t.Errorf("SubstituteType(T->int) = %v, want int", result)
	}
}

func TestSubstituteTypePointer(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := SubstituteType(&PointerType{Elem: &NamedType{Name: "T"}}, mapping)
	if pt, ok := result.(*PointerType); !ok {
		t.Fatalf("expected PointerType, got %T", result)
	} else if nt, ok := pt.Elem.(*NamedType); !ok || nt.Name != "int" {
		t.Errorf("SubstituteType(*T->int) elem = %v, want int", pt.Elem)
	}
}

func TestSubstituteTypeArray(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := SubstituteType(&ArrayType{Elem: &NamedType{Name: "T"}}, mapping)
	if at, ok := result.(*ArrayType); !ok {
		t.Fatalf("expected ArrayType, got %T", result)
	} else if nt, ok := at.Elem.(*NamedType); !ok || nt.Name != "int" {
		t.Errorf("SubstituteType([]T->int) elem = %v, want int", at.Elem)
	}
}

func TestSubstituteTypeMap(t *testing.T) {
	mapping := map[string]Type{"K": &NamedType{Name: "string"}, "V": &NamedType{Name: "int"}}
	result := SubstituteType(&MapType{Key: &NamedType{Name: "K"}, Elem: &NamedType{Name: "V"}}, mapping)
	if mt, ok := result.(*MapType); !ok {
		t.Fatalf("expected MapType, got %T", result)
	} else {
		if k, ok := mt.Key.(*NamedType); !ok || k.Name != "string" {
			t.Errorf("SubstituteType map key = %v, want string", mt.Key)
		}
		if v, ok := mt.Elem.(*NamedType); !ok || v.Name != "int" {
			t.Errorf("SubstituteType map elem = %v, want int", mt.Elem)
		}
	}
}

func TestSubstituteTypeChan(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := SubstituteType(&ChanType{Elem: &NamedType{Name: "T"}}, mapping)
	if ct, ok := result.(*ChanType); !ok {
		t.Fatalf("expected ChanType, got %T", result)
	} else if nt, ok := ct.Elem.(*NamedType); !ok || nt.Name != "int" {
		t.Errorf("SubstituteType(chan<T>->int) elem = %v, want int", ct.Elem)
	}
}

func TestSubstituteTypeTuple(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}, "U": &NamedType{Name: "bool"}}
	result := SubstituteType(&TupleType{Types: []Type{&NamedType{Name: "T"}, &NamedType{Name: "U"}}}, mapping)
	if tt, ok := result.(*TupleType); !ok {
		t.Fatalf("expected TupleType, got %T", result)
	} else if len(tt.Types) != 2 {
		t.Errorf("expected 2 types, got %d", len(tt.Types))
	}
}

func TestSubstituteTypeFunction(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}, "R": &NamedType{Name: "bool"}}
	result := SubstituteType(&FunctionType{
		ParamTypes: []Type{&NamedType{Name: "T"}},
		ReturnType: &NamedType{Name: "R"},
	}, mapping)
	if ft, ok := result.(*FunctionType); !ok {
		t.Fatalf("expected FunctionType, got %T", result)
	} else {
		if len(ft.ParamTypes) != 1 {
			t.Errorf("expected 1 param, got %d", len(ft.ParamTypes))
		}
		if r, ok := ft.ReturnType.(*NamedType); !ok || r.Name != "bool" {
			t.Errorf("SubstituteType fn ret = %v, want bool", ft.ReturnType)
		}
	}
}

func TestSubstituteTypeNoMatch(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := SubstituteType(&NamedType{Name: "U"}, mapping)
	if nt, ok := result.(*NamedType); !ok || nt.Name != "U" {
		t.Errorf("SubstituteType(U with T->int) = %v, want U", result)
	}
}

func TestSubstituteTypeGenericArgs(t *testing.T) {
	mapping := map[string]Type{"T": &NamedType{Name: "int"}}
	result := SubstituteType(&NamedType{Name: "Box", Args: []Type{&NamedType{Name: "T"}}}, mapping)
	if nt, ok := result.(*NamedType); !ok {
		t.Fatalf("expected NamedType, got %T", result)
	} else {
		if nt.Name != "Box" {
			t.Errorf("SubstituteType generic name = %q, want Box", nt.Name)
		}
		if len(nt.Args) != 1 {
			t.Fatalf("expected 1 arg, got %d", len(nt.Args))
		}
		if arg, ok := nt.Args[0].(*NamedType); !ok || arg.Name != "int" {
			t.Errorf("SubstituteType generic arg = %v, want int", nt.Args[0])
		}
	}
}
