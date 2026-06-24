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

// Package codegen_test contains unit tests for the code generator.
// Tests verify that Skink source produces valid LLVM IR for common
// language constructs.
package codegen

import (
	"strings"
	"testing"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
)

// parseProgram parses a Skink source string into an AST program.
func parseProgram(input string) *ast.Program {
	l := lexer.New(input)
	p := parser.New(l)
	return p.ParseProgram()
}

func TestEmitSimpleMain(t *testing.T) {
	input := `fn main() -> int {
		return 42
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "define i32 @main(i32 %argc, i8** %argv)") {
		t.Errorf("IR missing main function declaration")
	}
	if !strings.Contains(ir, "ret i32 42") {
		t.Errorf("IR missing return 42")
	}
}

func TestEmitAdd(t *testing.T) {
	input := `fn main() -> int {
		return 1 + 2
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "add i32 1, 2") {
		t.Errorf("IR missing add instruction")
	}
}

func TestEmitVarDecl(t *testing.T) {
	input := `fn main() -> int {
		x := 10
		return x
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "alloca i32") {
		t.Errorf("IR missing alloca for variable")
	}
	if !strings.Contains(ir, "store i32 10, i32*") {
		t.Errorf("IR missing store for variable init")
	}
	if !strings.Contains(ir, "load i32, i32*") {
		t.Errorf("IR missing load for variable read")
	}
}

func TestEmitIfStmt(t *testing.T) {
	input := `fn main() -> int {
		if true {
			return 1
		}
		return 0
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "br i1") {
		t.Errorf("IR missing conditional branch")
	}
	if !strings.Contains(ir, "L1:") {
		t.Errorf("IR missing labels")
	}
}

func TestEmitWhileStmt(t *testing.T) {
	input := `fn main() -> int {
		x := 0
		while x < 3 {
			x = x + 1
		}
		return x
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "br label") {
		t.Errorf("IR missing branch to loop header")
	}
	if !strings.Contains(ir, "icmp slt i32") {
		t.Errorf("IR missing slt comparison")
	}
}

func TestEmitCall(t *testing.T) {
	input := `fn add(a: int, b: int) -> int {
		return a + b
	}

	fn main() -> int {
		return add(1, 2)
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "define i32 @add(i32 %a, i32 %b)") {
		t.Errorf("IR missing add function")
	}
	if !strings.Contains(ir, "call i32 @add(i32 1, i32 2)") {
		t.Errorf("IR missing call to add")
	}
}

func TestEmitPrint(t *testing.T) {
	input := `fn main() {
		print("hello")
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "declare i32 @printf(i8*, ...)") {
		t.Errorf("IR missing printf declaration")
	}
	if !strings.Contains(ir, "call i32 (i8*, ...) @printf(") {
		t.Errorf("IR missing printf call")
	}
}

// --- Expression tests ---

func TestEmitPrefixNegation(t *testing.T) {
	input := `fn main() -> int {
		return -5
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "sub i32 0, 5") {
		t.Errorf("IR missing negation via sub i32 0, 5")
	}
}

func TestEmitPrefixNot(t *testing.T) {
	input := `fn main() -> int {
		return !true
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "xor i1 1, 1") {
		t.Errorf("IR missing logical not via xor i1")
	}
}

func TestEmitPrefixBitwiseNot(t *testing.T) {
	input := `fn main() -> int {
		return ~0
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "xor i32 0, -1") {
		t.Errorf("IR missing bitwise not via xor i32, -1")
	}
}

func TestEmitBooleanLiteral(t *testing.T) {
	input := `fn main() -> int {
		return false
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	// Boolean false is i1, then zext to i32 before returning from int function.
	if !strings.Contains(ir, "zext i1 0 to i32") {
		t.Errorf("IR missing zext for boolean false")
	}
	if !strings.Contains(ir, "ret i32") {
		t.Errorf("IR missing return")
	}
}

func TestEmitStringLiteral(t *testing.T) {
	input := `fn main() {
		print("hello")
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "private constant") {
		t.Errorf("IR missing private string constant")
	}
	if !strings.Contains(ir, "getelementptr inbounds") {
		t.Errorf("IR missing getelementptr for string")
	}
}

// --- Comparison and logical operator tests ---

func TestEmitComparisons(t *testing.T) {
	input := `fn main() -> int {
		a := 1 == 2
		b := 1 != 2
		c := 1 < 2
		d := 1 > 2
		e := 1 <= 2
		f := 1 >= 2
		return 0
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	checks := []string{
		"icmp eq i32",
		"icmp ne i32",
		"icmp slt i32",
		"icmp sgt i32",
		"icmp sle i32",
		"icmp sge i32",
	}
	for _, want := range checks {
		if !strings.Contains(ir, want) {
			t.Errorf("IR missing comparison: %s", want)
		}
	}
}

func TestEmitLogicalOperators(t *testing.T) {
	input := `fn main() -> int {
		return true && false || true
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "and i1") {
		t.Errorf("IR missing and i1")
	}
	if !strings.Contains(ir, "or i1") {
		t.Errorf("IR missing or i1")
	}
}

// --- Statement tests ---

func TestEmitAssignment(t *testing.T) {
	input := `fn main() -> int {
		x := 10
		x = 20
		return x
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	// Two stores: initial (10) and assignment (20)
	count := strings.Count(ir, "store i32")
	if count < 2 {
		t.Errorf("Expected at least 2 store instructions, got %d", count)
	}
}

func TestEmitForLoop(t *testing.T) {
	input := `fn main() -> int {
		sum := 0
		for i := 0; i < 5; i = i + 1 {
			sum = sum + i
		}
		return sum
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "br label %L1") {
		t.Errorf("IR missing loop header branch")
	}
	if !strings.Contains(ir, "icmp slt i32") {
		t.Errorf("IR missing loop condition slt")
	}
	if !strings.Contains(ir, "add i32") {
		t.Errorf("IR missing add for loop post-step")
	}
}

// --- Array tests ---

func TestEmitArrayLiteral(t *testing.T) {
	input := `fn main() -> int {
		arr := [1, 2, 3]
		return arr[0]
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "call i8* @Skink_rc_alloc") {
		t.Errorf("IR missing heap allocation via Skink_rc_alloc")
	}
	if !strings.Contains(ir, "store i64 3, i64*") {
		t.Errorf("IR missing length prefix store")
	}
	if !strings.Contains(ir, "getelementptr inbounds i32, i32*") {
		t.Errorf("IR missing index elementptr")
	}
}

func TestEmitEmptyArray(t *testing.T) {
	input := `fn main() -> int {
		arr := []
		return 0
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	// Empty array should return null pointer
	if !strings.Contains(ir, "null") {
		t.Errorf("IR missing null for empty array")
	}
}

// --- Declaration tests ---

func TestEmitStructDecl(t *testing.T) {
	input := `struct Point {
		x: int
		y: int
	}

	fn main() -> int {
		return 0
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "%struct.Point = type { i32, i32 }") {
		t.Errorf("IR missing struct type definition")
	}
}

func TestEmitGlobalConst(t *testing.T) {
	input := `const ANSWER = 42

	fn main() -> int {
		return ANSWER
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "@ANSWER = constant i32 42") {
		t.Errorf("IR missing global constant declaration")
	}
	// Const values are inlined directly at use sites.
	if !strings.Contains(ir, "ret i32 42") {
		t.Errorf("IR missing inlined constant value")
	}
}

// Helper to isolate IR of a specific function
func extractFunctionIR(ir, name string) string {
	lines := strings.Split(ir, "\n")
	var res []string
	inFunc := false
	for _, line := range lines {
		if strings.HasPrefix(line, "define ") && strings.Contains(line, "@"+name) {
			inFunc = true
		}
		if inFunc {
			res = append(res, line)
			if line == "}" {
				inFunc = false
			}
		}
	}
	return strings.Join(res, "\n")
}

// --- Control-flow terminator tracking tests ---

func TestEarlyReturnInIf(t *testing.T) {
	input := `fn sign(n: int) -> int {
		if n < 0 {
			return -1
		}
		return 1
	}

	fn main() -> int {
		return sign(-5)
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	// Both branches should have a ret, and there should NOT be a
	// fallback ret after the if block (both branches terminate).
	isolated := extractFunctionIR(ir, "sign") + "\n" + extractFunctionIR(ir, "main")
	lines := strings.Split(isolated, "\n")
	retCount := 0
	for _, line := range lines {
		if strings.Contains(line, "ret i32") {
			retCount++
		}
	}
	// sign has 2 ret, main has 1 ret -> 3 total
	if retCount != 3 {
		t.Errorf("Expected exactly 3 ret instructions (2 in sign, 1 in main), got %d", retCount)
	}
}

func TestIfElseBothReturn(t *testing.T) {
	input := `fn max(a: int, b: int) -> int {
		if a > b {
			return a
		} else {
			return b
		}
	}

	fn main() -> int {
		return max(1, 2)
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	isolated := extractFunctionIR(ir, "max") + "\n" + extractFunctionIR(ir, "main")
	lines := strings.Split(isolated, "\n")
	retCount := 0
	for _, line := range lines {
		if strings.Contains(line, "ret i32") {
			retCount++
		}
	}
	// max has 2 ret (both branches), main has 1 ret -> 3 total
	// No extra fallback ret should be emitted since both branches terminate.
	if retCount != 3 {
		t.Errorf("Expected exactly 3 ret instructions, got %d", retCount)
	}
}

// --- Float arithmetic tests ---

func TestEmitFloatAdd(t *testing.T) {
	input := `fn main() -> float {
		return 1.5 + 2.5
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	skinkMainIR := extractFunctionIR(ir, "_skink_main")
	if !strings.Contains(skinkMainIR, "fadd double 1.5") {
		t.Errorf("IR missing fadd for float addition")
	}
	if strings.Contains(skinkMainIR, "add i32") {
		t.Errorf("IR incorrectly used i32 add for float")
	}
}

func TestEmitFloatNegation(t *testing.T) {
	input := `fn main() -> float {
		return -3.14
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "fsub double 0.0") {
		t.Errorf("IR missing fsub for float negation")
	}
}

func TestEmitFloatComparison(t *testing.T) {
	input := `fn main() -> int {
		return 1.0 < 2.0
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "fcmp olt double") {
		t.Errorf("IR missing fcmp olt for float comparison")
	}
	// Comparison produces i1, then zext to i32 for return
	if !strings.Contains(ir, "zext i1") {
		t.Errorf("IR missing zext i1 to i32 for bool return")
	}
}

// --- Bool-to-int zext tests ---

func TestEmitBoolComparisonStored(t *testing.T) {
	input := `fn main() -> int {
		result := 1 < 2
		return result
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "icmp slt i32 1, 2") {
		t.Errorf("IR missing icmp slt")
	}
	if !strings.Contains(ir, "zext i1") {
		t.Errorf("IR missing zext when storing comparison result to i32")
	}
}

func TestEmitBoolAssignment(t *testing.T) {
	input := `fn main() -> int {
		result := false
		result = 1 == 2
		return result
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	// Should have zext for the assignment of comparison to i32 variable
	if !strings.Contains(ir, "zext i1") {
		t.Errorf("IR missing zext when assigning comparison to i32 variable")
	}
}

func TestEmitForInLoop(t *testing.T) {
	input := `fn main() -> int {
		items := [1, 2, 3]
		sum := 0
		for i in items {
			sum = sum + i
		}
		return sum
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	// Should contain index-based iteration pattern
	if !strings.Contains(ir, "icmp slt") {
		t.Errorf("IR missing index comparison for for-in loop")
	}
	if !strings.Contains(ir, "getelementptr") {
		t.Errorf("IR missing element access for for-in loop")
	}
	if !strings.Contains(ir, "add i32") {
		t.Errorf("IR missing increment for for-in loop")
	}
}

// --- Struct tests ---

func TestEmitStructInitAndFieldAccess(t *testing.T) {
	input := `struct Point {
		x: int
		y: int
	}
	fn main() -> int {
		p := Point{x: 3, y: 4}
		return p.x + p.y
	}`
	prog := parseProgram(input)
	cg := New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	ir := cg.String()

	if !strings.Contains(ir, "%struct.Point = type { i32, i32 }") {
		t.Errorf("IR missing struct type definition")
	}
	if !strings.Contains(ir, "alloca %struct.Point") {
		t.Errorf("IR missing struct alloca")
	}
	if !strings.Contains(ir, "getelementptr inbounds %struct.Point") {
		t.Errorf("IR missing struct field GEP")
	}
}
