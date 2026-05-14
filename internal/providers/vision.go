package providers

import (
	"context"
)

type ImageInput struct {
	MediaType string
	Data      string
}

type VisionClient interface {
	Diagnose(ctx context.Context, userText string, image ImageInput) (string, error)
}
