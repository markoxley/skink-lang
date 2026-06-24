# Skink Language Manual

This manual covers the core features of the Skink language. For the web framework reference, see `web_framework.md`.

## Modules

Every Skink source file starts with a module declaration:

```skink
module main
```

Modules are the unit of namespacing. Declarations exported from one module are prefixed with the module name when imported into another, e.g. `mylib.Add`.

## Imports

Import declarations bring other modules into scope. The default alias for an import is the last component of the path:

```skink
import "std/str"
import "std/json"

fn main() -> int {
    s := str.Concat("hello", " world")
    v := json.FromString(s)
    return 0
}
```

### Import aliases

To avoid name clashes, imports can be renamed with the `as` keyword:

```skink
import "std/str" as s
import "std/json" as j

fn main() -> int {
    s2 := s.Concat("hello", " world")
    v := j.FromString(s2)
    return 0
}
```

The legacy Go-style form is also supported:

```skink
import s "std/str"
```

Aliases can be combined in import blocks:

```skink
import {
    "std/str" as s,
    "std/json" as j
}
```

Two imports in the same module may not use the same alias.

An import must be used: a hard error is reported if a module is imported but none of its exported symbols are referenced. Use the blank identifier `as _` for imports that are loaded only for side effects:

```skink
import _ "std/libc"
```

## Type aliases

A type alias gives a new name to an existing type:

```skink
type IntList = []int
type Handler = fn(Request, *ResponseWriter)

pub type Handler = fn(Request, *ResponseWriter)
```

Aliases are transparent: an alias is fully interchangeable with the underlying type.

## Functions

Functions are declared with `fn`:

```skink
fn add(a: int, b: int) -> int {
    return a + b
}

pub fn add(a: int, b: int) -> int {
    return a + b
}
```

Multi-value returns are supported:

```skink
fn divide(a: int, b: int) -> (int, error) {
    if b == 0 {
        return 0, error.New("division by zero")
    }
    return a / b, nil
}
```

The `defer` statement schedules a function call to run when the surrounding function returns:

```skink
fn processFile(path: string) -> error {
    f := open(path)
    defer close(f)
    // ... use f ...
    return nil
}
```

Deferred calls are executed in LIFO order (last deferred, first executed).

## Variables

Variables are declared with `var` or the short form `:=`:

```skink
var x: int = 10
x := 10
s := "hello"
```

Top-level `var` declarations may be exported with `pub`.

Local variables must be read at least once; a variable that is declared and never used produces a hard build error. Function parameters are exempt, and the blank identifier `_` can be used to explicitly ignore a value:

```skink
x, _ := pair()
```

## Control flow

```skink
if x > 0 {
    print("positive")
} else if x < 0 {
    print("negative")
} else {
    print("zero")
}

for i := 0; i < 10; i = i + 1 {
    print(i)
}

for item in items {
    print(item)
}

while i <= n {
    print(i)
    i = i + 1
}

until x > 100 {
    x = x * 2
}

switch x {
case 1:
    print("one")
case 2:
    print("two")
default:
    print("other")
}

match x {
    1 => print("one"),
    2 => print("two"),
    _ => print("other")
}
```

`switch` statements must include a `default` case, and `match` expressions must include a `_` wildcard arm. Both are required to ensure the construct is exhaustive; missing them produces a hard build error.

`while` loops execute as long as the condition remains true. `until` loops execute until the condition becomes true (equivalent to `while !condition`).

## Compiler safety checks

The compiler enforces three hard build errors to prevent common mistakes:

1. **Unused imports** — every imported module must be referenced. Use `import _ "path"` when a module is loaded only for side effects.
2. **Unused variables** — every local variable must be read. Function parameters are exempt, and `_` can be used to ignore values.
3. **Non-exhaustive switch/match** — every `switch` must have a `default` case, and every `match` must have a `_` wildcard arm.

```skink
// OK: helper is used via helper.Process
import "helper"

// OK: x is used, y is explicitly ignored
x, _ := pair()

// OK: switch has a default case
switch x {
case 1:
    print("one")
default:
    print("other")
}

// OK: match has a wildcard arm
return match x {
    1 => "one",
    _ => "other"
}
```

## Structs

```skink
struct Point {
    x: int
    y: int
}

pub struct Point {
    x: int
    y: int
}

fn (p *Point) Move(dx: int, dy: int) {
    p.x = p.x + dx
    p.y = p.y + dy
}
```

Struct literals use field names or positional values:

```skink
p := Point{x: 1, y: 2}
p2 := Point{1, 2}
```

## Enums

Enums declare a set of named constants:

```skink
enum Color {
    Red
    Green
    Blue
}

pub enum Color {
    Red
    Green
    Blue
}
```

Enum variants are accessed as `Color.Red`, `Color.Green`, etc. Under the hood, enums are compiled as integer constants starting from 0.

## Pointers

Pointer types are written with `*`, and address-of uses `&`:

```skink
fn scale(p: *Point, factor: int) {
    p.x = p.x * factor
    p.y = p.y * factor
}

p := &Point{1, 2}
scale(p, 3)
```

## Generics

Skink supports generics through monomorphization. Structs and functions can be parameterized by types:

```skink
struct Entry<K, V> {
    key: K
    value: V
}

pub fn New<K, V>() -> Entry<K, V> {
    return Entry<K, V>{key: default, value: default}
}
```

Generic instantiations are specialized at compile time into concrete implementations. The standard library includes generic collections like `HashMap<K, V>` in `std/hashmap`.

## Collections

```skink
arr := [1, 2, 3]
m := make(map[string]int)
m["a"] = 1
s := set{1, 2, 3}
```

## Error handling

Functions can return an `error` value. The `?` operator propagates non-nil errors:

```skink
fn readConfig() -> (Config, error) {
    text, err := readFile("config.json")?
    cfg, err := json.FromString(text)?
    return cfg, nil
}
```

## Services and rulesets

Services declare a set of methods that concrete types can implement:

```skink
service Writer {
    fn Write(self: Writer, b: []byte)
    fn Flush(self: Writer)
}
```

Rulesets are used for periodic event-driven rules.

## Templates

Templates (also known as traits) define duck-typed interfaces that types can satisfy by implementing the required methods:

```skink
template Reader {
    fn Read(self: Reader, buf: []byte) -> (int, error)
}

template Writer {
    fn Write(self: Writer, data: []byte) -> (int, error)
}
```

Unlike services, templates are duck-typed: any type with methods matching the template's signature can be used where the template is expected, without explicit declaration.

## Concurrency

Channels, goroutines, and futures are supported:

```skink
ch := make(chan<int>)
spawn fn() {
    ch <- 42
}()
v := <-ch
```

## C interoperability

Header files can be imported directly:

```skink
import "C:mylib.h"

fn main() -> int {
    return int(mylib.compute(5))
}
```

## Web framework

For HTTP routing, request/response handling, and RESTful APIs, see `docs/web_framework.md`.
