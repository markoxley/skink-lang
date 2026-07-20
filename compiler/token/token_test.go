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

package token

import "testing"

// TestLookupIdent verifies that the keyword lookup table correctly maps
// reserved words to their token types and returns IDENT for non-keywords.

func TestLookupIdent(t *testing.T) {
	tests := []struct {
		input string
		want  Type
	}{
		{"fn", FN},
		{"if", IF},
		{"return", RETURN},
		{"true", TRUE},
		{"false", FALSE},
		{"nil", NIL},
		{"foo", IDENT},
		{"bar123", IDENT},
		{"_baz", IDENT},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := LookupIdent(tt.input)
			if got != tt.want {
				t.Errorf("LookupIdent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTokenTypes(t *testing.T) {
	// Verify that literal token types use their string value.
	if ASSIGN != "=" {
		t.Errorf("ASSIGN = %q, want %q", ASSIGN, "=")
	}
	if COLONASS != ":=" {
		t.Errorf("COLONASS = %q, want %q", COLONASS, ":=")
	}
	if ARROW != "->" {
		t.Errorf("ARROW = %q, want %q", ARROW, "->")
	}
	if DBLSTAR != "**" {
		t.Errorf("DBLSTAR = %q, want %q", DBLSTAR, "**")
	}
}

func TestLookupIdentKeywords(t *testing.T) {
	tests := []struct {
		input string
		want  Type
	}{
		{"pub", PUB},
		{"const", CONST},
		{"var", VAR},
		{"struct", STRUCT},
		{"enum", ENUM},
		{"if", IF},
		{"else", ELSE},
		{"for", FOR},
		{"while", WHILE},
		{"return", RETURN},
		{"defer", DEFER},
		{"async", ASYNC},
		{"await", AWAIT},
		{"spawn", SPAWN},
		{"switch", SWITCH},
		{"template", TEMPLATE},
		{"comptime", COMPTIME},
		{"extern", EXTERN},
		{"module", MODULE},
		{"import", IMPORT},
		{"sizeof", SIZEOF},
		{"alignof", ALIGNOF},
		{"chan", CHAN},
		{"set", SET},
		{"tensor", TENSOR},
		{"void", VOID},
		{"int", INT},
		{"float", FLOAT},
		{"bool", BOOL},
		{"string", STRING_TYPE},
		{"bytes", BYTES_TYPE},
		{"nil", NIL},
		{"error", ERROR},
		{"iota", IOTA},
		{"_unknown", IDENT},
		{"MyType", IDENT},
	}

	for _, tt := range tests {
		got := LookupIdent(tt.input)
		if got != tt.want {
			t.Errorf("LookupIdent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTokenStruct(t *testing.T) {
	tok := Token{Type: INT, Literal: "42", Line: 3, Column: 5}
	if tok.Type != INT {
		t.Errorf("Type mismatch")
	}
	if tok.Literal != "42" {
		t.Errorf("Literal mismatch")
	}
	if tok.Line != 3 {
		t.Errorf("Line mismatch")
	}
	if tok.Column != 5 {
		t.Errorf("Column mismatch")
	}
}
