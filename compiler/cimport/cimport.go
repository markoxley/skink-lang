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

// Package cimport parses C header files and generates Skink AST declarations.
//
// Supported C constructs:
//   - Function declarations  -> extern fn
//   - Struct declarations    -> struct
//   - Enum declarations      -> enum
//   - Typedefs               -> type aliases (ignored for now; types are mapped directly)
//   - #define constants       -> const (simple integer constants only)
package cimport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/token"
)

// Declarations parses a C header file and returns the equivalent Skink declarations.
func Declarations(path string) ([]ast.Declaration, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := newCParser(string(src))
	p.baseDir = filepath.Dir(path)
	return p.parseDeclarations()
}

// --- C type mapping ---

// mapCType converts a C type string to its Skink AST type equivalent.
func (p *cParser) mapCType(ct string) ast.Type {
	ct = strings.TrimSpace(ct)
	ct = strings.TrimSuffix(ct, " const")
	ct = strings.TrimSuffix(ct, " const ")
	ct = strings.TrimSpace(ct)

	// Resolve typedefs
	if p.typedefs != nil {
		if resolved, ok := p.typedefs[ct]; ok {
			return p.mapCType(resolved)
		}
	}

	// Pointer types
	if strings.HasSuffix(ct, "*") {
		inner := strings.TrimSpace(strings.TrimSuffix(ct, "*"))
		// void* -> *u8
		if inner == "void" {
			return &ast.PointerType{Elem: &ast.NamedType{Name: "u8"}}
		}
		base := p.mapCType(inner)
		return &ast.PointerType{Elem: base}
	}

	switch ct {
	case "void":
		return &ast.NamedType{Name: "void"}
	case "char":
		return &ast.NamedType{Name: "int8"}
	case "unsigned char":
		return &ast.NamedType{Name: "uint8"}
	case "short":
		return &ast.NamedType{Name: "int16"}
	case "unsigned short":
		return &ast.NamedType{Name: "uint16"}
	case "int":
		return &ast.NamedType{Name: "int"}
	case "unsigned int":
		return &ast.NamedType{Name: "uint"}
	case "long":
		return &ast.NamedType{Name: "int"}
	case "unsigned long":
		return &ast.NamedType{Name: "uint"}
	case "long long":
		return &ast.NamedType{Name: "int64"}
	case "unsigned long long":
		return &ast.NamedType{Name: "uint64"}
	case "float":
		return &ast.NamedType{Name: "float"}
	case "double":
		return &ast.NamedType{Name: "float"}
	case "size_t":
		return &ast.NamedType{Name: "uint"}
	case "ssize_t":
		return &ast.NamedType{Name: "int"}
	case "bool", "_Bool":
		return &ast.NamedType{Name: "bool"}
	default:
		// Could be a typedef'd name or struct tag.
		if strings.HasPrefix(ct, "struct ") {
			name := strings.TrimPrefix(ct, "struct ")
			name = strings.TrimSpace(name)
			return &ast.NamedType{Name: name}
		}
		if strings.HasPrefix(ct, "enum ") {
			name := strings.TrimPrefix(ct, "enum ")
			name = strings.TrimSpace(name)
			return &ast.NamedType{Name: name}
		}
		return &ast.NamedType{Name: ct}
	}
}

// --- Simple C tokenizer/parser ---

// cTokenKind represents the kind of a C tokenizer token.
type cTokenKind int

const (
	cEOF cTokenKind = iota
	cIdent
	cNumber
	cString
	cLParen
	cRParen
	cLBrace
	cRBrace
	cLBracket
	cRBracket
	cSemicolon
	cComma
	cStar
	cAssign
	cMinus
	cEllipsis
	cHash
	cNewline
	cOther
)

// cToken is a single token from the C header tokenizer.
type cToken struct {
	kind cTokenKind
	val  string
}

// ppState tracks preprocessor conditional branch state.
type ppState struct {
	active   bool // current branch is active
	anyTaken bool // any branch in this block was active
}

// cParser is a simple hand-written C header parser.
type cParser struct {
	src      string
	pos      int
	tok      cToken
	decls    []ast.Declaration
	typedefs map[string]string
	defines  map[string]int64 // tracks integer #defines for cross-references
	ppStack  []ppState        // preprocessor conditional stack
	baseDir  string
	seen     map[string]bool // prevent circular includes
}

// newCParser creates a C header parser for the given source.
func newCParser(src string) *cParser {
	p := &cParser{src: src}
	p.next()
	return p
}

// ppActive reports whether all active preprocessor branches are true.
func (p *cParser) ppActive() bool {
	for _, s := range p.ppStack {
		if !s.active {
			return false
		}
	}
	return true
}

// skipInactive skips tokens until the next preprocessor directive
// that can terminate an inactive block (#elif, #else, #endif).
func (p *cParser) skipInactive() {
	for p.tok.kind != cEOF {
		if p.tok.kind == cHash {
			p.next()
			if p.tok.kind == cIdent {
				switch p.tok.val {
				case "elif", "else", "endif":
					return // leave the directive on cIdent
				case "ifdef", "ifndef", "if":
					// Nested conditional — skip its entire block.
					p.skipLine()
					p.ppStack = append(p.ppStack, ppState{active: false, anyTaken: true})
					p.skipInactive()
					if p.tok.kind == cIdent && p.tok.val == "endif" {
						p.skipLine()
						if len(p.ppStack) > 0 {
							p.ppStack = p.ppStack[:len(p.ppStack)-1]
						}
					}
					continue
				}
			}
		}
		p.next()
	}
}

// next advances to the next C token.
func (p *cParser) next() {
	p.tok = p.lex()
}

// lex returns the next token from the C source.
func (p *cParser) lex() cToken {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\r' {
			p.pos++
			continue
		}
		if c == '\n' {
			p.pos++
			return cToken{kind: cNewline, val: "\n"}
		}
		if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
			// Single-line comment
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*' {
			// Multi-line comment
			p.pos += 2
			for p.pos < len(p.src) {
				if p.src[p.pos] == '*' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
					p.pos += 2
					break
				}
				p.pos++
			}
			continue
		}
		break
	}
	if p.pos >= len(p.src) {
		return cToken{kind: cEOF, val: ""}
	}
	c := p.src[p.pos]
	switch c {
	case '(':
		p.pos++
		return cToken{kind: cLParen, val: "("}
	case ')':
		p.pos++
		return cToken{kind: cRParen, val: ")"}
	case '{':
		p.pos++
		return cToken{kind: cLBrace, val: "{"}
	case '}':
		p.pos++
		return cToken{kind: cRBrace, val: "}"}
	case '[':
		p.pos++
		return cToken{kind: cLBracket, val: "["}
	case ']':
		p.pos++
		return cToken{kind: cRBracket, val: "]"}
	case ';':
		p.pos++
		return cToken{kind: cSemicolon, val: ";"}
	case ',':
		p.pos++
		return cToken{kind: cComma, val: ","}
	case '*':
		p.pos++
		return cToken{kind: cStar, val: "*"}
	case '=':
		p.pos++
		return cToken{kind: cAssign, val: "="}
	case '-':
		p.pos++
		return cToken{kind: cMinus, val: "-"}
	case '.':
		if p.pos+2 < len(p.src) && p.src[p.pos+1] == '.' && p.src[p.pos+2] == '.' {
			p.pos += 3
			return cToken{kind: cEllipsis, val: "..."}
		}
		p.pos++
		return cToken{kind: cOther, val: "."}
	case '#':
		p.pos++
		return cToken{kind: cHash, val: "#"}
	}
	if isCIdentStart(c) {
		start := p.pos
		for p.pos < len(p.src) && isCIdentChar(p.src[p.pos]) {
			p.pos++
		}
		return cToken{kind: cIdent, val: p.src[start:p.pos]}
	}
	if c >= '0' && c <= '9' {
		start := p.pos
		for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == 'x' || p.src[p.pos] == 'X' || p.src[p.pos] == 'a' && p.src[p.pos] <= 'f' || p.src[p.pos] >= 'A' && p.src[p.pos] <= 'F') {
			p.pos++
		}
		return cToken{kind: cNumber, val: p.src[start:p.pos]}
	}
	if c == '"' {
		start := p.pos
		p.pos++
		for p.pos < len(p.src) && p.src[p.pos] != '"' {
			if p.src[p.pos] == '\\' {
				p.pos += 2
			} else {
				p.pos++
			}
		}
		if p.pos < len(p.src) {
			p.pos++
		}
		return cToken{kind: cString, val: p.src[start:p.pos]}
	}
	// Single char token
	p.pos++
	return cToken{kind: cOther, val: string(c)}
}

// isCIdentStart reports whether c can start a C identifier.
func isCIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// isCIdentChar reports whether c can continue a C identifier.
func isCIdentChar(c byte) bool {
	return isCIdentStart(c) || (c >= '0' && c <= '9')
}

// --- Parsing ---

// parseDeclarations parses all top-level declarations from the C header.
func (p *cParser) parseDeclarations() ([]ast.Declaration, error) {
	for p.tok.kind != cEOF {
		decls, err := p.parseTopLevel()
		if err != nil {
			return nil, err
		}
		p.decls = append(p.decls, decls...)
	}
	return p.decls, nil
}

// parseTopLevel parses a single top-level C construct.
func (p *cParser) parseTopLevel() ([]ast.Declaration, error) {
	// Skip empty lines.
	for p.tok.kind == cNewline || p.tok.kind == cSemicolon {
		p.next()
	}
	if p.tok.kind == cEOF {
		return nil, nil
	}

	// In inactive preprocessor branch, skip until a directive.
	if !p.ppActive() {
		if p.tok.kind == cHash {
			return p.parseDirective()
		}
		// Skip tokens until newline or hash.
		for p.tok.kind != cNewline && p.tok.kind != cEOF && p.tok.kind != cHash {
			p.next()
		}
		return nil, nil
	}

	if p.tok.kind == cHash {
		return p.parseDirective()
	}

	// typedef
	if p.tok.kind == cIdent && p.tok.val == "typedef" {
		return p.parseTypedef()
	}

	// struct declaration
	if p.tok.kind == cIdent && p.tok.val == "struct" {
		return p.parseStructDecl()
	}

	// enum declaration
	if p.tok.kind == cIdent && p.tok.val == "enum" {
		return p.parseEnumDecl()
	}

	// extern "C" block or single declaration
	if p.tok.kind == cIdent && p.tok.val == "extern" {
		p.next()
		if p.tok.kind == cString {
			p.next() // consume "C"
			if p.tok.kind == cLBrace {
				p.next() // consume {
				var blockDecls []ast.Declaration
				for p.tok.kind != cRBrace && p.tok.kind != cEOF {
					decls, err := p.parseTopLevel()
					if err != nil {
						return nil, err
					}
					blockDecls = append(blockDecls, decls...)
				}
				if p.tok.kind == cRBrace {
					p.next()
				}
				return blockDecls, nil
			}
			// extern "C" single declaration — fall through
		}
	}

	// __attribute__((...)) before declaration — skip it.
	for p.tok.kind == cIdent && p.tok.val == "__attribute__" {
		p.next()
		if p.tok.kind == cLParen {
			p.skipParens()
		}
	}

	// Function declaration or variable declaration.
	return p.parseDeclOrFunction()
}

// parseDirective handles preprocessor directives.
func (p *cParser) parseDirective() ([]ast.Declaration, error) {
	p.next() // consume #
	if p.tok.kind != cIdent {
		p.skipLine()
		return nil, nil
	}
	name := p.tok.val
	switch name {
	case "define":
		if !p.ppActive() {
			p.skipLine()
			return nil, nil
		}
		return p.parseDefine()
	case "include":
		if !p.ppActive() {
			p.skipLine()
			return nil, nil
		}
		return p.parseInclude()
	case "ifdef":
		return p.parseIfdef(true)
	case "ifndef":
		return p.parseIfdef(false)
	case "if":
		return p.parseIf()
	case "elif":
		return p.parseElif()
	case "else":
		return p.parsePpElse()
	case "endif":
		return p.parseEndif()
	}
	p.skipLine()
	return nil, nil
}

// parseDefine parses a #define directive into a constant declaration.
func (p *cParser) parseDefine() ([]ast.Declaration, error) {
	p.next() // consume 'define'
	if p.tok.kind != cIdent {
		p.skipLine()
		return nil, nil
	}
	name := p.tok.val
	p.next()

	// Collect remaining tokens on this line.
	var toks []cToken
	for p.tok.kind != cNewline && p.tok.kind != cEOF {
		toks = append(toks, p.tok)
		p.next()
	}
	if p.tok.kind == cNewline {
		p.next()
	}

	var val int64
	if len(toks) > 0 {
		ev := &exprEval{toks: toks, defines: p.defines}
		v, ok := ev.parse()
		if !ok {
			return nil, nil
		}
		val = v
	} else {
		// #define NAME with no value — treat as boolean flag (1).
		val = 1
	}

	if p.defines == nil {
		p.defines = make(map[string]int64)
	}
	p.defines[name] = val

	decl := &ast.ConstDecl{
		Token: token.Token{Type: token.CONST, Literal: "const"},
		Name:  name,
		Value: &ast.IntegerLiteral{Value: val},
	}
	return []ast.Declaration{decl}, nil
}

// --- Simple expression evaluator for #define values ---

// exprEval evaluates simple integer expressions in #define directives.
type exprEval struct {
	toks    []cToken
	pos     int
	defines map[string]int64
}

// peek returns the current token without advancing.
func (e *exprEval) peek() cToken {
	if e.pos < len(e.toks) {
		return e.toks[e.pos]
	}
	return cToken{kind: cEOF, val: ""}
}

// next returns the current token and advances.
func (e *exprEval) next() cToken {
	t := e.peek()
	if e.pos < len(e.toks) {
		e.pos++
	}
	return t
}

// parse evaluates the expression and returns its integer value.
func (e *exprEval) parse() (int64, bool) {
	return e.parseLogOr()
}

// logical OR
// parseLogOr parses logical OR.
func (e *exprEval) parseLogOr() (int64, bool) {
	left, ok := e.parseLogAnd()
	if !ok {
		return 0, false
	}
	for e.isDoubleOp("||") {
		e.next()
		e.next()
		right, ok := e.parseLogAnd()
		if !ok {
			return 0, false
		}
		if left != 0 || right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, true
}

// logical AND
// parseLogAnd parses logical AND.
func (e *exprEval) parseLogAnd() (int64, bool) {
	left, ok := e.parseBitOr()
	if !ok {
		return 0, false
	}
	for e.isDoubleOp("&&") {
		e.next()
		e.next()
		right, ok := e.parseBitOr()
		if !ok {
			return 0, false
		}
		if left != 0 && right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, true
}

// bitwise OR
// parseBitOr parses bitwise OR.
func (e *exprEval) parseBitOr() (int64, bool) {
	left, ok := e.parseBitXor()
	if !ok {
		return 0, false
	}
	for e.peek().kind == cOther && e.peek().val == "|" && !e.isDoubleOp("||") {
		e.next()
		right, ok := e.parseBitXor()
		if !ok {
			return 0, false
		}
		left = left | right
	}
	return left, true
}

// bitwise XOR
// parseBitXor parses bitwise XOR.
func (e *exprEval) parseBitXor() (int64, bool) {
	left, ok := e.parseBitAnd()
	if !ok {
		return 0, false
	}
	for e.peek().kind == cOther && e.peek().val == "^" {
		e.next()
		right, ok := e.parseBitAnd()
		if !ok {
			return 0, false
		}
		left = left ^ right
	}
	return left, true
}

// bitwise AND
// parseBitAnd parses bitwise AND.
func (e *exprEval) parseBitAnd() (int64, bool) {
	left, ok := e.parseEq()
	if !ok {
		return 0, false
	}
	for e.peek().kind == cOther && e.peek().val == "&" && !e.isDoubleOp("&&") {
		e.next()
		right, ok := e.parseEq()
		if !ok {
			return 0, false
		}
		left = left & right
	}
	return left, true
}

// equality == !=
// parseEq parses equality operators.
func (e *exprEval) parseEq() (int64, bool) {
	left, ok := e.parseRel()
	if !ok {
		return 0, false
	}
	for {
		if e.isDoubleOp("==") {
			e.next()
			e.next()
			right, ok := e.parseRel()
			if !ok {
				return 0, false
			}
			if left == right {
				left = 1
			} else {
				left = 0
			}
		} else if e.isDoubleOp("!=") {
			e.next()
			e.next()
			right, ok := e.parseRel()
			if !ok {
				return 0, false
			}
			if left != right {
				left = 1
			} else {
				left = 0
			}
		} else {
			break
		}
	}
	return left, true
}

// relational < > <= >=
// parseRel parses relational operators.
func (e *exprEval) parseRel() (int64, bool) {
	left, ok := e.parseShift()
	if !ok {
		return 0, false
	}
	for {
		if e.isDoubleOp("<=") {
			e.next()
			e.next()
			right, ok := e.parseShift()
			if !ok {
				return 0, false
			}
			if left <= right {
				left = 1
			} else {
				left = 0
			}
		} else if e.isDoubleOp(">=") {
			e.next()
			e.next()
			right, ok := e.parseShift()
			if !ok {
				return 0, false
			}
			if left >= right {
				left = 1
			} else {
				left = 0
			}
		} else if e.peek().kind == cOther && e.peek().val == "<" && !e.isDoubleOp("<<") {
			e.next()
			right, ok := e.parseShift()
			if !ok {
				return 0, false
			}
			if left < right {
				left = 1
			} else {
				left = 0
			}
		} else if e.peek().kind == cOther && e.peek().val == ">" && !e.isDoubleOp(">>") {
			e.next()
			right, ok := e.parseShift()
			if !ok {
				return 0, false
			}
			if left > right {
				left = 1
			} else {
				left = 0
			}
		} else {
			break
		}
	}
	return left, true
}

// parseShift parses shift operators.
func (e *exprEval) parseShift() (int64, bool) {
	left, ok := e.parseAdd()
	if !ok {
		return 0, false
	}
	for {
		if e.isDoubleOp("<<") {
			e.next()
			e.next()
			right, ok := e.parseAdd()
			if !ok {
				return 0, false
			}
			left = left << uint(right)
		} else if e.isDoubleOp(">>") {
			e.next()
			e.next()
			right, ok := e.parseAdd()
			if !ok {
				return 0, false
			}
			left = left >> uint(right)
		} else {
			break
		}
	}
	return left, true
}

// parseAdd parses addition and subtraction.
func (e *exprEval) parseAdd() (int64, bool) {
	left, ok := e.parseMul()
	if !ok {
		return 0, false
	}
	for {
		if e.peek().kind == cOther && e.peek().val == "+" {
			e.next()
			right, ok := e.parseMul()
			if !ok {
				return 0, false
			}
			left = left + right
		} else if e.peek().kind == cMinus || (e.peek().kind == cOther && e.peek().val == "-") {
			e.next()
			right, ok := e.parseMul()
			if !ok {
				return 0, false
			}
			left = left - right
		} else {
			break
		}
	}
	return left, true
}

// parseMul parses multiplication, division, and modulo.
func (e *exprEval) parseMul() (int64, bool) {
	left, ok := e.parseUnary()
	if !ok {
		return 0, false
	}
	for {
		if e.peek().kind == cOther && e.peek().val == "*" {
			e.next()
			right, ok := e.parseUnary()
			if !ok {
				return 0, false
			}
			left = left * right
		} else if e.peek().kind == cOther && e.peek().val == "/" {
			e.next()
			right, ok := e.parseUnary()
			if !ok {
				return 0, false
			}
			left = left / right
		} else if e.peek().kind == cOther && e.peek().val == "%" {
			e.next()
			right, ok := e.parseUnary()
			if !ok {
				return 0, false
			}
			left = left % right
		} else {
			break
		}
	}
	return left, true
}

// parseUnary parses unary operators.
func (e *exprEval) parseUnary() (int64, bool) {
	t := e.peek()
	if t.kind == cOther && t.val == "+" {
		e.next()
		return e.parseUnary()
	}
	if t.kind == cMinus || (t.kind == cOther && t.val == "-") {
		e.next()
		v, ok := e.parseUnary()
		if !ok {
			return 0, false
		}
		return -v, true
	}
	if t.kind == cOther && t.val == "~" {
		e.next()
		v, ok := e.parseUnary()
		if !ok {
			return 0, false
		}
		return ^v, true
	}
	if t.kind == cOther && t.val == "!" {
		e.next()
		v, ok := e.parseUnary()
		if !ok {
			return 0, false
		}
		if v == 0 {
			return 1, true
		}
		return 0, true
	}
	return e.parsePrimary()
}

// parsePrimary parses primary expressions (numbers, identifiers, parentheses).
func (e *exprEval) parsePrimary() (int64, bool) {
	t := e.peek()
	switch t.kind {
	case cNumber:
		e.next()
		numStr := t.val
		numStr = stripCSuffix(numStr)
		v, err := strconv.ParseInt(numStr, 0, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	case cIdent:
		e.next()
		if t.val == "defined" {
			if e.peek().kind == cLParen {
				e.next()
				if e.peek().kind == cIdent {
					name := e.peek().val
					e.next()
					if e.peek().kind == cRParen {
						e.next()
					}
					if e.defines != nil {
						if _, ok := e.defines[name]; ok {
							return 1, true
						}
					}
					return 0, true
				}
			} else if e.peek().kind == cIdent {
				name := e.peek().val
				e.next()
				if e.defines != nil {
					if _, ok := e.defines[name]; ok {
						return 1, true
					}
				}
				return 0, true
			}
		}
		if e.defines != nil {
			if v, ok := e.defines[t.val]; ok {
				return v, true
			}
		}
		return 0, true
	case cLParen:
		e.next()
		v, ok := e.parseLogOr()
		if !ok {
			return 0, false
		}
		if e.peek().kind != cRParen {
			return 0, false
		}
		e.next()
		return v, true
	}
	return 0, false
}

// isDoubleOp checks if the next two tokens form the given two-character operator.
func (e *exprEval) isDoubleOp(op string) bool {
	if e.pos+1 >= len(e.toks) {
		return false
	}
	return e.toks[e.pos].val == string(op[0]) && e.toks[e.pos+1].val == string(op[1])
}

// stripCSuffix removes C integer literal suffixes like u, U, l, L.
func stripCSuffix(s string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == 'u' || last == 'U' || last == 'l' || last == 'L' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// --- Preprocessor conditional directives ---

// parseIfdef handles #ifdef and #ifndef directives.
func (p *cParser) parseIfdef(wantDefined bool) ([]ast.Declaration, error) {
	p.next() // consume 'ifdef' / 'ifndef'
	var defined bool
	if p.tok.kind == cIdent {
		if p.defines != nil {
			_, defined = p.defines[p.tok.val]
		}
		p.next()
	}
	p.skipLine()
	parentActive := p.ppActive()
	active := parentActive && (defined == wantDefined)
	p.ppStack = append(p.ppStack, ppState{active: active, anyTaken: active})
	if !active {
		p.skipInactive()
		if p.tok.kind == cIdent && p.tok.val == "endif" {
			return p.parseEndif()
		}
	}
	return nil, nil
}

// parseIf handles #if directives.
func (p *cParser) parseIf() ([]ast.Declaration, error) {
	p.next() // consume 'if'
	var toks []cToken
	for p.tok.kind != cNewline && p.tok.kind != cEOF {
		toks = append(toks, p.tok)
		p.next()
	}
	p.skipLine()
	active := false
	if p.ppActive() {
		ev := &exprEval{toks: toks, defines: p.defines}
		if val, ok := ev.parse(); ok && val != 0 {
			active = true
		}
	}
	p.ppStack = append(p.ppStack, ppState{active: active, anyTaken: active})
	if !active {
		p.skipInactive()
		if p.tok.kind == cIdent && p.tok.val == "endif" {
			return p.parseEndif()
		}
	}
	return nil, nil
}

// parseElif handles #elif directives.
func (p *cParser) parseElif() ([]ast.Declaration, error) {
	p.next() // consume 'elif'
	if len(p.ppStack) == 0 {
		p.skipLine()
		return nil, nil
	}
	state := &p.ppStack[len(p.ppStack)-1]
	parentActive := true
	if len(p.ppStack) > 1 {
		for i := 0; i < len(p.ppStack)-1; i++ {
			if !p.ppStack[i].active {
				parentActive = false
				break
			}
		}
	}
	var toks []cToken
	for p.tok.kind != cNewline && p.tok.kind != cEOF {
		toks = append(toks, p.tok)
		p.next()
	}
	p.skipLine()
	if parentActive && !state.anyTaken {
		ev := &exprEval{toks: toks, defines: p.defines}
		if val, ok := ev.parse(); ok && val != 0 {
			state.active = true
			state.anyTaken = true
			return nil, nil
		}
	}
	state.active = false
	p.skipInactive()
	if p.tok.kind == cIdent && p.tok.val == "endif" {
		return p.parseEndif()
	}
	return nil, nil
}

// parsePpElse handles #else directives.
func (p *cParser) parsePpElse() ([]ast.Declaration, error) {
	p.next() // consume 'else'
	p.skipLine()
	if len(p.ppStack) == 0 {
		return nil, nil
	}
	state := &p.ppStack[len(p.ppStack)-1]
	parentActive := true
	if len(p.ppStack) > 1 {
		for i := 0; i < len(p.ppStack)-1; i++ {
			if !p.ppStack[i].active {
				parentActive = false
				break
			}
		}
	}
	if parentActive && !state.anyTaken {
		state.active = true
		state.anyTaken = true
		return nil, nil
	}
	state.active = false
	p.skipInactive()
	if p.tok.kind == cIdent && p.tok.val == "endif" {
		return p.parseEndif()
	}
	return nil, nil
}

// parseEndif handles #endif directives.
func (p *cParser) parseEndif() ([]ast.Declaration, error) {
	p.next() // consume 'endif'
	p.skipLine()
	if len(p.ppStack) > 0 {
		p.ppStack = p.ppStack[:len(p.ppStack)-1]
	}
	return nil, nil
}

// parseInclude handles #include directives.
func (p *cParser) parseInclude() ([]ast.Declaration, error) {
	p.next() // consume 'include'

	var includePath string
	if p.tok.kind == cString {
		includePath = strings.Trim(p.tok.val, "\"")
		p.next()
	} else if p.tok.kind == cOther && p.tok.val == "<" {
		p.next()
		var parts []string
		for p.tok.kind != cEOF && p.tok.kind != cNewline {
			if p.tok.kind == cOther && p.tok.val == ">" {
				p.next()
				break
			}
			parts = append(parts, p.tok.val)
			p.next()
		}
		includePath = strings.Join(parts, "")
	}
	p.skipLine()

	if includePath == "" {
		return nil, nil
	}

	resolved := p.resolveIncludePath(includePath)
	if resolved == "" {
		return nil, nil
	}

	if p.seen == nil {
		p.seen = make(map[string]bool)
	}
	if p.seen[resolved] {
		return nil, nil
	}
	p.seen[resolved] = true

	src, err := os.ReadFile(resolved)
	if err != nil {
		return nil, nil
	}

	sub := newCParser(string(src))
	sub.baseDir = filepath.Dir(resolved)
	sub.seen = p.seen
	sub.typedefs = p.typedefs
	sub.defines = p.defines

	decls, err := sub.parseDeclarations()
	if err != nil {
		return nil, nil
	}
	return decls, nil
}

// resolveIncludePath resolves an include path relative to baseDir or system paths.
func (p *cParser) resolveIncludePath(inc string) string {
	// Local include: relative to baseDir
	if !filepath.IsAbs(inc) && p.baseDir != "" {
		candidate := filepath.Join(p.baseDir, inc)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// System include paths
	systemPaths := []string{
		"/usr/include",
		"/usr/local/include",
	}
	for _, dir := range systemPaths {
		candidate := filepath.Join(dir, inc)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

// skipLine consumes tokens until the end of the current line.
func (p *cParser) skipLine() {
	for p.tok.kind != cNewline && p.tok.kind != cEOF {
		p.next()
	}
	if p.tok.kind == cNewline {
		p.next()
	}
}

// parseTypedef handles typedef declarations.
func (p *cParser) parseTypedef() ([]ast.Declaration, error) {
	p.next() // consume 'typedef'

	// struct typedef: typedef struct [Tag] { ... } Alias;
	if p.tok.kind == cIdent && p.tok.val == "struct" {
		return p.parseTypedefStruct()
	}
	// enum typedef: typedef enum [Tag] { ... } Alias;
	if p.tok.kind == cIdent && p.tok.val == "enum" {
		return p.parseTypedefEnum()
	}

	// Simple typedef: collect tokens until ';'.
	var parts []string
	for p.tok.kind != cSemicolon && p.tok.kind != cEOF {
		if p.tok.kind == cIdent || p.tok.kind == cStar {
			parts = append(parts, p.tok.val)
		} else if p.tok.kind == cLParen || p.tok.kind == cRParen {
			// Function pointer typedef; too complex for now.
			p.skipLine()
			if p.tok.kind == cSemicolon {
				p.next()
			}
			return nil, nil
		}
		p.next()
	}
	if p.tok.kind == cSemicolon {
		p.next()
	}
	if len(parts) >= 2 {
		newName := parts[len(parts)-1]
		baseType := strings.Join(parts[:len(parts)-1], " ")
		if p.typedefs == nil {
			p.typedefs = make(map[string]string)
		}
		p.typedefs[newName] = baseType
	}
	return nil, nil
}

// parseTypedefStruct handles typedef struct declarations.
func (p *cParser) parseTypedefStruct() ([]ast.Declaration, error) {
	p.next() // consume 'struct'

	var tagName string
	if p.tok.kind == cIdent {
		tagName = p.tok.val
		p.next()
	}

	var fields []*ast.FieldDecl
	if p.tok.kind == cLBrace {
		p.next() // consume '{'
		for p.tok.kind != cRBrace && p.tok.kind != cEOF {
			ftype, fname, ok := p.parseField()
			if !ok {
				p.skipLine()
				continue
			}
			fields = append(fields, &ast.FieldDecl{
				Name: fname,
				Type: ftype,
			})
			if p.tok.kind == cSemicolon {
				p.next()
			}
		}
		if p.tok.kind == cRBrace {
			p.next()
		}
	}

	var alias string
	if p.tok.kind == cIdent {
		alias = p.tok.val
		p.next()
	}
	if p.tok.kind == cSemicolon {
		p.next()
	}
	if alias == "" {
		return nil, nil
	}

	if p.typedefs == nil {
		p.typedefs = make(map[string]string)
	}

	// If we parsed fields, emit a struct declaration named after the alias.
	if len(fields) > 0 {
		decl := &ast.StructDecl{
			Token:  token.Token{Type: token.STRUCT, Literal: "struct"},
			Name:   alias,
			Fields: fields,
		}
		p.typedefs[alias] = "struct " + alias
		return []ast.Declaration{decl}, nil
	}

	// Opaque typedef: typedef struct Tag Alias;
	if tagName != "" {
		p.typedefs[alias] = "struct " + tagName
	} else {
		p.typedefs[alias] = alias
	}
	return nil, nil
}

// parseTypedefEnum handles typedef enum declarations.
func (p *cParser) parseTypedefEnum() ([]ast.Declaration, error) {
	p.next() // consume 'enum'

	var tagName string
	if p.tok.kind == cIdent {
		tagName = p.tok.val
		p.next()
	}

	var variants []string
	if p.tok.kind == cLBrace {
		p.next() // consume '{'
		for p.tok.kind != cRBrace && p.tok.kind != cEOF {
			if p.tok.kind == cIdent {
				variants = append(variants, p.tok.val)
				p.next()
			}
			if p.tok.kind == cAssign {
				p.next()
				// Skip integer literal.
				if p.tok.kind == cNumber || p.tok.kind == cIdent {
					p.next()
				} else if p.tok.kind == cMinus {
					p.next()
					if p.tok.kind == cNumber {
						p.next()
					}
				}
			}
			if p.tok.kind == cComma {
				p.next()
			}
		}
		if p.tok.kind == cRBrace {
			p.next()
		}
	}

	var alias string
	if p.tok.kind == cIdent {
		alias = p.tok.val
		p.next()
	}
	if p.tok.kind == cSemicolon {
		p.next()
	}
	if alias == "" {
		return nil, nil
	}

	if p.typedefs == nil {
		p.typedefs = make(map[string]string)
	}

	if len(variants) > 0 {
		decl := &ast.EnumDecl{
			Token:    token.Token{Type: token.ENUM, Literal: "enum"},
			Name:     alias,
			Variants: variants,
		}
		p.typedefs[alias] = alias
		return []ast.Declaration{decl}, nil
	}

	if tagName != "" {
		p.typedefs[alias] = tagName
	} else {
		p.typedefs[alias] = alias
	}
	return nil, nil
}

// parseStructDecl parses a struct declaration.
func (p *cParser) parseStructDecl() ([]ast.Declaration, error) {
	p.next() // consume 'struct'
	if p.tok.kind != cIdent {
		p.skipLine()
		return nil, nil
	}
	name := p.tok.val
	p.next()
	if p.tok.kind != cLBrace {
		// Forward declaration or typedef use; skip.
		p.skipLine()
		return nil, nil
	}
	p.next() // consume '{'

	var fields []*ast.FieldDecl
	for p.tok.kind != cRBrace && p.tok.kind != cEOF {
		// Parse field: type name [size];
		ftype, fname, ok := p.parseField()
		if !ok {
			p.skipLine()
			continue
		}
		fields = append(fields, &ast.FieldDecl{
			Name: fname,
			Type: ftype,
		})
		if p.tok.kind == cSemicolon {
			p.next()
		}
	}
	if p.tok.kind == cRBrace {
		p.next()
	}
	if p.tok.kind == cSemicolon {
		p.next()
	}

	decl := &ast.StructDecl{
		Token:  token.Token{Type: token.STRUCT, Literal: "struct"},
		Name:   name,
		Fields: fields,
	}
	return []ast.Declaration{decl}, nil
}

// parseField parses a single field inside a struct.
func (p *cParser) parseField() (ast.Type, string, bool) {
	// Collect type tokens until identifier.
	var typeTokens []string
	for p.tok.kind == cIdent || p.tok.kind == cStar {
		if p.tok.kind == cIdent && p.tok.val == "struct" {
			p.next()
			if p.tok.kind == cIdent {
				typeTokens = append(typeTokens, "struct", p.tok.val)
				p.next()
			}
			continue
		}
		if p.tok.kind == cStar {
			typeTokens = append(typeTokens, "*")
			p.next()
			continue
		}
		// Check if this is the field name (followed by ; or [ or ,)
		if p.peekIsFieldName() {
			break
		}
		typeTokens = append(typeTokens, p.tok.val)
		p.next()
	}
	if p.tok.kind != cIdent {
		return nil, "", false
	}
	fname := p.tok.val
	p.next()
	// Skip array size.
	if p.tok.kind == cLBracket {
		p.next()
		for p.tok.kind != cRBracket && p.tok.kind != cEOF {
			p.next()
		}
		if p.tok.kind == cRBracket {
			p.next()
		}
		typeTokens = append(typeTokens, "*")
	}
	ftype := p.mapCType(strings.Join(typeTokens, " "))
	return ftype, fname, true
}

// peekIsFieldName is a heuristic to detect whether the current token is a field name.
func (p *cParser) peekIsFieldName() bool {
	// Simple heuristic: if current token is an identifier and
	// the next non-space token is ; [ or , it's the field name.
	// Our lexer already skips whitespace, so we just look at current.
	if p.tok.kind != cIdent {
		return false
	}
	// If the token is a C keyword, it's part of the type.
	switch p.tok.val {
	case "int", "unsigned", "signed", "short", "long", "char", "float", "double", "void", "const", "struct", "enum":
		return false
	}
	// If it's a known typedef, it's part of the type.
	if p.typedefs != nil {
		if _, ok := p.typedefs[p.tok.val]; ok {
			return false
		}
	}
	return true
}

// parseEnumDecl parses an enum declaration.
func (p *cParser) parseEnumDecl() ([]ast.Declaration, error) {
	p.next() // consume 'enum'
	if p.tok.kind != cIdent {
		p.skipLine()
		return nil, nil
	}
	name := p.tok.val
	p.next()
	if p.tok.kind != cLBrace {
		p.skipLine()
		return nil, nil
	}
	p.next() // consume '{'

	var variants []string
	for p.tok.kind != cRBrace && p.tok.kind != cEOF {
		if p.tok.kind != cIdent {
			p.next()
			continue
		}
		cname := p.tok.val
		p.next()
		if p.tok.kind == cOther && p.tok.val == "=" {
			p.next()
			if p.tok.kind == cNumber {
				p.next()
			}
		}
		variants = append(variants, cname)
		if p.tok.kind == cComma {
			p.next()
		}
	}
	if p.tok.kind == cRBrace {
		p.next()
	}
	if p.tok.kind == cSemicolon {
		p.next()
	}

	decl := &ast.EnumDecl{
		Token:    token.Token{Type: token.ENUM, Literal: "enum"},
		Name:     name,
		Variants: variants,
	}
	return []ast.Declaration{decl}, nil
}

// parseDeclOrFunction parses a variable or function declaration.
func (p *cParser) parseDeclOrFunction() ([]ast.Declaration, error) {
	// Collect declaration tokens up to ; or {
	// First, skip qualifiers.
	for p.tok.kind == cIdent && (p.tok.val == "extern" || p.tok.val == "static" || p.tok.val == "inline" || p.tok.val == "const" || p.tok.val == "volatile" || p.tok.val == "__attribute__") {
		if p.tok.val == "__attribute__" {
			p.next()
			if p.tok.kind == cLParen {
				p.skipParens()
			}
			continue
		}
		p.next()
	}

	// Now we should have type + name.
	var typeParts []string
	for p.tok.kind == cIdent || p.tok.kind == cStar {
		if p.tok.kind == cIdent && p.tok.val == "struct" {
			p.next()
			if p.tok.kind == cIdent {
				typeParts = append(typeParts, "struct", p.tok.val)
				p.next()
			}
			continue
		}
		if p.tok.kind == cStar {
			typeParts = append(typeParts, "*")
			p.next()
			continue
		}
		// Heuristic: if current token is the name (not a type keyword), break.
		if p.tok.kind == cIdent && p.peekIsFieldName() {
			break
		}
		typeParts = append(typeParts, p.tok.val)
		p.next()
	}

	if p.tok.kind != cIdent {
		// Not a declaration we can handle; skip.
		p.skipLine()
		return nil, nil
	}

	name := p.tok.val
	p.next()

	if p.tok.kind == cLParen {
		// Function declaration.
		return p.parseFunctionDecl(typeParts, name)
	}

	// Variable declaration; skip.
	p.skipLine()
	return nil, nil
}

// parseFunctionDecl parses a C function declaration into an ExternFnDecl.
func (p *cParser) parseFunctionDecl(typeParts []string, name string) ([]ast.Declaration, error) {
	p.next() // consume '('
	var params []*ast.Param
	var variadic bool
	for p.tok.kind != cRParen && p.tok.kind != cEOF {
		if p.tok.kind == cIdent && p.tok.val == "void" {
			p.next()
			if p.tok.kind == cRParen {
				break
			}
		}
		// Variadic parameter.
		if p.tok.kind == cEllipsis {
			p.next()
			variadic = true
			if p.tok.kind == cComma {
				p.next()
			}
			continue
		}
		ptype, pname, ok := p.parseParam()
		if !ok {
			p.next()
			continue
		}
		params = append(params, &ast.Param{
			Name: pname,
			Type: ptype,
		})
		if p.tok.kind == cComma {
			p.next()
		}
	}
	if p.tok.kind == cRParen {
		p.next()
	}
	// Skip to semicolon or body.
	for p.tok.kind != cSemicolon && p.tok.kind != cLBrace && p.tok.kind != cEOF {
		p.next()
	}
	if p.tok.kind == cLBrace {
		// Skip function body.
		p.next()
		depth := 1
		for depth > 0 && p.tok.kind != cEOF {
			if p.tok.kind == cLBrace {
				depth++
			} else if p.tok.kind == cRBrace {
				depth--
			}
			p.next()
		}
		return nil, nil // Skip function definitions; we only want declarations.
	}
	if p.tok.kind == cSemicolon {
		p.next()
	}

	retType := p.mapCType(strings.Join(typeParts, " "))
	var paramTypes []ast.Type
	for _, f := range params {
		paramTypes = append(paramTypes, f.Type)
	}

	decl := &ast.ExternFnDecl{
		Token:      token.Token{Type: token.IMPORT, Literal: "extern"},
		Name:       name,
		Params:     params,
		ReturnType: retType,
		Varargs:    variadic,
	}
	_ = paramTypes
	return []ast.Declaration{decl}, nil
}

// parseParam parses a single function parameter.
func (p *cParser) parseParam() (ast.Type, string, bool) {
	var typeTokens []string
	for p.tok.kind == cIdent || p.tok.kind == cStar {
		if p.tok.kind == cIdent && p.tok.val == "struct" {
			p.next()
			if p.tok.kind == cIdent {
				typeTokens = append(typeTokens, "struct", p.tok.val)
				p.next()
			}
			continue
		}
		if p.tok.kind == cStar {
			typeTokens = append(typeTokens, "*")
			p.next()
			continue
		}
		// Heuristic: if next token is ) or , then this is the param name.
		if p.peekIsParamName() {
			break
		}
		typeTokens = append(typeTokens, p.tok.val)
		p.next()
	}

	// Function pointer parameter: type (*name)(params)
	if p.tok.kind == cLParen {
		return p.parseFunctionPointerParam(typeTokens)
	}

	if p.tok.kind != cIdent {
		return nil, "", false
	}
	pname := p.tok.val
	p.next()
	// Skip array declarator.
	if p.tok.kind == cLBracket {
		p.next()
		for p.tok.kind != cRBracket && p.tok.kind != cEOF {
			p.next()
		}
		if p.tok.kind == cRBracket {
			p.next()
		}
		typeTokens = append(typeTokens, "*")
	}
	ptype := p.mapCType(strings.Join(typeTokens, " "))
	return ptype, pname, true
}

// parseFunctionPointerParam handles C function-pointer parameters like
//
//	int (*compar)(const void *, const void *)
//
// It returns a void* type and the parameter name.
func (p *cParser) parseFunctionPointerParam(typeTokens []string) (ast.Type, string, bool) {
	p.next() // consume '('
	// Expect '*' then the name then ')'
	if p.tok.kind == cStar {
		p.next()
	}
	var pname string
	if p.tok.kind == cIdent {
		pname = p.tok.val
		p.next()
	}
	if p.tok.kind == cRParen {
		p.next()
	}
	// Skip the parameter list ( ... )
	if p.tok.kind == cLParen {
		p.skipParens()
	}
	// Map function pointer to void*.
	return &ast.PointerType{Elem: &ast.NamedType{Name: "void"}}, pname, pname != ""
}

// peekIsParamName is a heuristic to detect parameter names.
func (p *cParser) peekIsParamName() bool {
	if p.tok.kind != cIdent {
		return false
	}
	switch p.tok.val {
	case "int", "unsigned", "signed", "short", "long", "char", "float", "double", "void", "const", "struct", "enum":
		return false
	}
	if p.typedefs != nil {
		if _, ok := p.typedefs[p.tok.val]; ok {
			return false
		}
	}
	return true
}

// skipParens skips balanced parentheses.
func (p *cParser) skipParens() {
	if p.tok.kind == cLParen {
		p.next()
		depth := 1
		for depth > 0 && p.tok.kind != cEOF {
			if p.tok.kind == cLParen {
				depth++
			} else if p.tok.kind == cRParen {
				depth--
			}
			p.next()
		}
	}
}
