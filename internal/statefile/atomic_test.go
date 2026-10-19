package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteAndFailedRenameCleanTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := Write(path, []byte(`{"session":"complete"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"session":"complete"}` {
		t.Fatal("incomplete write", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state permissions", err)
	}
	target := filepath.Join(dir, "directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Write(target, []byte("must fail")); err == nil {
		t.Fatal("rename failure ignored")
	}
	entries, err := filepath.Glob(filepath.Join(dir, ".easyprofile-*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary file leaked", entries, err)
	}
}
