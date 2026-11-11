package review

import (
	"fmt"
	"os"
	"path/filepath"
)

func resolveReviewRoot(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("repository directory is required")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("review repository: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("review target must be a directory: %s", root)
	}
	for candidate := root; ; candidate = filepath.Dir(candidate) {
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("locate repository root: %w", err)
		}
		if filepath.Dir(candidate) == candidate {
			break
		}
	}
	return root, nil
}
