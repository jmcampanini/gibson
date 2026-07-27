package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

const defaultBind = "127.0.0.1"

type Config struct {
	Server   Server                 `toml:"server"`
	Sessions map[string]SessionType `toml:"sessions"`
}

type LoadResult struct {
	Config      Config
	UnknownKeys []string
}

type Server struct {
	Port  int    `toml:"port"`
	Bind  string `toml:"bind"`
	PiBin string `toml:"pi_bin"`
}

type SessionType struct {
	Description string   `toml:"description"`
	Model       string   `toml:"model"`
	Thinking    string   `toml:"thinking"`
	ExtraArgs   []string `toml:"extra_args"`
}

func Load(checkoutRoot string) (LoadResult, error) {
	path := filepath.Join(checkoutRoot, "gibson.toml")
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LoadResult{}, fmt.Errorf("gibson.toml not found at %s", path)
		}
		return LoadResult{}, fmt.Errorf("read gibson.toml at %s: %w", path, err)
	}

	cfg := Config{Sessions: make(map[string]SessionType)}
	metadata, err := toml.Decode(string(contents), &cfg)
	if err != nil {
		return LoadResult{}, fmt.Errorf("gibson.toml: %w", err)
	}
	if cfg.Server.Bind == "" {
		cfg.Server.Bind = defaultBind
	}
	if err := cfg.validate(metadata.IsDefined("server", "port")); err != nil {
		return LoadResult{}, err
	}

	unknownKeys := make([]string, 0, len(metadata.Undecoded()))
	for _, key := range metadata.Undecoded() {
		unknownKeys = append(unknownKeys, key.String())
	}
	sort.Strings(unknownKeys)

	return LoadResult{Config: cfg, UnknownKeys: unknownKeys}, nil
}

func (c Config) Validate() error {
	return c.validate(c.Server.Port != 0)
}

func (c Config) validate(portDefined bool) error {
	if !portDefined {
		return errors.New("gibson.toml: server.port is required")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("gibson.toml: server.port must be 1-65535, got %d", c.Server.Port)
	}

	names := make([]string, 0, len(c.Sessions))
	for name := range c.Sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if c.Sessions[name].Description == "" {
			return fmt.Errorf("gibson.toml: sessions.%s.description is required", name)
		}
	}
	return nil
}
