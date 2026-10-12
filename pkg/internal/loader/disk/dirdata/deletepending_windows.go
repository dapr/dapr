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
	"syscall"
)

// isDeletePending reports whether err is Windows refusing to open a file which
// has been deleted but whose handles are not all closed yet.
//
// Deleting a file another process still holds open does not remove it on
// Windows. The file enters a delete pending state: it keeps appearing in the
// directory listing, but opening it fails with ERROR_ACCESS_DENIED until the
// last handle closes. A resource file being watched by dapr is exactly that
// case, so a delete observed mid scan surfaces here rather than as the
// fs.ErrNotExist a POSIX system would return.
func isDeletePending(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
