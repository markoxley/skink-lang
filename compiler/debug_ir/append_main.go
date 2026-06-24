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

// append_main is a small debug utility that compiles a snippet of Skink
// source code and prints the generated LLVM IR. It is used for manual
// verification of code generation.
package main

import (
	"fmt"

	"github.com/skink-lang/compiler/codegen"
	"github.com/skink-lang/compiler/lexer"
	"github.com/skink-lang/compiler/parser"
	"github.com/skink-lang/compiler/types"
)

func main() {
	input := `fn main() -> int {
		arr := [1]
		arr = append(arr, 2)
		arr = append(arr, 3)
		return arr[2]
	}`

	l := lexer.New(input)
	p := parser.New(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		fmt.Println("Parse errors:", p.Errors())
		return
	}
	checker := types.NewChecker()
	checker.CheckProgram(prog)
	if len(checker.Errors()) > 0 {
		fmt.Println("Type errors:", checker.Errors())
		return
	}
	cg := codegen.New()
	cg.EmitProgram(prog)
	defer cg.Reset()
	fmt.Println(cg.String())
}
