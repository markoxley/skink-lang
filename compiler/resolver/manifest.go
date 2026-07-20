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

// Package resolver handles import path resolution and multi-file program assembly.
package resolver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dependency represents a parsed package requirement.
type Dependency struct {
	Path    string // e.g. "github.com/user/repo"
	Version string // e.g. "v1.0.0"
}

// Manifest represents a skink.mod package manifest.
type Manifest struct {
	Module   string
	Version  string
	Requires []Dependency
}

// ParseManifest reads a skink.mod file and returns the parsed manifest.
func ParseManifest(path string) (*Manifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseManifestContent(string(content))
}

// ParseManifestContent parses skink.mod content and returns the manifest.
// Supported syntax:
//
//	module name
//	version x.y.z
//	require github.com/user/repo v1.0.0
//	requires github.com/user/repo v1.0.0
//	github.com/user/repo v1.0.0  (implicit, no keyword)
//	require (
//	    github.com/user/repo v1.0.0
//	)
//
// Comments start with # or //.
func ParseManifestContent(content string) (*Manifest, error) {
	m := &Manifest{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	inRequire := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		// Single-line block comment
		if strings.HasPrefix(line, "/*") && strings.HasSuffix(line, "*/") {
			continue
		}

		// Block require start
		if line == "require (" {
			inRequire = true
			continue
		}

		// Block require end
		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		// Inside block require
		if inRequire {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				m.Requires = append(m.Requires, Dependency{
					Path:    strings.Trim(parts[0], `"`),
					Version: strings.Trim(parts[1], `"`),
				})
			}
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 1 {
			continue
		}

		key := strings.ToLower(parts[0])

		switch key {
		case "module":
			if len(parts) >= 2 {
				m.Module = strings.Trim(strings.Join(parts[1:], " "), `"`)
			}
		case "version":
			if len(parts) >= 2 {
				m.Version = strings.Trim(strings.Join(parts[1:], " "), `"`)
			}
		case "require", "requires":
			if len(parts) >= 2 {
				path := strings.Trim(parts[1], `"`)
				version := ""
				if len(parts) >= 3 {
					version = strings.Trim(parts[2], `"`)
				}
				m.Requires = append(m.Requires, Dependency{Path: path, Version: version})
			}
		default:
			// Implicit dependency: path version (no keyword)
			if len(parts) >= 2 {
				m.Requires = append(m.Requires, Dependency{
					Path:    strings.Trim(parts[0], `"`),
					Version: strings.Trim(parts[1], `"`),
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	return m, nil
}

// FindManifest searches upward from dir for a skink.mod file.
func FindManifest(dir string) (string, *Manifest, error) {
	for {
		candidate := filepath.Join(dir, "skink.mod")
		if _, err := os.Stat(candidate); err == nil {
			m, err := ParseManifest(candidate)
			if err != nil {
				return "", nil, err
			}
			return candidate, m, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil, nil
}
