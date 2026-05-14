package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"cc-vision-gateway/internal/config"
)

type QwenVision struct {
	baseURL    string
	apiKey     string
	model      string
	promptMode string
	client     *http.Client
}

func NewQwenVision(cfg config.Config) *QwenVision {
	return &QwenVision{
		baseURL:    strings.TrimRight(cfg.VisionBaseURL, "/"),
		apiKey:     cfg.VisionAPIKey,
		model:      cfg.VisionModel,
		promptMode: cfg.VisionPromptMode,
		client:     &http.Client{Timeout: cfg.VisionTimeout},
	}
}

func (q *QwenVision) Diagnose(ctx context.Context, userText string, image ImageInput) (string, error) {
	prompt := buildVisionPrompt(userText, q.promptMode)
	dataURL := fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data)
	payload := map[string]any{
		"model": q.model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
		"temperature": 0.1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+q.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("vision provider returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("vision provider returned empty content")
	}
	return out.Choices[0].Message.Content, nil
}

func buildVisionPrompt(userText, mode string) string {
	if strings.TrimSpace(userText) == "" {
		userText = "(用户未提供额外文字需求)"
	}
	if mode == "fast" {
		return `你是给代码模型使用的视觉诊断器。请用简洁 Markdown 描述图片中与用户需求相关的信息。

用户需求：
` + userText + `

输出：
1. 图片类型
2. 关键可见文字/OCR
3. 关键视觉事实
4. 针对用户需求的诊断结论
5. 给后续代码模型的下一步建议
6. 不确定性

要求：具体、简洁，不要声称已经修改代码。`
	}
	return `你是给代码模型使用的视觉诊断器。

请将用户提供的图片转换成纯文本代码模型可以理解的任务诊断上下文。

用户原始需求：
` + userText + `

请输出结构化 Markdown，包含：
1. 图片类型：UI 截图 / 报错截图 / 设计稿 / 终端截图 / 浏览器截图 / 其他
2. OCR：提取所有可见文字，保留错误信息、按钮文案、文件路径、代码片段
3. 视觉事实：页面区域、组件层级、位置、颜色、间距、对齐、尺寸
4. 诊断结论：图片暴露出的具体问题
5. 建议目标文本模型执行：明确、可操作的下一步
6. 不确定性：哪些判断只是推测，不能当作事实

要求：
- 尽量具体
- 不要泛泛而谈
- 不要声称已经修改代码
- 不要输出最终代码补丁`
}

var _ VisionClient = (*QwenVision)(nil)
