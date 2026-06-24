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

// Package parser implements a recursive-descent Pratt parser for the Skink
// programming language. It transforms a stream of tokens from the lexer into
// an abstract syntax tree (AST) that the type checker and code generator
// consume.
//
// The parser uses a three-token lookahead buffer (cur, peek, peek2) to handle
// complex constructs such as generic type parameters, method receivers, and
// ambiguous expression boundaries. It supports the full Skink grammar including
// functions, structs, enums, templates, comptime blocks, channels, rulesets,
// pattern matching, and LINQ-style query expressions.
package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/token"
)

// Parser transforms a stream of tokens from the lexer into an AST.
// It maintains a three-token lookahead window (current, peek, peek2)
// which is essential for disambiguating generic syntax, method receivers,
// and other constructs that require more than one token of context.
type Parser struct {
	l        *lexer.Lexer // source lexer producing the token stream
	curTok   token.Token  // token currently under examination
	peekTok  token.Token  // one token ahead
	peek2Tok token.Token  // two tokens ahead
	errors   []string     // accumulated parse error messages
}

// New creates a Parser backed by the given Lexer.
// It immediately primes the lookahead buffer by reading three tokens
// so that curTok, peekTok, and peek2Tok are all valid.
func New(l *lexer.Lexer) *Parser {
	p := &Parser{l: l}
	p.nextToken()
	p.nextToken()
	p.nextToken()
	return p
}

// Errors returns all parse errors collected during parsing.
// Callers should check this slice after ParseProgram to determine
// whether the source was syntactically valid.
func (p *Parser) Errors() []string { return p.errors }

// peekError records an error when the parser expected a specific token type
// but found something else in the peek position.
func (p *Parser) peekError(t token.Type) {
	msg := fmt.Sprintf("expected next token to be %s, got %s instead (%q) at %d:%d",
		t, p.peekTok.Type, p.peekTok.Literal, p.peekTok.Line, p.peekTok.Column)
	p.errors = append(p.errors, msg)
}

// nextToken advances the three-token lookahead window by one position.
// Newlines and plain comments are skipped automatically so that the parser
// only sees meaningful tokens.
func (p *Parser) nextToken() {
	p.curTok = p.peekTok
	p.peekTok = p.peek2Tok
	p.peek2Tok = p.l.NextToken()
	// Skip newlines and comments between tokens during parsing
	for p.peek2Tok.Type == token.NEWLINE || p.peek2Tok.Type == token.COMMENT {
		p.peek2Tok = p.l.NextToken()
	}
}

// isTypeStart reports whether the given token can begin a type expression.
// This includes primitive types, composite type constructors (array, chan,
// set, tensor), identifiers (named or imported types), and pointer types.
func isTypeStart(t token.Type) bool {
	switch t {
	case token.LBRACKET, token.STAR, token.IDENT, token.SET, token.CHAN, token.TENSOR,
		token.INT, token.INT8, token.INT16, token.INT32, token.INT64,
		token.UINT, token.UINT8, token.UINT16, token.UINT32, token.UINT64,
		token.FLOAT, token.BOOL, token.STRING_TYPE, token.BYTES_TYPE,
		token.VOID, token.ERROR:
		return true
	}
	return false
}

// ParseProgram parses the entire token stream into a Program AST node.
// It collects top-level declarations (functions, structs, enums, constants,
// variables, imports, etc.) and attaches any preceding documentation comments
// to the declaration that follows them.
func (p *Parser) ParseProgram() *ast.Program {
	program := &ast.Program{Declarations: []ast.Declaration{}}
	var pendingDocs []string
	for p.curTok.Type != token.EOF {
		// Accumulate leading doc comments.
		for p.curTok.Type == token.DOC {
			pendingDocs = append(pendingDocs, strings.TrimSpace(strings.TrimPrefix(p.curTok.Literal, "///")))
			p.nextToken()
		}
		decl := p.parseDeclaration()
		if decl != nil {
			// Expand import blocks into individual declarations.
			if block, ok := decl.(*ast.ImportBlockDecl); ok {
				for _, imp := range block.Decls {
					program.Declarations = append(program.Declarations, imp)
				}
				pendingDocs = nil
			} else {
				// Attach accumulated docs to supported declaration types.
				switch d := decl.(type) {
				case *ast.FnDecl:
					if len(pendingDocs) > 0 {
						d.Doc = strings.Join(pendingDocs, "\n")
					}
				case *ast.StructDecl:
					if len(pendingDocs) > 0 {
						d.Doc = strings.Join(pendingDocs, "\n")
					}
				case *ast.EnumDecl:
					if len(pendingDocs) > 0 {
						d.Doc = strings.Join(pendingDocs, "\n")
					}
				case *ast.ConstDecl:
					if len(pendingDocs) > 0 {
						d.Doc = strings.Join(pendingDocs, "\n")
					}
				case *ast.VarDecl:
					if len(pendingDocs) > 0 {
						d.Doc = strings.Join(pendingDocs, "\n")
					}
				}
				program.Declarations = append(program.Declarations, decl)
				pendingDocs = nil
			}
		}
		// consume remaining newlines/comments after a declaration
		for p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC {
			if p.curTok.Type == token.DOC {
				pendingDocs = append(pendingDocs, strings.TrimSpace(strings.TrimPrefix(p.curTok.Literal, "///")))
			}
			p.nextToken()
		}
	}
	return program
}

// parseDeclaration parses a single top-level declaration.
// It handles attributes (e.g. [inline]), visibility (pub), and dispatches
// to the appropriate parser based on the current token type.
func (p *Parser) parseDeclaration() ast.Declaration {
	var attributes []string
	if p.curTok.Type == token.LBRACKET {
		p.nextToken() // consume '['
		for p.curTok.Type != token.RBRACKET && p.curTok.Type != token.EOF {
			if p.curTok.Type == token.IDENT {
				attrName := p.curTok.Literal
				p.nextToken()
				if p.curTok.Type == token.LPAREN {
					p.nextToken() // consume '('
					var parts []string
					for p.curTok.Type != token.RPAREN && p.curTok.Type != token.EOF {
						parts = append(parts, p.curTok.Literal)
						p.nextToken()
					}
					if p.curTok.Type == token.RPAREN {
						p.nextToken() // consume ')'
					}
					attrName = fmt.Sprintf("%s(%s)", attrName, strings.Join(parts, ""))
				}
				attributes = append(attributes, attrName)
			} else if p.curTok.Type == token.COMMA {
				p.nextToken()
			} else {
				p.nextToken()
			}
		}
		if p.curTok.Type == token.RBRACKET {
			p.nextToken() // consume ']'
		}
	}

	pub := false
	if p.curTok.Type == token.PUB {
		pub = true
		p.nextToken()
	}

	var decl ast.Declaration
	switch p.curTok.Type {
	case token.FN:
		decl = p.parseFnDecl(pub)
	case token.EXTERN:
		p.nextToken() // consume 'extern'
		if p.curTok.Type == token.FN {
			decl = p.parseExternFnDecl()
		} else {
			p.errors = append(p.errors, fmt.Sprintf("expected fn after extern, got %s", p.curTok.Type))
			p.skipUntilNewlineOrBrace()
			return nil
		}
	case token.CONST:
		decl = p.parseConstDecl(pub)
	case token.VAR:
		p.nextToken() // consume 'var'
		decl = p.parseTopLevelVarDecl(pub)
	case token.TYPE:
		decl = p.parseTypeAliasDecl(pub)
	case token.STRUCT:
		decl = p.parseStructDecl(pub)
	case token.ENUM:
		decl = p.parseEnumDecl(pub)
	case token.SERVICE:
		decl = p.parseServiceDecl(pub)
	case token.RULESET:
		decl = p.parseRulesetDecl(pub)
	case token.TEMPLATE:
		decl = p.parseTemplateDecl(pub)
	case token.MODULE:
		p.nextToken() // consume 'module'
		decl = p.parseModuleDecl()
	case token.IMPORT:
		p.nextToken() // consume 'import'
		decl = p.parseImportDecl()
	case token.NEWLINE, token.COMMENT, token.DOC:
		p.nextToken()
		return nil
	default:
		p.errors = append(p.errors, fmt.Sprintf("unexpected token %s (%q) at %d:%d",
			p.curTok.Type, p.curTok.Literal, p.curTok.Line, p.curTok.Column))
		p.nextToken()
		return nil
	}

	if len(attributes) > 0 && decl != nil {
		if fd, ok := decl.(*ast.FnDecl); ok {
			fd.Attributes = attributes
		} else if sd, ok := decl.(*ast.StructDecl); ok {
			sd.Attributes = attributes
		}
	}
	return decl
}

// skipUntilNewlineOrBrace consumes tokens until a newline, right brace, or EOF.
func (p *Parser) skipUntilNewlineOrBrace() {
	for p.curTok.Type != token.EOF && p.curTok.Type != token.NEWLINE && p.curTok.Type != token.RBRACE {
		p.nextToken()
	}
}

// parseTypeParamList parses generic type parameters in angle brackets:
//
//	<T, U: Comparable>
//
// It returns the parameter names and their optional bounds.
func (p *Parser) parseTypeParamList() ([]string, []ast.Type) {
	var tparams []string
	var bounds []ast.Type
	if p.curTok.Type != token.LT {
		return tparams, bounds
	}
	p.nextToken() // consume '<'
	for {
		if p.curTok.Type != token.IDENT {
			p.peekError(token.IDENT)
			return nil, nil
		}
		tparams = append(tparams, p.curTok.Literal)
		p.nextToken()
		if p.curTok.Type == token.COLON {
			p.nextToken() // consume ':'
			bounds = append(bounds, p.parseType())
		} else {
			bounds = append(bounds, nil)
		}
		if p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.GT {
			p.nextToken()
			break
		}
		p.peekError(token.GT)
		return nil, nil
	}
	return tparams, bounds
}

// parseFnDecl parses a function declaration after the 'fn' keyword.
// It handles generic parameters, receiver methods, parameters, return type,
// and the function body.
func (p *Parser) parseFnDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'fn'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()
	for p.curTok.Type == token.DOT {
		p.nextToken()
		if p.curTok.Type != token.IDENT {
			p.peekError(token.IDENT)
			return nil
		}
		name = fmt.Sprintf("%s.%s", name, p.curTok.Literal)
		p.nextToken()
	}

	var typeParams []string
	var typeParamBounds []ast.Type
	if p.curTok.Type == token.LT {
		typeParams, typeParamBounds = p.parseTypeParamList()
	}

	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	params := p.parseParamList()

	if p.curTok.Type != token.RPAREN {
		p.peekError(token.RPAREN)
		return nil
	}
	p.nextToken() // consume ')'

	var retType ast.Type
	if p.curTok.Type == token.ARROW {
		p.nextToken() // consume '->'
		if p.curTok.Type == token.LPAREN {
			// Multiple return types: -> (int, int)
			p.nextToken() // consume '('
			var types []ast.Type
			types = append(types, p.parseType())
			for p.curTok.Type == token.COMMA {
				p.nextToken() // consume ','
				types = append(types, p.parseType())
			}
			if p.curTok.Type == token.RPAREN {
				p.nextToken() // consume ')'
			} else {
				p.errors = append(p.errors, fmt.Sprintf("expected ')', got %s", p.curTok.Type))
			}
			retType = &ast.TupleType{Token: p.curTok, Types: types}
		} else {
			retType = p.parseType()
		}
	}

	if p.curTok.Type != token.LBRACE {
		// Bodyless function declaration (e.g. service interface method).
		for p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
		return &ast.FnDecl{
			Token:           tok,
			Pub:             pub,
			Name:            name,
			TypeParams:      typeParams,
			TypeParamBounds: typeParamBounds,
			Params:          params,
			ReturnType:      retType,
			Body:            nil,
		}
	}
	body := p.parseBlockStmt()

	return &ast.FnDecl{
		Token:           tok,
		Pub:             pub,
		Name:            name,
		TypeParams:      typeParams,
		TypeParamBounds: typeParamBounds,
		Params:          params,
		ReturnType:      retType,
		Body:            body,
	}
}

// parseExternFnDecl parses an extern function declaration:
//
//	extern fn name(params) -> ret
//
// These functions have no body; they declare a symbol imported from C.
func (p *Parser) parseExternFnDecl() ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'fn'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	params := p.parseParamList()

	if p.curTok.Type != token.RPAREN {
		p.peekError(token.RPAREN)
		return nil
	}
	p.nextToken() // consume ')'

	var retType ast.Type
	if p.curTok.Type == token.ARROW {
		p.nextToken() // consume '->'
		retType = p.parseType()
	}

	// Optional newline after declaration.
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}

	return &ast.ExternFnDecl{
		Token:      tok,
		Name:       name,
		Params:     params,
		ReturnType: retType,
	}
}

// parseParamList parses a comma-separated parameter list inside parentheses.
// Each parameter may have a name, type annotation, and default value.
func (p *Parser) parseParamList() []*ast.Param {
	var params []*ast.Param
	if p.curTok.Type == token.RPAREN {
		return params
	}
	for {
		if p.curTok.Type != token.IDENT {
			p.peekError(token.IDENT)
			return nil
		}
		name := p.curTok.Literal
		p.nextToken()
		if p.curTok.Type != token.COLON {
			p.peekError(token.COLON)
			return nil
		}
		p.nextToken()
		variadic := false
		if p.curTok.Type == token.ELLIPSIS {
			variadic = true
			p.nextToken() // consume '...'
		}
		typ := p.parseType()
		params = append(params, &ast.Param{Name: name, Type: typ, Variadic: variadic})
		if p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.RPAREN {
			break
		}
		p.peekError(token.RPAREN)
		return nil
	}
	return params
}

// parseType parses any Skink type expression.
// This includes primitives, named types, arrays, pointers, channels,
// sets, tensors, tuples, function types, and generic instantiations.
func (p *Parser) parseType() ast.Type {
	switch p.curTok.Type {
	case token.IDENT:
		t := &ast.NamedType{Token: p.curTok, Name: p.curTok.Literal}
		p.nextToken()
		// Module-qualified type: pkg.Type
		for p.curTok.Type == token.DOT {
			p.nextToken() // consume '.'
			if p.curTok.Type != token.IDENT {
				p.peekError(token.IDENT)
				return t
			}
			t.Name = t.Name + "." + p.curTok.Literal
			p.nextToken()
		}
		// Generic instantiation: Foo<int, string>
		if p.curTok.Type == token.LT {
			p.nextToken() // consume '<'
			for {
				arg := p.parseType()
				t.Args = append(t.Args, arg)
				if p.curTok.Type == token.COMMA {
					p.nextToken()
					continue
				}
				if p.curTok.Type == token.GT {
					p.nextToken()
					break
				}
				p.peekError(token.GT)
				return t
			}
		}
		return t
	case token.INT, token.INT8, token.INT16, token.INT32, token.INT64,
		token.UINT, token.UINT8, token.UINT16, token.UINT32, token.UINT64,
		token.FLOAT, token.BOOL, token.STRING_TYPE, token.BYTES_TYPE,
		token.VOID, token.MAP, token.CHAN, token.SET, token.TENSOR, token.ERROR:
		name := strings.ToLower(string(p.curTok.Type))
		// Fix token type names that don't map directly.
		switch p.curTok.Type {
		case token.STRING_TYPE:
			name = "string"
		case token.BYTES_TYPE:
			name = "bytes"
		}
		// Handle map[KeyType]ValueType syntax.
		if p.curTok.Type == token.MAP && p.peekTok.Type == token.LBRACKET {
			tok := p.curTok
			p.nextToken() // consume 'map'
			p.nextToken() // consume '['
			key := p.parseType()
			if p.curTok.Type != token.RBRACKET {
				p.peekError(token.RBRACKET)
				return nil
			}
			p.nextToken() // consume ']'
			elem := p.parseType()
			return &ast.MapType{Token: tok, Key: key, Elem: elem}
		}
		// Handle set<ElementType> syntax.
		if p.curTok.Type == token.SET && p.peekTok.Type == token.LT {
			tok := p.curTok
			p.nextToken() // consume 'set'
			p.nextToken() // consume '<'
			elem := p.parseType()
			if p.curTok.Type == token.GT {
				p.nextToken()
			}
			return &ast.SetType{Token: tok, Elem: elem}
		}
		// Handle chan<ElementType> syntax.
		if p.curTok.Type == token.CHAN && p.peekTok.Type == token.LT {
			tok := p.curTok
			p.nextToken() // consume 'chan'
			p.nextToken() // consume '<'
			elem := p.parseType()
			if p.curTok.Type == token.GT {
				p.nextToken()
			}
			return &ast.ChanType{Token: tok, Elem: elem}
		}
		// Handle tensor<ElementType> syntax.
		if p.curTok.Type == token.TENSOR && p.peekTok.Type == token.LT {
			tok := p.curTok
			p.nextToken() // consume 'tensor'
			p.nextToken() // consume '<'
			elem := p.parseType()
			if p.curTok.Type == token.GT {
				p.nextToken()
			}
			return &ast.TensorType{Token: tok, Elem: elem}
		}
		t := &ast.NamedType{Token: p.curTok, Name: name}
		p.nextToken()
		return t
	case token.STAR:
		tok := p.curTok
		p.nextToken()
		elem := p.parseType()
		return &ast.PointerType{Token: tok, Elem: elem}
	case token.LBRACKET:
		tok := p.curTok
		p.nextToken()
		if p.curTok.Type != token.RBRACKET {
			p.peekError(token.RBRACKET)
			return nil
		}
		p.nextToken()
		elem := p.parseType()
		return &ast.ArrayType{Token: tok, Elem: elem}
	case token.FN:
		return p.parseFunctionType()
	default:
		p.errors = append(p.errors, fmt.Sprintf("expected type, got %s at %d:%d",
			p.curTok.Type, p.curTok.Line, p.curTok.Column))
		p.nextToken()
		return nil
	}
}

// parseFunctionType parses a function type literal:
//
//	fn(int, int) -> int
func (p *Parser) parseFunctionType() ast.Type {
	tok := p.curTok
	p.nextToken() // consume 'fn'
	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	var paramTypes []ast.Type
	if p.curTok.Type != token.RPAREN {
		paramTypes = append(paramTypes, p.parseType())
		for p.curTok.Type == token.COMMA {
			p.nextToken() // consume ','
			paramTypes = append(paramTypes, p.parseType())
		}
	}
	if p.curTok.Type != token.RPAREN {
		p.peekError(token.RPAREN)
		return nil
	}
	p.nextToken() // consume ')'
	var retType ast.Type
	if p.curTok.Type == token.ARROW {
		p.nextToken() // consume '->'
		retType = p.parseType()
	}
	return &ast.FunctionType{Token: tok, ParamTypes: paramTypes, ReturnType: retType}
}

// parseBlockStmt parses a braced block of statements.
func (p *Parser) parseBlockStmt() *ast.BlockStmt {
	block := &ast.BlockStmt{Token: p.curTok}
	p.nextToken() // consume '{'
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		stmt := p.parseStatement()
		if stmt != nil {
			block.Statements = append(block.Statements, stmt)
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}
	return block
}

// parseStatement parses a single statement.
// It uses the current token to dispatch to the correct statement parser.
func (p *Parser) parseStatement() ast.Statement {
	switch p.curTok.Type {
	case token.IDENT:
		if p.peekTok.Type == token.COLONASS {
			return p.parseVarStmt()
		}
		if p.isAssignOp(p.peekTok.Type) {
			return p.parseAssignmentStmt()
		}
		// Could be a, b = foo() (tuple assignment) or a, b := 1, 2 (tuple var)
		if p.peekTok.Type == token.COMMA {
			// Parse comma-separated identifiers first.
			var names []string
			names = append(names, p.curTok.Literal)
			p.nextToken() // consume first ident
			for p.curTok.Type == token.COMMA {
				p.nextToken() // consume ','
				if p.curTok.Type != token.IDENT {
					p.errors = append(p.errors, fmt.Sprintf("expected identifier, got %s", p.curTok.Type))
					return nil
				}
				names = append(names, p.curTok.Literal)
				p.nextToken() // consume ident
			}
			// Now curTok is the operator.
			if p.curTok.Type == token.COLONASS {
				return p.finishTupleVarStmt(names)
			}
			if p.isAssignOp(p.curTok.Type) {
				return p.finishTupleAssignmentStmtForNames(names)
			}
			p.errors = append(p.errors, fmt.Sprintf("expected := or = after identifiers, got %s", p.curTok.Type))
			return nil
		}
		// Could be p.x = 42 or arr[0] = 1 (field/index assignment)
		if p.peekTok.Type == token.DOT || p.peekTok.Type == token.LBRACKET {
			lvalue := p.parseLValue()
			if p.isAssignOp(p.curTok.Type) {
				return p.finishAssignmentStmt(lvalue)
			}
			// Not an assignment — continue parsing as expression statement
			expr := p.parseExpressionFromLeft(lvalue, LOWEST)
			if p.curTok.Type == token.NEWLINE {
				p.nextToken()
			}
			return &ast.ExprStmt{Token: p.curTok, Expr: expr}
		}
		return p.parseExprStmt()
	case token.RETURN:
		return p.parseReturnStmt()
	case token.BREAK:
		return p.parseBreakStmt()
	case token.CONTINUE:
		return p.parseContinueStmt()
	case token.IF:
		return p.parseIfStmt()
	case token.FOR:
		return p.parseForStmt()
	case token.WHILE:
		return p.parseWhileStmt()
	case token.UNTIL:
		return p.parseUntilStmt()
	case token.DEFER:
		return p.parseDeferStmt()
	case token.UNSAFE:
		return p.parseUnsafeStmt()
	case token.WITH:
		return p.parseWithStmt()
	case token.MATCH:
		return &ast.ExprStmt{Expr: p.parseMatchExpr()}
	case token.COMPTIME:
		return p.parseComptimeStmt()
	case token.SPAWN:
		return p.parseSpawnStmt()
	case token.SELECT:
		return p.parseSelectStmt()
	case token.SWITCH:
		return p.parseSwitchStmt()
	case token.VAR:
		p.nextToken() // consume 'var'
		if p.curTok.Type == token.LBRACE {
			return p.parseVarBlockStmt()
		}
		return p.parseVarDecl()
	case token.LBRACE:
		return p.parseBlockStmt()
	case token.NEWLINE, token.COMMENT, token.DOC:
		p.nextToken()
		return nil
	default:
		return p.parseExprStmt()
	}
}

// isAssignOp reports whether t is an assignment or compound assignment operator.
func (p *Parser) isAssignOp(t token.Type) bool {
	switch t {
	case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ,
		token.PERCEQ, token.AMPEQ, token.PIPEEQ, token.CARETEQ, token.LSHIFTEQ, token.RSHIFTEQ:
		return true
	}
	return false
}

// parseVarStmt parses a short variable declaration: ident := expr.
func (p *Parser) parseVarStmt() ast.Statement {
	tok := p.curTok
	name := p.curTok.Literal
	p.nextToken() // consume ident
	p.nextToken() // consume :=
	value := p.parseExpression(LOWEST)
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.VarStmt{Token: tok, Name: name, Value: value, Implicit: true}
}

// parseVarDecl parses a single var declaration: x = expr or x: int = expr
// The 'var' keyword has already been consumed.
func (p *Parser) parseVarDecl() ast.Statement {
	tok := p.curTok
	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	name := p.curTok.Literal
	p.nextToken() // consume ident

	var varType ast.Type
	if p.curTok.Type == token.COLON {
		p.nextToken() // consume ':'
		varType = p.parseType()
	} else if isTypeStart(p.curTok.Type) {
		varType = p.parseType()
	}

	var value ast.Expression
	if p.curTok.Type == token.ASSIGN {
		p.nextToken() // consume '='
		value = p.parseExpression(LOWEST)
	}

	if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
		p.nextToken()
	}
	return &ast.VarStmt{Token: tok, Name: name, Type: varType, Value: value, Implicit: false}
}

// parseTopLevelVarDecl parses a module-level var declaration.
func (p *Parser) parseTopLevelVarDecl(pub bool) ast.Declaration {
	tok := p.curTok
	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	name := p.curTok.Literal
	p.nextToken() // consume ident

	var varType ast.Type
	if p.curTok.Type == token.COLON {
		p.nextToken() // consume ':'
		varType = p.parseType()
	} else if p.curTok.Type == token.LBRACKET || p.curTok.Type == token.STAR || p.curTok.Type == token.IDENT || p.curTok.Type == token.SET || p.curTok.Type == token.CHAN {
		varType = p.parseType()
	}

	var value ast.Expression
	if p.curTok.Type == token.ASSIGN {
		p.nextToken() // consume '='
		value = p.parseExpression(LOWEST)
	}

	if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
		p.nextToken()
	}
	return &ast.VarDecl{Token: tok, Pub: pub, Name: name, Type: varType, Value: value}
}

// parseVarBlockStmt parses a var block: { a = 1, b = 2 }
// The 'var' keyword has already been consumed, and curTok is '{'.
func (p *Parser) parseVarBlockStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume '{'
	var decls []*ast.VarStmt
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}
		decl := p.parseVarDecl()
		if decl != nil {
			if vs, ok := decl.(*ast.VarStmt); ok {
				decls = append(decls, vs)
			}
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}
	return &ast.VarBlockStmt{Token: tok, Decls: decls}
}

// parseExprStmt parses a statement that is a bare expression.
func (p *Parser) parseExprStmt() ast.Statement {
	expr := p.parseExpression(LOWEST)
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.ExprStmt{Token: p.curTok, Expr: expr}
}

// parseReturnStmt parses a return statement with optional values.
func (p *Parser) parseReturnStmt() ast.Statement {
	tok := p.curTok
	p.nextToken()
	var values []ast.Expression
	if p.curTok.Type != token.NEWLINE && p.curTok.Type != token.RBRACE &&
		p.curTok.Type != token.CASE && p.curTok.Type != token.DEFAULT {
		values = append(values, p.parseExpression(LOWEST))
		for p.curTok.Type == token.COMMA {
			p.nextToken()
			values = append(values, p.parseExpression(LOWEST))
		}
	}
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.ReturnStmt{Token: tok, Values: values}
}

// parseBreakStmt parses a break statement.
func (p *Parser) parseBreakStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'break'
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.BreakStmt{Token: tok}
}

// parseContinueStmt parses a continue statement.
func (p *Parser) parseContinueStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'continue'
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.ContinueStmt{Token: tok}
}

// parseIfStmt parses an if statement with optional else.
func (p *Parser) parseIfStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'if'
	cond := p.parseExpression(LOWEST)
	conseq := p.parseBlockStmt()
	var alt ast.Statement
	if p.curTok.Type == token.ELSE {
		p.nextToken()
		if p.curTok.Type == token.IF {
			alt = p.parseIfStmt()
		} else {
			alt = p.parseBlockStmt()
		}
	}
	return &ast.IfStmt{Token: tok, Condition: cond, Consequence: conseq, Alternative: alt}
}

// parseIfExpr parses an if expression (returns a value).
func (p *Parser) parseIfExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'if'
	cond := p.parseExpression(LOWEST)
	conseq := p.parseBlockStmt()
	var alt *ast.BlockStmt
	if p.curTok.Type == token.ELSE {
		p.nextToken()
		if p.curTok.Type == token.LBRACE {
			alt = p.parseBlockStmt()
		} else {
			p.errors = append(p.errors, fmt.Sprintf("expected { after else in if expression, got %s", p.curTok.Type))
			return nil
		}
	} else {
		p.errors = append(p.errors, "if expression requires an else branch")
		return nil
	}
	return &ast.IfExpr{Token: tok, Condition: cond, Consequence: conseq, Alternative: alt}
}

// parseAssignmentStmt parses an assignment or compound-assignment statement.
func (p *Parser) parseAssignmentStmt() ast.Statement {
	lvalue := p.parseLValue()
	if lvalue == nil {
		return nil
	}
	return p.finishAssignmentStmt(lvalue)
}

// finishTupleVarStmt parses a, b := expr
func (p *Parser) finishTupleVarStmt(names []string) ast.Statement {
	if p.curTok.Type != token.COLONASS {
		p.errors = append(p.errors, fmt.Sprintf("expected :=, got %s", p.curTok.Type))
		return nil
	}
	p.nextToken() // consume :=
	value := p.parseExpression(LOWEST)
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.TupleVarStmt{
		Token:    p.curTok,
		Names:    names,
		Value:    value,
		Implicit: true,
	}
}

// finishTupleAssignmentStmtForNames parses a, b = expr from already-collected names.
func (p *Parser) finishTupleAssignmentStmtForNames(names []string) ast.Statement {
	if !p.isAssignOp(p.curTok.Type) {
		p.errors = append(p.errors, fmt.Sprintf("expected assignment operator, got %s at %d:%d",
			p.curTok.Type, p.curTok.Line, p.curTok.Column))
		return nil
	}
	operator := p.curTok.Literal
	p.nextToken()
	value := p.parseExpression(LOWEST)
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	var lvalues []ast.Expression
	for _, name := range names {
		lvalues = append(lvalues, &ast.Identifier{Token: p.curTok, Value: name})
	}
	return &ast.TupleAssignmentStmt{
		Token:    p.curTok,
		LValues:  lvalues,
		Operator: operator,
		Value:    value,
	}
}

// finishAssignmentStmt parses the right-hand side of an assignment after the operator.
func (p *Parser) finishAssignmentStmt(lvalue ast.Expression) ast.Statement {
	if !p.isAssignOp(p.curTok.Type) {
		p.errors = append(p.errors, fmt.Sprintf("expected assignment operator, got %s at %d:%d",
			p.curTok.Type, p.curTok.Line, p.curTok.Column))
		return nil
	}
	operator := p.curTok.Literal
	p.nextToken()
	value := p.parseExpression(LOWEST)
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.AssignmentStmt{
		Token:    p.curTok,
		LValue:   lvalue,
		Operator: operator,
		Value:    value,
	}
}

// parseLValue parses the left-hand side of an assignment.
func (p *Parser) parseLValue() ast.Expression {
	// Simple lvalue: identifier, identifier[index], identifier.field, *expr
	switch p.curTok.Type {
	case token.IDENT:
		var expr ast.Expression = &ast.Identifier{Token: p.curTok, Value: p.curTok.Literal}
		p.nextToken()
		for p.curTok.Type == token.DOT || p.curTok.Type == token.LBRACKET {
			if p.curTok.Type == token.DOT {
				p.nextToken() // consume '.'
				if p.curTok.Type != token.IDENT {
					p.peekError(token.IDENT)
					return nil
				}
				expr = &ast.FieldAccessExpr{
					Token: p.curTok,
					Left:  expr,
					Field: p.curTok.Literal,
				}
				p.nextToken()
			} else if p.curTok.Type == token.LBRACKET {
				p.nextToken() // consume '['
				var index ast.Expression
				// Handle ^n from-end index: arr[^1]
				if p.curTok.Type == token.CARET {
					p.nextToken() // consume '^'
					index = &ast.FromEndIndexExpr{Token: p.curTok, Operand: p.parseExpression(LOWEST)}
				} else {
					index = p.parseExpression(LOWEST)
				}
				if p.curTok.Type == token.RBRACKET {
					p.nextToken()
				} else {
					p.peekError(token.RBRACKET)
					return nil
				}
				expr = &ast.IndexExpr{
					Token: p.curTok,
					Left:  expr,
					Index: index,
				}
			}
		}
		return expr
	case token.STAR:
		p.nextToken()
		return &ast.PrefixExpr{
			Token:    p.curTok,
			Operator: "*",
			Right:    p.parseExpression(LOWEST),
		}
	default:
		p.errors = append(p.errors, fmt.Sprintf("unexpected lvalue token %s at %d:%d",
			p.curTok.Type, p.curTok.Line, p.curTok.Column))
		return nil
	}
}

// parseConstDecl parses a const declaration or const block.
func (p *Parser) parseConstDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'const'

	// const { A = 1, B = 2 }
	if p.curTok.Type == token.LBRACE {
		p.nextToken() // consume '{'
		var decls []*ast.ConstDecl
		for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
			if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
				p.nextToken()
				continue
			}
			if p.curTok.Type != token.IDENT {
				p.peekError(token.IDENT)
				p.skipUntilNewlineOrBrace()
				return nil
			}
			name := p.curTok.Literal
			p.nextToken()
			if p.curTok.Type != token.ASSIGN {
				p.peekError(token.ASSIGN)
				p.skipUntilNewlineOrBrace()
				return nil
			}
			p.nextToken() // consume '='
			value := p.parseExpression(LOWEST)
			decls = append(decls, &ast.ConstDecl{Token: tok, Pub: pub, Name: name, Value: value})
			// Skip optional comma/newline
			if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
				p.nextToken()
			}
		}
		if p.curTok.Type == token.RBRACE {
			p.nextToken()
		}
		return &ast.ConstBlockDecl{Token: tok, Decls: decls}
	}

	// Single const declaration: const A = 1
	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type != token.ASSIGN {
		p.peekError(token.ASSIGN)
		return nil
	}
	p.nextToken() // consume '='

	value := p.parseExpression(LOWEST)
	if p.curTok.Type == token.NEWLINE {
		p.nextToken()
	}
	return &ast.ConstDecl{
		Token: tok,
		Pub:   pub,
		Name:  name,
		Value: value,
	}
}

// parseTypeAliasDecl parses a type alias declaration: type Name = TypeExpr
func (p *Parser) parseTypeAliasDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'type'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	name := p.curTok.Literal
	p.nextToken() // consume name

	if p.curTok.Type != token.ASSIGN {
		p.peekError(token.ASSIGN)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	p.nextToken() // consume '='

	aliasType := p.parseType()
	if aliasType == nil {
		p.errors = append(p.errors, fmt.Sprintf("expected type expression after '=' in type alias %q", name))
		return nil
	}

	return &ast.TypeAliasDecl{
		Token: tok,
		Pub:   pub,
		Name:  name,
		Type:  aliasType,
	}
}

// parseStructDecl parses a struct declaration with fields and methods.
func (p *Parser) parseStructDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'struct'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	var typeParams []string
	var typeParamBounds []ast.Type
	if p.curTok.Type == token.LT {
		typeParams, typeParamBounds = p.parseTypeParamList()
	}

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'

	var fields []*ast.FieldDecl
	var methods []*ast.FnDecl
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC {
			p.nextToken()
			continue
		}

		if p.curTok.Type == token.FN || (p.curTok.Type == token.PUB && p.peekTok.Type == token.FN) {
			fnPub := false
			if p.curTok.Type == token.PUB {
				fnPub = true
				p.nextToken() // consume 'pub'
			}
			decl := p.parseFnDecl(fnPub)
			if fn, ok := decl.(*ast.FnDecl); ok {
				methods = append(methods, fn)
			}
			continue
		}

		if p.curTok.Type != token.IDENT {
			p.peekError(token.IDENT)
			return nil
		}
		fieldTok := p.curTok
		fieldName := p.curTok.Literal
		p.nextToken()
		if p.curTok.Type == token.COLON {
			// Regular named field: name: Type
			p.nextToken() // consume ':'
			fieldType := p.parseType()
			// Optional bit width for bitfields: field: type : N
			var bitWidth *int
			if p.curTok.Type == token.COLON {
				p.nextToken() // consume ':'
				if p.curTok.Type != token.INT {
					p.peekError(token.INT)
					return nil
				}
				bw := int(p.curTok.Literal[0] - '0')
				// Handle multi-digit literals.
				for i := 1; i < len(p.curTok.Literal); i++ {
					bw = bw*10 + int(p.curTok.Literal[i]-'0')
				}
				bitWidth = &bw
				p.nextToken()
			}
			fields = append(fields, &ast.FieldDecl{
				Token:    fieldTok,
				Name:     fieldName,
				Type:     fieldType,
				BitWidth: bitWidth,
			})
		} else {
			// Embedded (anonymous) field: just the type name.
			fields = append(fields, &ast.FieldDecl{
				Token:    fieldTok,
				Name:     fieldName,
				Type:     &ast.NamedType{Token: fieldTok, Name: fieldName},
				Embedded: true,
			})
		}
		// optional comma
		if p.curTok.Type == token.COMMA {
			p.nextToken()
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.StructDecl{
		Token:           tok,
		Pub:             pub,
		Name:            name,
		TypeParams:      typeParams,
		TypeParamBounds: typeParamBounds,
		Fields:          fields,
		Methods:         methods,
	}
}

// parseServiceDecl parses a service interface declaration or implementation.
// Interface: service Name { fn ... }
// Implementation: service Name for Type { fn ... }
func (p *Parser) parseServiceDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'service'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	forType := ""
	if p.curTok.Type == token.FOR {
		p.nextToken() // consume 'for'
		if p.curTok.Type != token.IDENT {
			p.peekError(token.IDENT)
			return nil
		}
		forType = p.curTok.Literal
		p.nextToken()
	}

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'

	var methods []*ast.FnDecl
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		if p.curTok.Type != token.FN && p.curTok.Type != token.PUB {
			p.peekError(token.FN)
			return nil
		}
		var fnPub bool
		if p.curTok.Type == token.PUB {
			fnPub = true
			p.nextToken()
		}
		if p.curTok.Type != token.FN {
			p.peekError(token.FN)
			return nil
		}
		decl := p.parseFnDecl(fnPub)
		if fn, ok := decl.(*ast.FnDecl); ok {
			methods = append(methods, fn)
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.ServiceDecl{
		Token:   tok,
		Pub:     pub,
		Name:    name,
		ForType: forType,
		Methods: methods,
	}
}

// parseRulesetDecl parses a ruleset declaration containing rules.
func (p *Parser) parseRulesetDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'ruleset'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'

	var rules []*ast.RuleDecl
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		if p.curTok.Type != token.RULE {
			p.peekError(token.RULE)
			return nil
		}
		rule := p.parseRuleDecl()
		if rule != nil {
			rules = append(rules, rule)
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.RulesetDecl{
		Token: tok,
		Pub:   pub,
		Name:  name,
		Rules: rules,
	}
}

// parseTemplateDecl parses a template (duck-typed trait) declaration.
func (p *Parser) parseTemplateDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'template'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	var typeParams []string
	if p.curTok.Type == token.LT {
		typeParams, _ = p.parseTypeParamList()
	}

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'

	var methods []*ast.FnDecl
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC || p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		if p.curTok.Type != token.FN {
			p.peekError(token.FN)
			p.skipUntilNewlineOrBrace()
			continue
		}
		fnSig := p.parseFnSignature()
		if fnSig != nil {
			methods = append(methods, fnSig)
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.TemplateDecl{
		Token:      tok,
		Pub:        pub,
		Name:       name,
		TypeParams: typeParams,
		Methods:    methods,
	}
}

// parseFnSignature parses a function signature without a body (used in templates).
func (p *Parser) parseFnSignature() *ast.FnDecl {
	tok := p.curTok
	p.nextToken() // consume 'fn'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	var typeParams []string
	if p.curTok.Type == token.LT {
		typeParams, _ = p.parseTypeParamList()
	}

	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	params := p.parseParamList()

	if p.curTok.Type != token.RPAREN {
		p.peekError(token.RPAREN)
		return nil
	}
	p.nextToken() // consume ')'

	var retType ast.Type
	if p.curTok.Type == token.ARROW {
		p.nextToken() // consume '->'
		if p.curTok.Type == token.LPAREN {
			p.nextToken() // consume '('
			var types []ast.Type
			types = append(types, p.parseType())
			for p.curTok.Type == token.COMMA {
				p.nextToken()
				types = append(types, p.parseType())
			}
			if p.curTok.Type == token.RPAREN {
				p.nextToken()
			}
			retType = &ast.TupleType{Token: p.curTok, Types: types}
		} else {
			retType = p.parseType()
		}
	}

	return &ast.FnDecl{
		Token:      tok,
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		ReturnType: retType,
	}
}

// parseRuleDecl parses a single rule inside a ruleset.
func (p *Parser) parseRuleDecl() *ast.RuleDecl {
	tok := p.curTok
	p.nextToken() // consume 'rule'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type != token.WHEN {
		p.peekError(token.WHEN)
		return nil
	}
	p.nextToken() // consume 'when'

	condition := p.parseExpression(LOWEST)

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'

	// Parse action block.
	body := &ast.BlockStmt{Token: tok, Statements: []ast.Statement{}}
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		// Handle priority: N as a special statement inside rule body.
		if (p.curTok.Type == token.PRIORITY || (p.curTok.Type == token.IDENT && p.curTok.Literal == "priority")) && p.peekTok.Type == token.COLON {
			p.nextToken() // consume 'priority'
			p.nextToken() // consume ':'
			if p.curTok.Type != token.INT {
				p.peekError(token.INT)
				return nil
			}
			// Skip priority for now.
			p.nextToken()
			continue
		}
		// Handle action: label.
		if (p.curTok.Type == token.ACTION || (p.curTok.Type == token.IDENT && p.curTok.Literal == "action")) && p.peekTok.Type == token.COLON {
			p.nextToken() // consume 'action'
			p.nextToken() // consume ':'
			// Expect opening brace for action block.
			if p.curTok.Type == token.LBRACE {
				p.nextToken() // consume '{'
			}
			// Parse the statements in the action block.
			for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
				if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC {
					p.nextToken()
					continue
				}
				if p.curTok.Type == token.NEWLINE {
					p.nextToken()
					continue
				}
				stmt := p.parseStatement()
				if stmt != nil {
					body.Statements = append(body.Statements, stmt)
				}
			}
			if p.curTok.Type == token.RBRACE {
				p.nextToken() // consume closing '}' of action block
			}
			continue
		}
		stmt := p.parseStatement()
		if stmt != nil {
			body.Statements = append(body.Statements, stmt)
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.RuleDecl{
		Token:     tok,
		Name:      name,
		Condition: condition,
		Action:    body,
		Priority:  0,
	}
}

// parseEnumDecl parses an enum declaration.
func (p *Parser) parseEnumDecl(pub bool) ast.Declaration {
	tok := p.curTok
	p.nextToken() // consume 'enum'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	name := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'

	var variants []string
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.COMMENT || p.curTok.Type == token.DOC || p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		if p.curTok.Type != token.IDENT {
			p.peekError(token.IDENT)
			p.skipUntilNewlineOrBrace()
			return nil
		}
		variants = append(variants, p.curTok.Literal)
		p.nextToken()
		// optional comma
		if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.EnumDecl{
		Token:    tok,
		Pub:      pub,
		Name:     name,
		Variants: variants,
	}
}

// parseModuleDecl parses a module declaration.
func (p *Parser) parseModuleDecl() ast.Declaration {
	tok := p.curTok
	name := ""
	switch p.curTok.Type {
	case token.IDENT, token.TENSOR, token.CHAN, token.SET, token.ERROR:
		name = p.curTok.Literal
	case token.INT, token.INT8, token.INT16, token.INT32, token.INT64,
		token.UINT, token.UINT8, token.UINT16, token.UINT32, token.UINT64,
		token.FLOAT, token.BOOL, token.STRING_TYPE, token.BYTES_TYPE,
		token.VOID:
		// Reject numeric literals (e.g., 123); accept keywords like "int", "float".
		if _, err := strconv.ParseInt(p.curTok.Literal, 0, 64); err == nil {
			p.peekError(token.IDENT)
			p.skipUntilNewlineOrBrace()
			return nil
		}
		if _, err := strconv.ParseFloat(p.curTok.Literal, 64); err == nil {
			p.peekError(token.IDENT)
			p.skipUntilNewlineOrBrace()
			return nil
		}
		name = p.curTok.Literal
	default:
		p.peekError(token.IDENT)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	p.nextToken()
	return &ast.ModuleDecl{Token: tok, Name: name}
}

// parseImportDecl parses a single import or an import block.
//
//	import "path"
//	import alias "path"          // legacy Go-style alias
//	import "path" as alias       // Python-style alias
func (p *Parser) parseImportDecl() ast.Declaration {
	tok := p.curTok
	// Import block: import { "a", "b" }
	if p.curTok.Type == token.LBRACE {
		return p.parseImportBlock(tok)
	}
	var alias string
	// Legacy Go-style alias: import alias "path"
	if p.curTok.Type == token.IDENT && p.peekTok.Type == token.STRING {
		alias = p.curTok.Literal
		p.nextToken() // consume alias
	}
	if p.curTok.Type != token.STRING {
		p.peekError(token.STRING)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	path := p.curTok.Literal
	p.nextToken() // consume string
	// Python-style alias: import "path" as alias
	if alias == "" && p.curTok.Type == token.AS && p.peekTok.Type == token.IDENT {
		p.nextToken() // consume as
		alias = p.curTok.Literal
		p.nextToken() // consume alias
	}
	if alias == "" {
		// Use last path component as alias
		parts := strings.Split(path, "/")
		alias = parts[len(parts)-1]
	}
	return &ast.ImportDecl{Token: tok, Path: path, Alias: alias}
}

// parseImportBlock parses a braced block of import declarations.
//
//	import { "a", "b" }
//	import { alias "a" }
//	import { "a" as alias, "b" as b2 }
func (p *Parser) parseImportBlock(tok token.Token) ast.Declaration {
	p.nextToken() // consume '{'
	var decls []*ast.ImportDecl
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		var alias string
		// Legacy Go-style alias: import { alias "path" }
		if p.curTok.Type == token.IDENT && p.peekTok.Type == token.STRING {
			alias = p.curTok.Literal
			p.nextToken() // consume alias
		}
		if p.curTok.Type != token.STRING {
			p.peekError(token.STRING)
			p.skipUntilNewlineOrBrace()
			return &ast.ImportBlockDecl{Token: tok, Decls: decls}
		}
		path := p.curTok.Literal
		p.nextToken() // consume string
		// Python-style alias: import { "path" as alias }
		if alias == "" && p.curTok.Type == token.AS && p.peekTok.Type == token.IDENT {
			p.nextToken() // consume as
			alias = p.curTok.Literal
			p.nextToken() // consume alias
		}
		if alias == "" {
			parts := strings.Split(path, "/")
			alias = parts[len(parts)-1]
		}
		decls = append(decls, &ast.ImportDecl{Token: tok, Path: path, Alias: alias})
		if p.curTok.Type == token.COMMA {
			p.nextToken()
		}
		// Allow newlines between imports
		for p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken() // consume '}'
	}
	return &ast.ImportBlockDecl{Token: tok, Decls: decls}
}

// parseForStmt parses a for loop (C-style, for-in, infinite, or condition-only).
func (p *Parser) parseForStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'for'

	// Check for 'for ident in expr { }'
	if p.curTok.Type == token.IDENT && p.peekTok.Type == token.IN {
		variable := p.curTok.Literal
		p.nextToken() // consume ident
		p.nextToken() // consume 'in'
		iterable := p.parseExpression(LOWEST)
		body := p.parseBlockStmt()
		return &ast.ForStmt{
			Token: tok,
			Iterator: &ast.ForInStmt{
				Variable: variable,
				Iterable: iterable,
			},
			Body: body,
		}
	}

	// Check for 'for ident := range expr { }'
	if p.curTok.Type == token.IDENT && p.peekTok.Type == token.COLONASS {
		variable := p.curTok.Literal
		p.nextToken() // consume ident
		p.nextToken() // consume :=
		if p.curTok.Type == token.RANGE {
			p.nextToken() // consume 'range'
			iterable := p.parseExpression(LOWEST)
			body := p.parseBlockStmt()
			return &ast.ForStmt{
				Token: tok,
				Iterator: &ast.ForInStmt{
					Variable: variable,
					Iterable: iterable,
					IsRange:  true,
				},
				Body: body,
			}
		}
		// Not a range loop — reconstruct init expression manually.
		value := p.parseExpression(LOWEST)
		init := &ast.VarStmt{
			Token:    token.Token{Type: token.COLONASS, Literal: ":="},
			Name:     variable,
			Value:    value,
			Implicit: true,
		}
		// Continue with C-style parsing.
		if p.curTok.Type != token.SEMICOLON {
			p.errors = append(p.errors, fmt.Sprintf("expected ';' in for loop at %d:%d",
				p.curTok.Line, p.curTok.Column))
			p.skipUntilNewlineOrBrace()
			return nil
		}
		p.nextToken() // consume ';'
		var cond ast.Expression
		if p.curTok.Type != token.SEMICOLON {
			cond = p.parseExpression(LOWEST)
		}
		if p.curTok.Type != token.SEMICOLON {
			p.errors = append(p.errors, fmt.Sprintf("expected second ';' in for loop at %d:%d",
				p.curTok.Line, p.curTok.Column))
			p.skipUntilNewlineOrBrace()
			return nil
		}
		p.nextToken() // consume ';'
		var post ast.Statement
		if p.curTok.Type != token.LBRACE {
			post = p.parseStatement()
		}
		body := p.parseBlockStmt()
		return &ast.ForStmt{
			Token:     tok,
			Init:      init,
			Condition: cond,
			Post:      post,
			Body:      body,
		}
	}

	// Infinite for loop: `for { ... }`
	if p.curTok.Type == token.LBRACE {
		body := p.parseBlockStmt()
		return &ast.ForStmt{Token: tok, Body: body}
	}

	// Condition-only for loop: `for <cond> { ... }`
	if p.curTok.Type != token.SEMICOLON && p.curTok.Type != token.VAR {
		if !(p.curTok.Type == token.IDENT && (p.peekTok.Type == token.COLONASS || p.peekTok.Type == token.ASSIGN)) &&
			p.peekTok.Type != token.IN {
			cond := p.parseExpression(LOWEST)
			if p.curTok.Type != token.LBRACE {
				p.peekError(token.LBRACE)
				p.skipUntilNewlineOrBrace()
				return nil
			}
			body := p.parseBlockStmt()
			return &ast.ForStmt{Token: tok, Condition: cond, Body: body}
		}
	}

	// C-style: for init ; cond ; post { }
	var init ast.Statement
	var cond ast.Expression
	var post ast.Statement

	if p.curTok.Type != token.SEMICOLON {
		// init statement
		if p.curTok.Type == token.IDENT && p.peekTok.Type == token.COLONASS {
			init = p.parseVarStmt()
		} else if p.curTok.Type == token.IDENT && p.isAssignOp(p.peekTok.Type) {
			init = p.parseAssignmentStmt()
		} else {
			init = p.parseExprStmt()
		}
	}
	if p.curTok.Type == token.SEMICOLON {
		p.nextToken() // consume ';'
	} else {
		// Not C-style, maybe just 'for condition { }'
		// But grammar doesn't allow this; treat as error
		p.errors = append(p.errors, fmt.Sprintf("expected ';' in for loop at %d:%d",
			p.curTok.Line, p.curTok.Column))
		p.skipUntilNewlineOrBrace()
		return nil
	}

	if p.curTok.Type != token.SEMICOLON {
		cond = p.parseExpression(LOWEST)
	}
	if p.curTok.Type == token.SEMICOLON {
		p.nextToken() // consume ';'
	} else {
		p.errors = append(p.errors, fmt.Sprintf("expected second ';' in for loop at %d:%d",
			p.curTok.Line, p.curTok.Column))
		p.skipUntilNewlineOrBrace()
		return nil
	}

	if p.curTok.Type != token.LBRACE {
		// post statement (can be assignment, call, etc.)
		post = p.parseStatement()
	}

	body := p.parseBlockStmt()
	return &ast.ForStmt{
		Token:     tok,
		Init:      init,
		Condition: cond,
		Post:      post,
		Body:      body,
	}
}

// parseWhileStmt parses a while loop.
func (p *Parser) parseWhileStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'while'
	cond := p.parseExpression(LOWEST)
	body := p.parseBlockStmt()
	return &ast.WhileStmt{
		Token:     tok,
		Condition: cond,
		Body:      body,
	}
}

// parseUntilStmt parses an until loop.
func (p *Parser) parseUntilStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'until'
	cond := p.parseExpression(LOWEST)
	body := p.parseBlockStmt()
	return &ast.UntilStmt{
		Token:     tok,
		Condition: cond,
		Body:      body,
	}
}

// parseDeferStmt parses a defer statement.
func (p *Parser) parseDeferStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'defer'
	stmt := p.parseStatement()
	return &ast.DeferStmt{Token: tok, Statement: stmt}
}

// parseUnsafeStmt parses an unsafe block statement.
func (p *Parser) parseUnsafeStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'unsafe'
	body := p.parseBlockStmt()
	return &ast.UnsafeStmt{Token: tok, Body: body}
}

// parseWithStmt parses a with statement.
func (p *Parser) parseWithStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'with'
	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	name := p.curTok.Literal
	p.nextToken() // consume ident
	if p.curTok.Type != token.ASSIGN {
		p.peekError(token.ASSIGN)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	p.nextToken() // consume '='
	value := p.parseExpression(LOWEST)
	body := p.parseBlockStmt()
	return &ast.WithStmt{Token: tok, Name: name, Value: value, Body: body}
}

// parseMatchExpr parses a match expression with pattern arms.
func (p *Parser) parseMatchExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'match'
	subject := p.parseExpression(LOWEST)
	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	p.nextToken() // consume '{'

	var arms []*ast.MatchArm
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}

		// Parse pattern
		var pattern ast.Expression
		if p.curTok.Type == token.IDENT && p.curTok.Literal == "_" {
			pattern = &ast.Identifier{Token: p.curTok, Value: "_"}
			p.nextToken()
		} else {
			pattern = p.parseExpression(LOWEST)
		}

		// Optional guard: 'if condition'
		var guard ast.Expression
		if p.curTok.Type == token.IF {
			p.nextToken() // consume 'if'
			guard = p.parseExpression(LOWEST)
		}

		if p.curTok.Type != token.FATARROW {
			p.peekError(token.FATARROW)
			p.skipUntilNewlineOrBrace()
			return nil
		}
		p.nextToken() // consume '=>'

		// Parse body as a block or single expression
		var body *ast.BlockStmt
		if p.curTok.Type == token.LBRACE {
			body = p.parseBlockStmt()
		} else {
			expr := p.parseExpression(LOWEST)
			body = &ast.BlockStmt{Statements: []ast.Statement{&ast.ExprStmt{Expr: expr}}}
		}

		arms = append(arms, &ast.MatchArm{
			Token:   tok,
			Pattern: pattern,
			Guard:   guard,
			Body:    body,
		})

		// optional comma/newline
		if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}

	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}

	return &ast.MatchExpr{Token: tok, Subject: subject, Arms: arms}
}

// parseComptimeStmt parses a compile-time evaluated block.
func (p *Parser) parseComptimeStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'comptime'
	body := p.parseBlockStmt()
	return &ast.ComptimeStmt{Token: tok, Body: body}
}

// parseSpawnStmt parses a spawn goroutine statement.
func (p *Parser) parseSpawnStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'spawn'
	call := p.parseExpression(LOWEST)
	return &ast.SpawnStmt{Token: tok, Call: call}
}

// parseSelectStmt parses a select statement over channel operations.
func (p *Parser) parseSelectStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'select'
	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	p.nextToken() // consume '{'
	var cases []ast.SelectCase
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.DEFAULT {
			p.nextToken() // consume 'default'
			if p.curTok.Type == token.COLON {
				p.nextToken() // consume ':'
			}
			var stmts []ast.Statement
			for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF &&
				p.curTok.Type != token.CASE && p.curTok.Type != token.DEFAULT {
				if p.curTok.Type == token.NEWLINE {
					p.nextToken()
					continue
				}
				stmt := p.parseStatement()
				if stmt != nil {
					stmts = append(stmts, stmt)
				}
			}
			cases = append(cases, ast.SelectCase{
				Token:     p.curTok,
				IsDefault: true,
				Body:      &ast.BlockStmt{Statements: stmts},
			})
		} else if p.curTok.Type == token.CASE {
			p.nextToken() // consume 'case'
			// Check for receive-binding syntax: case val := <-ch
			recvVar := ""
			if p.curTok.Type == token.IDENT && p.peekTok.Type == token.COLONASS {
				recvVar = p.curTok.Literal
				p.nextToken() // consume variable name
				p.nextToken() // consume ':='
			}
			cond := p.parseExpression(LOWEST)
			if p.curTok.Type == token.COLON {
				p.nextToken() // consume ':'
			}
			// Parse body statements until newline or next case/default/}
			var stmts []ast.Statement
			for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF &&
				p.curTok.Type != token.CASE && p.curTok.Type != token.DEFAULT {
				if p.curTok.Type == token.NEWLINE {
					p.nextToken()
					continue
				}
				stmt := p.parseStatement()
				if stmt != nil {
					stmts = append(stmts, stmt)
				}
			}
			cases = append(cases, ast.SelectCase{
				Token:     p.curTok,
				Condition: cond,
				Body:      &ast.BlockStmt{Statements: stmts},
				RecvVar:   recvVar,
			})
		} else {
			p.errors = append(p.errors, fmt.Sprintf("expected 'case' or 'default' in select, got %s", p.curTok.Type))
			p.skipUntilNewlineOrBrace()
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}
	return &ast.SelectStmt{Token: tok, Cases: cases}
}

// parseSwitchStmt parses a C-style switch statement.
func (p *Parser) parseSwitchStmt() ast.Statement {
	tok := p.curTok
	p.nextToken() // consume 'switch'
	subject := p.parseExpression(LOWEST)
	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		p.skipUntilNewlineOrBrace()
		return nil
	}
	p.nextToken() // consume '{'
	var cases []ast.SwitchCase
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.NEWLINE {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.DEFAULT {
			p.nextToken() // consume 'default'
			if p.curTok.Type == token.COLON {
				p.nextToken() // consume ':'
			}
			var stmts []ast.Statement
			for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF &&
				p.curTok.Type != token.CASE && p.curTok.Type != token.DEFAULT {
				if p.curTok.Type == token.NEWLINE {
					p.nextToken()
					continue
				}
				stmt := p.parseStatement()
				if stmt != nil {
					stmts = append(stmts, stmt)
				}
			}
			cases = append(cases, ast.SwitchCase{
				Token:     p.curTok,
				IsDefault: true,
				Body:      &ast.BlockStmt{Statements: stmts},
			})
		} else if p.curTok.Type == token.CASE {
			p.nextToken() // consume 'case'
			var values []ast.Expression
			values = append(values, p.parseExpression(LOWEST))
			for p.curTok.Type == token.COMMA {
				p.nextToken() // consume ','
				values = append(values, p.parseExpression(LOWEST))
			}
			if p.curTok.Type == token.COLON {
				p.nextToken() // consume ':'
			}
			var stmts []ast.Statement
			for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF &&
				p.curTok.Type != token.CASE && p.curTok.Type != token.DEFAULT {
				if p.curTok.Type == token.NEWLINE {
					p.nextToken()
					continue
				}
				stmt := p.parseStatement()
				if stmt != nil {
					stmts = append(stmts, stmt)
				}
			}
			cases = append(cases, ast.SwitchCase{
				Token:  p.curTok,
				Values: values,
				Body:   &ast.BlockStmt{Statements: stmts},
			})
		} else {
			p.errors = append(p.errors, fmt.Sprintf("expected 'case' or 'default' in switch, got %s", p.curTok.Type))
			p.skipUntilNewlineOrBrace()
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}
	return &ast.SwitchStmt{Token: tok, Subject: subject, Cases: cases}
}

// parseAsyncExpr parses an async expression.
func (p *Parser) parseAsyncExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'async'
	expr := p.parseExpression(LOWEST)
	return &ast.AsyncExpr{Token: tok, Expr: expr}
}

// parseAwaitExpr parses an await expression.
func (p *Parser) parseAwaitExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'await'
	expr := p.parseExpression(LOWEST)
	return &ast.AwaitExpr{Token: tok, Expr: expr}
}

// parseSizeofExpr parses a sizeof(type) expression.
func (p *Parser) parseSizeofExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'sizeof'
	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	ty := p.parseType()
	if p.curTok.Type == token.RPAREN {
		p.nextToken()
	}
	return &ast.SizeofExpr{Token: tok, Type: ty}
}

// parseAlignofExpr parses an alignof(type) expression.
func (p *Parser) parseAlignofExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'alignof'
	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	ty := p.parseType()
	if p.curTok.Type == token.RPAREN {
		p.nextToken()
	}
	return &ast.AlignofExpr{Token: tok, Type: ty}
}

// parseMinExpr parses a min(Type) builtin expression.
func (p *Parser) parseMinExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'min'
	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	ty := p.parseType()
	if p.curTok.Type == token.RPAREN {
		p.nextToken()
	}
	return &ast.MinExpr{Token: tok, Type: ty}
}

// parseMaxExpr parses a max(Type) builtin expression.
func (p *Parser) parseMaxExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'max'
	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	ty := p.parseType()
	if p.curTok.Type == token.RPAREN {
		p.nextToken()
	}
	return &ast.MaxExpr{Token: tok, Type: ty}
}

// ---- Expression parsing with Pratt parser ----

type precedence int

const (
	LOWEST precedence = iota
	ASSIGN
	RANGE
	OR
	AND
	BITOR
	BITXOR
	BITAND
	EQUALS
	LESSGREATER
	SHIFT
	SUM
	PRODUCT
	POWER
	PREFIX
	POSTFIX
	CALL
	INDEX
)

var precedences = map[token.Type]precedence{
	token.OR:        OR,
	token.AND:       AND,
	token.PIPE:      BITOR,
	token.CARET:     BITXOR,
	token.AMPERSAND: BITAND,
	token.EQ:        EQUALS,
	token.NE:        EQUALS,
	token.LT:        LESSGREATER,
	token.GT:        LESSGREATER,
	token.LE:        LESSGREATER,
	token.GE:        LESSGREATER,
	token.IN:        LESSGREATER,
	token.LSHIFT:    SHIFT,
	token.RSHIFT:    SHIFT,
	token.PLUS:      SUM,
	token.MINUS:     SUM,
	token.STAR:      PRODUCT,
	token.SLASH:     PRODUCT,
	token.PERCENT:   PRODUCT,
	token.AT:        PRODUCT,
	token.DBLSTAR:   POWER,
	token.LPAREN:    CALL,
	token.LBRACKET:  INDEX,
	token.DOT:       INDEX, // field access has same precedence as index
	token.QUESTION:  POSTFIX,
	token.DOTDOT:    RANGE,  // range operator
	token.SEND:      ASSIGN, // ch <- value
	token.ASSIGN:    ASSIGN, // a = b in expression context
}

// peekPrecedence returns the precedence of the peek token.
func (p *Parser) peekPrecedence() precedence {
	if pr, ok := precedences[p.peekTok.Type]; ok {
		return pr
	}
	return LOWEST
}

// curPrecedence returns the precedence of the current token.
func (p *Parser) curPrecedence() precedence {
	if pr, ok := precedences[p.curTok.Type]; ok {
		return pr
	}
	return LOWEST
}

// parseExpression parses an expression using Pratt parser (top-down operator precedence).
// It first parses a prefix expression, then consumes infix operators with higher precedence.
func (p *Parser) parseExpression(precedence precedence) ast.Expression {
	prefix := p.prefixParseFns(p.curTok.Type)
	if prefix == nil {
		p.errors = append(p.errors, fmt.Sprintf("no prefix parse function for %s at %d:%d",
			p.curTok.Type, p.curTok.Line, p.curTok.Column))
		p.nextToken()
		return nil
	}
	leftExp := prefix()
	return p.parseExpressionFromLeft(leftExp, precedence)
}

// parseExpressionFromLeft continues parsing an infix expression from a left-hand side
// that has already been parsed. It consumes operators with precedence higher than the
// caller's binding power.
func (p *Parser) parseExpressionFromLeft(left ast.Expression, precedence precedence) ast.Expression {
	for precedence < p.curPrecedence() {
		infix := p.infixParseFns(p.curTok.Type)
		if infix == nil {
			break
		}
		left = infix(left)
	}

	// Check for struct init: TypeName{ field: value, ... } or pkg.TypeName{ field: value, ... }
	// Only when { is followed by } (empty) or ident : (field:value pair).
	if p.curTok.Type == token.LBRACE {
		typeName := ""
		switch expr := left.(type) {
		case *ast.Identifier:
			if len(expr.Value) > 0 && unicode.IsUpper(rune(expr.Value[0])) {
				typeName = expr.Value
			}
		case *ast.FieldAccessExpr:
			if len(expr.Field) > 0 && unicode.IsUpper(rune(expr.Field[0])) {
				typeName = expr.String()
			}
		case *ast.NamedType:
			if len(expr.Name) > 0 && unicode.IsUpper(rune(expr.Name[0])) {
				typeName = expr.Name
			}
		}
		if typeName != "" {
			isStructInit := false
			if p.peekTok.Type == token.RBRACE {
				isStructInit = true
			} else if p.peekTok.Type == token.IDENT && p.peek2Tok.Type == token.COLON {
				isStructInit = true
			}
			if isStructInit {
				left = p.parseStructInitExpression(typeName)
			}
		}
	}
	return left
}

// prefixParseFns returns the prefix parser for a given token type.
func (p *Parser) prefixParseFns(t token.Type) func() ast.Expression {
	switch t {
	case token.IDENT:
		return p.parseIdentifier
	case token.INT:
		if _, err := strconv.ParseInt(p.curTok.Literal, 0, 64); err == nil {
			return p.parseIntegerLiteral
		}
		return p.parseIdentifier
	case token.FLOAT:
		if _, err := strconv.ParseFloat(p.curTok.Literal, 64); err == nil {
			return p.parseFloatLiteral
		}
		return p.parseIdentifier
	case token.STRING:
		return p.parseStringLiteral
	case token.TRUE, token.FALSE:
		return p.parseBooleanLiteral
	case token.NIL:
		return p.parseNilLiteral
	case token.ERROR:
		return p.parseIdentifier
	case token.MINUS, token.BANG, token.TILDE, token.STAR, token.AMPERSAND, token.SEND:
		return p.parsePrefixExpression
	case token.LPAREN:
		return p.parseGroupedExpression
	case token.LBRACKET:
		return p.parseArrayLiteral
	case token.FN:
		return p.parseFnLiteral
	case token.IF:
		return p.parseIfExpr
	case token.MATCH:
		return p.parseMatchExpr
	case token.LBRACE:
		return p.parseMapLiteral
	case token.ASYNC:
		return p.parseAsyncExpr
	case token.AWAIT:
		return p.parseAwaitExpr
	case token.SIZEOF:
		return p.parseSizeofExpr
	case token.ALIGNOF:
		return p.parseAlignofExpr
	case token.SET:
		return p.parseSetLiteral
	case token.FROM:
		return p.parseQueryExpr
	case token.INT8, token.INT16, token.INT32, token.INT64,
		token.UINT, token.UINT8, token.UINT16, token.UINT32, token.UINT64,
		token.BOOL, token.STRING_TYPE, token.BYTES_TYPE,
		token.VOID, token.CHAN, token.TENSOR:
		return p.parseIdentifier
	default:
		return nil
	}
}

// infixParseFns returns the infix parser for a given token type.
func (p *Parser) infixParseFns(t token.Type) func(ast.Expression) ast.Expression {
	switch t {
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT,
		token.EQ, token.NE, token.LT, token.GT, token.LE, token.GE,
		token.AND, token.OR, token.PIPE, token.CARET, token.AMPERSAND,
		token.LSHIFT, token.RSHIFT, token.AT, token.DBLSTAR, token.IN, token.SEND:
		return p.parseInfixExpression
	case token.ASSIGN:
		return p.parseAssignmentExpression
	case token.LPAREN:
		return p.parseCallExpression
	case token.LBRACKET:
		return p.parseIndexExpression
	case token.DOT:
		return p.parseFieldAccessExpression
	case token.QUESTION:
		return p.parseErrorPropagationExpression
	case token.DOTDOT:
		return p.parseRangeExpression
	default:
		return nil
	}
}

// parseRangeExpression parses a range expression start..end.
func (p *Parser) parseRangeExpression(left ast.Expression) ast.Expression {
	expr := &ast.RangeExpr{Token: p.curTok, Start: left}
	p.nextToken() // consume '..'
	expr.End = p.parseExpression(LOWEST)
	return expr
}

// parseErrorPropagationExpression parses the ? postfix operator.
func (p *Parser) parseErrorPropagationExpression(left ast.Expression) ast.Expression {
	expr := &ast.ErrorPropagationExpr{Token: p.curTok, Expr: left}
	p.nextToken()
	return expr
}

// parseIdentifier parses an identifier, handling generic instantiations.
func (p *Parser) parseIdentifier() ast.Expression {
	expr := &ast.Identifier{Token: p.curTok, Value: p.curTok.Literal}
	isUpper := len(expr.Value) > 0 && expr.Value[0] >= 'A' && expr.Value[0] <= 'Z'

	// Handle generic reference: TypeName<TypeArgs> or fnName<TypeArgs>
	if p.peekTok.Type == token.LT {
		peek2IsType := (p.peek2Tok.Type == token.INT && p.peek2Tok.Literal == "int") ||
			p.peek2Tok.Type == token.INT8 || p.peek2Tok.Type == token.INT16 ||
			p.peek2Tok.Type == token.INT32 || p.peek2Tok.Type == token.INT64 ||
			p.peek2Tok.Type == token.UINT || p.peek2Tok.Type == token.UINT8 ||
			p.peek2Tok.Type == token.UINT16 || p.peek2Tok.Type == token.UINT32 ||
			p.peek2Tok.Type == token.UINT64 ||
			(p.peek2Tok.Type == token.FLOAT && p.peek2Tok.Literal == "float") ||
			p.peek2Tok.Type == token.BOOL || p.peek2Tok.Type == token.STRING_TYPE ||
			p.peek2Tok.Type == token.BYTES_TYPE || p.peek2Tok.Type == token.VOID ||
			p.peek2Tok.Type == token.CHAN || p.peek2Tok.Type == token.SET ||
			p.peek2Tok.Type == token.TENSOR || p.peek2Tok.Type == token.ERROR ||
			(p.peek2Tok.Type == token.IDENT && len(p.peek2Tok.Literal) > 0 &&
				p.peek2Tok.Literal[0] >= 'A' && p.peek2Tok.Literal[0] <= 'Z')
		shouldParseGeneric := isUpper || peek2IsType
		if shouldParseGeneric {
			p.nextToken() // consume ident, now on <
			args := p.parseGenericArgs()
			mangled := ast.MangleName(expr.Value, args)
			if p.curTok.Type == token.LBRACE && isUpper {
				isStructInit := false
				if p.peekTok.Type == token.RBRACE {
					isStructInit = true
				} else if p.peekTok.Type == token.IDENT && p.peek2Tok.Type == token.COLON {
					isStructInit = true
				}
				if isStructInit {
					return p.parseStructInitExpression(mangled)
				}
			}
			if p.curTok.Type == token.LPAREN {
				// Return identifier with mangled name so infix ( creates call.
				return &ast.Identifier{Token: expr.Token, Value: mangled}
			}
			if isUpper {
				// Otherwise return as NamedType expression.
				return &ast.NamedType{Token: expr.Token, Name: expr.Value, Args: args}
			}
			// Lowercase ident with < but not followed by ( — return mangled identifier.
			return &ast.Identifier{Token: expr.Token, Value: mangled}
		}
	}

	p.nextToken()
	return expr
}

// parseGenericArgs parses <T, U> and returns the type arguments.
// Precondition: current token is LT.
func (p *Parser) parseGenericArgs() []ast.Type {
	var args []ast.Type
	p.nextToken() // consume '<'
	for {
		arg := p.parseType()
		args = append(args, arg)
		if p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}
		if p.curTok.Type == token.GT {
			p.nextToken()
			break
		}
		p.peekError(token.GT)
		return args
	}
	return args
}

// parseIntegerLiteral parses an integer literal expression.
func (p *Parser) parseIntegerLiteral() ast.Expression {
	val, err := strconv.ParseInt(p.curTok.Literal, 0, 64)
	if err != nil {
		p.errors = append(p.errors, fmt.Sprintf("could not parse %q as integer at %d:%d",
			p.curTok.Literal, p.curTok.Line, p.curTok.Column))
		p.nextToken()
		return nil
	}
	lit := &ast.IntegerLiteral{Token: p.curTok, Value: val}
	p.nextToken()
	return lit
}

// parseFloatLiteral parses a floating-point literal expression.
func (p *Parser) parseFloatLiteral() ast.Expression {
	val, err := strconv.ParseFloat(p.curTok.Literal, 64)
	if err != nil {
		p.errors = append(p.errors, fmt.Sprintf("could not parse %q as float at %d:%d",
			p.curTok.Literal, p.curTok.Line, p.curTok.Column))
		p.nextToken()
		return nil
	}
	lit := &ast.FloatLiteral{Token: p.curTok, Value: val}
	p.nextToken()
	return lit
}

// parseStringLiteral parses a string literal expression.
func (p *Parser) parseStringLiteral() ast.Expression {
	lit := &ast.StringLiteral{Token: p.curTok, Value: p.curTok.Literal}
	p.nextToken()
	return lit
}

// parseBooleanLiteral parses a true/false literal expression.
func (p *Parser) parseBooleanLiteral() ast.Expression {
	lit := &ast.BooleanLiteral{Token: p.curTok, Value: p.curTok.Type == token.TRUE}
	p.nextToken()
	return lit
}

// parseNilLiteral parses a nil literal expression.
func (p *Parser) parseNilLiteral() ast.Expression {
	lit := &ast.NilLiteral{Token: p.curTok}
	p.nextToken()
	return lit
}

// parsePrefixExpression parses a prefix operator expression.
func (p *Parser) parsePrefixExpression() ast.Expression {
	expr := &ast.PrefixExpr{
		Token:    p.curTok,
		Operator: p.curTok.Literal,
	}
	p.nextToken()
	expr.Right = p.parseExpression(PREFIX)
	return expr
}

// parseInfixExpression parses a binary infix operator expression.
func (p *Parser) parseInfixExpression(left ast.Expression) ast.Expression {
	expr := &ast.InfixExpr{
		Token:    p.curTok,
		Operator: p.curTok.Literal,
		Left:     left,
	}
	precedence := p.curPrecedence()
	p.nextToken()
	expr.Right = p.parseExpression(precedence)
	return expr
}

// parseAssignmentExpression parses an assignment inside an expression context
// (e.g., a = b = 1). The right side is parsed with LOWEST precedence so that
// chained assignments are right-associative.
func (p *Parser) parseAssignmentExpression(left ast.Expression) ast.Expression {
	expr := &ast.InfixExpr{
		Token:    p.curTok,
		Operator: p.curTok.Literal,
		Left:     left,
	}
	p.nextToken()
	expr.Right = p.parseExpression(LOWEST)
	return expr
}

// parseCallExpression parses a function call expression.
func (p *Parser) parseCallExpression(function ast.Expression) ast.Expression {
	expr := &ast.CallExpr{Token: p.curTok, Function: function}
	p.nextToken() // consume '('

	// Handle min(Type) / max(Type) builtins when argument is a primitive type.
	if id, ok := function.(*ast.Identifier); ok && (id.Value == "min" || id.Value == "max") {
		if isPrimitiveTypeToken(p.curTok.Type, p.curTok.Literal) {
			ty := p.parseType()
			if p.curTok.Type == token.RPAREN {
				p.nextToken()
			}
			if id.Value == "min" {
				return &ast.MinExpr{Token: id.Token, Type: ty}
			}
			return &ast.MaxExpr{Token: id.Token, Type: ty}
		}
	}

	// Handle make(Type, capacity?) builtin — argument is a type expression.
	if id, ok := function.(*ast.Identifier); ok && id.Value == "make" {
		ty := p.parseType()
		var capExpr ast.Expression
		if p.curTok.Type == token.COMMA {
			p.nextToken() // consume ','
			capExpr = p.parseExpression(LOWEST)
		}
		if p.curTok.Type == token.RPAREN {
			p.nextToken()
		}
		return &ast.MakeExpr{Token: id.Token, Type: ty, Capacity: capExpr}
	}

	expr.Arguments = p.parseExpressionList(token.RPAREN)
	return expr
}

// isPrimitiveTypeToken reports whether a token represents a primitive type.
func isPrimitiveTypeToken(t token.Type, lit string) bool {
	switch t {
	case token.INT:
		return lit == "int"
	case token.INT8:
		return lit == "int8"
	case token.INT16:
		return lit == "int16"
	case token.INT32:
		return lit == "int32"
	case token.INT64:
		return lit == "int64"
	case token.UINT:
		return lit == "uint"
	case token.UINT8:
		return lit == "uint8"
	case token.UINT16:
		return lit == "uint16"
	case token.UINT32:
		return lit == "uint32"
	case token.UINT64:
		return lit == "uint64"
	case token.FLOAT:
		return lit == "float"
	case token.BOOL:
		return lit == "bool"
	case token.STRING_TYPE:
		return lit == "string"
	case token.BYTES_TYPE:
		return lit == "bytes"
	case token.VOID:
		return lit == "void"
	}
	return false
}

// parseExpressionList parses a comma-separated list of expressions until the end token.
func (p *Parser) parseExpressionList(end token.Type) []ast.Expression {
	var list []ast.Expression
	if p.curTok.Type == end {
		p.nextToken()
		return list
	}
	list = append(list, p.parseExpression(LOWEST))
	for p.curTok.Type == token.COMMA {
		p.nextToken()
		list = append(list, p.parseExpression(LOWEST))
	}
	if p.curTok.Type == end {
		p.nextToken()
	} else {
		p.peekError(end)
	}
	return list
}

// parseCollectionElement parses an element inside a collection literal.
func (p *Parser) parseCollectionElement() ast.Expression {
	if p.curTok.Type == token.DOTDOT {
		tok := p.curTok
		p.nextToken() // consume '..'
		operand := p.parseExpression(LOWEST)
		return &ast.SpreadExpr{Token: tok, Operand: operand}
	}
	return p.parseExpression(LOWEST)
}

// parseArrayLiteral parses an array or map literal.
func (p *Parser) parseArrayLiteral() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume '['

	// Empty bracket: [] or typed empty array []Type{}
	if p.curTok.Type == token.RBRACKET {
		// Look ahead: if ] is followed by a type and then {, it's []Type{}
		if isTypeStart(p.peekTok.Type) {
			p.nextToken() // consume ']'
			if isTypeStart(p.curTok.Type) {
				typ := p.parseType()
				if p.curTok.Type == token.LBRACE {
					p.nextToken() // consume '{'
					if p.curTok.Type == token.RBRACE {
						p.nextToken() // consume '}'
						return &ast.ArrayLiteral{Token: tok, Type: typ}
					}
					// Not empty — parse elements.
					var elements []ast.Expression
					for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
						if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
							p.nextToken()
							continue
						}
						elements = append(elements, p.parseExpression(LOWEST))
						if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
							p.nextToken()
						}
					}
					if p.curTok.Type == token.RBRACE {
						p.nextToken()
					}
					return &ast.ArrayLiteral{Token: tok, Elements: elements, Type: typ}
				}
				// Fall through: wasn't a typed array literal, return empty array.
				// The type token and whatever follows will be parsed by caller.
				return &ast.ArrayLiteral{Token: tok}
			}
		}
		p.nextToken()
		return &ast.ArrayLiteral{Token: tok}
	}

	// Typed empty array literal: []Type{}
	// Heuristic: if current token is an identifier (type name) and next is ] followed by {
	if p.curTok.Type == token.IDENT && p.peekTok.Type == token.RBRACKET {
		typ := p.parseType()
		if p.curTok.Type == token.RBRACKET {
			p.nextToken() // consume ']'
		} else {
			p.peekError(token.RBRACKET)
			return nil
		}
		if p.curTok.Type == token.LBRACE {
			p.nextToken() // consume '{'
			if p.curTok.Type == token.RBRACE {
				p.nextToken() // consume '}'
				return &ast.ArrayLiteral{Token: tok, Type: typ}
			}
			// Not empty — parse elements.
			var elements []ast.Expression
			for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
				if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
					p.nextToken()
					continue
				}
				elements = append(elements, p.parseExpression(LOWEST))
				if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
					p.nextToken()
				}
			}
			if p.curTok.Type == token.RBRACE {
				p.nextToken()
			}
			return &ast.ArrayLiteral{Token: tok, Elements: elements, Type: typ}
		}
		// Wasn't {} after ] — fall through to normal array parsing.
		// We already consumed the type and ]; reconstruct as first element.
		// This shouldn't normally happen because []Type without {} is a type expression,
		// not an array literal. But for safety, treat as empty array.
		return &ast.ArrayLiteral{Token: tok, Type: typ}
	}

	// Peek ahead: if first element is followed by ':', treat as map literal.
	firstExpr := p.parseCollectionElement()
	if p.curTok.Type == token.COLON {
		p.nextToken() // consume ':'
		val := p.parseExpression(LOWEST)
		pairs := []ast.MapPair{{Key: firstExpr, Value: val}}
		for p.curTok.Type == token.COMMA {
			p.nextToken()
			if p.curTok.Type == token.RBRACKET {
				break
			}
			key := p.parseCollectionElement()
			if p.curTok.Type != token.COLON {
				p.peekError(token.COLON)
				p.skipUntilNewlineOrBrace()
				break
			}
			p.nextToken() // consume ':'
			val = p.parseExpression(LOWEST)
			pairs = append(pairs, ast.MapPair{Key: key, Value: val})
		}
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
		} else {
			p.peekError(token.RBRACKET)
		}
		return &ast.MapLiteral{Token: tok, Pairs: pairs}
	}

	// Regular array literal: first element already parsed, parse rest.
	var elements []ast.Expression
	elements = append(elements, firstExpr)
	for p.curTok.Type == token.COMMA {
		p.nextToken()
		if p.curTok.Type == token.RBRACKET {
			break
		}
		elements = append(elements, p.parseCollectionElement())
	}
	if p.curTok.Type == token.RBRACKET {
		p.nextToken()
	} else {
		p.peekError(token.RBRACKET)
	}
	return &ast.ArrayLiteral{Token: tok, Elements: elements}
}

// parseInfixMapOrStructLiteral handles expr{...} after a left expression.
// If left is a type reference (identifier or field access starting with uppercase),
// it parses a struct init; otherwise it parses a map literal with the left as first key.
func (p *Parser) parseInfixMapOrStructLiteral(left ast.Expression) ast.Expression {
	typeName := ""
	switch expr := left.(type) {
	case *ast.Identifier:
		if len(expr.Value) > 0 && unicode.IsUpper(rune(expr.Value[0])) {
			typeName = expr.Value
		}
	case *ast.FieldAccessExpr:
		if len(expr.Field) > 0 && unicode.IsUpper(rune(expr.Field[0])) {
			// Reconstruct dotted type name from the full field access.
			typeName = expr.String()
		}
	}
	if typeName != "" {
		return p.parseStructInitExpression(typeName)
	}
	// Not a struct init — parse as map literal with left as first key.
	return p.parseMapLiteralWithLeft(left)
}

// parseMapLiteral parses a map literal.
func (p *Parser) parseMapLiteral() ast.Expression {
	return p.parseMapLiteralWithLeft(nil)
}

// parseMapLiteralWithLeft parses a map literal when the first key is already parsed.
func (p *Parser) parseMapLiteralWithLeft(left ast.Expression) ast.Expression {
	tok := p.curTok
	p.nextToken() // consume '{'
	var pairs []ast.MapPair
	if left != nil {
		// left was already parsed as the first key expression
		if p.curTok.Type != token.COLON {
			p.peekError(token.COLON)
			p.skipUntilNewlineOrBrace()
			return nil
		}
		p.nextToken() // consume ':'
		value := p.parseExpression(LOWEST)
		pairs = append(pairs, ast.MapPair{Key: left, Value: value})
		if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}
		key := p.parseExpression(LOWEST)
		if p.curTok.Type != token.COLON {
			p.peekError(token.COLON)
			p.skipUntilNewlineOrBrace()
			return nil
		}
		p.nextToken() // consume ':'
		value := p.parseExpression(LOWEST)
		pairs = append(pairs, ast.MapPair{Key: key, Value: value})
		if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}
	return &ast.MapLiteral{Token: tok, Pairs: pairs}
}

// parseSetLiteral parses a set literal.
func (p *Parser) parseSetLiteral() ast.Expression {
	set := &ast.SetLiteral{Token: p.curTok}
	p.nextToken() // consume 'set'
	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	p.nextToken() // consume '{'
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type == token.NEWLINE || p.curTok.Type == token.COMMA {
			p.nextToken()
			continue
		}
		set.Elements = append(set.Elements, p.parseExpression(LOWEST))
		if p.curTok.Type == token.COMMA || p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken()
	}
	return set
}

// parseFnLiteral parses an anonymous function literal.
func (p *Parser) parseFnLiteral() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'fn'

	if p.curTok.Type != token.LPAREN {
		p.peekError(token.LPAREN)
		return nil
	}
	p.nextToken() // consume '('
	params := p.parseParamList()

	if p.curTok.Type != token.RPAREN {
		p.peekError(token.RPAREN)
		return nil
	}
	p.nextToken() // consume ')'

	var retType ast.Type
	if p.curTok.Type == token.ARROW {
		p.nextToken() // consume '->'
		if p.curTok.Type == token.LPAREN {
			p.nextToken() // consume '('
			var types []ast.Type
			types = append(types, p.parseType())
			for p.curTok.Type == token.COMMA {
				p.nextToken()
				types = append(types, p.parseType())
			}
			if p.curTok.Type == token.RPAREN {
				p.nextToken()
			}
			retType = &ast.TupleType{Token: p.curTok, Types: types}
		} else {
			retType = p.parseType()
		}
	}

	if p.curTok.Type != token.LBRACE {
		p.peekError(token.LBRACE)
		return nil
	}
	body := p.parseBlockStmt()

	return &ast.FnLiteral{
		Token:      tok,
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}
}

// parseIndexExpression parses an index or slice expression.
func (p *Parser) parseIndexExpression(left ast.Expression) ast.Expression {
	tok := p.curTok
	p.nextToken() // consume '['

	// Check for omitted start slice: arr[..end] or arr[..]
	if p.curTok.Type == token.DOTDOT {
		p.nextToken() // consume '..'
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
			return &ast.SliceExpr{Token: tok, Left: left, Start: nil, End: nil}
		}
		end := p.parseExpression(LOWEST)
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
		} else {
			p.peekError(token.RBRACKET)
		}
		return &ast.SliceExpr{Token: tok, Left: left, Start: nil, End: end}
	}

	// Check for ^ from-end index: arr[^1]
	if p.curTok.Type == token.CARET {
		p.nextToken() // consume '^'
		operand := p.parseExpression(LOWEST)
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
		} else {
			p.peekError(token.RBRACKET)
		}
		return &ast.IndexExpr{Token: tok, Left: left, Index: &ast.FromEndIndexExpr{Token: tok, Operand: operand}}
	}

	// Parse first expression with RANGE precedence so that .. is NOT consumed
	// (it is handled manually below for slice syntax).
	first := p.parseExpression(RANGE)
	// Check for omitted end slice: arr[start..]
	if p.curTok.Type == token.DOTDOT {
		p.nextToken() // consume '..'
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
			return &ast.SliceExpr{Token: tok, Left: left, Start: first, End: nil}
		}
		// arr[start..end] — parse end and treat as slice
		end := p.parseExpression(LOWEST)
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
		} else {
			p.peekError(token.RBRACKET)
		}
		return &ast.SliceExpr{Token: tok, Left: left, Start: first, End: end}
	}
	// Check for slice syntax: arr[start:end]
	if p.curTok.Type == token.COLON {
		p.nextToken() // consume ':'
		end := p.parseExpression(LOWEST)
		if p.curTok.Type == token.RBRACKET {
			p.nextToken()
		} else {
			p.peekError(token.RBRACKET)
		}
		return &ast.SliceExpr{Token: tok, Left: left, Start: first, End: end}
	}
	if p.curTok.Type == token.RBRACKET {
		p.nextToken()
	} else {
		p.peekError(token.RBRACKET)
	}
	return &ast.IndexExpr{Token: tok, Left: left, Index: first}
}

// parseFieldAccessExpression parses obj.Field
func (p *Parser) parseFieldAccessExpression(left ast.Expression) ast.Expression {
	p.nextToken() // consume '.'
	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return &ast.FieldAccessExpr{Token: p.curTok, Left: left, Field: ""}
	}
	field := p.curTok.Literal
	isUpper := len(field) > 0 && field[0] >= 'A' && field[0] <= 'Z'

	if p.peekTok.Type == token.LT {
		peek2IsType := (p.peek2Tok.Type == token.INT && p.peek2Tok.Literal == "int") ||
			p.peek2Tok.Type == token.INT8 || p.peek2Tok.Type == token.INT16 ||
			p.peek2Tok.Type == token.INT32 || p.peek2Tok.Type == token.INT64 ||
			p.peek2Tok.Type == token.UINT || p.peek2Tok.Type == token.UINT8 ||
			p.peek2Tok.Type == token.UINT16 || p.peek2Tok.Type == token.UINT32 ||
			p.peek2Tok.Type == token.UINT64 ||
			(p.peek2Tok.Type == token.FLOAT && p.peek2Tok.Literal == "float") ||
			p.peek2Tok.Type == token.BOOL || p.peek2Tok.Type == token.STRING_TYPE ||
			p.peek2Tok.Type == token.BYTES_TYPE || p.peek2Tok.Type == token.VOID ||
			p.peek2Tok.Type == token.CHAN || p.peek2Tok.Type == token.SET ||
			p.peek2Tok.Type == token.TENSOR || p.peek2Tok.Type == token.ERROR ||
			(p.peek2Tok.Type == token.IDENT && len(p.peek2Tok.Literal) > 0 &&
				p.peek2Tok.Literal[0] >= 'A' && p.peek2Tok.Literal[0] <= 'Z')
		shouldParseGeneric := isUpper || peek2IsType
		if shouldParseGeneric {
			p.nextToken() // consume field name, now on <
			args := p.parseGenericArgs()
			mangled := ast.MangleName(field, args)
			return &ast.FieldAccessExpr{Token: p.curTok, Left: left, Field: mangled}
		}
	}

	p.nextToken() // consume field name
	return &ast.FieldAccessExpr{Token: p.curTok, Left: left, Field: field}
}

// parseStructInitExpression parses TypeName{ field: value, ... }
// Precondition: current token is LBRACE
func (p *Parser) parseStructInitExpression(typeName string) ast.Expression {
	tok := p.curTok
	p.nextToken() // consume '{'
	fields := make(map[string]ast.Expression)
	for p.curTok.Type != token.RBRACE && p.curTok.Type != token.EOF {
		if p.curTok.Type != token.IDENT {
			p.errors = append(p.errors, fmt.Sprintf("expected field name, got %s", p.curTok.Type))
			break
		}
		fieldName := p.curTok.Literal
		p.nextToken() // consume field name
		if p.curTok.Type != token.COLON {
			p.errors = append(p.errors, fmt.Sprintf("expected ':', got %s", p.curTok.Type))
			break
		}
		p.nextToken() // consume ':'
		fields[fieldName] = p.parseExpression(LOWEST)
		if p.curTok.Type == token.COMMA {
			p.nextToken() // consume ','
		}
		// Allow optional newline between fields
		for p.curTok.Type == token.NEWLINE {
			p.nextToken()
		}
	}
	if p.curTok.Type == token.RBRACE {
		p.nextToken() // consume '}'
	} else {
		p.errors = append(p.errors, fmt.Sprintf("expected '}', got %s", p.curTok.Type))
	}
	return &ast.StructInitExpr{Token: tok, Type: typeName, Fields: fields}
}

// parseGroupedExpression parses a parenthesized expression.
func (p *Parser) parseGroupedExpression() ast.Expression {
	p.nextToken() // consume '('
	expr := p.parseExpression(LOWEST)
	if p.curTok.Type == token.RPAREN {
		p.nextToken()
	} else {
		p.peekError(token.RPAREN)
	}
	return expr
}

// parseQueryExpr parses a LINQ-style query expression:
//
//	from x in source where x > 5 select x * 2
func (p *Parser) parseQueryExpr() ast.Expression {
	tok := p.curTok
	p.nextToken() // consume 'from'

	if p.curTok.Type != token.IDENT {
		p.peekError(token.IDENT)
		return nil
	}
	variable := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type != token.IN {
		p.peekError(token.IN)
		return nil
	}
	p.nextToken() // consume 'in'

	iterable := p.parseExpression(LOWEST)

	query := &ast.QueryExpr{
		Token: tok,
		From: &ast.FromClause{
			Token:    tok,
			Variable: variable,
			Iterable: iterable,
		},
	}

	// Parse optional clauses: where, orderby, group by, join
	for {
		switch p.curTok.Type {
		case token.WHERE:
			wtok := p.curTok
			p.nextToken() // consume 'where'
			cond := p.parseExpression(LOWEST)
			query.Clauses = append(query.Clauses, &ast.WhereClause{Token: wtok, Condition: cond})
		case token.ORDERBY:
			otok := p.curTok
			p.nextToken() // consume 'orderby'
			key := p.parseExpression(LOWEST)
			descending := false
			if p.curTok.Type == token.DESCENDING {
				descending = true
				p.nextToken()
			} else if p.curTok.Type == token.ASCENDING {
				p.nextToken()
			}
			query.Clauses = append(query.Clauses, &ast.OrderByClause{Token: otok, Key: key, Descending: descending})
		case token.GROUP:
			gtok := p.curTok
			p.nextToken() // consume 'group'
			key := p.parseExpression(LOWEST)
			if p.curTok.Type == token.BY {
				p.nextToken() // consume 'by'
			}
			query.Clauses = append(query.Clauses, &ast.GroupByClause{Token: gtok, Key: key})
		case token.JOIN:
			jtok := p.curTok
			p.nextToken() // consume 'join'
			if p.curTok.Type != token.IDENT {
				p.peekError(token.IDENT)
				return nil
			}
			joinVar := p.curTok.Literal
			p.nextToken()
			if p.curTok.Type != token.IN {
				p.peekError(token.IN)
				return nil
			}
			p.nextToken() // consume 'in'
			joinSource := p.parseExpression(LOWEST)
			var leftKey, rightKey ast.Expression
			query.Clauses = append(query.Clauses, &ast.JoinClause{
				Token:    jtok,
				Variable: joinVar,
				Source:   joinSource,
				LeftKey:  leftKey,
				RightKey: rightKey,
			})
		default:
			goto clausesDone
		}
	}
clausesDone:

	// Parse select clause (required)
	if p.curTok.Type != token.SELECT {
		p.peekError(token.SELECT)
		return nil
	}
	stok := p.curTok
	p.nextToken() // consume 'select'
	selectExpr := p.parseExpression(LOWEST)
	query.Select = &ast.SelectClause{Token: stok, Expression: selectExpr}

	return query
}
