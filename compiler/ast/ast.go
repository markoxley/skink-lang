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

// Package ast defines the abstract syntax tree (AST) node types for the Skink
// programming language. Each syntactic construct in Skink has a corresponding
// AST node type that the parser produces and the type checker / code generator
// consume.
//
// The AST is structured around three core interfaces:
//   - Node: the base interface for all AST nodes
//   - Statement: nodes that represent executable statements
//   - Expression: nodes that represent value-producing expressions
//
// Additionally, Declaration is an interface for top-level declarations.
package ast

import (
	"strings"

	"github.com/skink-lang/compiler/token"
)

// Node is the base interface for every AST node. It provides a way to
// retrieve the token literal that the node was parsed from and a String()
// method for pretty-printing.
type Node interface {
	TokenLiteral() string
	String() string
}

// Statement is the interface for all executable statement nodes.
// The statementNode() method is a marker that prevents expressions from
// being used where statements are expected.
type Statement interface {
	Node
	statementNode()
}

// Expression is the interface for all value-producing expression nodes.
// The expressionNode() method is a marker that prevents statements from
// being used where expressions are expected.
type Expression interface {
	Node
	expressionNode()
}

// Program is the root AST node for a Skink source file.
type Program struct {
	Declarations []Declaration
}

func (p *Program) TokenLiteral() string {
	if len(p.Declarations) > 0 {
		return p.Declarations[0].TokenLiteral()
	}
	return ""
}

func (p *Program) String() string {
	var out string
	for _, d := range p.Declarations {
		out += d.String()
	}
	return out
}

// Declaration is any top-level declaration (fn, struct, enum, etc.).
type Declaration interface {
	Node
	declarationNode()
}

// FnDecl represents a function declaration.
type FnDecl struct {
	Token           token.Token
	Pub             bool // true if exported with 'pub'
	Name            string
	TypeParams      []string // generic type parameters: <T, U>
	TypeParamBounds []Type   // parallel to TypeParams: bound type for each param, nil if none
	Params          []*Param
	ReturnType      Type
	Body            *BlockStmt
	Attributes      []string // attributes: [cuda], etc.
	Doc             string   // leading documentation comment(s)
}

func (f *FnDecl) declarationNode()     {}
func (f *FnDecl) TokenLiteral() string { return f.Token.Literal }
func (f *FnDecl) String() string       { return f.Token.Literal + " " + f.Name }

// ExternFnDecl represents an external function declaration.
type ExternFnDecl struct {
	Token      token.Token
	Name       string
	Params     []*Param
	ReturnType Type
	Varargs    bool // true if the function is variadic (...)
}

func (e *ExternFnDecl) declarationNode()     {}
func (e *ExternFnDecl) TokenLiteral() string { return e.Token.Literal }
func (e *ExternFnDecl) String() string       { return "extern " + e.Name }

// Param is a single function parameter.
type Param struct {
	Name     string
	Type     Type
	Variadic bool // true if this is a variadic parameter (...T)
}

// Variable declaration statement
type VarStmt struct {
	Token    token.Token
	Name     string
	Type     Type
	Value    Expression
	Implicit bool // true for :=
}

func (v *VarStmt) statementNode()       {}
func (v *VarStmt) TokenLiteral() string { return v.Token.Literal }
func (v *VarStmt) String() string       { return v.Token.Literal + " " + v.Name }

// TupleVarStmt is a multi-value variable declaration: a, b := foo()
type TupleVarStmt struct {
	Token    token.Token
	Names    []string
	Value    Expression
	Implicit bool // true for :=
}

func (v *TupleVarStmt) statementNode()       {}
func (v *TupleVarStmt) TokenLiteral() string { return v.Token.Literal }
func (v *TupleVarStmt) String() string {
	return v.Token.Literal + " " + strings.Join(v.Names, ", ")
}

// VarBlockStmt is a block of variable declarations: var { a = 1, b = 2 }
type VarBlockStmt struct {
	Token token.Token
	Decls []*VarStmt
}

func (v *VarBlockStmt) statementNode()       {}
func (v *VarBlockStmt) TokenLiteral() string { return v.Token.Literal }
func (v *VarBlockStmt) String() string       { return "var {}" }

// Expression statement
type ExprStmt struct {
	Token token.Token
	Expr  Expression
}

func (e *ExprStmt) statementNode()       {}
func (e *ExprStmt) TokenLiteral() string { return e.Token.Literal }
func (e *ExprStmt) String() string       { return e.Expr.String() }

// Block statement
type BlockStmt struct {
	Token      token.Token
	Statements []Statement
}

func (b *BlockStmt) statementNode()       {}
func (b *BlockStmt) TokenLiteral() string { return b.Token.Literal }
func (b *BlockStmt) String() string {
	var out string
	for _, s := range b.Statements {
		out += s.String()
	}
	return out
}

// Assignment statement
type AssignmentStmt struct {
	Token    token.Token
	LValue   Expression
	Operator string
	Value    Expression
}

func (a *AssignmentStmt) statementNode()       {}
func (a *AssignmentStmt) TokenLiteral() string { return a.Token.Literal }
func (a *AssignmentStmt) String() string {
	return a.LValue.String() + " " + a.Operator + " " + a.Value.String()
}

// TupleAssignmentStmt is a multi-value assignment: a, b = foo()
type TupleAssignmentStmt struct {
	Token    token.Token
	LValues  []Expression
	Operator string
	Value    Expression
}

func (a *TupleAssignmentStmt) statementNode()       {}
func (a *TupleAssignmentStmt) TokenLiteral() string { return a.Token.Literal }
func (a *TupleAssignmentStmt) String() string {
	var parts []string
	for _, lv := range a.LValues {
		parts = append(parts, lv.String())
	}
	return strings.Join(parts, ", ") + " " + a.Operator + " " + a.Value.String()
}

// Const declaration
type ConstDecl struct {
	Token token.Token
	Pub   bool
	Name  string
	Value Expression
	Doc   string // leading documentation comment(s)
}

func (c *ConstDecl) declarationNode()     {}
func (c *ConstDecl) TokenLiteral() string { return c.Token.Literal }
func (c *ConstDecl) String() string       { return "const " + c.Name }

// ConstBlockDecl represents a block of constant declarations: const { A = 1, B = 2 }
type ConstBlockDecl struct {
	Token token.Token
	Decls []*ConstDecl
}

func (c *ConstBlockDecl) declarationNode()     {}
func (c *ConstBlockDecl) TokenLiteral() string { return c.Token.Literal }
func (c *ConstBlockDecl) String() string       { return "const { ... }" }

// VarDecl represents a top-level variable declaration.
type VarDecl struct {
	Token token.Token
	Pub   bool
	Name  string
	Type  Type
	Value Expression
	Doc   string // leading documentation comment(s)
}

func (v *VarDecl) declarationNode()     {}
func (v *VarDecl) TokenLiteral() string { return v.Token.Literal }
func (v *VarDecl) String() string       { return "var " + v.Name }

// Struct declaration
type StructDecl struct {
	Token           token.Token
	Pub             bool
	Name            string
	TypeParams      []string // generic type parameters: <T, U>
	TypeParamBounds []Type   // parallel to TypeParams: bound type for each param, nil if none
	Fields          []*FieldDecl
	Methods         []*FnDecl
	Attributes      []string // attributes: [packed], etc.
	Doc             string   // leading documentation comment(s)
}

// FieldDecl is a struct field declaration
type FieldDecl struct {
	Token    token.Token
	Name     string
	Type     Type
	BitWidth *int // optional bit width for bitfields
	Embedded bool // true if this is an embedded (anonymous) field
}

func (s *StructDecl) declarationNode()     {}
func (s *StructDecl) TokenLiteral() string { return s.Token.Literal }
func (s *StructDecl) String() string       { return "struct " + s.Name }

// Enum declaration
type EnumDecl struct {
	Token    token.Token
	Pub      bool
	Name     string
	Variants []string
	Doc      string // leading documentation comment(s)
}

func (e *EnumDecl) declarationNode()     {}
func (e *EnumDecl) TokenLiteral() string { return e.Token.Literal }
func (e *EnumDecl) String() string       { return "enum " + e.Name }

// ModuleDecl represents a module declaration: module foo

type ModuleDecl struct {
	Token token.Token
	Name  string
}

func (m *ModuleDecl) declarationNode()     {}
func (m *ModuleDecl) TokenLiteral() string { return m.Token.Literal }
func (m *ModuleDecl) String() string       { return "module " + m.Name }

// ImportDecl represents an import declaration:
//
//	import "path"
//	import name "path"      // legacy alias
//	import "path" as name
type ImportDecl struct {
	Token token.Token
	Path  string
	Alias string // optional alias; empty means use last path component
}

func (i *ImportDecl) declarationNode()     {}
func (i *ImportDecl) TokenLiteral() string { return i.Token.Literal }
func (i *ImportDecl) String() string {
	if i.Alias != "" {
		return "import " + i.Path + " as " + i.Alias
	}
	return "import " + i.Path
}

// ImportBlockDecl represents a block of imports: import { "a", "b" }
type ImportBlockDecl struct {
	Token token.Token
	Decls []*ImportDecl
}

func (i *ImportBlockDecl) declarationNode()     {}
func (i *ImportBlockDecl) TokenLiteral() string { return i.Token.Literal }
func (i *ImportBlockDecl) String() string       { return "import { ... }" }

// ServiceDecl represents a service declaration: service Name { fn ... }
// or a service implementation: service Name for Type { fn ... }
type ServiceDecl struct {
	Token   token.Token
	Pub     bool
	Name    string
	ForType string // implementing type name (empty for interface-only declaration)
	Methods []*FnDecl
}

func (s *ServiceDecl) declarationNode()     {}
func (s *ServiceDecl) TokenLiteral() string { return s.Token.Literal }
func (s *ServiceDecl) String() string       { return "service " + s.Name }

// RuleDecl represents a single rule inside a ruleset.
type RuleDecl struct {
	Token     token.Token
	Name      string
	Condition Expression
	Action    *BlockStmt
	Priority  int
}

func (r *RuleDecl) declarationNode()     {}
func (r *RuleDecl) TokenLiteral() string { return r.Token.Literal }
func (r *RuleDecl) String() string       { return "rule " + r.Name }

// RulesetDecl represents a ruleset declaration: ruleset Name { rule ... }
type RulesetDecl struct {
	Token token.Token
	Pub   bool
	Name  string
	Rules []*RuleDecl
}

func (r *RulesetDecl) declarationNode()     {}
func (r *RulesetDecl) TokenLiteral() string { return r.Token.Literal }
func (r *RulesetDecl) String() string       { return "ruleset " + r.Name }

// TemplateDecl represents a template declaration: template Name { fn method(self: Name) -> T }
type TemplateDecl struct {
	Token      token.Token
	Pub        bool
	Name       string
	Methods    []*FnDecl // method signatures without bodies
	TypeParams []string  // template-level type parameters (rarely used)
}

func (t *TemplateDecl) declarationNode()     {}
func (t *TemplateDecl) TokenLiteral() string { return t.Token.Literal }
func (t *TemplateDecl) String() string       { return "template " + t.Name }

// TypeAliasDecl represents a type alias declaration: type Name = TypeExpr
type TypeAliasDecl struct {
	Token token.Token
	Pub   bool
	Name  string
	Type  Type   // the underlying type expression
	Doc   string // leading documentation comment(s)
}

func (t *TypeAliasDecl) declarationNode()     {}
func (t *TypeAliasDecl) TokenLiteral() string { return t.Token.Literal }
func (t *TypeAliasDecl) String() string       { return "type " + t.Name + " = " + t.Type.String() }

// For statement
type ForStmt struct {
	Token     token.Token
	Init      Statement
	Condition Expression
	Post      Statement
	Body      *BlockStmt
	Iterator  *ForInStmt // mutually exclusive with Init/Condition/Post
}

// ForInStmt represents the 'for x in collection' or 'for x := range ch' variant
type ForInStmt struct {
	Variable string
	Iterable Expression
	IsRange  bool // true for 'for x := range ch' (channel iteration)
}

func (f *ForStmt) statementNode()       {}
func (f *ForStmt) TokenLiteral() string { return f.Token.Literal }
func (f *ForStmt) String() string       { return "for" }

// While statement
type WhileStmt struct {
	Token     token.Token
	Condition Expression
	Body      *BlockStmt
}

func (w *WhileStmt) statementNode()       {}
func (w *WhileStmt) TokenLiteral() string { return w.Token.Literal }
func (w *WhileStmt) String() string       { return "while" }

// Until statement
// Runs until the condition becomes true (equivalent to while !condition).
type UntilStmt struct {
	Token     token.Token
	Condition Expression
	Body      *BlockStmt
}

func (u *UntilStmt) statementNode()       {}
func (u *UntilStmt) TokenLiteral() string { return u.Token.Literal }
func (u *UntilStmt) String() string       { return "until" }

// Return statement
type ReturnStmt struct {
	Token  token.Token
	Values []Expression
}

func (r *ReturnStmt) statementNode()       {}
func (r *ReturnStmt) TokenLiteral() string { return r.Token.Literal }
func (r *ReturnStmt) String() string       { return r.Token.Literal }

// Break statement
type BreakStmt struct {
	Token token.Token
}

func (b *BreakStmt) statementNode()       {}
func (b *BreakStmt) TokenLiteral() string { return b.Token.Literal }
func (b *BreakStmt) String() string       { return "break" }

// Continue statement
type ContinueStmt struct {
	Token token.Token
}

func (c *ContinueStmt) statementNode()       {}
func (c *ContinueStmt) TokenLiteral() string { return c.Token.Literal }
func (c *ContinueStmt) String() string       { return "continue" }

// Comptime statement — compile-time evaluation block.
type ComptimeStmt struct {
	Token token.Token
	Body  *BlockStmt
}

func (c *ComptimeStmt) statementNode()       {}
func (c *ComptimeStmt) TokenLiteral() string { return c.Token.Literal }
func (c *ComptimeStmt) String() string       { return "comptime" }

// Defer statement — defers execution of a statement until function exit.
type DeferStmt struct {
	Token     token.Token
	Statement Statement
}

func (d *DeferStmt) statementNode()       {}
func (d *DeferStmt) TokenLiteral() string { return d.Token.Literal }
func (d *DeferStmt) String() string       { return "defer" }

// Unsafe statement — bypasses safety checks for a block.
type UnsafeStmt struct {
	Token token.Token
	Body  *BlockStmt
}

func (u *UnsafeStmt) statementNode()       {}
func (u *UnsafeStmt) TokenLiteral() string { return u.Token.Literal }
func (u *UnsafeStmt) String() string       { return "unsafe" }

// Spawn statement — starts a concurrent task.
type SpawnStmt struct {
	Token token.Token
	Call  Expression
}

func (s *SpawnStmt) statementNode()       {}
func (s *SpawnStmt) TokenLiteral() string { return s.Token.Literal }
func (s *SpawnStmt) String() string       { return "spawn" }

// Select statement — waits on multiple channel operations.
type SelectStmt struct {
	Token token.Token
	Cases []SelectCase
}

// SelectCase represents a single case in a select statement.
type SelectCase struct {
	Token     token.Token
	Condition Expression
	Body      *BlockStmt
	IsDefault bool
	RecvVar   string // variable name for receive-binding (e.g. "val" in "case val := <-ch")
}

func (s *SelectStmt) statementNode()       {}
func (s *SelectStmt) TokenLiteral() string { return s.Token.Literal }
func (s *SelectStmt) String() string       { return "select" }

// Switch statement — C-style value-based switch.
type SwitchStmt struct {
	Token   token.Token
	Subject Expression
	Cases   []SwitchCase
}

// SwitchCase represents a single case in a switch statement.
type SwitchCase struct {
	Token     token.Token
	Values    []Expression // empty for default
	Body      *BlockStmt
	IsDefault bool
}

func (s *SwitchStmt) statementNode()       {}
func (s *SwitchStmt) TokenLiteral() string { return s.Token.Literal }
func (s *SwitchStmt) String() string       { return "switch" }

// With statement — scoped resource management.
type WithStmt struct {
	Token token.Token
	Name  string
	Value Expression
	Body  *BlockStmt
}

func (w *WithStmt) statementNode()       {}
func (w *WithStmt) TokenLiteral() string { return w.Token.Literal }
func (w *WithStmt) String() string       { return "with" }

// If statement
type IfStmt struct {
	Token       token.Token
	Condition   Expression
	Consequence *BlockStmt
	Alternative Statement
}

func (i *IfStmt) statementNode()       {}
func (i *IfStmt) TokenLiteral() string { return i.Token.Literal }
func (i *IfStmt) String() string       { return i.Token.Literal }

// If expression: if a > b { a } else { b }
type IfExpr struct {
	Token       token.Token
	Condition   Expression
	Consequence *BlockStmt
	Alternative *BlockStmt
}

func (i *IfExpr) expressionNode()      {}
func (i *IfExpr) TokenLiteral() string { return i.Token.Literal }
func (i *IfExpr) String() string       { return i.Token.Literal }

// MatchArm represents a single arm in a match expression.
type MatchArm struct {
	Token   token.Token
	Pattern Expression // literal, identifier, or wildcard
	Guard   Expression // optional guard condition
	Body    *BlockStmt
}

// Match expression: match x { 1 => "one", _ => "other" }
type MatchExpr struct {
	Token   token.Token
	Subject Expression
	Arms    []*MatchArm
}

func (m *MatchExpr) expressionNode()      {}
func (m *MatchExpr) TokenLiteral() string { return m.Token.Literal }
func (m *MatchExpr) String() string       { return "match" }

// Identifier expression
type Identifier struct {
	Token token.Token
	Value string
}

func (i *Identifier) expressionNode()      {}
func (i *Identifier) TokenLiteral() string { return i.Token.Literal }
func (i *Identifier) String() string       { return i.Value }

// Integer literal
type IntegerLiteral struct {
	Token token.Token
	Value int64
}

func (i *IntegerLiteral) expressionNode()      {}
func (i *IntegerLiteral) TokenLiteral() string { return i.Token.Literal }
func (i *IntegerLiteral) String() string       { return i.Token.Literal }

// Float literal
type FloatLiteral struct {
	Token token.Token
	Value float64
}

func (f *FloatLiteral) expressionNode()      {}
func (f *FloatLiteral) TokenLiteral() string { return f.Token.Literal }
func (f *FloatLiteral) String() string       { return f.Token.Literal }

// String literal
type StringLiteral struct {
	Token token.Token
	Value string
}

func (s *StringLiteral) expressionNode()      {}
func (s *StringLiteral) TokenLiteral() string { return s.Token.Literal }
func (s *StringLiteral) String() string       { return s.Token.Literal }

// Boolean literal
type BooleanLiteral struct {
	Token token.Token
	Value bool
}

func (b *BooleanLiteral) expressionNode()      {}
func (b *BooleanLiteral) TokenLiteral() string { return b.Token.Literal }
func (b *BooleanLiteral) String() string       { return b.Token.Literal }

// Nil literal
type NilLiteral struct {
	Token token.Token
}

func (n *NilLiteral) expressionNode()      {}
func (n *NilLiteral) TokenLiteral() string { return n.Token.Literal }
func (n *NilLiteral) String() string       { return "nil" }

// Prefix expression
type PrefixExpr struct {
	Token    token.Token
	Operator string
	Right    Expression
}

func (p *PrefixExpr) expressionNode()      {}
func (p *PrefixExpr) TokenLiteral() string { return p.Token.Literal }
func (p *PrefixExpr) String() string       { return "(" + p.Operator + p.Right.String() + ")" }

// Infix expression
type InfixExpr struct {
	Token    token.Token
	Left     Expression
	Operator string
	Right    Expression
}

func (i *InfixExpr) expressionNode()      {}
func (i *InfixExpr) TokenLiteral() string { return i.Token.Literal }
func (i *InfixExpr) String() string {
	return "(" + i.Left.String() + " " + i.Operator + " " + i.Right.String() + ")"
}

// Array literal
type ArrayLiteral struct {
	Token    token.Token
	Elements []Expression
	Type     Type // Explicit element type for empty array literals like []int{}
}

func (a *ArrayLiteral) expressionNode()      {}
func (a *ArrayLiteral) TokenLiteral() string { return a.Token.Literal }
func (a *ArrayLiteral) String() string       { return "[...]" }

// Set literal: set{1, 2, 3}
type SetLiteral struct {
	Token    token.Token
	Elements []Expression
}

func (s *SetLiteral) expressionNode()      {}
func (s *SetLiteral) TokenLiteral() string { return s.Token.Literal }
func (s *SetLiteral) String() string       { return "set{...}" }

// Map literal: { "key": value, "key2": value2 }
type MapLiteral struct {
	Token token.Token
	Pairs []MapPair
}

type MapPair struct {
	Key   Expression
	Value Expression
}

func (m *MapLiteral) expressionNode()      {}
func (m *MapLiteral) TokenLiteral() string { return m.Token.Literal }
func (m *MapLiteral) String() string       { return "{...}" }

// Async expression: async <call>
type AsyncExpr struct {
	Token token.Token
	Expr  Expression
}

func (a *AsyncExpr) expressionNode()      {}
func (a *AsyncExpr) TokenLiteral() string { return a.Token.Literal }
func (a *AsyncExpr) String() string       { return "async" }

// Await expression: await <future>
type AwaitExpr struct {
	Token token.Token
	Expr  Expression
}

func (a *AwaitExpr) expressionNode()      {}
func (a *AwaitExpr) TokenLiteral() string { return a.Token.Literal }
func (a *AwaitExpr) String() string       { return "await" }

// Sizeof expression: sizeof(Type)
type SizeofExpr struct {
	Token token.Token
	Type  Type
}

func (s *SizeofExpr) expressionNode()      {}
func (s *SizeofExpr) TokenLiteral() string { return s.Token.Literal }
func (s *SizeofExpr) String() string       { return "sizeof" }

// Alignof expression: alignof(Type)
type AlignofExpr struct {
	Token token.Token
	Type  Type
}

func (a *AlignofExpr) expressionNode()      {}
func (a *AlignofExpr) TokenLiteral() string { return a.Token.Literal }
func (a *AlignofExpr) String() string       { return "alignof" }

// Make expression: make(Type, capacity)
type MakeExpr struct {
	Token    token.Token
	Type     Type
	Capacity Expression
}

func (m *MakeExpr) expressionNode()      {}
func (m *MakeExpr) TokenLiteral() string { return m.Token.Literal }
func (m *MakeExpr) String() string       { return "make" }

// Min expression: min(Type)
type MinExpr struct {
	Token token.Token
	Type  Type
}

func (m *MinExpr) expressionNode()      {}
func (m *MinExpr) TokenLiteral() string { return m.Token.Literal }
func (m *MinExpr) String() string       { return "min" }

// Max expression: max(Type)
type MaxExpr struct {
	Token token.Token
	Type  Type
}

func (m *MaxExpr) expressionNode()      {}
func (m *MaxExpr) TokenLiteral() string { return m.Token.Literal }
func (m *MaxExpr) String() string       { return "max" }

// Index expression
type IndexExpr struct {
	Token token.Token
	Left  Expression
	Index Expression
}

func (i *IndexExpr) expressionNode()      {}
func (i *IndexExpr) TokenLiteral() string { return i.Token.Literal }
func (i *IndexExpr) String() string       { return i.Left.String() + "[...]" }

// FromEndIndexExpr represents ^n inside an index, meaning "n from the end".
type FromEndIndexExpr struct {
	Token   token.Token
	Operand Expression
}

func (f *FromEndIndexExpr) expressionNode()      {}
func (f *FromEndIndexExpr) TokenLiteral() string { return "^" + f.Operand.TokenLiteral() }
func (f *FromEndIndexExpr) String() string       { return "^" + f.Operand.String() }

// Slice expression: arr[start:end]
type SliceExpr struct {
	Token token.Token
	Left  Expression
	Start Expression
	End   Expression
}

func (s *SliceExpr) expressionNode()      {}
func (s *SliceExpr) TokenLiteral() string { return s.Token.Literal }
func (s *SliceExpr) String() string       { return s.Left.String() + "[...]" }

// Range expression: start..end
type RangeExpr struct {
	Token token.Token
	Start Expression
	End   Expression
}

func (r *RangeExpr) expressionNode()      {}
func (r *RangeExpr) TokenLiteral() string { return r.Token.Literal }
func (r *RangeExpr) String() string       { return "range" }

// Spread expression: ..expr inside a collection literal.
type SpreadExpr struct {
	Token   token.Token
	Operand Expression
}

func (s *SpreadExpr) expressionNode()      {}
func (s *SpreadExpr) TokenLiteral() string { return ".." + s.Operand.TokenLiteral() }
func (s *SpreadExpr) String() string       { return ".." + s.Operand.String() }

// QueryExpr represents a LINQ-style query expression.
// from x in source [clauses] select expr
type QueryExpr struct {
	Token   token.Token
	From    *FromClause
	Clauses []QueryClause
	Select  *SelectClause
}

func (q *QueryExpr) expressionNode()      {}
func (q *QueryExpr) TokenLiteral() string { return q.Token.Literal }
func (q *QueryExpr) String() string       { return "query" }

// QueryClause is the interface for all LINQ query clause types.
type QueryClause interface {
	clauseNode()
}

// FromClause is the initial clause in a query expression.
type FromClause struct {
	Token    token.Token
	Variable string
	Iterable Expression
}

func (f *FromClause) clauseNode() {}

// WhereClause filters elements in a query expression.
type WhereClause struct {
	Token     token.Token
	Condition Expression
}

func (w *WhereClause) clauseNode() {}

// OrderByClause sorts elements in a query expression.
type OrderByClause struct {
	Token      token.Token
	Key        Expression
	Descending bool
}

func (o *OrderByClause) clauseNode() {}

// GroupByClause groups elements by a key in a query expression.
type GroupByClause struct {
	Token token.Token
	Key   Expression
}

func (g *GroupByClause) clauseNode() {}

// JoinClause joins two sources in a query expression.
type JoinClause struct {
	Token    token.Token
	Variable string
	Source   Expression
	LeftKey  Expression
	RightKey Expression
}

func (j *JoinClause) clauseNode() {}

// SelectClause projects the final result in a query expression.
type SelectClause struct {
	Token      token.Token
	Expression Expression
}

// FieldAccess expression: obj.Field
type FieldAccessExpr struct {
	Token token.Token
	Left  Expression
	Field string
}

func (f *FieldAccessExpr) expressionNode()      {}
func (f *FieldAccessExpr) TokenLiteral() string { return f.Token.Literal }
func (f *FieldAccessExpr) String() string {
	return f.Left.String() + "." + f.Field
}

// StructInit expression: Point{x: 1, y: 2}
type StructInitExpr struct {
	Token  token.Token
	Type   string
	Fields map[string]Expression
}

func (s *StructInitExpr) expressionNode()      {}
func (s *StructInitExpr) TokenLiteral() string { return s.Token.Literal }
func (s *StructInitExpr) String() string {
	return s.Type + "{...}"
}

// FnLiteral represents an anonymous function / closure literal.
type FnLiteral struct {
	Token      token.Token
	Params     []*Param
	ReturnType Type
	Body       *BlockStmt
	Captures   []string // names of variables captured from enclosing scope
}

func (f *FnLiteral) expressionNode()      {}
func (f *FnLiteral) TokenLiteral() string { return f.Token.Literal }
func (f *FnLiteral) String() string       { return "fn(...) { ... }" }

// Call expression
type CallExpr struct {
	Token     token.Token
	Function  Expression
	Arguments []Expression
}

func (c *CallExpr) expressionNode()      {}
func (c *CallExpr) TokenLiteral() string { return c.Token.Literal }
func (c *CallExpr) String() string {
	return c.Function.String() + "(...)"
}

// ErrorPropagationExpr represents the ? postfix operator on a call expression.
// e.g., foo()?  — if foo returns an error, propagate it.
type ErrorPropagationExpr struct {
	Token token.Token
	Expr  Expression
}

func (e *ErrorPropagationExpr) expressionNode()      {}
func (e *ErrorPropagationExpr) TokenLiteral() string { return e.Token.Literal }
func (e *ErrorPropagationExpr) String() string       { return e.Expr.String() + "?" }

// Type interface for AST type nodes
type Type interface {
	Node
	typeNode()
}

// Named type
type NamedType struct {
	Token token.Token
	Name  string
	Args  []Type // generic arguments for Foo<int, string>
}

func (n *NamedType) typeNode()            {}
func (n *NamedType) expressionNode()      {}
func (n *NamedType) TokenLiteral() string { return n.Token.Literal }
func (n *NamedType) String() string {
	if len(n.Args) == 0 {
		return n.Name
	}
	var parts []string
	for _, a := range n.Args {
		parts = append(parts, a.String())
	}
	return n.Name + "<" + strings.Join(parts, ", ") + ">"
}

// Pointer type
type PointerType struct {
	Token token.Token
	Elem  Type
}

func (p *PointerType) typeNode()            {}
func (p *PointerType) expressionNode()      {}
func (p *PointerType) TokenLiteral() string { return p.Token.Literal }
func (p *PointerType) String() string       { return "*" + p.Elem.String() }

// FunctionType represents fn(paramTypes) -> retType.
type FunctionType struct {
	Token      token.Token
	ParamTypes []Type
	ReturnType Type
}

func (f *FunctionType) typeNode()            {}
func (f *FunctionType) expressionNode()      {}
func (f *FunctionType) TokenLiteral() string { return f.Token.Literal }
func (f *FunctionType) String() string {
	var b strings.Builder
	b.WriteString("fn(")
	for i, t := range f.ParamTypes {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(t.String())
	}
	b.WriteString(")")
	if f.ReturnType != nil {
		b.WriteString(" -> ")
		b.WriteString(f.ReturnType.String())
	}
	return b.String()
}

// Array type
type ArrayType struct {
	Token token.Token
	Elem  Type
}

func (a *ArrayType) typeNode()            {}
func (a *ArrayType) expressionNode()      {}
func (a *ArrayType) TokenLiteral() string { return a.Token.Literal }
func (a *ArrayType) String() string       { return "[]" + a.Elem.String() }

// Map type: map[key]value
type MapType struct {
	Token token.Token
	Key   Type
	Elem  Type
}

func (m *MapType) typeNode()            {}
func (m *MapType) expressionNode()      {}
func (m *MapType) TokenLiteral() string { return m.Token.Literal }
func (m *MapType) String() string       { return "map[" + m.Key.String() + "]" + m.Elem.String() }

// Set type: set<T>
type SetType struct {
	Token token.Token
	Elem  Type
}

func (s *SetType) typeNode()            {}
func (s *SetType) expressionNode()      {}
func (s *SetType) TokenLiteral() string { return s.Token.Literal }
func (s *SetType) String() string       { return "set<" + s.Elem.String() + ">" }

// Chan type: chan<T>
type ChanType struct {
	Token token.Token
	Elem  Type
}

func (c *ChanType) typeNode()            {}
func (c *ChanType) expressionNode()      {}
func (c *ChanType) TokenLiteral() string { return c.Token.Literal }
func (c *ChanType) String() string       { return "chan<" + c.Elem.String() + ">" }

// Tensor type: tensor<T>
type TensorType struct {
	Token token.Token
	Elem  Type
}

func (t *TensorType) typeNode()            {}
func (t *TensorType) expressionNode()      {}
func (t *TensorType) TokenLiteral() string { return t.Token.Literal }
func (t *TensorType) String() string       { return "tensor<" + t.Elem.String() + ">" }

// Tuple type for multiple return values: (int, int, string)
type TupleType struct {
	Token token.Token
	Types []Type
}

func (t *TupleType) typeNode()            {}
func (t *TupleType) expressionNode()      {}
func (t *TupleType) TokenLiteral() string { return t.Token.Literal }
func (t *TupleType) String() string {
	var parts []string
	for _, ty := range t.Types {
		parts = append(parts, ty.String())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
