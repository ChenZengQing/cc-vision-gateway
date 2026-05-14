package anthropic

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"

	"cc-vision-gateway/internal/config"
	"cc-vision-gateway/internal/providers"
)

func preprocessImage(imageInput providers.ImageInput, cfg config.Config) providers.ImageInput {
	if !cfg.VisionPreprocess || cfg.VisionMaxDimension <= 0 {
		return imageInput
	}
	raw, err := base64.StdEncoding.DecodeString(imageInput.Data)
	if err != nil {
		return imageInput
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return imageInput
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return imageInput
	}

	maxDim := width
	if height > maxDim {
		maxDim = height
	}
	targetWidth, targetHeight := width, height
	if maxDim > cfg.VisionMaxDimension {
		targetWidth = width * cfg.VisionMaxDimension / maxDim
		targetHeight = height * cfg.VisionMaxDimension / maxDim
		if targetWidth < 16 {
			targetWidth = 16
		}
		if targetHeight < 16 {
			targetHeight = 16
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sy := bounds.Min.Y + y*height/targetHeight
		for x := 0; x < targetWidth; x++ {
			sx := bounds.Min.X + x*width/targetWidth
			dst.Set(x, y, flatten(src.At(sx, sy)))
		}
	}

	quality := cfg.VisionJPEGQuality
	if quality < 40 || quality > 95 {
		quality = 78
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return imageInput
	}
	encoded := base64.StdEncoding.EncodeToString(out.Bytes())
	if len(encoded) >= len(imageInput.Data) && maxDim <= cfg.VisionMaxDimension {
		return imageInput
	}
	return providers.ImageInput{
		MediaType: "image/jpeg",
		Data:      encoded,
	}
}

func flatten(c color.Color) color.Color {
	r, g, b, a := c.RGBA()
	if a == 0xffff {
		return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
	}
	alpha := float64(a) / 0xffff
	blend := func(v uint32) uint8 {
		value := alpha*float64(v>>8) + (1-alpha)*255
		return uint8(value)
	}
	return color.RGBA{R: blend(r), G: blend(g), B: blend(b), A: 0xff}
}
