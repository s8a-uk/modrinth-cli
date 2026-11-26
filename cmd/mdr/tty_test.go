package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsTTYNonTerminals(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	for name, f := range map[string]*os.File{"devnull": devNull, "pipe": w, "file": file} {
		if isTTY(f) {
			t.Errorf("isTTY(%s) = true, want false", name)
		}
	}
}
