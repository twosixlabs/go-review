package changes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ownedDirectory(storage, directory string) error {
	if !filepath.IsAbs(directory) || filepath.Dir(directory) != filepath.Clean(storage) ||
		!strings.HasPrefix(filepath.Base(directory), "comparison-") {
		return fmt.Errorf("not a directly owned comparison directory: %s", directory)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("comparison directory must not be a symlink")
	}
	return nil
}

// Discard is only for unpublished or retired artifacts, after their readers finish.
func Discard(storage string, c *Comparison) error {
	if c == nil {
		return nil
	}
	if err := ownedDirectory(storage, c.Directory); err != nil {
		return err
	}
	return os.RemoveAll(c.Directory)
}
