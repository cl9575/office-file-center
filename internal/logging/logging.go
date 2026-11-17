package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

func Setup(dir string) (io.Closer, error) {
	path := filepath.Join(dir, "server.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 10<<20 {
		_ = os.Remove(path + ".3")
		_ = os.Rename(path+".2", path+".3")
		_ = os.Rename(path+".1", path+".2")
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return f, nil
}
