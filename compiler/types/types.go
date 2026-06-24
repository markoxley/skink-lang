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

// Package types defines the Skink type system representations used by the
// compiler's type checker and code generator.
//
// It includes interfaces and structs for primitive types, pointers, arrays,
// tuples, functions, structs, enums, generic parameters, channels, sets,
// tensors, and user-defined named types.
package types

import (
	"fmt"
	"strings"
)

// Type is the common interface implemented by every concrete type in Skink.
// All type values support string formatting and structural equality testing.
type Type interface {
	String() string
	Equals(Type) bool
}

// BuiltInType represents primitive types such as int, float, bool, string,
// and bytes. These types are known to the compiler without declaration.
type BuiltInType struct {
	Name string
}

func (b *BuiltInType) String() string { return b.Name }
func (b *BuiltInType) Equals(other Type) bool {
	o, ok := other.(*BuiltInType)
	return ok && o.Name == b.Name
}

// PointerType represents a pointer to another type (*T).
type PointerType struct {
	Elem Type
}

func (p *PointerType) String() string { return "*" + p.Elem.String() }
func (p *PointerType) Equals(other Type) bool {
	o, ok := other.(*PointerType)
	return ok && p.Elem.Equals(o.Elem)
}

// ArrayType represents a dynamically-sized slice type ([]T).
type ArrayType struct {
	Elem Type
}

func (a *ArrayType) String() string { return "[]" + a.Elem.String() }
func (a *ArrayType) Equals(other Type) bool {
	o, ok := other.(*ArrayType)
	return ok && a.Elem.Equals(o.Elem)
}

// SetType represents a mathematical set of elements (set<T>).
type SetType struct {
	Elem Type
}

func (s *SetType) String() string { return "set<" + s.Elem.String() + ">" }
func (s *SetType) Equals(other Type) bool {
	o, ok := other.(*SetType)
	return ok && s.Elem.Equals(o.Elem)
}

// MapType represents an associative array mapping keys to values (map[K]V).
type MapType struct {
	Key  Type
	Elem Type
}

func (m *MapType) String() string { return "map[" + m.Key.String() + "]" + m.Elem.String() }
func (m *MapType) Equals(other Type) bool {
	o, ok := other.(*MapType)
	return ok && m.Key.Equals(o.Key) && m.Elem.Equals(o.Elem)
}

// ChanType represents a communication channel (chan<T>).
type ChanType struct {
	Elem Type
}

func (c *ChanType) String() string { return "chan<" + c.Elem.String() + ">" }
func (c *ChanType) Equals(other Type) bool {
	o, ok := other.(*ChanType)
	return ok && c.Elem.Equals(o.Elem)
}

// TensorType represents a multi-dimensional tensor (tensor<T>).
type TensorType struct {
	Elem Type
}

func (t *TensorType) String() string { return "tensor<" + t.Elem.String() + ">" }
func (t *TensorType) Equals(other Type) bool {
	o, ok := other.(*TensorType)
	return ok && t.Elem.Equals(o.Elem)
}

// NamedType represents a user-defined named type such as a struct or enum.
type NamedType struct {
	Name string
}

func (n *NamedType) String() string { return n.Name }
func (n *NamedType) Equals(other Type) bool {
	o, ok := other.(*NamedType)
	return ok && strings.ReplaceAll(o.Name, ".", "_") == strings.ReplaceAll(n.Name, ".", "_")
}

// ServiceType represents a service interface with named methods.
type ServiceType struct {
	Name    string
	Methods map[string]Type
}

func (s *ServiceType) String() string { return "service " + s.Name }
func (s *ServiceType) Equals(other Type) bool {
	o, ok := other.(*ServiceType)
	return ok && o.Name == s.Name
}

// RulesetType represents a declarative ruleset used for rule-based logic.
type RulesetType struct {
	Name string
}

func (r *RulesetType) String() string { return "ruleset " + r.Name }
func (r *RulesetType) Equals(other Type) bool {
	o, ok := other.(*RulesetType)
	return ok && o.Name == r.Name
}

// TemplateType represents a template constraint (interface/trait).
type TemplateType struct {
	Name string
}

func (t *TemplateType) String() string { return "template " + t.Name }
func (t *TemplateType) Equals(other Type) bool {
	o, ok := other.(*TemplateType)
	return ok && o.Name == t.Name
}

// FunctionType represents fn(params) -> ret.
type FunctionType struct {
	Params   []Type
	Ret      []Type // multiple returns for Go-style error tuples
	Variadic bool   // true if last parameter is ...
}

func (f *FunctionType) String() string { return "<function>" }
func (f *FunctionType) Equals(other Type) bool {
	o, ok := other.(*FunctionType)
	if !ok || len(f.Params) != len(o.Params) || len(f.Ret) != len(o.Ret) {
		return false
	}
	for i := range f.Params {
		if !f.Params[i].Equals(o.Params[i]) {
			return false
		}
	}
	for i := range f.Ret {
		if !f.Ret[i].Equals(o.Ret[i]) {
			return false
		}
	}
	return f.Variadic == o.Variadic
}

// TupleType represents (T1, T2, ...) from multi-return values.
type TupleType struct {
	Types []Type
}

func (t *TupleType) String() string { return fmt.Sprintf("%v", t.Types) }
func (t *TupleType) Equals(other Type) bool {
	o, ok := other.(*TupleType)
	if !ok || len(t.Types) != len(o.Types) {
		return false
	}
	for i := range t.Types {
		if !t.Types[i].Equals(o.Types[i]) {
			return false
		}
	}
	return true
}

// NilType is the type of nil.
type NilType struct{}

func (n *NilType) String() string { return "nil" }
func (n *NilType) Equals(other Type) bool {
	_, ok := other.(*NilType)
	return ok
}

// InterfaceType represents an interface with a set of required methods.
type InterfaceType struct {
	Name    string
	Methods map[string]Type
}

func (i *InterfaceType) String() string { return i.Name }
func (i *InterfaceType) Equals(other Type) bool {
	o, ok := other.(*InterfaceType)
	return ok && o.Name == i.Name
}

// ErrorType is the built-in error interface: interface { String() -> string }.
type ErrorType struct {
	InterfaceType
}

func (e *ErrorType) String() string { return "error" }
func (e *ErrorType) Equals(other Type) bool {
	_, ok := other.(*ErrorType)
	return ok
}

// AnyType is used for untyped constants before they are assigned.
type AnyType struct{}

func (a *AnyType) String() string { return "any" }
func (a *AnyType) Equals(other Type) bool {
	_, ok := other.(*AnyType)
	return ok
}

// UnknownType signals an error during inference.
type UnknownType struct{}

func (u *UnknownType) String() string { return "<unknown>" }
func (u *UnknownType) Equals(other Type) bool {
	_, ok := other.(*UnknownType)
	return ok
}

// Built-in type instances.
var (
	Int     = &BuiltInType{Name: "int"}
	Int8    = &BuiltInType{Name: "int8"}
	Int16   = &BuiltInType{Name: "int16"}
	Int32   = &BuiltInType{Name: "int32"}
	Int64   = &BuiltInType{Name: "int64"}
	Uint    = &BuiltInType{Name: "uint"}
	Uint8   = &BuiltInType{Name: "uint8"}
	Uint16  = &BuiltInType{Name: "uint16"}
	Uint32  = &BuiltInType{Name: "uint32"}
	Uint64  = &BuiltInType{Name: "uint64"}
	Float   = &BuiltInType{Name: "float"}
	Bool    = &BuiltInType{Name: "bool"}
	String  = &BuiltInType{Name: "string"}
	Bytes   = &BuiltInType{Name: "bytes"}
	Void    = &BuiltInType{Name: "void"}
	Error   = &ErrorType{InterfaceType{Name: "error", Methods: map[string]Type{"String": &FunctionType{Ret: []Type{String}}}}}
	Nil     = &NilType{}
	Any     = &AnyType{}
	Unknown = &UnknownType{}
)

// IsNumeric returns true for integer and float types.
func IsNumeric(t Type) bool {
	b, ok := t.(*BuiltInType)
	if !ok {
		return false
	}
	switch b.Name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "float":
		return true
	}
	return false
}

// IsInteger returns true for integer types only.
func IsInteger(t Type) bool {
	b, ok := t.(*BuiltInType)
	if !ok {
		return false
	}
	switch b.Name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// IsAssignable checks if src can be assigned to dst (basic compat + narrowing check).
func IsAssignable(dst, src Type) bool {
	if dst.Equals(src) {
		return true
	}
	// Any accepts everything.
	if dst.Equals(Any) {
		return true
	}
	// Any integer type can be assigned to any other integer type (implicit widening/promotion).
	if IsInteger(dst) && IsInteger(src) {
		return true
	}
	// Untyped integer constants can assign to any integer type
	if src.Equals(Any) && IsInteger(dst) {
		return true
	}
	// Untyped float constants can assign to float
	if src.Equals(Any) && dst.Equals(Float) {
		return true
	}
	// Bool can be assigned to int (zero-extended).
	if src.Equals(Bool) && IsInteger(dst) {
		return true
	}
	// nil can assign to error, pointer, and string types.
	if src.Equals(Nil) {
		if dst.Equals(Error) || dst.Equals(String) {
			return true
		}
		if _, ok := dst.(*PointerType); ok {
			return true
		}
		if _, ok := dst.(*ArrayType); ok {
			return true
		}
		if _, ok := dst.(*ChanType); ok {
			return true
		}
		if _, ok := dst.(*MapType); ok {
			return true
		}
	}
	// Map literals typed as map[any]any (e.g. "{}") can flow into concrete map types.
	if dstMap, ok := dst.(*MapType); ok {
		if srcMap, ok := src.(*MapType); ok {
			keyCompatible := srcMap.Key.Equals(dstMap.Key) || srcMap.Key.Equals(Any)
			valCompatible := srcMap.Elem.Equals(dstMap.Elem) || srcMap.Elem.Equals(Any)
			if keyCompatible && valCompatible {
				return true
			}
		}
	}
	// Empty array literal ([]Any) can assign to any array type.
	if srcArr, ok := src.(*ArrayType); ok && srcArr.Elem.Equals(Any) {
		if _, ok := dst.(*ArrayType); ok {
			return true
		}
	}
	return false
}

// LookupType maps AST type names to type-system types.
func LookupType(name string) Type {
	switch name {
	case "int":
		return Int
	case "int8":
		return Int8
	case "int16":
		return Int16
	case "int32":
		return Int32
	case "int64":
		return Int64
	case "uint":
		return Uint
	case "uint8":
		return Uint8
	case "uint16":
		return Uint16
	case "uint32":
		return Uint32
	case "uint64":
		return Uint64
	case "float":
		return Float
	case "bool":
		return Bool
	case "string":
		return String
	case "bytes":
		return Bytes
	case "void":
		return Void
	case "error":
		return Error
	default:
		return &NamedType{Name: name}
	}
}
