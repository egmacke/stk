package git

import (
	"os"
	"path/filepath"
)

func pathExists(base, p string) bool {
	if p == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	_, err := os.Stat(p)
	return err == nil
}
