# Complete Skink Manifest (skink.mod) demonstration

// Define the name of the module/project
module github.com/username/complete-demo

// Declare current module version
version 2.4.1

# 1. Single-line require syntax with explicit 'require' and version
require github.com/example/lib-a v1.0.0

# 2. Block require syntax for grouping multiple imports together
require (
	github.com/example/math-pkg v2.3.4
	github.com/example/json-utils v0.9.1-beta
)

# 3. Alternative/Implicit line format without 'require' prefix
github.com/example/logger v4.5.6

# 4. Alternative key spelling 'requires'
requires github.com/example/utility-helpers v3.2.1
