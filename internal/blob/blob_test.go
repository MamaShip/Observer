package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("test/%d/obj.bin", time.Now().UnixNano())
	data := make([]byte, 100<<10)
	_, _ = rand.Read(data)

	if _, err := s.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: %v, want ErrNotFound", err)
	}
	if err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
	r, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %v (got %d bytes)", err, len(got))
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
	for _, bad := range []string{"", "/abs", "a/../b", `a\b`} {
		if err := s.Put(ctx, bad, bytes.NewReader(nil), 0, ""); err == nil {
			t.Errorf("Put(%q) accepted an invalid key", bad)
		}
	}
}

func TestDir(t *testing.T) {
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, d)
}

// TestS3 runs against a real bucket when TEST_S3_* is set, e.g. on the VPS:
//
//	go test -c ./internal/blob && TEST_S3_ENDPOINT=$S3_ENDPOINT ... ./blob.test -test.run S3
func TestS3(t *testing.T) {
	c := S3Config{
		Endpoint: os.Getenv("TEST_S3_ENDPOINT"), Region: os.Getenv("TEST_S3_REGION"), Bucket: os.Getenv("TEST_S3_BUCKET"),
		AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("TEST_S3_SECRET_KEY"),
	}
	if c.Endpoint == "" || c.Bucket == "" {
		t.Skip("TEST_S3_ENDPOINT / TEST_S3_BUCKET not set")
	}
	s, err := NewS3(c)
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, s)
}

func TestKeys(t *testing.T) {
	var sha [32]byte
	sha[0], sha[31] = 0xab, 0x01
	if k := AssetKey(sha); k != "assets/ab/ab"+strings.Repeat("0", 60)+"01" {
		t.Fatalf("AssetKey = %s", k)
	}
	if k := SnapshotKey("0193", "page.html"); k != "snapshots/0193/page.html" {
		t.Fatalf("SnapshotKey = %s", k)
	}
}
