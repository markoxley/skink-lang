# Skink Manifest Specification (skink.mod)

A Skink package manifest is defined in a file named `skink.mod` in the root of your project directory. This guide describes the supported fields and grammar.

## Fields and Sections

### 1. `module`
Declares the name of the module.
- Syntax: `module name` or `module github.com/user/project`

### 2. `version`
Declares the version of the module.
- Syntax: `version x.y.z`

### 3. `require` / `requires`
Specifies external dependencies required by the project. Multiple formats are accepted:

#### Single-line Explicit `require`
- `require github.com/user/repo v1.0.0`
- `requires "github.com/user/repo" v1.0.0`

#### Block `require`
Useful for grouping many dependencies:
```text
require (
    github.com/user/repo-a v1.0.0
    github.com/user/repo-b v2.3.4
)
```

#### Implicit Dependency Declaration
You can also declare a dependency listing only the import path and the version without the `require` prefix:
- `github.com/user/repo v1.0.0`

## Comments and Spacing
- Comments start with either `#` or `//`.
- Blank lines and whitespace are ignored.
