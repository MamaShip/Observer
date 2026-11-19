// Package blob stores snapshot files and images: Wasabi (S3) in production,
// a local directory in development and tests. Object layout: plan.md §8.
package blob

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is returned by Get for a missing key.
var ErrNotFound = errors.New("blob: not found")

// Store is an object store with immutable-by-convention keys.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// AssetKey is where an image with the given sha256 is stored.
func AssetKey(sha [32]byte) string {
	h := hex.EncodeToString(sha[:])
	return "assets/" + h[:2] + "/" + h
}

// SnapshotKey is where one file of a snapshot is stored (name is "page.html" or "raw.html.gz").
func SnapshotKey(snapshotID, name string) string {
	return "snapshots/" + snapshotID + "/" + name
}

func validKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.Contains(key, "\\") {
		return fmt.Errorf("blob: invalid key %q", key)
	}
	return nil
}

// S3Config configures an S3-compatible store.
type S3Config struct {
	Endpoint  string // URL, e.g. https://s3.ap-southeast-1.wasabisys.com
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// S3 stores objects in an S3-compatible bucket.
type S3 struct {
	cl     *minio.Client
	bucket string
}

// NewS3 connects to the bucket. It does not make a request.
func NewS3(c S3Config) (*S3, error) {
	secure := !strings.HasPrefix(c.Endpoint, "http://")
	endpoint := strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: secure,
		Region: c.Region,
	})
	if err != nil {
		return nil, err
	}
	return &S3{cl: cl, bucket: c.Bucket}, nil
}

// Put implements Store.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := validKey(key); err != nil {
		return err
	}
	_, err := s.cl.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	obj, err := s.cl.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat surfaces a missing key now rather than on Read.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

// Delete implements Store.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	return s.cl.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// Dir stores objects as files under a directory (development and tests).
type Dir struct{ root string }

// NewDir creates the directory if needed.
func NewDir(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &Dir{root: root}, nil
}

func (d *Dir) path(key string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

// Put implements Store. Writes are atomic: readers never see a partial file.
func (d *Dir) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// Get implements Store.
func (d *Dir) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Delete implements Store.
func (d *Dir) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
