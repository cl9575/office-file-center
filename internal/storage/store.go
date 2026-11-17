package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"lanfile/internal/model"
)

var ErrTooLarge = errors.New("file too large")

type Saved struct {
	ID     string
	Size   int64
	SHA256 string
}

type Store struct {
	ObjectsDir string
	TempDir    string
	Guard      sync.RWMutex
}

func New(objectsDir, tempDir string) *Store {
	return &Store{ObjectsDir: objectsDir, TempDir: tempDir}
}

func (s *Store) ObjectPath(id string) string {
	if len(id) < 2 {
		return filepath.Join(s.ObjectsDir, "_invalid", id)
	}
	return filepath.Join(s.ObjectsDir, id[:2], id)
}

func (s *Store) Save(r io.Reader, maxSize int64) (Saved, error) {
	tmp, err := os.CreateTemp(s.TempDir, "upload-*.tmp")
	if err != nil {
		return Saved{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(r, maxSize+1))
	if err != nil {
		tmp.Close()
		return Saved{}, err
	}
	if n > maxSize {
		tmp.Close()
		return Saved{}, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Saved{}, err
	}
	if err := tmp.Close(); err != nil {
		return Saved{}, err
	}
	id := model.NewID()
	dst := s.ObjectPath(id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Saved{}, err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return Saved{}, err
	}
	return Saved{ID: id, Size: n, SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}

func (s *Store) Open(id string) (*os.File, error) {
	return os.Open(s.ObjectPath(id))
}

func (s *Store) Exists(id string) bool {
	info, err := os.Stat(s.ObjectPath(id))
	return err == nil && info.Mode().IsRegular()
}

func (s *Store) ObjectInfo(id string) (int64, error) {
	info, err := os.Stat(s.ObjectPath(id))
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Store) DeleteObject(id string) error {
	err := os.Remove(s.ObjectPath(id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) CleanupTemp(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(s.TempDir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "upload-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(s.TempDir, e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func (s *Store) ListObjectIDs() ([]string, error) {
	var ids []string
	entries, err := os.ReadDir(s.ObjectsDir)
	if err != nil {
		return nil, err
	}
	for _, dir := range entries {
		if !dir.IsDir() || len(dir.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.ObjectsDir, dir.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			ids = append(ids, f.Name())
		}
	}
	return ids, nil
}

func (s *Store) Verify(id string, size int64, wantSHA string) error {
	f, err := s.Open(id)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != size {
		return errors.New("文件大小不一致")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantSHA) {
		return errors.New("文件摘要不一致")
	}
	return nil
}

func (s *Store) CopyTo(id, dstDir string) error {
	src := s.ObjectPath(id)
	dst := filepath.Join(dstDir, id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

func (s *Store) FreeSpace() (int64, error) {
	p, err := windows.UTF16PtrFromString(s.ObjectsDir)
	if err != nil {
		return 0, err
	}
	var freeAvailable, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvailable, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(freeAvailable), nil
}
