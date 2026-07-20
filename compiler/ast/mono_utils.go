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

// Package ast (mono_utils.go) provides helper functions for generic monomorphization:
// type mangling, generic argument extraction, and type replacement utilities.
package ast

import (
	"sort"
	"strings"
)

// sortedGenericStructNames returns the keys of a generic-struct map in a
// deterministic (sorted) order so monomorphization output does not depend on
// Go's randomized map iteration order.
func sortedGenericStructNames(m map[string]*StructDecl) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// sortedGenericFnNames returns the keys of a generic-fn map in sorted order.
func sortedGenericFnNames(m map[string]*FnDecl) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// MangleName produces a concrete name from a generic name and type arguments.
// e.g. MangleName("Stack", ["int"]) -> "Stack_int"
func MangleName(name string, args []Type) string {
	if len(args) == 0 {
		return name
	}
	var parts []string
	for _, a := range args {
		parts = append(parts, typeMangleKey(a))
	}
	return name + "_" + strings.Join(parts, "_")
}

// unmangle splits a mangled name back into base name and type args.
// For now, this is best-effort and only handles simple cases.
func unmangle(mangled string) (string, []Type) {
	idx := strings.Index(mangled, "_")
	if idx <= 0 {
		return mangled, nil
	}
	return mangled[:idx], nil
}

// typeMangleKey produces a stable string key for a type.
func typeMangleKey(t Type) string {
	if t == nil {
		return "void"
	}
	switch tt := t.(type) {
	case *NamedType:
		// Replace dots with underscores to avoid issues with module resolution in mangled names.
		return strings.ReplaceAll(MangleName(tt.Name, tt.Args), ".", "_")
	case *PointerType:
		return "ptr_" + typeMangleKey(tt.Elem)
	case *ArrayType:
		return "arr_" + typeMangleKey(tt.Elem)
	case *SetType:
		return "set_" + typeMangleKey(tt.Elem)
	case *MapType:
		return "map_" + typeMangleKey(tt.Key) + "_" + typeMangleKey(tt.Elem)
	case *ChanType:
		return "chan_" + typeMangleKey(tt.Elem)
	case *TupleType:
		var parts []string
		for _, ty := range tt.Types {
			parts = append(parts, typeMangleKey(ty))
		}
		return "tup_" + strings.Join(parts, "_")
	case *FunctionType:
		return "fn"
	}
	return "unknown"
}

// demangleName tries to split a name like "Box_int" into generic name "Box"
// and concrete args [int]. It uses prefix matching against the known generic name.
func demangleName(name, genName string, typeParams []string) ([]Type, bool) {
	prefix := genName + "_"
	if !strings.HasPrefix(name, prefix) {
		return nil, false
	}
	suffix := strings.TrimPrefix(name, prefix)
	if len(typeParams) == 0 {
		return nil, false
	}
	// For single-arg generics, suffix is the type key.
	if len(typeParams) == 1 {
		arg := typeFromMangleKey(suffix)
		if arg != nil {
			return []Type{arg}, true
		}
		return nil, false
	}
	// Multi-arg: split by '_' and parse each key.
	keys := strings.Split(suffix, "_")
	if len(keys) != len(typeParams) {
		return nil, false
	}
	var args []Type
	for _, k := range keys {
		arg := typeFromMangleKey(k)
		if arg == nil {
			return nil, false
		}
		args = append(args, arg)
	}
	return args, true
}

// typeFromMangleKey reverses typeMangleKey for primitive types.
func typeFromMangleKey(key string) Type {
	if strings.HasPrefix(key, "ptr_") {
		sub := typeFromMangleKey(strings.TrimPrefix(key, "ptr_"))
		if sub != nil {
			return &PointerType{Elem: sub}
		}
	}
	if strings.HasPrefix(key, "arr_") {
		sub := typeFromMangleKey(strings.TrimPrefix(key, "arr_"))
		if sub != nil {
			return &ArrayType{Elem: sub}
		}
	}
	if strings.HasPrefix(key, "set_") {
		sub := typeFromMangleKey(strings.TrimPrefix(key, "set_"))
		if sub != nil {
			return &SetType{Elem: sub}
		}
	}
	switch key {
	case "int":
		return &NamedType{Name: "int"}
	case "int8":
		return &NamedType{Name: "int8"}
	case "int16":
		return &NamedType{Name: "int16"}
	case "int32":
		return &NamedType{Name: "int32"}
	case "int64":
		return &NamedType{Name: "int64"}
	case "uint":
		return &NamedType{Name: "uint"}
	case "uint8":
		return &NamedType{Name: "uint8"}
	case "uint16":
		return &NamedType{Name: "uint16"}
	case "uint32":
		return &NamedType{Name: "uint32"}
	case "uint64":
		return &NamedType{Name: "uint64"}
	case "float":
		return &NamedType{Name: "float"}
	case "bool":
		return &NamedType{Name: "bool"}
	case "string":
		return &NamedType{Name: "string"}
	case "bytes":
		return &NamedType{Name: "bytes"}
	case "void":
		return &NamedType{Name: "void"}
	}
	if len(key) > 0 {
		return &NamedType{Name: key}
	}
	return nil
}

// SubstituteType replaces type parameters with concrete types in a Type.
func SubstituteType(t Type, mapping map[string]Type) Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *NamedType:
		if len(tt.Args) == 0 {
			if replacement, ok := mapping[tt.Name]; ok {
				return replacement
			}
			return tt
		}
		// Generic instantiation: substitute args too.
		var newArgs []Type
		for _, a := range tt.Args {
			newArgs = append(newArgs, SubstituteType(a, mapping))
		}
		return &NamedType{Token: tt.Token, Name: tt.Name, Args: newArgs}
	case *PointerType:
		return &PointerType{Token: tt.Token, Elem: SubstituteType(tt.Elem, mapping)}
	case *ArrayType:
		return &ArrayType{Token: tt.Token, Elem: SubstituteType(tt.Elem, mapping)}
	case *SetType:
		return &SetType{Token: tt.Token, Elem: SubstituteType(tt.Elem, mapping)}
	case *MapType:
		return &MapType{
			Token: tt.Token,
			Key:   SubstituteType(tt.Key, mapping),
			Elem:  SubstituteType(tt.Elem, mapping),
		}
	case *ChanType:
		return &ChanType{Token: tt.Token, Elem: SubstituteType(tt.Elem, mapping)}
	case *TupleType:
		var newTypes []Type
		for _, ty := range tt.Types {
			newTypes = append(newTypes, SubstituteType(ty, mapping))
		}
		return &TupleType{Token: tt.Token, Types: newTypes}
	case *FunctionType:
		var newParams []Type
		for _, pt := range tt.ParamTypes {
			newParams = append(newParams, SubstituteType(pt, mapping))
		}
		return &FunctionType{
			Token:      tt.Token,
			ParamTypes: newParams,
			ReturnType: SubstituteType(tt.ReturnType, mapping),
		}
	}
	return t
}
