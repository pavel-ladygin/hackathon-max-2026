package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSnapshotFilesSortsByNumericSkip(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"page-100.json", "page-0.json", "page-20.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(`{"values":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := snapshotFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(directory, "page-0.json"),
		filepath.Join(directory, "page-20.json"),
		filepath.Join(directory, "page-100.json"),
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}
