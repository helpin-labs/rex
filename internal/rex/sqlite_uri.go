package rex

import (
	"net/url"
	"path/filepath"
	"strings"
)

// SQLite file URIs need escaped names and a leading slash before Windows drives.
func sqliteFileURI(path string) string {
	name := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return (&url.URL{Scheme: "file", Path: name}).String()
}
