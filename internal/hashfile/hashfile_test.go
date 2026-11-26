package hashfile_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/s8a-uk/modrinth-cli/internal/hashfile"
)

func write(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.jar")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKnownSums(t *testing.T) {
	cases := []struct {
		data         string
		sha1, sha512 string
	}{
		{"", "da39a3ee5e6b4b0d3255bfef95601890afd80709",
			"cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"},
		{"abc", "a9993e364706816aba3e25717850c26c9cd0d89d",
			"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
	}
	for _, tc := range cases {
		s, err := hashfile.File(context.Background(), write(t, []byte(tc.data)))
		if err != nil {
			t.Fatal(err)
		}
		if s.SHA1 != tc.sha1 || s.SHA512 != tc.sha512 || s.Size != int64(len(tc.data)) {
			t.Errorf("%q: got %+v", tc.data, s)
		}
		w := hashfile.NewWriter()
		_, _ = w.Write([]byte(tc.data))
		if w.Sums() != s {
			t.Errorf("%q: Writer %+v != File %+v", tc.data, w.Sums(), s)
		}
	}
}

func TestLargeFile(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB, several read chunks
	s, err := hashfile.File(context.Background(), write(t, data))
	if err != nil {
		t.Fatal(err)
	}
	h1, h512 := sha1.Sum(data), sha512.Sum512(data)
	if s.SHA1 != hex.EncodeToString(h1[:]) || s.SHA512 != hex.EncodeToString(h512[:]) || s.Size != 1<<20 {
		t.Errorf("got %+v", s)
	}
}

func TestCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hashfile.File(ctx, write(t, []byte("abc"))); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestMissingFile(t *testing.T) {
	if _, err := hashfile.File(context.Background(), filepath.Join(t.TempDir(), "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}
