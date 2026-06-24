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

package cimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMapCTypePrimitives(t *testing.T) {
	p := newCParser("")
	tests := []struct {
		cType string
		want  string
	}{
		{"void", "void"},
		{"char", "int8"},
		{"unsigned char", "uint8"},
		{"short", "int16"},
		{"unsigned short", "uint16"},
		{"int", "int"},
		{"unsigned int", "uint"},
		{"long", "int"},
		{"unsigned long", "uint"},
		{"long long", "int64"},
		{"unsigned long long", "uint64"},
		{"float", "float"},
		{"double", "float"},
		{"size_t", "uint"},
		{"ssize_t", "int"},
		{"bool", "bool"},
		{"_Bool", "bool"},
	}
	for _, tt := range tests {
		got := p.mapCType(tt.cType)
		if got.String() != tt.want {
			t.Errorf("mapCType(%q) = %q, want %q", tt.cType, got.String(), tt.want)
		}
	}
}

func TestMapCTypePointer(t *testing.T) {
	p := newCParser("")
	// void* -> *u8
	got := p.mapCType("void*")
	if got.String() != "*u8" {
		t.Errorf("mapCType(void*) = %q, want %q", got.String(), "*u8")
	}
	// int* -> *int
	got = p.mapCType("int*")
	if got.String() != "*int" {
		t.Errorf("mapCType(int*) = %q, want %q", got.String(), "*int")
	}
}

func TestMapCTypeStruct(t *testing.T) {
	p := newCParser("")
	got := p.mapCType("struct Foo")
	if got.String() != "Foo" {
		t.Errorf("mapCType(struct Foo) = %q, want %q", got.String(), "Foo")
	}
}

func TestMapCTypeEnum(t *testing.T) {
	p := newCParser("")
	got := p.mapCType("enum Color")
	if got.String() != "Color" {
		t.Errorf("mapCType(enum Color) = %q, want %q", got.String(), "Color")
	}
}

func TestMapCTypeTypedef(t *testing.T) {
	p := newCParser("")
	p.typedefs = map[string]string{"myint": "int"}
	got := p.mapCType("myint")
	if got.String() != "int" {
		t.Errorf("mapCType(myint via typedef) = %q, want %q", got.String(), "int")
	}
}

func TestLexTokens(t *testing.T) {
	p := newCParser("int foo(void);")
	tokens := []cTokenKind{cIdent, cIdent, cLParen, cIdent, cRParen, cSemicolon}
	for i, want := range tokens {
		if p.tok.kind != want {
			t.Errorf("token %d: got %v, want %v", i, p.tok.kind, want)
		}
		p.next()
	}
	if p.tok.kind != cEOF {
		t.Errorf("final token: got %v, want cEOF", p.tok.kind)
	}
}

func TestLexNumber(t *testing.T) {
	p := newCParser("42")
	if p.tok.kind != cNumber || p.tok.val != "42" {
		t.Errorf("number token: got %v %q, want cNumber 42", p.tok.kind, p.tok.val)
	}
}

func TestLexString(t *testing.T) {
	p := newCParser(`"hello"`)
	if p.tok.kind != cString {
		t.Errorf("string token: got %v, want cString", p.tok.kind)
	}
}

func TestLexComment(t *testing.T) {
	p := newCParser("// comment\nint")
	// Skip the newline token that follows the comment line
	if p.tok.kind == cNewline {
		p.next()
	}
	if p.tok.kind != cIdent || p.tok.val != "int" {
		t.Errorf("after comment: got %v %q, want cIdent int", p.tok.kind, p.tok.val)
	}
}

func TestLexMultiLineComment(t *testing.T) {
	p := newCParser("/* comment */ int")
	if p.tok.kind != cIdent || p.tok.val != "int" {
		t.Errorf("after multiline comment: got %v %q, want cIdent int", p.tok.kind, p.tok.val)
	}
}

func TestPpActiveEmpty(t *testing.T) {
	p := newCParser("")
	if !p.ppActive() {
		t.Error("ppActive with empty stack should be true")
	}
}

func TestPpActiveInactive(t *testing.T) {
	p := newCParser("")
	p.ppStack = []ppState{{active: false}}
	if p.ppActive() {
		t.Error("ppActive with inactive state should be false")
	}
}

func TestDeclarationsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	header := filepath.Join(tmpDir, "test.h")
	if err := os.WriteFile(header, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	decls, err := Declarations(header)
	if err != nil {
		t.Fatalf("Declarations error: %v", err)
	}
	if len(decls) != 0 {
		t.Errorf("expected 0 decls, got %d", len(decls))
	}
}

func TestDeclarationsSimpleDefine(t *testing.T) {
	tmpDir := t.TempDir()
	header := filepath.Join(tmpDir, "test.h")
	content := "#define MAX 100\n"
	if err := os.WriteFile(header, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	decls, err := Declarations(header)
	if err != nil {
		t.Fatalf("Declarations error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
}

func TestExprEvalSimple(t *testing.T) {
	ev := &exprEval{
		toks: []cToken{
			{kind: cNumber, val: "42"},
		},
	}
	v, ok := ev.parse()
	if !ok || v != 42 {
		t.Errorf("exprEval.parse() = %d, %v, want 42, true", v, ok)
	}
}

func TestExprEvalAdd(t *testing.T) {
	ev := &exprEval{
		toks: []cToken{
			{kind: cNumber, val: "1"},
			{kind: cOther, val: "+"},
			{kind: cNumber, val: "2"},
		},
	}
	v, ok := ev.parse()
	if !ok || v != 3 {
		t.Errorf("exprEval 1+2 = %d, %v, want 3, true", v, ok)
	}
}

func TestExprEvalLogicalOr(t *testing.T) {
	ev := &exprEval{
		toks: []cToken{
			{kind: cNumber, val: "1"},
			{kind: cOther, val: "|"},
			{kind: cOther, val: "|"},
			{kind: cNumber, val: "0"},
		},
	}
	v, ok := ev.parse()
	if !ok || v != 1 {
		t.Errorf("exprEval 1||0 = %d, %v, want 1, true", v, ok)
	}
}

func TestExprEvalDefined(t *testing.T) {
	ev := &exprEval{
		toks: []cToken{
			{kind: cIdent, val: "defined"},
			{kind: cLParen, val: "("},
			{kind: cIdent, val: "FOO"},
			{kind: cRParen, val: ")"},
		},
		defines: map[string]int64{"FOO": 1},
	}
	v, ok := ev.parse()
	if !ok || v != 1 {
		t.Errorf("exprEval defined(FOO) = %d, %v, want 1, true", v, ok)
	}
}

func TestStripCSuffix(t *testing.T) {
	if stripCSuffix("100U") != "100" {
		t.Errorf("stripCSuffix(100U) = %q, want %q", stripCSuffix("100U"), "100")
	}
	if stripCSuffix("100L") != "100" {
		t.Errorf("stripCSuffix(100L) = %q, want %q", stripCSuffix("100L"), "100")
	}
	if stripCSuffix("100") != "100" {
		t.Errorf("stripCSuffix(100) = %q, want %q", stripCSuffix("100"), "100")
	}
}
