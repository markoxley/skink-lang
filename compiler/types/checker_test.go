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

package types

import (
	"strings"
	"testing"

	"github.com/skink-lang/compiler/ast"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
)

// parseProgram parses input and returns the AST along with parser errors.
func parseProgram(input string) (*ast.Program, []string) {
	l := lexer.New(input)
	p := parser.New(l)
	prog := p.ParseProgram()
	return prog, p.Errors()
}

// checkErrors asserts that the error count matches expected.
func checkErrors(t *testing.T, errors []string, expected int) {
	t.Helper()
	if len(errors) != expected {
		t.Errorf("expected %d errors, got %d:", expected, len(errors))
		for _, err := range errors {
			t.Logf("  %s", err)
		}
	}
}

func TestNoErrors(t *testing.T) {
	input := `fn main() -> int {
		return 42
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestTypeMismatch(t *testing.T) {
	input := `fn main() -> int {
		return "hello"
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestUndefinedVariable(t *testing.T) {
	input := `fn main() -> int {
		return x + 1
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestVarInference(t *testing.T) {
	input := `fn main() -> int {
		x := 10
		return x
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestVarInferenceWrongReturn(t *testing.T) {
	input := `fn main() -> int {
		x := "hello"
		return x
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestIfConditionMustBeBool(t *testing.T) {
	input := `fn main() -> int {
		if 1 {
			return 1
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestArithmeticOperators(t *testing.T) {
	input := `fn main() -> int {
		return 1 + 2 * 3
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestArithmeticWithString(t *testing.T) {
	input := `fn main() -> int {
		return 1 + "hello"
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestLogicalOperators(t *testing.T) {
	input := `fn main() -> int {
		if true && false {
			return 1
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestLogicalWithInt(t *testing.T) {
	input := `fn main() -> int {
		if 1 && true {
			return 1
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestComparison(t *testing.T) {
	input := `fn main() -> int {
		if 5 > 3 {
			return 1
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestPrefixMinus(t *testing.T) {
	input := `fn main() -> int {
		return -42
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestPrefixBang(t *testing.T) {
	input := `fn main() -> int {
		if !true {
			return 1
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestFunctionCall(t *testing.T) {
	input := `fn add(a: int, b: int) -> int {
		return a + b
	}

	fn main() -> int {
		return add(1, 2)
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestFunctionCallWrongArgs(t *testing.T) {
	input := `fn add(a: int, b: int) -> int {
		return a + b
	}

	fn main() -> int {
		return add(1, "hello")
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestFunctionCallWrongCount(t *testing.T) {
	input := `fn add(a: int, b: int) -> int {
		return a + b
	}

	fn main() -> int {
		return add(1)
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestAssignment(t *testing.T) {
	input := `fn main() -> int {
		x := 10
		x = 20
		return x
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestAssignmentTypeMismatch(t *testing.T) {
	input := `fn main() -> int {
		x := 10
		x = "hello"
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestWhileLoop(t *testing.T) {
	input := `fn main() -> int {
		x := 0
		while x < 10 {
			x = x + 1
		}
		return x
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestWhileConditionMustBeBool(t *testing.T) {
	input := `fn main() -> int {
		while 1 {
			return 0
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestConstDecl(t *testing.T) {
	input := `const MAX = 100

	fn main() -> int {
		return MAX
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestForInLoop(t *testing.T) {
	input := `fn main() -> int {
		items := [1, 2, 3]
		for i in items {
			print(i)
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestArrayLiteral(t *testing.T) {
	input := `fn main() -> int {
		items := [1, 2, 3]
		return items[0]
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestArrayLiteralTypeMismatch(t *testing.T) {
	input := `fn main() -> int {
		items := [1, "hello", 3]
		return items[0]
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
}

func TestIndexExpr(t *testing.T) {
	input := `fn main() -> int {
		items := [1, 2, 3]
		return items[1]
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestForCStyleLoop(t *testing.T) {
	input := `fn main() -> int {
		for i := 0; i < 10; i = i + 1 {
			print(i)
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestUnusedVariableError(t *testing.T) {
	input := `fn main() -> int {
		unused := 42
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
	if len(c.Errors()) > 0 && !strings.Contains(c.Errors()[0], "unused variable") {
		t.Errorf("expected unused variable error, got %q", c.Errors()[0])
	}
}

func TestUnusedVariableFunctionParameterExempt(t *testing.T) {
	input := `fn main(unused: int) -> int {
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestUnusedVariableBlankIdentifierExempt(t *testing.T) {
	input := `fn main() -> int {
		_ := 42
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestUnusedImportError(t *testing.T) {
	input := `module main
import "helper"
fn main() -> int {
	return 0
}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
	if len(c.Errors()) > 0 && !strings.Contains(c.Errors()[0], "unused import") {
		t.Errorf("expected unused import error, got %q", c.Errors()[0])
	}
}

func TestBlankImportAllowed(t *testing.T) {
	input := `module main
import _ "helper"
fn main() -> int {
	return 0
}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestSwitchMissingDefaultError(t *testing.T) {
	input := `fn main() -> int {
		x := 1
		switch x {
		case 1:
			return 1
		case 2:
			return 2
		}
		return 0
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
	if len(c.Errors()) > 0 && !strings.Contains(c.Errors()[0], "default") {
		t.Errorf("expected default case error, got %q", c.Errors()[0])
	}
}

func TestSwitchWithDefaultNoError(t *testing.T) {
	input := `fn main() -> int {
		x := 1
		switch x {
		case 1:
			return 1
		default:
			return 0
		}
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}

func TestMatchMissingWildcardError(t *testing.T) {
	input := `fn main() -> int {
		x := 1
		return match x {
			1 => 1
			2 => 2
		}
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 1)
	if len(c.Errors()) > 0 && !strings.Contains(c.Errors()[0], "wildcard") {
		t.Errorf("expected wildcard arm error, got %q", c.Errors()[0])
	}
}

func TestMatchWithWildcardNoError(t *testing.T) {
	input := `fn main() -> int {
		x := 1
		return match x {
			1 => 1
			_ => 0
		}
	}`
	prog, parseErrs := parseProgram(input)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	c := NewChecker()
	c.CheckProgram(prog)
	checkErrors(t, c.Errors(), 0)
}
