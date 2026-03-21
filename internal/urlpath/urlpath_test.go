package urlpath

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "file"},
		{"plain", "photo.png", "photo.png"},
		{"strips slashes", "a/b\\c", "abc"},
		{"strips control", "a\x00b", "ab"},
		{"only separators", "//", "file"},
		{"unicode kept", "файл.txt", "файл.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeFilename(tt.in); got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPathFromURL(t *testing.T) {
	t.Run("empty url", func(t *testing.T) {
		_, _, _, _, err := PathFromURL("", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error %q should mention empty", err.Error())
		}
	})

	t.Run("parse error", func(t *testing.T) {
		_, _, _, _, err := PathFromURL("http://[", "x")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("stable hash and path", func(t *testing.T) {
		u := "https://files.slack.com/files-pri/T123-F456/photo.png"
		a, b, c, filename, err := PathFromURL(u, "fallback")
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(u))
		hexHash := hex.EncodeToString(sum[:])
		if a != string(hexHash[0]) || b != string(hexHash[1]) || c != string(hexHash[2]) {
			t.Fatalf("prefix mismatch: got %s/%s/%s, want first 3 of %s", a, b, c, hexHash)
		}
		wantName := hexHash + "_" + "photo.png"
		if filename != wantName {
			t.Errorf("filename = %q; want %q", filename, wantName)
		}
	})

	t.Run("fallback when path empty", func(t *testing.T) {
		u := "https://example.com"
		_, _, _, filename, err := PathFromURL(u, "myfile.bin")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(filename, "_myfile.bin") {
			t.Errorf("filename %q should end with _myfile.bin", filename)
		}
	})

	t.Run("default file when no path and no fallback", func(t *testing.T) {
		u := "https://example.com"
		_, _, _, filename, err := PathFromURL(u, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(filename, "_file") {
			t.Errorf("filename %q should end with _file", filename)
		}
	})

	t.Run("dot path uses fallback", func(t *testing.T) {
		u := "https://example.com/."
		_, _, _, filename, err := PathFromURL(u, "doc.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(filename, "_doc.pdf") {
			t.Errorf("filename %q should end with _doc.pdf", filename)
		}
	})

	t.Run("filename has no path separators", func(t *testing.T) {
		u := "https://example.com/a/b/c"
		_, _, _, filename, err := PathFromURL(u, "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
			t.Errorf("filename must not contain slash: %q", filename)
		}
	})
}

func TestRelativePath(t *testing.T) {
	u := "https://files.slack.com/foo/bar.png"
	rel, err := RelativePath(u, "")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(rel, "/")
	if len(parts) != 4 {
		t.Fatalf("want 4 path parts, got %d: %q", len(parts), rel)
	}
	if len(parts[0]) != 1 || len(parts[1]) != 1 || len(parts[2]) != 1 {
		t.Errorf("expected single-char shards, got %v", parts[:3])
	}
	if !strings.Contains(parts[3], "_") {
		t.Errorf("last segment should be hash_filename: %q", parts[3])
	}

	t.Run("error from PathFromURL", func(t *testing.T) {
		_, err := RelativePath("", "")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
