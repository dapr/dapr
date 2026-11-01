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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

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

const (
	// readAttempts bounds the retries of a read refused by a sharing
	// violation, which a scanner on Windows causes for a few milliseconds
	// after a file is written.
	readAttempts   = 5
	readRetryDelay = 20 * time.Millisecond
)

// ErrLocked reports a file still held exclusively by another process after
// the read retries. The scan is incomplete rather than failed: a caller must
// retry it instead of reconciling the partial result, which would unload the
// resources the file holds.
var ErrLocked = errors.New("resource file held by another process")

// readFileFn is replaced in tests to inject read errors.
var readFileFn = os.ReadFile

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
		content, err := readFile(filepath.Join(dir, f.Name()))
		if err != nil {
			// File may have been deleted between the directory listing and
			// the read (e.g. during a rapid write/delete cycle or shutdown).
			// Skip it; the next FSWatcher event will re-trigger a scan.
			//
			// Windows reports the same race as a delete pending file rather
			// than a missing one, since the watcher's own handle keeps the
			// entry in the listing until it closes.
			if errors.Is(err, fs.ErrNotExist) || isDeletePending(err) {
				continue
			}
			if isLocked(err) {
				return nil, fmt.Errorf("%w: %s: %w", ErrLocked, filepath.Join(dir, f.Name()), err)
			}
			return nil, fmt.Errorf("failed to read file %s: %w", filepath.Join(dir, f.Name()), err)
		}
		entry.Files = append(entry.Files, FileEntry{Name: f.Name(), Content: content})
	}
	return entry, nil
}

// readFile reads the file, retrying briefly while another process holds it
// exclusively.
func readFile(path string) ([]byte, error) {
	var content []byte
	var err error
	for attempt := range readAttempts {
		if attempt > 0 {
			time.Sleep(readRetryDelay)
		}
		content, err = readFileFn(path)
		if err == nil || !isLocked(err) {
			return content, err
		}
	}
	return nil, err
}
