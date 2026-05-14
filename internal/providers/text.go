package providers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"cc-vision-gateway/internal/config"
)

type TextClient struct {
	cfg    config.Config
	client *http.Client
}

type UpstreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

func NewTextClient(cfg config.Config) *TextClient {
	return &TextClient{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.TextTimeout},
	}
}

func (c *TextClient) Do(ctx context.Context, path string, body []byte, headers http.Header) (*UpstreamResponse, error) {
	if c.cfg.TextAPIFormat != "anthropic_compatible" {
		return nil, ErrOpenAICompatibleNotImplemented
	}
	url := strings.TrimRight(c.cfg.TextBaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyForwardHeaders(req.Header, headers)
	req.Header.Set("Authorization", "Bearer "+c.cfg.TextAPIKey)
	req.Header.Set("x-api-key", c.cfg.TextAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	return &UpstreamResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

func copyForwardHeaders(dst, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		switch lower {
		case "host", "content-length", "authorization", "x-api-key", "anthropic-api-key":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
