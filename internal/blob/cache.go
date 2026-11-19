package blob

import (
	"container/list"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cache is a read-through cache of another store on local disk, evicting
// least recently used files beyond maxBytes. Keys are immutable by
// convention (content hashes, snapshot ids), so entries never go stale.
// Puts and deletes go to the backing store; Get fills the cache.
type Cache struct {
	Store
	dir string
	max int64

	mu      sync.Mutex
	size    int64
	lru     *list.List // front = most recently used; values are *cacheEntry
	entries map[string]*list.Element
}

type cacheEntry struct {
	key  string
	size int64
}

// NewCache wraps backing with a cache in dir, indexing files already there.
func NewCache(backing Store, dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	c := &Cache{Store: backing, dir: dir, max: maxBytes, lru: list.New(), entries: map[string]*list.Element{}}
	type found struct {
		key  string
		size int64
		mod  time.Time
	}
	var files []found
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return os.Remove(p) // left over from a crash
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, found{filepath.ToSlash(rel), info.Size(), info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files {
		c.entries[f.key] = c.lru.PushBack(&cacheEntry{f.key, f.size})
		c.size += f.size
	}
	c.evict()
	return c, nil
}

func (c *Cache) path(key string) string { return filepath.Join(c.dir, filepath.FromSlash(key)) }

// Get implements Store, serving from disk when possible.
func (c *Cache) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	c.mu.Lock()
	el, hit := c.entries[key]
	if hit {
		c.lru.MoveToFront(el)
	}
	c.mu.Unlock()
	if hit {
		f, err := os.Open(c.path(key))
		if err == nil {
			now := time.Now()
			_ = os.Chtimes(f.Name(), now, now) // keep LRU order across restarts
			return f, nil
		}
		c.forget(key) // removed behind our back; refetch
	}

	r, err := c.Store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := c.fill(key, r); err != nil {
		return nil, err
	}
	return os.Open(c.path(key))
}

// fill copies r into the cache under key.
func (c *Cache) fill(key string, r io.Reader) error {
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok { // filled concurrently
		c.size -= el.Value.(*cacheEntry).size
		c.lru.Remove(el)
	}
	c.entries[key] = c.lru.PushFront(&cacheEntry{key, n})
	c.size += n
	c.evict()
	return nil
}

// evict removes least recently used files until the cache fits. Callers
// hold c.mu. A file being read stays readable after removal on Unix.
func (c *Cache) evict() {
	for c.size > c.max && c.lru.Len() > 0 {
		el := c.lru.Back()
		e := el.Value.(*cacheEntry)
		c.lru.Remove(el)
		delete(c.entries, e.key)
		c.size -= e.size
		_ = os.Remove(c.path(e.key)) // a failure only leaves an orphan file
	}
}

func (c *Cache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.size -= el.Value.(*cacheEntry).size
		c.lru.Remove(el)
		delete(c.entries, key)
	}
}

// Delete implements Store, removing the key from both the cache and the
// backing store.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	c.forget(key)
	if err := os.Remove(c.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return c.Store.Delete(ctx, key)
}

// Size reports the bytes currently cached.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}
