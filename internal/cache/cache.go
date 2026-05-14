package cache

import (
	"cc-vision-gateway/internal/config"
	"errors"
	"time"
)

type ImageCache interface {
	Get(key string) (string, bool)
	Set(key, value string, ttl time.Duration) error
	Close() error
}

func Open(cfg config.Config) (ImageCache, error) {
	if !cfg.EnableImageCache {
		return Noop{}, nil
	}
	switch cfg.ImageCacheBackend {
	case "bolt":
		return OpenBolt(cfg.ImageCachePath)
	case "memory":
		return NewMemory(), nil
	default:
		return nil, errors.New("unsupported IMAGE_CACHE_BACKEND")
	}
}

type Noop struct{}

func (Noop) Get(string) (string, bool)               { return "", false }
func (Noop) Set(string, string, time.Duration) error { return nil }
func (Noop) Close() error                            { return nil }
