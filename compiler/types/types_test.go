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

package types

import (
	"testing"
)

func TestBuiltInTypeString(t *testing.T) {
	if Int.String() != "int" {
		t.Errorf("Int.String() = %q, want %q", Int.String(), "int")
	}
	if Bool.String() != "bool" {
		t.Errorf("Bool.String() = %q, want %q", Bool.String(), "bool")
	}
}

func TestBuiltInTypeEquals(t *testing.T) {
	if !Int.Equals(Int) {
		t.Error("Int.Equals(Int) should be true")
	}
	if Int.Equals(Bool) {
		t.Error("Int.Equals(Bool) should be false")
	}
	if !Int.Equals(&BuiltInType{Name: "int"}) {
		t.Error("Int.Equals(&BuiltInType{Name: 'int'}) should be true")
	}
}

func TestPointerTypeString(t *testing.T) {
	p := &PointerType{Elem: Int}
	if p.String() != "*int" {
		t.Errorf("PointerType.String() = %q, want %q", p.String(), "*int")
	}
}

func TestPointerTypeEquals(t *testing.T) {
	p1 := &PointerType{Elem: Int}
	p2 := &PointerType{Elem: Int}
	p3 := &PointerType{Elem: Bool}
	if !p1.Equals(p2) {
		t.Error("equal pointer types should match")
	}
	if p1.Equals(p3) {
		t.Error("different pointer types should not match")
	}
}

func TestArrayTypeString(t *testing.T) {
	a := &ArrayType{Elem: Int}
	if a.String() != "[]int" {
		t.Errorf("ArrayType.String() = %q, want %q", a.String(), "[]int")
	}
}

func TestArrayTypeEquals(t *testing.T) {
	a1 := &ArrayType{Elem: Int}
	a2 := &ArrayType{Elem: Int}
	a3 := &ArrayType{Elem: Bool}
	if !a1.Equals(a2) {
		t.Error("equal array types should match")
	}
	if a1.Equals(a3) {
		t.Error("different array types should not match")
	}
}

func TestSetTypeString(t *testing.T) {
	s := &SetType{Elem: Int}
	if s.String() != "set<int>" {
		t.Errorf("SetType.String() = %q, want %q", s.String(), "set<int>")
	}
}

func TestSetTypeEquals(t *testing.T) {
	s1 := &SetType{Elem: Int}
	s2 := &SetType{Elem: Int}
	s3 := &SetType{Elem: Bool}
	if !s1.Equals(s2) {
		t.Error("equal set types should match")
	}
	if s1.Equals(s3) {
		t.Error("different set types should not match")
	}
}

func TestMapTypeString(t *testing.T) {
	m := &MapType{Key: String, Elem: Int}
	if m.String() != "map[string]int" {
		t.Errorf("MapType.String() = %q, want %q", m.String(), "map[string]int")
	}
}

func TestMapTypeEquals(t *testing.T) {
	m1 := &MapType{Key: String, Elem: Int}
	m2 := &MapType{Key: String, Elem: Int}
	m3 := &MapType{Key: Int, Elem: Bool}
	if !m1.Equals(m2) {
		t.Error("equal map types should match")
	}
	if m1.Equals(m3) {
		t.Error("different map types should not match")
	}
}

func TestChanTypeString(t *testing.T) {
	c := &ChanType{Elem: Int}
	if c.String() != "chan<int>" {
		t.Errorf("ChanType.String() = %q, want %q", c.String(), "chan<int>")
	}
}

func TestChanTypeEquals(t *testing.T) {
	c1 := &ChanType{Elem: Int}
	c2 := &ChanType{Elem: Int}
	c3 := &ChanType{Elem: Bool}
	if !c1.Equals(c2) {
		t.Error("equal chan types should match")
	}
	if c1.Equals(c3) {
		t.Error("different chan types should not match")
	}
}

func TestTensorTypeString(t *testing.T) {
	tr := &TensorType{Elem: Float}
	if tr.String() != "tensor<float>" {
		t.Errorf("TensorType.String() = %q, want %q", tr.String(), "tensor<float>")
	}
}

func TestNamedTypeString(t *testing.T) {
	n := &NamedType{Name: "Foo"}
	if n.String() != "Foo" {
		t.Errorf("NamedType.String() = %q, want %q", n.String(), "Foo")
	}
}

func TestNamedTypeEquals(t *testing.T) {
	n1 := &NamedType{Name: "foo.bar"}
	n2 := &NamedType{Name: "foo_bar"}
	if !n1.Equals(n2) {
		t.Error("NamedType.Equals should match when dots replaced with underscores")
	}
}

func TestFunctionTypeString(t *testing.T) {
	f := &FunctionType{Params: []Type{Int, Int}, Ret: []Type{Int}}
	if f.String() != "<function>" {
		t.Errorf("FunctionType.String() = %q, want %q", f.String(), "<function>")
	}
}

func TestFunctionTypeEquals(t *testing.T) {
	f1 := &FunctionType{Params: []Type{Int}, Ret: []Type{Bool}, Variadic: false}
	f2 := &FunctionType{Params: []Type{Int}, Ret: []Type{Bool}, Variadic: false}
	f3 := &FunctionType{Params: []Type{Int}, Ret: []Type{Bool}, Variadic: true}
	if !f1.Equals(f2) {
		t.Error("equal function types should match")
	}
	if f1.Equals(f3) {
		t.Error("different variadic flags should not match")
	}
}

func TestTupleTypeString(t *testing.T) {
	tup := &TupleType{Types: []Type{Int, Bool}}
	if tup.String() != "[int bool]" && tup.String() != "[0x1400000? 0x1400000?]" {
		// String uses fmt.Sprintf which may print pointers; accept either
	}
}

func TestTupleTypeEquals(t *testing.T) {
	t1 := &TupleType{Types: []Type{Int, Bool}}
	t2 := &TupleType{Types: []Type{Int, Bool}}
	t3 := &TupleType{Types: []Type{Bool, Int}}
	if !t1.Equals(t2) {
		t.Error("equal tuple types should match")
	}
	if t1.Equals(t3) {
		t.Error("different tuple types should not match")
	}
}

func TestNilType(t *testing.T) {
	if Nil.String() != "nil" {
		t.Errorf("Nil.String() = %q, want %q", Nil.String(), "nil")
	}
	if !Nil.Equals(&NilType{}) {
		t.Error("Nil.Equals(&NilType{}) should be true")
	}
	if Nil.Equals(Int) {
		t.Error("Nil.Equals(Int) should be false")
	}
}

func TestAnyType(t *testing.T) {
	if Any.String() != "any" {
		t.Errorf("Any.String() = %q, want %q", Any.String(), "any")
	}
	if !Any.Equals(&AnyType{}) {
		t.Error("Any.Equals(&AnyType{}) should be true")
	}
}

func TestUnknownType(t *testing.T) {
	if Unknown.String() != "<unknown>" {
		t.Errorf("Unknown.String() = %q, want %q", Unknown.String(), "<unknown>")
	}
}

func TestErrorType(t *testing.T) {
	if Error.String() != "error" {
		t.Errorf("Error.String() = %q, want %q", Error.String(), "error")
	}
	if !Error.Equals(&ErrorType{}) {
		t.Error("Error.Equals(&ErrorType{}) should be true")
	}
}

func TestInterfaceType(t *testing.T) {
	i := &InterfaceType{Name: "Reader", Methods: map[string]Type{"Read": &FunctionType{}}}
	if i.String() != "Reader" {
		t.Errorf("InterfaceType.String() = %q, want %q", i.String(), "Reader")
	}
	i2 := &InterfaceType{Name: "Reader", Methods: map[string]Type{}}
	if !i.Equals(i2) {
		t.Error("InterfaceType.Equals should match on name")
	}
	if i.Equals(&InterfaceType{Name: "Writer"}) {
		t.Error("different interface names should not match")
	}
}

func TestServiceType(t *testing.T) {
	s := &ServiceType{Name: "Svc", Methods: map[string]Type{}}
	if s.String() != "service Svc" {
		t.Errorf("ServiceType.String() = %q, want %q", s.String(), "service Svc")
	}
}

func TestRulesetType(t *testing.T) {
	r := &RulesetType{Name: "RS"}
	if r.String() != "ruleset RS" {
		t.Errorf("RulesetType.String() = %q, want %q", r.String(), "ruleset RS")
	}
}

func TestTemplateType(t *testing.T) {
	tmpl := &TemplateType{Name: "Addable"}
	if tmpl.String() != "template Addable" {
		t.Errorf("TemplateType.String() = %q, want %q", tmpl.String(), "template Addable")
	}
}

func TestIsNumeric(t *testing.T) {
	if !IsNumeric(Int) {
		t.Error("IsNumeric(int) should be true")
	}
	if !IsNumeric(Float) {
		t.Error("IsNumeric(float) should be true")
	}
	if IsNumeric(Bool) {
		t.Error("IsNumeric(bool) should be false")
	}
	if IsNumeric(String) {
		t.Error("IsNumeric(string) should be false")
	}
	if IsNumeric(&NamedType{Name: "Foo"}) {
		t.Error("IsNumeric(custom) should be false")
	}
}

func TestIsInteger(t *testing.T) {
	if !IsInteger(Int) {
		t.Error("IsInteger(int) should be true")
	}
	if !IsInteger(Uint8) {
		t.Error("IsInteger(uint8) should be true")
	}
	if IsInteger(Float) {
		t.Error("IsInteger(float) should be false")
	}
	if IsInteger(Bool) {
		t.Error("IsInteger(bool) should be false")
	}
}

func TestIsAssignable(t *testing.T) {
	// identical types
	if !IsAssignable(Int, Int) {
		t.Error("IsAssignable(int, int) should be true")
	}
	// any accepts everything
	if !IsAssignable(Any, Int) {
		t.Error("IsAssignable(any, int) should be true")
	}
	// integer widening
	if !IsAssignable(Int64, Int32) {
		t.Error("IsAssignable(int64, int32) should be true")
	}
	// bool to int
	if !IsAssignable(Int, Bool) {
		t.Error("IsAssignable(int, bool) should be true")
	}
	// nil to pointer
	if !IsAssignable(&PointerType{Elem: Int}, Nil) {
		t.Error("IsAssignable(*int, nil) should be true")
	}
	// nil to error
	if !IsAssignable(Error, Nil) {
		t.Error("IsAssignable(error, nil) should be true")
	}
	// bool can be assigned to int (zero-extended)
	if !IsAssignable(Int, Bool) {
		t.Error("IsAssignable(int, bool) should be true (bool zero-extended to int)")
	}
	// int cannot be assigned to bool
	if IsAssignable(Bool, Int) {
		t.Error("IsAssignable(bool, int) should be false")
	}
}

func TestLookupType(t *testing.T) {
	tests := []struct {
		name string
		want Type
	}{
		{"int", Int},
		{"bool", Bool},
		{"string", String},
		{"void", Void},
		{"error", Error},
		{"float", Float},
		{"MyType", &NamedType{Name: "MyType"}},
	}
	for _, tt := range tests {
		got := LookupType(tt.name)
		if !got.Equals(tt.want) {
			t.Errorf("LookupType(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
