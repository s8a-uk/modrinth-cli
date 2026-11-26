// Package hashfile computes the sha1 and sha512 of files and of data being written.
package hashfile

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"
	"os"
)

// Sums are the hashes and size of some data.
type Sums struct {
	SHA1   string // lower-case hex, 40 digits
	SHA512 string // lower-case hex, 128 digits
	Size   int64
}

// chunk is the read size of File; ctx is checked between chunks.
const chunk = 256 << 10

// File reads the file once and returns both hashes. A cancelled ctx stops the read.
func File(ctx context.Context, path string) (Sums, error) {
	f, err := os.Open(path)
	if err != nil {
		return Sums{}, err
	}
	defer f.Close()
	w := NewWriter()
	buf := make([]byte, chunk)
	for {
		if err := ctx.Err(); err != nil {
			return Sums{}, err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
		}
		if err == io.EOF {
			return w.Sums(), nil
		}
		if err != nil {
			return Sums{}, err
		}
	}
}

// Writer hashes everything written to it (for verifying downloads).
type Writer struct {
	sha1   hash.Hash
	sha512 hash.Hash
	n      int64
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer {
	return &Writer{sha1: sha1.New(), sha512: sha512.New()}
}

// Write adds p to both hashes. It never fails.
func (w *Writer) Write(p []byte) (int, error) {
	w.sha1.Write(p)
	w.sha512.Write(p)
	w.n += int64(len(p))
	return len(p), nil
}

// Sums returns the hashes of the bytes written so far.
func (w *Writer) Sums() Sums {
	return Sums{
		SHA1:   hex.EncodeToString(w.sha1.Sum(nil)),
		SHA512: hex.EncodeToString(w.sha512.Sum(nil)),
		Size:   w.n,
	}
}
