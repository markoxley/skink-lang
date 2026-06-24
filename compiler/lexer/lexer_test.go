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

package lexer

import (
	"testing"

	"github.com/skink-lang/compiler/token"
)

// TestNextToken exercises the lexer's ability to tokenize a representative
// snippet of Skink source code including keywords, identifiers, literals,
// comments, and every operator category.
func TestNextToken(t *testing.T) {
	input := `fn add(a: int, b: int) -> int {
		return a + b
	}
	// a comment
	/* block comment */
	name := "hello"
	10 3.14 true false nil
	+ - * / % ** && || == != <= >= -> := =>
	`

	tests := []struct {
		expectedType    token.Type
		expectedLiteral string
	}{
		{token.FN, "fn"},
		{token.IDENT, "add"},
		{token.LPAREN, "("},
		{token.IDENT, "a"},
		{token.COLON, ":"},
		{token.INT, "int"},
		{token.COMMA, ","},
		{token.IDENT, "b"},
		{token.COLON, ":"},
		{token.INT, "int"},
		{token.RPAREN, ")"},
		{token.ARROW, "->"},
		{token.INT, "int"},
		{token.LBRACE, "{"},
		{token.RETURN, "return"},
		{token.IDENT, "a"},
		{token.PLUS, "+"},
		{token.IDENT, "b"},
		{token.RBRACE, "}"},
		{token.COMMENT, "// a comment"},
		{token.COMMENT, "/* block comment */"},
		{token.IDENT, "name"},
		{token.COLONASS, ":="},
		{token.STRING, "hello"},
		{token.INT, "10"},
		{token.FLOAT, "3.14"},
		{token.TRUE, "true"},
		{token.FALSE, "false"},
		{token.NIL, "nil"},
		{token.PLUS, "+"},
		{token.MINUS, "-"},
		{token.STAR, "*"},
		{token.SLASH, "/"},
		{token.PERCENT, "%"},
		{token.DBLSTAR, "**"},
		{token.AND, "&&"},
		{token.OR, "||"},
		{token.EQ, "=="},
		{token.NE, "!="},
		{token.LE, "<="},
		{token.GE, ">="},
		{token.ARROW, "->"},
		{token.COLONASS, ":="},
		{token.FATARROW, "=>"},
		{token.EOF, ""},
	}

	l := New(input)

	for i, tt := range tests {
		// Skip NEWLINE tokens — the parser ignores them
		tok := l.NextToken()
		for tok.Type == token.NEWLINE {
			tok = l.NextToken()
		}
		if tok.Type != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%q, got=%q (literal=%q)",
				i, tt.expectedType, tok.Type, tok.Literal)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q",
				i, tt.expectedLiteral, tok.Literal)
		}
	}
}

func TestStringLiteral(t *testing.T) {
	input := `"hello world"`
	l := New(input)
	tok := l.NextToken()
	if tok.Type != token.STRING {
		t.Errorf("expected STRING, got %q", tok.Type)
	}
	if tok.Literal != "hello world" {
		t.Errorf("expected literal 'hello world', got %q", tok.Literal)
	}
}

func TestBytesLiteral(t *testing.T) {
	input := "`raw bytes`"
	l := New(input)
	tok := l.NextToken()
	if tok.Type != token.STRING {
		t.Errorf("expected STRING for bytes literal, got %q", tok.Type)
	}
	if tok.Literal != "raw bytes" {
		t.Errorf("expected literal 'raw bytes', got %q", tok.Literal)
	}
}

func TestDocComment(t *testing.T) {
	input := "/// This is docs"
	l := New(input)
	tok := l.NextToken()
	if tok.Type != token.DOC {
		t.Errorf("expected DOC, got %q", tok.Type)
	}
}

func TestIntegerTypes(t *testing.T) {
	input := "42 0xFF 0b1010 0o77"
	l := New(input)

	expected := []struct {
		typ token.Type
		lit string
	}{
		{token.INT, "42"},
		{token.INT, "0xFF"},
		{token.INT, "0b1010"},
		{token.INT, "0o77"},
		{token.EOF, ""},
	}

	for _, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Errorf("expected %q, got %q", exp.typ, tok.Type)
		}
		if tok.Literal != exp.lit {
			t.Errorf("expected literal %q, got %q", exp.lit, tok.Literal)
		}
	}
}

func TestPositionTracking(t *testing.T) {
	input := "fn test()"
	l := New(input)

	tok := l.NextToken() // fn
	if tok.Line != 1 || tok.Column != 1 {
		t.Errorf("fn token at wrong position: line=%d col=%d", tok.Line, tok.Column)
	}

	tok = l.NextToken() // test
	if tok.Line != 1 || tok.Column != 4 {
		t.Errorf("test token at wrong position: line=%d col=%d", tok.Line, tok.Column)
	}
}

func TestEmptyInput(t *testing.T) {
	l := New("")
	tok := l.NextToken()
	if tok.Type != token.EOF {
		t.Errorf("expected EOF for empty input, got %q", tok.Type)
	}
}

func TestFloatLiteral(t *testing.T) {
	input := "3.14 1.0e10 2.5E-3"
	l := New(input)

	expected := []struct {
		typ token.Type
		lit string
	}{
		{token.FLOAT, "3.14"},
		{token.FLOAT, "1.0e10"},
		{token.FLOAT, "2.5E-3"},
		{token.EOF, ""},
	}

	for _, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Errorf("expected %q, got %q", exp.typ, tok.Type)
		}
		if tok.Literal != exp.lit {
			t.Errorf("expected literal %q, got %q", exp.lit, tok.Literal)
		}
	}
}

func TestCompoundAssignments(t *testing.T) {
	input := "+= -= *= /= %= &= |= ^= <<= >>="
	l := New(input)

	expected := []token.Type{
		token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ,
		token.PERCEQ, token.AMPEQ, token.PIPEEQ, token.CARETEQ,
		token.LSHIFTEQ, token.RSHIFTEQ, token.EOF,
	}

	for _, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp {
			t.Errorf("expected %q, got %q", exp, tok.Type)
		}
	}
}

func TestBitwiseAndShift(t *testing.T) {
	input := "<< >> & | ^ ~"
	l := New(input)

	expected := []token.Type{
		token.LSHIFT, token.RSHIFT,
		token.AMPERSAND, token.PIPE, token.CARET, token.TILDE,
		token.EOF,
	}

	for _, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp {
			t.Errorf("expected %q, got %q", exp, tok.Type)
		}
	}
}

func TestSendOperator(t *testing.T) {
	input := "<-"
	l := New(input)
	tok := l.NextToken()
	if tok.Type != token.SEND {
		t.Errorf("expected SEND, got %q", tok.Type)
	}
	if tok.Literal != "<-" {
		t.Errorf("expected literal '<-', got %q", tok.Literal)
	}
}

func TestEllipsisAndRange(t *testing.T) {
	input := "... .."
	l := New(input)

	tok := l.NextToken()
	if tok.Type != token.ELLIPSIS {
		t.Errorf("expected ELLIPSIS, got %q", tok.Type)
	}

	tok = l.NextToken()
	if tok.Type != token.DOTDOT {
		t.Errorf("expected DOTDOT, got %q", tok.Type)
	}
}

func TestStringEscapes(t *testing.T) {
	input := `"hello\nworld"`
	l := New(input)
	tok := l.NextToken()
	if tok.Type != token.STRING {
		t.Errorf("expected STRING, got %q", tok.Type)
	}
	if tok.Literal != `hello\nworld` {
		t.Errorf("expected literal with escape, got %q", tok.Literal)
	}
}

func TestIllegalCharacter(t *testing.T) {
	input := "$"
	l := New(input)
	tok := l.NextToken()
	if tok.Type != token.ILLEGAL {
		t.Errorf("expected ILLEGAL, got %q", tok.Type)
	}
	if tok.Literal != "$" {
		t.Errorf("expected literal '$', got %q", tok.Literal)
	}
}

func TestMultilinePosition(t *testing.T) {
	input := "fn\ntest"
	l := New(input)

	tok := l.NextToken() // fn
	if tok.Line != 1 {
		t.Errorf("expected line 1, got %d", tok.Line)
	}

	tok = l.NextToken() // newline
	for tok.Type == token.NEWLINE {
		tok = l.NextToken()
	}

	if tok.Line != 2 {
		t.Errorf("expected line 2 for second token, got %d", tok.Line)
	}
	if tok.Literal != "test" {
		t.Errorf("expected 'test', got %q", tok.Literal)
	}
}
