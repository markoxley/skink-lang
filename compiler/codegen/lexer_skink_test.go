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

// lexer_skink_test.go compiles the Skink standard library lexer module
// and verifies that it produces valid LLVM IR.
package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationSkinkLexer(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "test.skink")
	src := "1 + 23 * (45 - 6)"
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	os.Setenv("SKINK_HOME", filepath.Join("..", ".."))
	defer os.Unsetenv("SKINK_HOME")

	code, out := compileAndRunResolved(t, "skink_lexer", map[string]string{
		"main.skink": `module main
import "std/fs"

struct Token {
    typ: int
    val: string
}

fn isDigit(c: int) -> bool {
    return c >= 48 && c <= 57
}

fn isSpace(c: int) -> bool {
    return c == 32 || c == 10 || c == 9
}

fn lex(src: string) -> []Token {
    var tokens []Token
    n := len(src)
    pos := 0
    for pos < n {
        c := src[pos]
        if isSpace(c) {
            pos = pos + 1
            continue
        }
        if isDigit(c) {
            start := pos
            for pos < n && isDigit(src[pos]) {
                pos = pos + 1
            }
            tokens = append(tokens, Token{typ: 1, val: substr(src, start, pos - start)})
            continue
        }
        if c == 43 {
            tokens = append(tokens, Token{typ: 2, val: "+"})
            pos = pos + 1
            continue
        }
        if c == 45 {
            tokens = append(tokens, Token{typ: 3, val: "-"})
            pos = pos + 1
            continue
        }
        if c == 42 {
            tokens = append(tokens, Token{typ: 4, val: "*"})
            pos = pos + 1
            continue
        }
        if c == 47 {
            tokens = append(tokens, Token{typ: 5, val: "/"})
            pos = pos + 1
            continue
        }
        if c == 40 {
            tokens = append(tokens, Token{typ: 6, val: "("})
            pos = pos + 1
            continue
        }
        if c == 41 {
            tokens = append(tokens, Token{typ: 7, val: ")"})
            pos = pos + 1
            continue
        }
        pos = pos + 1
    }
    return tokens
}

// Return a substring from start with given length.
fn substr(src: string, start: int, length: int) -> string {
    var buf []int
    for i := 0; i < length; i = i + 1 {
        buf = append(buf, src[start + i])
    }
    // Convert int array to string via byte buffer
    result := malloc(length + 1)
    for i := 0; i < length; i = i + 1 {
        result[i] = buf[i]
    }
    result[length] = 0
    return result
}

fn strEq(a: string, b: string) -> bool {
    if len(a) != len(b) {
        return false
    }
    n := len(a)
    for i := 0; i < n; i = i + 1 {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

fn main() -> int {
    src, errRead := fs.ReadAll("` + srcFile + `")
    if errRead.message != "" {
        return 99
    }
    tokens := lex(src)
    n := len(tokens)
    if n != 9 {
        return 100 + n
    }
    if tokens[0].typ != 1 || !strEq(tokens[0].val, "1") {
        return 200
    }
    if tokens[1].typ != 2 || !strEq(tokens[1].val, "+") {
        return 300
    }
    if tokens[2].typ != 1 || !strEq(tokens[2].val, "23") {
        return 400
    }
    if tokens[3].typ != 4 || !strEq(tokens[3].val, "*") {
        return 500
    }
    if tokens[4].typ != 6 || !strEq(tokens[4].val, "(") {
        return 600
    }
    if tokens[5].typ != 1 || !strEq(tokens[5].val, "45") {
        return 700
    }
    if tokens[6].typ != 3 || !strEq(tokens[6].val, "-") {
        return 800
    }
    if tokens[7].typ != 1 || !strEq(tokens[7].val, "6") {
        return 900
    }
    if tokens[8].typ != 7 || !strEq(tokens[8].val, ")") {
        return 1000
    }
    return 42
}`})
	if code != 42 {
		t.Errorf("expected exit code 42, got %d (output: %q)", code, out)
	}
}
