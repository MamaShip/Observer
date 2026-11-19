package blob

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// countingStore counts Gets that reach the backing store.
type countingStore struct {
	Store
	gets atomic.Int32
}

func (c *countingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	c.gets.Add(1)
	return c.Store.Get(ctx, key)
}

func read(t *testing.T, s Store, key string) string {
	t.Helper()
	r, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%s): %v", key, err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCache(t *testing.T) {
	ctx := context.Background()
	dir, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backing := &countingStore{Store: dir}
	cacheDir := t.TempDir()
	c, err := NewCache(backing, cacheDir, 250)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a/1", "a/2", "a/3"} {
		data := strings.Repeat(k[2:], 100)
		if err := c.Put(ctx, k, bytes.NewReader([]byte(data)), 100, ""); err != nil {
			t.Fatal(err)
		}
	}

	// Miss, then hit.
	if got := read(t, c, "a/1"); got != strings.Repeat("1", 100) {
		t.Fatalf("a/1 = %q", got)
	}
	read(t, c, "a/1")
	if n := backing.gets.Load(); n != 1 {
		t.Fatalf("backing reads = %d, want 1", n)
	}

	// Fill to the cap; the least recently used entry goes.
	read(t, c, "a/2")
	read(t, c, "a/1") // a/1 is now more recent than a/2
	read(t, c, "a/3") // 300 bytes > 250: evict a/2
	if c.Size() != 200 {
		t.Errorf("cache size = %d, want 200", c.Size())
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "a", "2")); !os.IsNotExist(err) {
		t.Error("a/2 was not evicted")
	}
	before := backing.gets.Load()
	read(t, c, "a/1")
	if backing.gets.Load() != before {
		t.Error("a/1 was evicted instead of a/2")
	}

	// A new Cache over the same directory finds the files.
	c2, err := NewCache(backing, cacheDir, 250)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Size() != 200 {
		t.Errorf("reopened cache size = %d", c2.Size())
	}

	// Delete removes both copies.
	if err := c2.Delete(ctx, "a/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Get(ctx, "a/1"); err == nil {
		t.Error("deleted key still readable")
	}
}
