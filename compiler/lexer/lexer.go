// Copyright 2026 Mark Oxley Oxley
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

// Package lexer implements a UTF-8 aware tokenizer (scanner) for the Skink
// programming language. It converts raw source text into a stream of typed
// tokens that the parser consumes.
//
// The lexer handles all Skink lexical features including identifiers,
// numeric literals (decimal, hex, binary, octal), string and bytes literals,
// line and block comments, documentation comments, and a rich set of
// operators and punctuation.
package lexer

import (
	"unicode"
	"unicode/utf8"

	"github.com/skink-lang/compiler/token"
)

// Lexer turns a source string into a stream of typed tokens.
// It maintains UTF-8 aware position tracking so that every token
// carries accurate line and column information for error reporting.
type Lexer struct {
	input   string // full source text being scanned
	pos     int    // byte index of the current rune
	readPos int    // byte index after the current rune
	ch      rune   // current rune under the cursor
	line    int    // current line number (1-based)
	col     int    // current column number (1-based)
}

// New creates a Lexer for the given input string.
// The lexer starts at line 1, column 0 and immediately reads
// the first character so that ch is always valid.
func New(input string) *Lexer {
	l := &Lexer{input: input, line: 1, col: 0}
	l.readChar()
	return l
}

// readChar advances the lexer to the next UTF-8 rune in the input.
// It updates pos, readPos, ch, line, and col accordingly.
// When the end of input is reached ch is set to 0 (EOF sentinel).
func (l *Lexer) readChar() {
	l.pos = l.readPos
	if l.readPos >= len(l.input) {
		l.ch = 0
	} else {
		r, size := utf8.DecodeRuneInString(l.input[l.readPos:])
		l.ch = r
		l.readPos += size
	}
	if l.ch == '\n' {
		l.line++
		l.col = 0
	} else {
		l.col++
	}
}

// peekChar returns the next rune without advancing the lexer.
// It returns 0 when the end of input has been reached.
func (l *Lexer) peekChar() rune {
	if l.readPos >= len(l.input) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.input[l.readPos:])
	return r
}

// skipWhitespace advances past spaces, tabs, and carriage returns.
func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
		l.readChar()
	}
}

// NextToken returns the next token from the input stream.
func (l *Lexer) NextToken() token.Token {
	l.skipWhitespace()

	tok := token.Token{Line: l.line, Column: l.col}

	switch l.ch {
	case '\n':
		tok.Type = token.NEWLINE
		tok.Literal = "\\n"
		l.readChar()
	case '=':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.EQ
			tok.Literal = "=="
		} else if l.peekChar() == '>' {
			l.readChar()
			tok.Type = token.FATARROW
			tok.Literal = "=>"
		} else {
			tok.Type = token.ASSIGN
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case ':':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.COLONASS
			tok.Literal = ":="
		} else {
			tok.Type = token.COLON
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '+':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.PLUSEQ
			tok.Literal = "+="
		} else {
			tok.Type = token.PLUS
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '-':
		if l.peekChar() == '>' {
			l.readChar()
			tok.Type = token.ARROW
			tok.Literal = "->"
		} else if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.MINUSEQ
			tok.Literal = "-="
		} else {
			tok.Type = token.MINUS
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '*':
		if l.peekChar() == '*' {
			l.readChar()
			tok.Type = token.DBLSTAR
			tok.Literal = "**"
		} else if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.STAREQ
			tok.Literal = "*="
		} else {
			tok.Type = token.STAR
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '/':
		if l.peekChar() == '/' {
			tok = l.readLineComment()
		} else if l.peekChar() == '*' {
			tok = l.readBlockComment()
		} else if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.SLASHEQ
			tok.Literal = "/="
			l.readChar()
		} else {
			tok.Type = token.SLASH
			tok.Literal = string(l.ch)
			l.readChar()
		}
	case '%':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.PERCEQ
			tok.Literal = "%="
		} else {
			tok.Type = token.PERCENT
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '<':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.LE
			tok.Literal = "<="
		} else if l.peekChar() == '-' {
			l.readChar()
			tok.Type = token.SEND
			tok.Literal = "<-"
		} else if l.peekChar() == '<' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok.Type = token.LSHIFTEQ
				tok.Literal = "<<="
			} else {
				tok.Type = token.LSHIFT
				tok.Literal = "<<"
			}
		} else {
			tok.Type = token.LT
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '>':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.GE
			tok.Literal = ">="
		} else if l.peekChar() == '>' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok.Type = token.RSHIFTEQ
				tok.Literal = ">>="
			} else {
				tok.Type = token.RSHIFT
				tok.Literal = ">>"
			}
		} else {
			tok.Type = token.GT
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '&':
		if l.peekChar() == '&' {
			l.readChar()
			tok.Type = token.AND
			tok.Literal = "&&"
		} else if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.AMPEQ
			tok.Literal = "&="
		} else {
			tok.Type = token.AMPERSAND
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '|':
		if l.peekChar() == '|' {
			l.readChar()
			tok.Type = token.OR
			tok.Literal = "||"
		} else if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.PIPEEQ
			tok.Literal = "|="
		} else {
			tok.Type = token.PIPE
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '^':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.CARETEQ
			tok.Literal = "^="
		} else {
			tok.Type = token.CARET
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '~':
		tok.Type = token.TILDE
		tok.Literal = string(l.ch)
		l.readChar()
	case '!':
		if l.peekChar() == '=' {
			l.readChar()
			tok.Type = token.NE
			tok.Literal = "!="
		} else {
			tok.Type = token.BANG
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case '@':
		tok.Type = token.AT
		tok.Literal = string(l.ch)
		l.readChar()
	case '?':
		tok.Type = token.QUESTION
		tok.Literal = string(l.ch)
		l.readChar()
	case '(':
		tok.Type = token.LPAREN
		tok.Literal = string(l.ch)
		l.readChar()
	case ')':
		tok.Type = token.RPAREN
		tok.Literal = string(l.ch)
		l.readChar()
	case '{':
		tok.Type = token.LBRACE
		tok.Literal = string(l.ch)
		l.readChar()
	case '}':
		tok.Type = token.RBRACE
		tok.Literal = string(l.ch)
		l.readChar()
	case '[':
		tok.Type = token.LBRACKET
		tok.Literal = string(l.ch)
		l.readChar()
	case ']':
		tok.Type = token.RBRACKET
		tok.Literal = string(l.ch)
		l.readChar()
	case ',':
		tok.Type = token.COMMA
		tok.Literal = string(l.ch)
		l.readChar()
	case '.':
		if l.peekChar() == '.' {
			l.readChar()
			if l.peekChar() == '.' {
				l.readChar()
				tok.Type = token.ELLIPSIS
				tok.Literal = "..."
			} else {
				tok.Type = token.DOTDOT
				tok.Literal = ".."
			}
		} else {
			tok.Type = token.DOT
			tok.Literal = string(l.ch)
		}
		l.readChar()
	case ';':
		tok.Type = token.SEMICOLON
		tok.Literal = string(l.ch)
		l.readChar()
	case '"':
		tok = l.readString('"')
	case '`':
		tok = l.readString('`')
	case 0:
		tok.Type = token.EOF
		tok.Literal = ""
	default:
		if isDigit(l.ch) {
			tok = l.readNumber()
		} else if isLetter(l.ch) {
			tok = l.readIdentifier()
		} else {
			tok.Type = token.ILLEGAL
			tok.Literal = string(l.ch)
			l.readChar()
		}
	}
	return tok
}

// readIdentifier consumes an identifier or keyword and returns its token.
func (l *Lexer) readIdentifier() token.Token {
	startLine := l.line
	startCol := l.col
	start := l.pos
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
	literal := l.input[start:l.pos]
	return token.Token{
		Type:    token.LookupIdent(literal),
		Literal: literal,
		Line:    startLine,
		Column:  startCol,
	}
}

// readNumber consumes a numeric literal (integer or float) and returns its token.
func (l *Lexer) readNumber() token.Token {
	startLine := l.line
	startCol := l.col
	start := l.pos
	isFloat := false

	// Handle hex (0x), binary (0b), octal (0o) prefixes
	if l.ch == '0' {
		next := l.peekChar()
		if next == 'x' || next == 'X' || next == 'b' || next == 'B' || next == 'o' || next == 'O' {
			l.readChar() // consume '0'
			l.readChar() // consume 'x'/'b'/'o'
			for isHexDigit(l.ch) || l.ch == '_' {
				l.readChar()
			}
			literal := l.input[start:l.pos]
			return token.Token{Type: token.INT, Literal: literal, Line: startLine, Column: startCol}
		}
	}

	for isDigit(l.ch) || l.ch == '.' || l.ch == 'e' || l.ch == 'E' || l.ch == '+' || l.ch == '-' || l.ch == '_' {
		if l.ch == '.' {
			if isFloat {
				break
			}
			// Don't consume '.' if it's part of '..'
			if l.peekChar() == '.' {
				break
			}
			isFloat = true
		}
		l.readChar()
	}
	literal := l.input[start:l.pos]
	typ := token.INT
	if isFloat {
		typ = token.FLOAT
	}
	return token.Token{
		Type:    typ,
		Literal: literal,
		Line:    startLine,
		Column:  startCol,
	}
}

// readString consumes a string literal up to the closing quote.
func (l *Lexer) readString(quote rune) token.Token {
	startLine := l.line
	startCol := l.col
	l.readChar() // consume opening quote
	start := l.pos
	for l.ch != quote && l.ch != 0 {
		if l.ch == '\\' {
			l.readChar()
			if l.ch == 0 {
				break
			}
		}
		l.readChar()
	}
	literal := l.input[start:l.pos]
	if l.ch == quote {
		l.readChar()
	}
	return token.Token{
		Type:    token.STRING,
		Literal: literal,
		Line:    startLine,
		Column:  startCol,
	}
}

// readLineComment consumes a // comment and returns a COMMENT or DOC token.
func (l *Lexer) readLineComment() token.Token {
	startLine := l.line
	startCol := l.col
	start := l.pos
	for l.ch != '\n' && l.ch != 0 {
		l.readChar()
	}
	literal := l.input[start:l.pos]
	typ := token.COMMENT
	if len(literal) > 2 && literal[2] == '/' {
		typ = token.DOC
	}
	return token.Token{
		Type:    typ,
		Literal: literal,
		Line:    startLine,
		Column:  startCol,
	}
}

// readBlockComment consumes a /* */ comment and returns a COMMENT token.
func (l *Lexer) readBlockComment() token.Token {
	startLine := l.line
	startCol := l.col
	start := l.pos
	l.readChar() // consume '/'
	l.readChar() // consume '*'
	for {
		if l.ch == '*' && l.peekChar() == '/' {
			l.readChar()
			l.readChar()
			break
		}
		if l.ch == 0 {
			break
		}
		l.readChar()
	}
	literal := l.input[start:l.pos]
	return token.Token{
		Type:    token.COMMENT,
		Literal: literal,
		Line:    startLine,
		Column:  startCol,
	}
}

// isLetter reports whether ch can start or continue an identifier.
func isLetter(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

// isDigit reports whether ch is a decimal digit.
func isDigit(ch rune) bool {
	return unicode.IsDigit(ch)
}

// isHexDigit reports whether ch is a hexadecimal digit.
func isHexDigit(ch rune) bool {
	return unicode.IsDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
