//go:build !windows
// +build !windows

package uri

func addLeadingSlash(dst, src []byte) []byte {
	// add leading slash for unix paths
	if len(src) == 0 || src[0] != '/' {
		dst = append(dst, '/')
	}

	return dst
}
