package anthropic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"cc-vision-gateway/internal/cache"
	"cc-vision-gateway/internal/config"
	"cc-vision-gateway/internal/providers"
)

type RewriteStats struct {
	ImageCount      int
	CacheHits       int
	VisionLatencyMS int64
	VisionFailures  int
}

type VisionSemaphore chan struct{}

func NewVisionSemaphore(limit int) VisionSemaphore {
	if limit < 1 {
		limit = 1
	}
	return make(chan struct{}, limit)
}

func RewriteImages(ctx context.Context, req map[string]any, cfg config.Config, vision providers.VisionClient, imageCache cache.ImageCache, sem VisionSemaphore) (RewriteStats, error) {
	userText := collectVisionUserText(req, cfg)
	messages, ok := req["messages"].([]any)
	if !ok {
		return RewriteStats{}, nil
	}
	targetMessageIndex := -1
	if cfg.ImageScanScope == "last_user" {
		targetMessageIndex = lastUserMessageIndex(messages)
	}

	var stats RewriteStats
	for messageIndex, rawMessage := range messages {
		if targetMessageIndex >= 0 && messageIndex != targetMessageIndex {
			continue
		}
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for blockIndex, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok || block["type"] != "image" {
				continue
			}
			image, err := extractImage(block)
			if err != nil {
				return stats, err
			}
			if int64(len(image.Data)*3/4) > cfg.MaxImageBytes {
				return stats, fmt.Errorf("image exceeds MAX_IMAGE_BYTES")
			}
			image = preprocessImage(image, cfg)
			stats.ImageCount++
			visionStart := time.Now()
			diagnosis, cacheHit, err := diagnoseWithCache(ctx, cfg, vision, imageCache, sem, userText, image)
			stats.VisionLatencyMS += time.Since(visionStart).Milliseconds()
			if err != nil {
				if cfg.VisionFailureMode == "fallback" {
					stats.VisionFailures++
					diagnosis = "[图片解析失败]\n视觉模型未能在时限内完成解析。目标文本模型不能读取原图，请只基于用户文字回答；如果用户的问题依赖图片细节，请简短说明需要重试或让用户补充图片描述。"
				} else {
					return stats, err
				}
			}
			if cacheHit {
				stats.CacheHits++
			}
			content[blockIndex] = map[string]any{
				"type": "text",
				"text": wrapDiagnosis(stats.ImageCount, messageIndex, cfg, diagnosis),
			}
		}
		message["content"] = content
	}
	return stats, nil
}

func extractImage(block map[string]any) (providers.ImageInput, error) {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return providers.ImageInput{}, errors.New("image block missing source")
	}
	if source["type"] != "base64" {
		return providers.ImageInput{}, errors.New("only base64 image source is supported")
	}
	mediaType, _ := source["media_type"].(string)
	data, _ := source["data"].(string)
	if mediaType == "" || data == "" {
		return providers.ImageInput{}, errors.New("image block missing media_type or data")
	}
	return providers.ImageInput{MediaType: mediaType, Data: data}, nil
}

func diagnoseWithCache(ctx context.Context, cfg config.Config, vision providers.VisionClient, imageCache cache.ImageCache, sem VisionSemaphore, userText string, image providers.ImageInput) (string, bool, error) {
	key := cacheKey(image, userText)
	if value, ok := imageCache.Get(key); ok {
		return value, true, nil
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return "", false, ctx.Err()
	}

	visionCtx, cancel := context.WithTimeout(ctx, cfg.VisionTimeout)
	defer cancel()
	value, err := vision.Diagnose(visionCtx, userText, image)
	if err != nil {
		return "", false, err
	}
	if err := imageCache.Set(key, value, cfg.ImageCacheTTL); err != nil {
		return "", false, err
	}
	return value, false, nil
}

func cacheKey(image providers.ImageInput, userText string) string {
	promptHash := sha256.Sum256([]byte(normalize(userText)))
	raw := image.MediaType + ":" + image.Data + ":vision_prompt_v1:" + hex.EncodeToString(promptHash[:])
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func wrapDiagnosis(imageNumber, messageIndex int, cfg config.Config, diagnosis string) string {
	return fmt.Sprintf(`[图片诊断上下文开始]
图片序号：%d
来源消息：user message index %d
Vision provider：%s
Vision model：%s

%s
[图片诊断上下文结束]`, imageNumber, messageIndex, cfg.VisionProvider, cfg.VisionModel, strings.TrimSpace(diagnosis))
}

func collectVisionUserText(req map[string]any, cfg config.Config) string {
	messages, ok := req["messages"].([]any)
	if !ok {
		return ""
	}
	if cfg.VisionContextScope == "last_user" {
		if idx := lastUserMessageIndex(messages); idx >= 0 {
			if message, ok := messages[idx].(map[string]any); ok {
				return collectMessageText(message)
			}
		}
		return ""
	}
	var b strings.Builder
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok || message["role"] != "user" {
			continue
		}
		switch content := message["content"].(type) {
		case string:
			b.WriteString(content)
			b.WriteString("\n")
		case []any:
			for _, rawBlock := range content {
				block, ok := rawBlock.(map[string]any)
				if !ok || block["type"] != "text" {
					continue
				}
				text, _ := block["text"].(string)
				if text != "" {
					b.WriteString(text)
					b.WriteString("\n")
				}
			}
		}
	}
	return b.String()
}

func LatestUserText(req map[string]any) string {
	messages, ok := req["messages"].([]any)
	if !ok {
		return ""
	}
	idx := lastUserMessageIndex(messages)
	if idx < 0 {
		return ""
	}
	message, ok := messages[idx].(map[string]any)
	if !ok {
		return ""
	}
	return collectMessageText(message)
}

func lastUserMessageIndex(messages []any) int {
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := messages[i].(map[string]any)
		if ok && message["role"] == "user" {
			return i
		}
	}
	return -1
}

func collectMessageText(message map[string]any) string {
	switch content := message["content"].(type) {
	case string:
		return content
	case []any:
		var b strings.Builder
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if text != "" {
				b.WriteString(text)
				b.WriteString("\n")
			}
		}
		return b.String()
	default:
		return ""
	}
}
