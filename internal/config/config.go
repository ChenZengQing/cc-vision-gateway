package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ProxyHost string
	ProxyPort string

	TextProvider         string
	TextBaseURL          string
	TextAPIKey           string
	TextModel            string
	TextFallbackModel    string
	TextImageModel       string
	TextImageFastModel   string
	TextImageStrongModel string
	TextImageRouting     string
	TextTimeout          time.Duration
	TextAPIFormat        string

	ModelMapFile   string
	StrictModelMap bool

	VisionProvider     string
	VisionBaseURL      string
	VisionAPIKey       string
	VisionModel        string
	VisionTimeout      time.Duration
	VisionPromptMode   string
	VisionContextScope string
	VisionPreprocess   bool
	VisionMaxDimension int
	VisionJPEGQuality  int

	EnableImageCache    bool
	ImageCacheBackend   string
	ImageCachePath      string
	ImageCacheTTL       time.Duration
	MaxImageBytes       int64
	MaxConcurrentVision int
	ImageScanScope      string

	VisionFailureMode string
	LogLevel          slog.Level
}

func Load() (Config, error) {
	cfg := Config{
		ProxyHost:            env("PROXY_HOST", "127.0.0.1"),
		ProxyPort:            env("PROXY_PORT", "8787"),
		TextProvider:         env("TEXT_PROVIDER", "deepseek"),
		TextBaseURL:          strings.TrimRight(env("TEXT_BASE_URL", "https://api.deepseek.com/anthropic"), "/"),
		TextAPIKey:           os.Getenv("TEXT_API_KEY"),
		TextModel:            env("TEXT_MODEL", "deepseek-v4-pro"),
		TextFallbackModel:    env("TEXT_FALLBACK_MODEL", "deepseek-v4-flash"),
		TextImageModel:       os.Getenv("TEXT_IMAGE_MODEL"),
		TextImageFastModel:   env("TEXT_IMAGE_FAST_MODEL", "deepseek-v4-flash"),
		TextImageStrongModel: env("TEXT_IMAGE_STRONG_MODEL", "deepseek-v4-pro"),
		TextImageRouting:     env("TEXT_IMAGE_ROUTING", "auto"),
		TextTimeout:          durationEnv("TEXT_TIMEOUT", 120*time.Second),
		TextAPIFormat:        env("TEXT_API_FORMAT", "anthropic_compatible"),
		ModelMapFile:         env("MODEL_MAP_FILE", "config/model-map.json"),
		StrictModelMap:       boolEnv("STRICT_MODEL_MAP", false),
		VisionProvider:       env("VISION_PROVIDER", "qwen"),
		VisionBaseURL:        strings.TrimRight(env("VISION_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"), "/"),
		VisionAPIKey:         os.Getenv("VISION_API_KEY"),
		VisionModel:          env("VISION_MODEL", "qwen3-vl-flash"),
		VisionTimeout:        durationEnv("VISION_TIMEOUT", 8*time.Second),
		VisionPromptMode:     env("VISION_PROMPT_MODE", "fast"),
		VisionContextScope:   env("VISION_CONTEXT_SCOPE", "last_user"),
		VisionPreprocess:     boolEnv("VISION_PREPROCESS", true),
		VisionMaxDimension:   intEnv("VISION_MAX_DIMENSION", 1024),
		VisionJPEGQuality:    intEnv("VISION_JPEG_QUALITY", 78),
		EnableImageCache:     boolEnv("ENABLE_IMAGE_CACHE", true),
		ImageCacheBackend:    env("IMAGE_CACHE_BACKEND", "bolt"),
		ImageCachePath:       env("IMAGE_CACHE_PATH", "data/image-cache.bolt"),
		ImageCacheTTL:        durationEnv("IMAGE_CACHE_TTL", 168*time.Hour),
		MaxImageBytes:        int64Env("MAX_IMAGE_BYTES", 10*1024*1024),
		MaxConcurrentVision:  intEnv("MAX_CONCURRENT_VISION", 4),
		ImageScanScope:       env("IMAGE_SCAN_SCOPE", "last_user"),
		VisionFailureMode:    env("VISION_FAILURE_MODE", "fallback"),
		LogLevel:             levelEnv("LOG_LEVEL", slog.LevelInfo),
	}

	if cfg.TextAPIKey == "" {
		return Config{}, errors.New("TEXT_API_KEY is required")
	}
	if cfg.VisionAPIKey == "" {
		return Config{}, errors.New("VISION_API_KEY is required")
	}
	if cfg.TextAPIFormat != "anthropic_compatible" && cfg.TextAPIFormat != "openai_compatible" {
		return Config{}, errors.New("TEXT_API_FORMAT must be anthropic_compatible or openai_compatible")
	}
	if cfg.VisionFailureMode != "error" && cfg.VisionFailureMode != "fallback" {
		return Config{}, errors.New("VISION_FAILURE_MODE must be error or fallback")
	}
	if cfg.EnableImageCache && cfg.ImageCacheBackend == "bolt" {
		if err := os.MkdirAll(filepath.Dir(cfg.ImageCachePath), 0o755); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func intEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func int64Env(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func levelEnv(key string, fallback slog.Level) slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return fallback
	}
}
