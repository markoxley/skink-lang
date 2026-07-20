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

// Package pkgmanager implements dependency fetching for the Skink package
// ecosystem. It downloads packages from a central repository as ZIP archives
// and extracts them into the local vendor directory.
package pkgmanager

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/skink-lang/compiler/resolver"
)

// Dependency represents a parsed package requirement.
type Dependency struct {
	Path    string // e.g. "github.com/user/repo"
	Version string // e.g. "v1.0.0"
}

// ParseModFile parses a skink.mod file content and returns its dependencies.
// It delegates to the shared resolver manifest parser for full syntax support.
func ParseModFile(content string) ([]Dependency, error) {
	m, err := resolver.ParseManifestContent(content)
	if err != nil {
		return nil, err
	}
	deps := make([]Dependency, len(m.Requires))
	for i, r := range m.Requires {
		deps[i] = Dependency{Path: r.Path, Version: r.Version}
	}
	return deps, nil
}

// DownloadAndUnpack fetches a dependency zip and unpacks it into the local cache.
func DownloadAndUnpack(dep Dependency, cacheDir string) error {
	// Construct Github Zip URL if it matches github.com/user/repo
	url := ""
	if strings.HasPrefix(dep.Path, "github.com/") {
		parts := strings.Split(dep.Path, "/")
		if len(parts) >= 3 {
			user := parts[1]
			repo := parts[2]
			url = fmt.Sprintf("https://github.com/%s/%s/archive/refs/tags/%s.zip", user, repo, dep.Version)
		}
	}

	if url == "" {
		return fmt.Errorf("unsupported package path: %s", dep.Path)
	}

	fmt.Printf("Fetching %s (%s)...\n", dep.Path, dep.Version)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// If tag download failed, try as a branch/commit (without refs/tags)
		parts := strings.Split(dep.Path, "/")
		user := parts[1]
		repo := parts[2]
		url2 := fmt.Sprintf("https://github.com/%s/%s/archive/%s.zip", user, repo, dep.Version)
		resp, err = http.Get(url2)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			return fmt.Errorf("download failed for %s", url)
		}
		defer resp.Body.Close()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// Unzip the package into cacheDir/dep.Path
	targetDir := filepath.Join(cacheDir, dep.Path)
	// Clear existing directory first to ensure clean state
	os.RemoveAll(targetDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("creating target dir: %w", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("invalid zip file: %w", err)
	}

	for _, file := range zipReader.File {
		// GitHub tags always contain a single top-level directory like "repo-version"
		// Strip the top-level directory from the entry path to unpack files directly.
		pathParts := strings.Split(file.Name, "/")
		if len(pathParts) <= 1 {
			continue // skip top-level directory itself
		}
		relPath := filepath.Join(pathParts[1:]...)
		relPath = filepath.Clean(relPath)
		if strings.HasPrefix(relPath, "..") {
			continue // skip paths that escape the archive
		}

		filePath := filepath.Join(targetDir, relPath)
		if file.FileInfo().IsDir() {
			os.MkdirAll(filePath, file.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return err
		}

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}

		srcFile, err := file.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		_, err = io.Copy(dstFile, srcFile)
		srcFile.Close()
		dstFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// FetchAll parses skink.mod and downloads all dependencies.
func FetchAll(cwd string) error {
	modPath := filepath.Join(cwd, "skink.mod")
	m, err := resolver.ParseManifest(modPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skink.mod not found in %s", cwd)
		}
		return fmt.Errorf("parsing skink.mod: %w", err)
	}

	if len(m.Requires) == 0 {
		fmt.Println("No dependencies found in skink.mod.")
		return nil
	}

	// Use project-local .skink/cache as local cache directory
	cacheDir := filepath.Join(cwd, ".skink", "cache")
	for _, rdep := range m.Requires {
		dep := Dependency{Path: rdep.Path, Version: rdep.Version}
		if err := DownloadAndUnpack(dep, cacheDir); err != nil {
			return fmt.Errorf("fetching %s: %w", dep.Path, err)
		}
	}

	fmt.Println("All dependencies fetched successfully.")
	return nil
}
