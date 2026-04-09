/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dirdata

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dapr/dapr/pkg/runtime/meta"
	kitstrings "github.com/dapr/kit/strings"
)

// DirDataLoader can load resources from pre-read directory data, avoiding
// repeated file system reads when multiple resource types need to be loaded
// from the same directories.
type DirDataLoader[T meta.Resource] interface {
	LoadFromDirData(data *DirData) ([]T, error)
}

// DirData holds pre-read YAML file contents from a set of directories.
// This allows multiple resource loaders to parse from the same file data
// without each independently opening and reading files from disk, which
// avoids file handle contention on Windows.
type DirData struct {
	Entries []DirEntry
}

// DirEntry holds the YAML files from a single directory.
type DirEntry struct {
	Dir   string
	Files []FileEntry
}

// FileEntry holds the name and content of a single YAML file.
type FileEntry struct {
	Name    string
	Content []byte
}

// ReadDirs reads all YAML files from the given directories into memory.
func ReadDirs(dirs []string) (*DirData, error) {
	data := &DirData{
		Entries: make([]DirEntry, 0, len(dirs)),
	}
	for _, dir := range dirs {
		entry, err := readDir(dir)
		if err != nil {
			return nil, err
		}
		data.Entries = append(data.Entries, *entry)
	}
	return data, nil
}

func readDir(dir string) (*DirEntry, error) {
	entry := &DirEntry{Dir: dir}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if !kitstrings.IsYaml(f.Name()) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", filepath.Join(dir, f.Name()), err)
		}
		entry.Files = append(entry.Files, FileEntry{Name: f.Name(), Content: content})
	}
	return entry, nil
}
