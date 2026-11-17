package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	DataDir string
	Addr    string
	TLSCert string
	TLSKey  string
	HTTP    bool
	Hosts   []string
	Version string
}

func (c *Config) Normalize() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("必须指定数据目录 -data")
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return err
	}
	c.DataDir = abs
	if c.Addr == "" {
		c.Addr = ":9443"
	}
	for _, d := range []string{c.DataDir, c.ObjectsDir(), c.TempDir(), c.CertDir(), c.LogDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls-cert 和 tls-key 必须同时提供")
	}
	return nil
}

func (c *Config) DBPath() string      { return filepath.Join(c.DataDir, "meta.db") }
func (c *Config) ObjectsDir() string  { return filepath.Join(c.DataDir, "objects") }
func (c *Config) TempDir() string     { return filepath.Join(c.DataDir, "tmp") }
func (c *Config) CertDir() string     { return filepath.Join(c.DataDir, "certs") }
func (c *Config) LogDir() string      { return filepath.Join(c.DataDir, "logs") }
