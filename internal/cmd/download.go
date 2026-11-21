package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// downloadOptions say how writeDownload publishes a file.
type downloadOptions struct {
	// overwrite replaces an existing file; without it an existing file is an error
	// (fs.ErrExist), decided when the file is published, not before.
	overwrite bool
	// mode is the permission of the file written.
	mode os.FileMode
}

// writeDownload writes body to path through a temporary file in the same directory, so a
// transfer that is cut short leaves no file that looks whole and no temporary file. Without
// overwrite the file is published with a hard link, which fails when the name exists, so two
// downloads to one name cannot both succeed.
func writeDownload(path string, body io.Reader, o downloadOptions) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kmctl-download-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(o.mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if o.overwrite {
		return os.Rename(tmp.Name(), path)
	}
	return os.Link(tmp.Name(), path)
}

// checkDownloadTarget fails before anything is asked of the cluster when the file cannot be
// written: its directory is missing, or the file exists and may not be replaced. It is a
// courtesy; writeDownload decides.
func checkDownloadTarget(path string, overwrite bool) error {
	if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
		return fmt.Errorf("the directory %s does not exist", filepath.Dir(path))
	}
	_, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !overwrite:
		return fmt.Errorf("%s: %w", path, fs.ErrExist)
	}
	return nil
}

// clean removes the control characters from text a gateway or operator supplied before it
// is printed, so a name cannot move the cursor or recolor the terminal.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, s)
}
