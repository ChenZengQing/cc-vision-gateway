# cc-vision-gateway

一个面向 Claude Code 和纯文本模型的小型生产级视觉网关。

它接收 Claude Code 发出的 Anthropic-compatible 请求，将 Claude 风格模型名映射到真实上游文本模型，把请求中的图片 block 转换为 Qwen-VL 生成的图片诊断上下文，再把改写后的纯文本请求转发给目标模型。

## 快速开始

```bash
cp .env.example .env
# 编辑 .env，填入 TEXT_API_KEY 和 VISION_API_KEY
docker compose up -d --build
```

配置 Claude Code：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
export ANTHROPIC_AUTH_TOKEN=local-anything
export ANTHROPIC_MODEL=claude-opus-4-7
claude
```

Claude Code app/Desktop 可能只允许选择或配置 Claude 风格模型名。这种情况下，使用 `config/model-map.json` 里的模型别名即可，例如 `claude-opus-4-7`、`claude-sonnet-4-6` 或 `claude-haiku-4-5`；网关会在服务端把它映射到真实上游模型。

DeepSeek V4 Pro 的真实上游上下文窗口可能大于 Claude 别名在 Claude Code UI 中显示的上下文窗口。这个显示值来自客户端侧模型元数据，不代表请求没有被路由到 `deepseek-v4-pro`。

健康检查：

```bash
curl http://127.0.0.1:8787/health
```

查看对客户端暴露的模型别名：

```bash
curl http://127.0.0.1:8787/v1/models
```

运行基础 smoke check：

```bash
BASE_URL=http://127.0.0.1:8787 SKIP_MESSAGE=true ./scripts/smoke.sh
```

如果已经配置真实 API Key，可以去掉 `SKIP_MESSAGE=true`，同时验证非图片 `/v1/messages` 请求。

运行图片链路 smoke test：

```bash
./scripts/smoke_image.sh
```

## 当前能力

- 文本上游：支持 `anthropic_compatible` provider，默认 DeepSeek。
- 图片请求：使用 `TEXT_IMAGE_ROUTING=auto`，简单看图问题走 `TEXT_IMAGE_FAST_MODEL`，代码、调试、布局类任务走 `TEXT_IMAGE_STRONG_MODEL`。
- 视觉上游：默认使用 OpenAI-compatible Qwen-VL endpoint。
- 缓存：使用 bbolt，只保存 Vision 诊断文本和必要元数据，不保存原始图片。
- 流式响应：按块复制上游响应并 Flush，避免聚合完整响应后再返回。
- 模型列表：`/v1/models` 和 `/models` 返回 Claude 风格客户端模型别名，不暴露真实上游模型名。

OpenAI-compatible 文本 provider 的协议转换尚未实现，属于下一阶段能力。原因是 tool call、SSE event、stop reason 等映射需要专门的兼容性测试。

## 默认模型映射

默认 `config/model-map.json`：

```json
{
  "claude-opus-4-7": "deepseek-v4-pro",
  "claude-opus-4-6": "deepseek-v4-pro",
  "claude-sonnet-4-6": "deepseek-v4-pro",
  "claude-haiku-4-5": "deepseek-v4-flash"
}
```

如果客户端传入的模型名没有命中映射，并且 `STRICT_MODEL_MAP=false`，网关会回退到：

```bash
TEXT_FALLBACK_MODEL=deepseek-v4-flash
```

## 图片处理策略

默认只处理最后一条用户消息里的图片：

```bash
IMAGE_SCAN_SCOPE=last_user
VISION_CONTEXT_SCOPE=last_user
```

这样可以避免历史上下文中的旧图片反复触发 Vision，也避免把大上下文塞给视觉模型。图片会先做尺寸压缩和格式转换，然后再调用 Qwen-VL；重复图片和相同任务会优先命中缓存。

Vision 调用失败时，默认使用：

```bash
VISION_FAILURE_MODE=fallback
```

也就是把图片解析失败信息注入给文本模型，让 Claude Code 会话不中断。如果你更希望严格失败，可以改为：

```bash
VISION_FAILURE_MODE=error
```

## 配置说明

最少需要配置：

```bash
TEXT_API_KEY=sk-xxx
VISION_API_KEY=sk-xxx
```

默认推荐：

```bash
TEXT_BASE_URL=https://api.deepseek.com/anthropic
TEXT_MODEL=deepseek-v4-pro
TEXT_FALLBACK_MODEL=deepseek-v4-flash

VISION_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
VISION_MODEL=qwen3-vl-flash
VISION_TIMEOUT=8s
```

Docker Compose 默认只把服务绑定到宿主机本地地址：

```yaml
ports:
  - "127.0.0.1:8787:8787"
```

不要把它直接暴露到公网，因为代理会接触用户 prompt、图片内容和 API Key。

## 更多设计细节

完整设计、取舍和生产化计划见 [docs/design.md](docs/design.md)。
