package urlpath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// PathFromURL computes the mirror path components from url_private. Returns the first three
// characters of the SHA256 hex hash (a, b, c) and a filename of the form "<fullhash>_<base>"
// so the path is unique and collision-free. fallbackName is used when the URL has no path
// segment or the segment is empty or ".".
func PathFromURL(urlPrivate string, fallbackName string) (a, b, c, filename string, err error) {
	if urlPrivate == "" {
		return "", "", "", "", fmt.Errorf("url_private is empty")
	}
	hash := sha256.Sum256([]byte(urlPrivate))
	hexHash := hex.EncodeToString(hash[:])
	if len(hexHash) < 3 {
		return "", "", "", "", fmt.Errorf("hash too short")
	}
	a = string(hexHash[0])
	b = string(hexHash[1])
	c = string(hexHash[2])

	parsed, err := url.Parse(urlPrivate)
	if err != nil {
		return "", "", "", "", fmt.Errorf("parse url: %w", err)
	}
	base := path.Base(parsed.Path)
	if base == "" || base == "." {
		if fallbackName != "" {
			base = fallbackName
		} else {
			base = "file"
		}
	}
	filename = hexHash + "_" + SanitizeFilename(base)
	return a, b, c, filename, nil
}

// RelativePath returns the relative path "a/b/c/<hash>_filename" for the given url_private, using
// fallbackName when the URL has no usable path segment.
func RelativePath(urlPrivate string, fallbackName string) (string, error) {
	a, b, c, filename, err := PathFromURL(urlPrivate, fallbackName)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{a, b, c, filename}, "/"), nil
}

// SanitizeFilename removes or replaces path separators and control characters so the result
// is safe for use as a single path component.
func SanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	s := b.String()
	if s == "" {
		return "file"
	}
	return s
}
