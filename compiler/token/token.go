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

// Package token defines the lexical token types, literals, and keyword mappings
// used by the Skink programming language lexer and parser.
//
// Each token carries its kind (Type), literal text, and source position (line,
// column) so that error messages can pinpoint the exact location of issues in
// the source code.
package token

// Type represents the lexical kind of a token (e.g. INT, IDENT, PLUS).
// It is implemented as a string so that token literals can double as
// their display names during debugging and error reporting.
type Type string

// Token represents a single lexical unit produced by the lexer.
// It stores the token kind, the raw text from the source, and the
// line/column where the token begins so that the compiler can
// report accurate error messages.
type Token struct {
	Type    Type   // lexical kind (e.g. IDENT, INT, PLUS)
	Literal string // raw text of the token from the source
	Line    int    // 1-based line number in the source file
	Column  int    // 1-based column number in the source file
}

// One-character and multi-character token types.
const (
	ILLEGAL Type = "ILLEGAL" // unrecognized character
	EOF     Type = "EOF"     // end of input
	NEWLINE Type = "NEWLINE" // line break
	IDENT   Type = "IDENT"   // identifier
	INT     Type = "INT"     // integer literal
	FLOAT   Type = "FLOAT"   // floating-point literal
	STRING  Type = "STRING"  // string literal
	BYTES   Type = "BYTES"   // raw bytes literal
	COMMENT Type = "COMMENT" // line or block comment
	DOC     Type = "DOC"     // documentation comment (///)

	ASSIGN    Type = "="
	COLON     Type = ":"
	COLONASS  Type = ":="
	COMMA     Type = ","
	SEMICOLON Type = ";"
	DOT       Type = "."
	DOTDOT    Type = ".."
	ELLIPSIS  Type = "..."
	ARROW     Type = "->"
	LPAREN    Type = "("
	RPAREN    Type = ")"
	LBRACE    Type = "{"
	RBRACE    Type = "}"
	LBRACKET  Type = "["
	RBRACKET  Type = "]"
	LT        Type = "<"
	GT        Type = ">"
	LE        Type = "<="
	SEND      Type = "<-"
	GE        Type = ">="
	EQ        Type = "=="
	NE        Type = "!="
	PLUS      Type = "+"
	MINUS     Type = "-"
	SLASH     Type = "/"
	STAR      Type = "*"
	PERCENT   Type = "%"
	AMPERSAND Type = "&"
	PIPE      Type = "|"
	CARET     Type = "^"
	TILDE     Type = "~"
	BANG      Type = "!"
	AND       Type = "&&"
	OR        Type = "||"
	AT        Type = "@"
	QUESTION  Type = "?"
	DBLSTAR   Type = "**"
	LSHIFT    Type = "<<"
	RSHIFT    Type = ">>"
	PLUSEQ    Type = "+="
	MINUSEQ   Type = "-="
	STAREQ    Type = "*="
	SLASHEQ   Type = "/="
	PERCEQ    Type = "%="
	AMPEQ     Type = "&="
	PIPEEQ    Type = "|="
	CARETEQ   Type = "^="
	LSHIFTEQ  Type = "<<="
	RSHIFTEQ  Type = ">>="
	FATARROW  Type = "=>"
)

var keywords = map[string]Type{
	"fn":         FN,
	"pub":        PUB,
	"const":      CONST,
	"var":        VAR,
	"type":       TYPE,
	"struct":     STRUCT,
	"enum":       ENUM,
	"service":    SERVICE,
	"ruleset":    RULESET,
	"rule":       RULE,
	"when":       WHEN,
	"action":     ACTION,
	"priority":   PRIORITY,
	"if":         IF,
	"else":       ELSE,
	"match":      MATCH,
	"for":        FOR,
	"in":         IN,
	"range":      RANGE,
	"while":      WHILE,
	"until":      UNTIL,
	"break":      BREAK,
	"continue":   CONTINUE,
	"return":     RETURN,
	"defer":      DEFER,
	"unsafe":     UNSAFE,
	"with":       WITH,
	"async":      ASYNC,
	"await":      AWAIT,
	"spawn":      SPAWN,
	"select":     SELECT,
	"switch":     SWITCH,
	"template":   TEMPLATE,
	"case":       CASE,
	"default":    DEFAULT,
	"comptime":   COMPTIME,
	"extern":     EXTERN,
	"module":     MODULE,
	"import":     IMPORT,
	"as":         AS,
	"true":       TRUE,
	"false":      FALSE,
	"nil":        NIL,
	"error":      ERROR,
	"iota":       IOTA,
	"sizeof":     SIZEOF,
	"alignof":    ALIGNOF,
	"map":        MAP,
	"chan":       CHAN,
	"set":        SET,
	"tensor":     TENSOR,
	"void":       VOID,
	"from":       FROM,
	"where":      WHERE,
	"orderby":    ORDERBY,
	"ascending":  ASCENDING,
	"descending": DESCENDING,
	"group":      GROUP,
	"by":         BY,
	"join":       JOIN,
	"into":       INTO,
	"int":        INT,
	"int8":       INT8,
	"int16":      INT16,
	"int32":      INT32,
	"int64":      INT64,
	"uint":       UINT,
	"uint8":      UINT8,
	"uint16":     UINT16,
	"uint32":     UINT32,
	"uint64":     UINT64,
	"float":      FLOAT,
	"bool":       BOOL,
	"string":     STRING_TYPE,
	"bytes":      BYTES_TYPE,
}

const (
	FN          Type = "FN"
	PUB         Type = "PUB"
	CONST       Type = "CONST"
	VAR         Type = "VAR"
	TYPE        Type = "TYPE"
	STRUCT      Type = "STRUCT"
	ENUM        Type = "ENUM"
	SERVICE     Type = "SERVICE"
	RULESET     Type = "RULESET"
	RULE        Type = "RULE"
	WHEN        Type = "WHEN"
	ACTION      Type = "ACTION"
	PRIORITY    Type = "PRIORITY"
	IF          Type = "IF"
	ELSE        Type = "ELSE"
	MATCH       Type = "MATCH"
	FOR         Type = "FOR"
	IN          Type = "IN"
	RANGE       Type = "RANGE"
	WHILE       Type = "WHILE"
	UNTIL       Type = "UNTIL"
	BREAK       Type = "BREAK"
	CONTINUE    Type = "CONTINUE"
	RETURN      Type = "RETURN"
	DEFER       Type = "DEFER"
	UNSAFE      Type = "UNSAFE"
	WITH        Type = "WITH"
	ASYNC       Type = "ASYNC"
	AWAIT       Type = "AWAIT"
	SPAWN       Type = "SPAWN"
	SELECT      Type = "SELECT"
	SWITCH      Type = "SWITCH"
	TEMPLATE    Type = "TEMPLATE"
	CASE        Type = "CASE"
	DEFAULT     Type = "DEFAULT"
	COMPTIME    Type = "COMPTIME"
	EXTERN      Type = "EXTERN"
	MODULE      Type = "MODULE"
	IMPORT      Type = "IMPORT"
	AS          Type = "AS"
	TRUE        Type = "TRUE"
	FALSE       Type = "FALSE"
	NIL         Type = "NIL"
	ERROR       Type = "ERROR"
	IOTA        Type = "IOTA"
	SIZEOF      Type = "SIZEOF"
	ALIGNOF     Type = "ALIGNOF"
	MIN         Type = "MIN"
	MAX         Type = "MAX"
	MAP         Type = "MAP"
	CHAN        Type = "CHAN"
	SET         Type = "SET"
	TENSOR      Type = "TENSOR"
	VOID        Type = "VOID"
	FROM        Type = "FROM"
	WHERE       Type = "WHERE"
	ORDERBY     Type = "ORDERBY"
	ASCENDING   Type = "ASCENDING"
	DESCENDING  Type = "DESCENDING"
	GROUP       Type = "GROUP"
	BY          Type = "BY"
	JOIN        Type = "JOIN"
	INTO        Type = "INTO"
	INT8        Type = "INT8"
	INT16       Type = "INT16"
	INT32       Type = "INT32"
	INT64       Type = "INT64"
	UINT        Type = "UINT"
	UINT8       Type = "UINT8"
	UINT16      Type = "UINT16"
	UINT32      Type = "UINT32"
	UINT64      Type = "UINT64"
	BOOL        Type = "BOOL"
	STRING_TYPE Type = "STRING_TYPE"
	BYTES_TYPE  Type = "BYTES_TYPE"
)

// LookupIdent returns the keyword token type for ident, or IDENT if it is not a keyword.
func LookupIdent(ident string) Type {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return IDENT
}
