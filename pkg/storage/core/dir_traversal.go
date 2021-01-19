package core

import (
	"context"
	"os"
)

// ListValidFilesInFilesystem discovers files in the given AferoContext that has a
// ContentType that contentTyper recognizes, and is not a path that is excluded by
// pathExcluder.
func ListValidFilesInFilesystem(ctx context.Context, fs AferoContext, contentTyper ContentTyper, pathExcluder PathExcluder) (files []string, err error) {
	err = fs.Walk(ctx, "", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only include valid files
		if !info.IsDir() && IsValidFileInFilesystem(ctx, fs, contentTyper, pathExcluder, path) {
			files = append(files, path)
		}
		return nil
	})
	return
}

// IsValidFileInFilesystem checks if file (a relative path) has a ContentType
// that contentTyper recognizes, and is not a path that is excluded by pathExcluder.
func IsValidFileInFilesystem(ctx context.Context, fs AferoContext, contentTyper ContentTyper, pathExcluder PathExcluder, file string) bool {
	// return false if this path should be excluded
	if pathExcluder.ShouldExcludePath(ctx, fs, file) {
		return false
	}

	// If the content type is valid for this path, err == nil => return true
	_, err := contentTyper.ContentTypeForPath(ctx, fs, file)
	return err == nil
}