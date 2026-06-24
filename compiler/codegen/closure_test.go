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

// closure_test.go contains integration tests for closure capture code generation.
package codegen

import "testing"

// TestIntegrationClosureCapture verifies that closures correctly capture
// and use variables from their enclosing scope.
func TestIntegrationClosureCapture(t *testing.T) {
	code, out := compileAndRun(t, "closure_capture", `fn main() -> int {
x := 10
f := fn(a: int) -> int { return a + x }
return f(5)
}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationClosurePassedToFunction(t *testing.T) {
	code, out := compileAndRun(t, "closure_passed", `fn apply(f: fn(int) -> int, a: int) -> int {
return f(a)
}
fn main() -> int {
x := 10
return apply(fn(a: int) -> int { return a + x }, 5)
}`)
	if code != 15 {
		t.Errorf("expected exit code 15, got %d (output: %q)", code, out)
	}
}

func TestIntegrationClosureMultipleCaptures(t *testing.T) {
	code, out := compileAndRun(t, "closure_multi", `fn main() -> int {
x := 3
y := 7
f := fn(a: int) -> int { return a + x + y }
return f(2)
}`)
	if code != 12 {
		t.Errorf("expected exit code 12, got %d (output: %q)", code, out)
	}
}

func TestIntegrationClosureMutateCapture(t *testing.T) {
	code, out := compileAndRun(t, "closure_mutate", `fn main() -> int {
x := 5
f := fn() -> int {
x = x + 3
return x
}
return f()
}`)
	if code != 8 {
		t.Errorf("expected exit code 8, got %d (output: %q)", code, out)
	}
}

func TestIntegrationClosureNonCaptureStillWorks(t *testing.T) {
	code, out := compileAndRun(t, "closure_nocap", `fn main() -> int {
f := fn(a: int) -> int { return a * 2 }
return f(21)
}`)
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}
