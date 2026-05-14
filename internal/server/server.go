package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"cc-vision-gateway/internal/anthropic"
	"cc-vision-gateway/internal/cache"
	"cc-vision-gateway/internal/config"
	"cc-vision-gateway/internal/providers"
	"cc-vision-gateway/internal/routing"
)

type Server struct {
	cfg      config.Config
	modelMap routing.ModelMap
	cache    cache.ImageCache
	text     *providers.TextClient
	vision   providers.VisionClient
	sem      anthropic.VisionSemaphore
	logger   *slog.Logger
}

func New(cfg config.Config, modelMap routing.ModelMap, imageCache cache.ImageCache, logger *slog.Logger) http.Handler {
	return &Server{
		cfg:      cfg,
		modelMap: modelMap,
		cache:    imageCache,
		text:     providers.NewTextClient(cfg),
		vision:   providers.NewQwenVision(cfg),
		sem:      anthropic.NewVisionSemaphore(cfg.MaxConcurrentVision),
		logger:   logger,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "cc-vision-gateway"})
	case r.Method == http.MethodGet && r.URL.Path == "/ready":
		s.ready(w)
	case r.Method == http.MethodGet && (r.URL.Path == "/v1/models" || r.URL.Path == "/models"):
		s.models(w)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		s.messages(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

func (s *Server) ready(w http.ResponseWriter) {
	if s.cfg.TextAPIKey == "" || s.cfg.VisionAPIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) models(w http.ResponseWriter) {
	entries := s.modelMap.Entries()
	models := make([]map[string]any, 0, len(entries)+1)
	seen := map[string]bool{}
	clientModels := make([]string, 0, len(entries))
	for clientModel := range entries {
		clientModels = append(clientModels, clientModel)
	}
	sort.Strings(clientModels)
	for _, clientModel := range clientModels {
		if clientModel == "" || seen[clientModel] {
			continue
		}
		seen[clientModel] = true
		models = append(models, map[string]any{
			"type":         "model",
			"id":           clientModel,
			"display_name": clientModel,
			"created_at":   "2024-01-01T00:00:00Z",
		})
	}
	if len(models) == 0 && s.cfg.TextModel != "" {
		models = append(models, map[string]any{
			"type":         "model",
			"id":           s.cfg.TextModel,
			"display_name": s.cfg.TextModel,
			"created_at":   "2024-01-01T00:00:00Z",
		})
	}
	firstID, lastID := "", ""
	if len(models) > 0 {
		firstID, _ = models[0]["id"].(string)
		lastID, _ = models[len(models)-1]["id"].(string)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":     models,
		"has_more": false,
		"first_id": firstID,
		"last_id":  lastID,
	})
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := requestID(r)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read request"})
		return
	}
	defer r.Body.Close()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}

	clientModel, _ := payload["model"].(string)
	upstreamModel, mapped := s.modelMap.Resolve(clientModel, s.cfg.TextFallbackModel, s.cfg.StrictModelMap)
	if upstreamModel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model is not mapped", "model": clientModel})
		return
	}

	stats, err := anthropic.RewriteImages(r.Context(), payload, s.cfg, s.vision, s.cache, s.sem)
	if err != nil {
		s.logger.Warn("vision rewrite failed", "request_id", requestID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if stats.ImageCount > 0 && s.cfg.TextImageModel != "" {
		upstreamModel = s.cfg.TextImageModel
	}
	if stats.ImageCount > 0 && s.cfg.TextImageRouting == "auto" {
		upstreamModel = s.routeImageModel(anthropic.LatestUserText(payload))
	}
	payload["model"] = upstreamModel

	outboundBody, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to encode upstream request"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.TextTimeout)
	defer cancel()
	upstreamStart := time.Now()
	upstream, err := s.text.Do(ctx, r.URL.Path, outboundBody, r.Header)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, providers.ErrOpenAICompatibleNotImplemented) {
			status = http.StatusNotImplemented
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	upstreamWaitMS := time.Since(upstreamStart).Milliseconds()
	defer upstream.Body.Close()

	copyHeaders(w.Header(), upstream.Header)
	w.WriteHeader(upstream.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	copyStart := time.Now()
	_, copyErr := copyFlush(w, upstream.Body, buf, flusher)
	streamCopyMS := time.Since(copyStart).Milliseconds()
	if copyErr != nil && !errors.Is(copyErr, context.Canceled) {
		s.logger.Warn("stream copy failed", "request_id", requestID, "error", copyErr)
	}

	s.logger.Info("request completed",
		"request_id", requestID,
		"client_model", clientModel,
		"upstream_model", upstreamModel,
		"model_mapped", mapped,
		"status", upstream.StatusCode,
		"image_count", stats.ImageCount,
		"cache_hits", stats.CacheHits,
		"vision_failures", stats.VisionFailures,
		"vision_latency_ms", stats.VisionLatencyMS,
		"text_upstream_wait_ms", upstreamWaitMS,
		"stream_copy_ms", streamCopyMS,
		"latency_ms", time.Since(start).Milliseconds(),
	)
}

func (s *Server) routeImageModel(userText string) string {
	text := strings.ToLower(userText)
	strongKeywords := []string{
		"修", "改", "代码", "实现", "布局", "组件", "报错", "错误", "debug", "bug",
		"fix", "implement", "code", "layout", "component", "error", "exception", "trace",
	}
	for _, keyword := range strongKeywords {
		if strings.Contains(text, keyword) {
			if s.cfg.TextImageStrongModel != "" {
				return s.cfg.TextImageStrongModel
			}
			return s.cfg.TextModel
		}
	}
	if s.cfg.TextImageFastModel != "" {
		return s.cfg.TextImageFastModel
	}
	if s.cfg.TextImageModel != "" {
		return s.cfg.TextImageModel
	}
	return s.cfg.TextModel
}

func copyFlush(dst http.ResponseWriter, src io.Reader, buf []byte, flusher http.Flusher) (int64, error) {
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if flusher != nil {
				flusher.Flush()
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		if lower == "content-length" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func requestID(r *http.Request) string {
	if value := r.Header.Get("x-request-id"); value != "" {
		return value
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
