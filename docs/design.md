# cc-vision-gateway 实现方案

## 1. 背景

当前目标是在 Claude Code TUI 中实现类似“原生图片输入”的体验：

```text
用户在 Claude Code TUI 中粘贴截图/图片并发送
        ↓
本地代理服务拦截 Anthropic Messages 请求
        ↓
检测请求中是否包含 image block
        ↓
如果包含图片，则调用支持视觉能力的模型分析图片
        ↓
将图片内容转换为纯文本代码模型可理解的诊断上下文
        ↓
再将改写后的请求转发给目标纯文本模型 API
        ↓
目标纯文本模型返回最终结果
        ↓
Claude Code TUI 正常显示结果
```

DeepSeek V4 Pro、部分 OpenAI-compatible 文本模型、本地大语言模型等纯文本模型，本身不能直接处理 Claude Code 发出的 image block。最佳方案是在 Claude Code 和目标文本模型之间加入一个本地 Anthropic-compatible vision gateway。

---

## 2. 总体结论

推荐使用：

```text
Go + scratch Docker 镜像 + 本地 Anthropic-compatible Vision Gateway
```

技术选型：

```text
语言：Go
运行方式：Docker / Docker Compose
HTTP 服务：net/http
JSON 处理：encoding/json
日志：log/slog
配置：os.Getenv + 自定义校验
缓存：内存 LRU + bbolt
上游转发：http.Client
流式响应：逐块读取 + Flush + 客户端断开感知
图片解析模型：默认 Qwen-VL，后续可扩展 Gemini / OpenRouter / OpenAI-compatible vision
目标模型：默认 DeepSeek V4 Pro，后续可扩展其他纯文本模型
最终镜像：scratch
构建方式：CGO_ENABLED=0 + -trimpath + -ldflags="-s -w"
```

选择 Go 的原因：

1. 本地长期运行稳定。
2. 单二进制，Docker 镜像简单。
3. 内存占用低。
4. HTTP 代理和 SSE 流式转发可靠。
5. 性能足够高，维护成本低于 Rust。
6. 比 Node 更适合长期作为本地守护服务。
7. 静态单二进制，配合 scratch 镜像可以把构建产物控制得很小。

不推荐引入：

```text
1. Gin / Echo / Fiber：本项目只是代理服务，net/http 足够，框架会增加体积和依赖面。
2. jsoniter / sonic：瓶颈不在 JSON 序列化，标准库更稳、更小。
3. SQLite CGO 版本：会破坏纯静态构建，增加镜像和交叉编译复杂度。
4. Node / Python：开发快，但运行时和镜像体积明显更大，不适合作为本方案的生产默认。
```

生产化目标不是功能最多，而是普通请求足够快、图片请求足够稳：

```text
1. 无图请求：只做直通代理，不引入明显额外延迟。
2. 有图请求：优先命中缓存，未命中才调用 Vision。
3. 流式响应：不聚合完整响应，逐块转发并 Flush。
4. 稳定性：所有外部调用都有 timeout、并发上限和取消逻辑。
5. 可诊断：每个请求都有 request_id、状态码、耗时和缓存命中日志。
6. 产物小：单二进制、无 CGO、无重型框架、scratch 镜像。
```

---

## 3. 系统架构

```text
┌─────────────────────┐
│   Claude Code TUI    │
│                     │
│ 粘贴图片 + 文本请求   │
└──────────┬──────────┘
           │
           │ Anthropic /v1/messages
           │ ANTHROPIC_BASE_URL=http://127.0.0.1:8787
           ▼
┌─────────────────────────────┐
│ cc-vision-gateway           │
│ Go + Docker                  │
│                              │
│ 1. 接收 Anthropic 请求        │
│ 2. 检测 image block           │
│ 3. 如有图片，调用 Vision 模型 │
│ 4. 将 image block 替换为文本  │
│ 5. 转发给目标文本模型         │
└──────────┬──────────────────┘
           │
           │ Anthropic-compatible request
           ▼
┌─────────────────────────────┐
│ Text Model API               │
│ default: deepseek-v4-pro     │
└──────────┬──────────────────┘
           │
           │ response / stream response
           ▼
┌─────────────────────┐
│   Claude Code TUI    │
│ 显示模型返回结果      │
└─────────────────────┘
```

---

## 4. 工作流程

### 4.1 无图片请求

```text
Claude Code → Local Proxy → Text Model → Local Proxy → Claude Code
```

代理不做额外处理，只透传请求和响应。

### 4.2 有图片请求

```text
Claude Code
  ↓
Local Proxy 检测到 content 中存在 type=image
  ↓
提取 base64 图片和 media_type
  ↓
调用 Vision provider
  ↓
获得结构化图片分析文本
  ↓
将原 image block 替换为 text block
  ↓
转发给目标文本模型
  ↓
目标文本模型基于图片诊断上下文继续回答/改代码
```

---

## 5. 环境变量设计

### 5.1 Claude Code 侧

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
export ANTHROPIC_AUTH_TOKEN=local-anything
export ANTHROPIC_MODEL=claude-opus-4-7
```

Claude Code 会将 Anthropic 请求发送到本地代理。

说明：

```text
Claude Code Desktop 可能只允许配置符合 Claude 命名规则的模型名。
因此客户端侧可以继续使用 Claude 风格模型别名，真实模型由代理侧 MODEL_MAP 映射。
```

### 5.2 Proxy 服务侧

```bash
export PROXY_PORT=8787
export PROXY_HOST=0.0.0.0

export TEXT_PROVIDER=deepseek
export TEXT_BASE_URL=https://api.deepseek.com/anthropic
export TEXT_API_KEY=sk-xxx
export TEXT_MODEL=deepseek-v4-pro
export TEXT_FALLBACK_MODEL=deepseek-v4-flash
export TEXT_IMAGE_ROUTING=auto
export TEXT_IMAGE_FAST_MODEL=deepseek-v4-flash
export TEXT_IMAGE_STRONG_MODEL=deepseek-v4-pro
export TEXT_TIMEOUT=30m
export TEXT_API_FORMAT=anthropic_compatible
export MODEL_MAP_FILE=/app/config/model-map.json
export STRICT_MODEL_MAP=false

export VISION_PROVIDER=qwen
export VISION_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
export VISION_API_KEY=xxx
export VISION_MODEL=qwen3.6-flash
export VISION_TIMEOUT=120s
export VISION_PROMPT_MODE=fast
export VISION_CONTEXT_SCOPE=last_user
export VISION_PREPROCESS=true
export VISION_MAX_DIMENSION=1024
export VISION_JPEG_QUALITY=78

export ENABLE_IMAGE_CACHE=true
export IMAGE_CACHE_BACKEND=bolt
export IMAGE_CACHE_PATH=/app/data/image-cache.bolt
export IMAGE_CACHE_TTL=168h
export MAX_IMAGE_BYTES=10485760
export MAX_CONCURRENT_VISION=4
export VISION_FAILURE_MODE=fallback
export LOG_LEVEL=info
```

说明：

```text
TEXT_* 表示最终负责代码推理和回答的纯文本模型。
VISION_* 表示只在请求包含图片时调用的视觉诊断模型。
MODEL_MAP_FILE 表示客户端传入的 Claude 模型名到真实文本模型名的映射文件。
DeepSeek + Qwen-VL 是默认推荐组合，但配置命名不绑定具体厂商。
```

后续切换其他 provider 时，只需要替换对应配置：

```bash
# Gemini / OpenRouter vision 示例
export VISION_PROVIDER=openrouter
export VISION_BASE_URL=https://openrouter.ai/api/v1
export VISION_API_KEY=sk-or-xxx
export VISION_MODEL=google/gemini-2.5-flash

# OpenAI-compatible 文本模型示例
export TEXT_PROVIDER=openai_compatible
export TEXT_BASE_URL=https://example.com/v1
export TEXT_API_KEY=sk-xxx
export TEXT_MODEL=some-text-only-model
export MODEL_MAP_FILE=/app/config/model-map.json
```

---

## 6. 模型映射设计

Claude Code CLI 通常可以通过环境变量指定模型，但 Claude Code Desktop 可能只允许选择或配置符合 Claude 自家命名规则的模型名。为了兼容这种限制，代理必须支持模型映射。

设计原则：

```text
1. Claude Code / Desktop 侧只看到 Claude 风格模型名。
2. Proxy 收到请求后读取 request.model。
3. 如果 request.model 命中 MODEL_MAP_FILE，则替换为真实 TEXT_MODEL。
4. 如果未命中，默认使用 TEXT_FALLBACK_MODEL，或根据 STRICT_MODEL_MAP 决定是否报错。
5. 日志同时记录 client_model 和 upstream_model，便于排查。
```

示例：

```text
client_model: claude-opus-4-7
upstream_model: deepseek-v4-pro
```

推荐配置：

```json
{
  "claude-opus-4-7": "deepseek-v4-pro",
  "claude-opus-4-6": "deepseek-v4-pro",
  "claude-sonnet-4-6": "deepseek-v4-pro",
  "claude-haiku-4-5": "deepseek-v4-flash"
}
```

对应环境变量：

```bash
MODEL_MAP_FILE=/app/config/model-map.json
STRICT_MODEL_MAP=false
```

映射策略：

```text
1. sonnet 名称默认映射到主力代码模型。
2. opus 名称默认映射到更强或更长上下文模型。
3. haiku 名称默认映射到低延迟模型。
4. 如果后续支持多文本 provider，MODEL_MAP value 可以扩展为 provider/model 形式。
```

扩展形式：

```json
{
  "claude-opus-4-7": "deepseek/deepseek-v4-pro",
  "claude-haiku-4-5": "openai_compatible/some-fast-model"
}
```

实现时应先支持简单字符串映射，后续再支持 provider/model 映射。

注意：

```text
不要把复杂 JSON 直接写进 docker compose env_file。
不同 shell、dotenv parser、Docker env_file 对引号和花括号的处理容易不一致。
生产默认使用 MODEL_MAP_FILE，更稳定也更容易审计。
```

---

## 7. Anthropic 请求改写逻辑

Claude Code 发出的图片内容一般会出现在 messages 的 content array 中：

```json
{
  "role": "user",
  "content": [
    {
      "type": "text",
      "text": "帮我修复这个页面布局"
    },
    {
      "type": "image",
      "source": {
        "type": "base64",
        "media_type": "image/png",
        "data": "..."
      }
    }
  ]
}
```

代理改写后：

```json
{
  "role": "user",
  "content": [
    {
      "type": "text",
      "text": "帮我修复这个页面布局"
    },
    {
      "type": "text",
      "text": "[图片已由 Vision 模型解析]\n图片类型：前端 UI 截图\nOCR：...\n布局结构：...\n问题：...\n建议：...\n[图片解析结束]"
    }
  ]
}
```

目标文本模型最终收到的只有 text block，因此可以正常理解并回答。

实现注意：

```text
1. Anthropic image block 使用 source.base64。
2. OpenAI-compatible vision provider 通常需要 image_url，格式是 data:{media_type};base64,{data}。
3. 因此调用 Qwen-VL / OpenRouter / OpenAI-compatible vision 时，必须做图片输入格式转换。
4. 不要把 Vision provider 的响应原样塞给目标文本模型，应包裹在明确边界标记中。
5. 对多个 image block，应按原消息顺序生成 [图片 1]、[图片 2] 等诊断上下文。
```

推荐注入格式：

```text
[图片诊断上下文开始]
图片序号：1
来源消息：user message index 3
Vision provider：qwen
Vision model：qwen3.6-flash

视觉事实：
...

诊断结论：
...

建议目标文本模型执行：
...

不确定性：
...
[图片诊断上下文结束]
```

---

## 8. 参考实现借鉴边界

可以参考两个已有项目，但只借鉴协议行为和边界处理，不照搬技术栈和产品路线。

### 8.1 claude-code-proxy

`claude-code-proxy` 更适合作为底层协议转换参考。

重点借鉴：

```text
1. Anthropic Messages 到 OpenAI-compatible chat/completions 的格式转换。
2. nested content array 的归一化。
3. tool_use / tool_result 到 OpenAI tool_calls / tool role 的映射。
4. streaming 和 non-streaming 双路径处理。
5. schema transformation，例如清理部分 provider 不支持的 schema 约束。
6. reasoning model / completion model 的动态模型选择思路。
```

不直接采用：

```text
1. Bun / Hono 技术栈。
2. Cloudflare Workers 部署路线。
3. npm package 分发方式。
```

### 8.2 Claudish

Claudish 更适合作为产品形态和 vision proxy 参考。

重点借鉴：

```text
1. Claude Code 协议兼容目标。
2. vision proxy 的触发时机和图片描述注入方式。
3. provider@model 显式路由思想。
4. debug / monitor 模式对定位协议问题的帮助。
5. 多 provider 配置的命名和用户体验。
```

不直接采用：

```text
1. 大而全的 universal model router 定位。
2. 交互式 setup 和复杂 CLI 体验。
3. Node / Bun 运行时依赖。
4. 大量 provider 内置适配导致的体积和维护成本。
```

本项目应保持定位：

```text
小体积、生产稳定、专注“纯文本模型的视觉适配层”。
```

---

## 9. Vision Prompt 设计

Vision 模型不直接完成代码任务，而是作为“图片转文本适配器”。

默认使用低延迟 fast prompt，减少图片请求的首 token 等待时间：

```text
你是给代码模型使用的视觉解析器。请结合用户需求，简洁描述图片中对完成任务有用的信息。

用户需求：
{user_text}

要求：
- 如果是 UI/设计稿/网页截图，说明布局、文字、明显问题和可执行修改建议。
- 如果是报错/终端/代码截图，提取关键错误、路径、代码、可能原因。
- 保留重要 OCR 文本。
- 不要声称已经修改代码。
- 输出控制在 800 字以内。
```

当用户更看重解析完整度而不是速度时，可以切换为 detailed prompt：

```text
你是一个给代码模型使用的视觉解析器。

请将用户提供的图片转换成纯文本代码模型可以理解的信息。

用户原始需求：
{user_text}

请输出以下内容：

1. 图片类型
   - UI 截图 / 报错截图 / 设计稿 / 终端截图 / 浏览器截图 / 其他

2. OCR
   - 提取所有可见文字
   - 保留错误信息、按钮文案、文件路径、代码片段

3. 视觉结构
   - 页面区域划分
   - 组件层级
   - 元素位置
   - 颜色、间距、对齐、大小

4. 如果是 UI 或设计稿
   - 布局问题
   - Tailwind/CSS 修改建议
   - 可能涉及的组件

5. 如果是报错截图
   - 完整错误信息
   - 可能原因
   - 排查方向

6. 给后续代码模型的任务提示
   - 用明确、可执行的语言说明下一步应如何处理

要求：
- 输出结构化 Markdown
- 尽量具体
- 不要泛泛而谈
- 不要声称已经修改代码
```

---

## 10. 服务模块设计

推荐目录结构：

```text
cc-vision-gateway/
  cmd/
    proxy/
      main.go
  internal/
    config/
      config.go
    anthropic/
      rewrite.go
      preprocess.go
    routing/
      model_map.go
    providers/
      errors.go
      text.go
      vision.go
      qwen.go
    cache/
      cache.go
      memory.go
      bolt.go
    server/
      server.go
  config/
    model-map.json
  scripts/
    smoke.sh
    smoke_image.sh
  docs/
    design.md
  Dockerfile
  docker-compose.yml
  .env.example
  README.md
  go.mod
  go.sum
```

---

## 11. 核心接口设计

### 11.1 健康检查

```http
GET /health
```

返回：

```json
{
  "ok": true,
  "service": "cc-vision-gateway"
}
```

### 11.2 模型列表

```http
GET /v1/models
GET /models
```

返回代理对客户端暴露的模型名，也就是 Claude Code / Desktop 可以选择或配置的 Claude 风格模型别名。

示例：

```json
{
  "data": [
    {
      "type": "model",
      "id": "claude-opus-4-7",
      "display_name": "claude-opus-4-7",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ],
  "has_more": false,
  "first_id": "claude-opus-4-7",
  "last_id": "claude-opus-4-7"
}
```

说明：

```text
1. /v1/models 用于兼容 Anthropic 风格模型列表。
2. /models 作为简短别名，便于本地调试。
3. 返回的 id 应优先来自 MODEL_MAP_FILE 的 key，而不是 upstream_model。
4. 这样可以让 Claude Code Desktop 继续看到 Claude 风格模型名。
5. 不在 /v1/models 暴露 upstream_model，避免破坏 Anthropic 兼容响应。
```

### 11.3 Anthropic Messages 代理

```http
POST /v1/messages
```

处理逻辑：

```text
1. 读取 request body
2. 解析 JSON
3. 默认只检测最后一条 user message 中是否存在 image block
4. 如果无图片：直接转发给目标文本模型
5. 如果有图片：
   a. 提取 base64 图片
   b. 图片预处理，限制尺寸并转为适合 Vision provider 的格式
   c. 计算 sha256 hash
   d. 查询缓存
   e. 无缓存则调用 Vision provider
   f. 替换 image block 为 text block
6. 保持原请求中的 stream 字段
7. 转发给目标文本模型
8. 如果 stream=true，流式透传响应
9. 如果 stream=false，普通 JSON 透传响应
```

说明：

```text
1. IMAGE_SCAN_SCOPE 默认是 last_user，避免历史上下文中旧图片反复触发 Vision。
2. VISION_CONTEXT_SCOPE 默认是 last_user，避免把 1M 文本上下文塞给视觉模型。
3. 如果需要处理整段历史，可显式切换为 all，但不建议作为默认生产配置。
```

### 11.4 Provider 兼容模式

目标文本模型 provider 分为两类，不能混在一个处理路径里：

```text
1. anthropic_compatible：上游本身接收 /v1/messages 形态。
   - 只需要做模型映射、字段清理、图片改写。
   - 例如 DeepSeek Anthropic-compatible API。

2. openai_compatible：上游接收 /chat/completions 形态。
   - 必须做 Anthropic Messages → OpenAI Chat Completions 转换。
   - 必须处理 tool_use/tool_result、stop_reason、SSE event 映射。
   - 可重点参考 claude-code-proxy 的转换规则。
```

因此 `TEXT_PROVIDER=deepseek` 不等价于 `TEXT_PROVIDER=openai_compatible`。
实现时必须在 provider 层声明 capability：

```text
api_format=anthropic_compatible | openai_compatible
supports_tools=true|false
supports_streaming=true|false
supports_reasoning=true|false
```

无图片请求也不能简单原始字节透传，至少需要完成：

```text
1. request.model 映射。
2. DeepSeek / 目标 provider 不支持字段清理。
3. 按 provider api_format 决定是否需要协议转换。
```

性能优化应理解为“最少解析和最少改写”，不是完全不解析。

---

## 12. SSE / Streaming 处理

Claude Code 体验依赖流式输出，因此代理必须支持 `stream=true`。

处理方式：

```text
1. 收到请求后不要改变 stream 字段
2. 转发给目标文本模型
3. 将目标文本模型返回的 Content-Type、状态码透传
4. 使用固定大小 buffer 逐块读取 response.Body
5. 每写入一块后 Flush
6. 监听 request context，客户端断开时立即取消上游请求
7. 设置合理的 read/write/idle timeout，避免连接泄漏
```

Go 中需要确认：

```go
flusher, ok := w.(http.Flusher)
```

如果支持，则每次写入后：

```go
flusher.Flush()
```

注意：

```text
1. anthropic_compatible 上游可以尽量透传 SSE event。
2. openai_compatible 上游不能直接透传，必须转换 delta、tool_calls、finish_reason。
3. 非 2xx 响应不要伪装成 SSE，应保留状态码和可读错误体。
4. Vision 预处理发生在调用目标文本模型之前，因此有图请求的首 token 延迟必然包含 Vision 延迟。
5. 有图请求应在日志中拆分 vision_latency_ms、text_upstream_wait_ms 和 stream_copy_ms。
```

---

## 13. 缓存策略

为减少 Vision 模型成本和图片请求延迟，生产环境必须启用持久化缓存。

缓存 key：

```text
sha256(processed_media_type + processed_image_bytes + vision_prompt_version + normalized_user_text_hash)
```

说明：

```text
同一张图片在不同用户任务下，Vision 输出侧重点可能不同。
因此缓存 key 不能只包含图片本身，还要包含 prompt 版本和用户需求摘要。实际实现中先对图片做尺寸和格式预处理，再用处理后的图片字节参与 hash，避免同一图片因格式差异造成缓存抖动。
```

缓存 value：

```json
{
  "vision_text": "...",
  "provider": "qwen",
  "model": "qwen3.6-flash",
  "created_at": "2026-05-13T12:00:00Z"
}
```

生产默认：

```text
bbolt
```

可选快速路径：

```text
内存 LRU + 持久化缓存
```

推荐策略：

```text
1. 先查内存 LRU，命中则毫秒级返回。
2. 未命中再查 bbolt。
3. 仍未命中才调用 Vision provider。
4. Vision 成功后同时写入内存和持久化缓存。
5. 设置 TTL，默认 7 天。
```

这样可以兼顾速度、成本和重启后的稳定性。

缓存安全约束：

```text
1. bbolt 文件只保存 vision_text 和必要元数据，不保存原始 base64 图片。
2. cache key 使用 hash，不能把用户 prompt 明文作为 key。
3. normalized_user_text 应先 hash 或截断后再参与 key 计算。
4. debug 模式如需保存改写前后 JSON，必须单独开关，且默认关闭。
```

---

## 14. 错误处理策略

### 14.1 Vision 调用失败

有两种策略：

#### 策略 A：直接返回错误

适合严格代码修改任务。

```text
Vision provider failed: xxx
```

#### 策略 B：降级转发

适合 Claude Code / Desktop 的日常使用，避免一次 Vision 超时直接中断会话。

将 image block 替换为：

```text
[图片解析失败]
当前请求包含图片，但 Vision 模型调用失败。
请用户提供图片描述，或稍后重试。
错误：xxx
```

然后继续转发给目标文本模型。

当前默认：

```text
VISION_FAILURE_MODE=fallback
```

原因：

```text
Vision provider 偶发超时或限流时，fallback 能给客户端返回可理解错误，
并提示用户补充图片描述或重试，Claude Code 会话不会直接断掉。
```

如果是严格代码修改流水线，不希望模型在缺少图片信息时继续推理，可以显式切换为：

```text
VISION_FAILURE_MODE=error
```

---

## 15. 安全与隐私

本地代理会接触：

```text
1. 用户 prompt
2. 图片 base64
3. TEXT_API_KEY
4. VISION_API_KEY
```

要求：

1. 默认不落盘保存图片。
2. 日志中不要打印 base64。
3. 日志中不要打印 API Key。
4. 只监听本机地址或 Docker 映射到 127.0.0.1。
5. 不要暴露到公网。

Docker 运行时建议对宿主机只绑定本机地址：

```text
127.0.0.1:8787:8787
```

容器内部可以使用：

```text
PROXY_HOST=0.0.0.0
```

这样 Docker 端口映射才能访问容器内服务，但公网暴露边界仍由宿主机端口绑定控制。

不要使用：

```text
0.0.0.0:8787:8787
```

除非明确知道风险。

---

## 16. Docker 方案

### 16.1 Dockerfile

```dockerfile
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /cc-vision-gateway ./cmd/proxy

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /cc-vision-gateway /cc-vision-gateway

EXPOSE 8787
ENTRYPOINT ["/cc-vision-gateway"]
```

说明：

```text
1. 使用 scratch 而不是 alpine，减少最终镜像体积和运行时攻击面。
2. CGO_ENABLED=0 保证静态链接，避免依赖 libc。
3. -trimpath 去掉本地构建路径。
4. -ldflags="-s -w" 去掉符号表和调试信息。
5. 只复制 ca-certificates 和单二进制，满足 HTTPS 上游访问即可。
```

### 16.2 docker-compose.yml

```yaml
services:
  cc-vision-gateway:
    build: .
    container_name: cc-vision-gateway
    ports:
      - "127.0.0.1:8787:8787"
    env_file:
      - .env
    volumes:
      - ./config:/app/config:ro
      - ./data:/app/data
    restart: unless-stopped
```

### 16.3 .env.example

```bash
PROXY_PORT=8787
PROXY_HOST=0.0.0.0

TEXT_PROVIDER=deepseek
TEXT_BASE_URL=https://api.deepseek.com/anthropic
TEXT_API_KEY=sk-xxx
TEXT_MODEL=deepseek-v4-pro
TEXT_FALLBACK_MODEL=deepseek-v4-flash
TEXT_IMAGE_ROUTING=auto
TEXT_IMAGE_FAST_MODEL=deepseek-v4-flash
TEXT_IMAGE_STRONG_MODEL=deepseek-v4-pro
TEXT_TIMEOUT=30m
TEXT_API_FORMAT=anthropic_compatible
MODEL_MAP_FILE=/app/config/model-map.json
STRICT_MODEL_MAP=false

VISION_PROVIDER=qwen
VISION_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
VISION_API_KEY=xxx
VISION_MODEL=qwen3.6-flash
VISION_PROMPT_MODE=fast
VISION_CONTEXT_SCOPE=last_user
VISION_PREPROCESS=true
VISION_MAX_DIMENSION=1024
VISION_JPEG_QUALITY=78

ENABLE_IMAGE_CACHE=true
IMAGE_CACHE_BACKEND=bolt
IMAGE_CACHE_PATH=/app/data/image-cache.bolt
IMAGE_CACHE_TTL=168h
MAX_IMAGE_BYTES=10485760
IMAGE_SCAN_SCOPE=last_user
VISION_TIMEOUT=120s
MAX_CONCURRENT_VISION=4
VISION_FAILURE_MODE=fallback
LOG_LEVEL=info
```

### 16.4 config/model-map.json

```json
{
  "claude-opus-4-7": "deepseek-v4-pro",
  "claude-opus-4-6": "deepseek-v4-pro",
  "claude-sonnet-4-6": "deepseek-v4-pro",
  "claude-haiku-4-5": "deepseek-v4-flash"
}
```

---

## 17. 使用方式

### 17.1 启动 Proxy

```bash
cp .env.example .env
# 编辑 .env，填入 TEXT_API_KEY 和 VISION_API_KEY

docker compose up -d --build
```

检查：

```bash
curl http://127.0.0.1:8787/health
```

预期：

```json
{"ok":true,"service":"cc-vision-gateway"}
```

### 17.2 配置 Claude Code

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
export ANTHROPIC_AUTH_TOKEN=local-anything
export ANTHROPIC_MODEL=claude-opus-4-7

claude
```

### 17.3 使用体验

在 Claude Code TUI 中：

```text
请根据这张截图修复页面布局
[直接粘贴截图]
```

代理会自动处理图片，目标文本模型最终收到的是文本化后的图片诊断上下文。

---

## 18. 生产化开发里程碑

### 阶段 1：高性能基础代理

目标：无图片请求走最短路径，代理只做必要鉴权、头部处理和流式转发。

验收：

```text
1. Claude Code 普通文本请求可正常返回目标文本模型结果。
2. 无图请求只做必要解析和改写，例如 model 映射、字段清理、provider 协议转换。
3. stream=true 时首 token 延迟主要由目标文本模型决定，代理不引入明显额外缓冲。
4. 并发请求下无连接泄漏、无 goroutine 泄漏。
```

### 阶段 2：稳定图片检测与限制

目标：能识别 Anthropic request 中的 image block，并保护本地代理不被超大图片拖垮。

验收：

```text
1. 日志显示 image=yes / image_count / image_bytes。
2. 不打印 base64。
3. 超过 MAX_IMAGE_BYTES 的请求快速失败。
4. 支持多图，但限制最大图片数量。
```

### 阶段 3：Vision 解析与缓存

目标：Vision 调用有超时、并发限制、缓存和可观测指标。

验收：

```text
1. 同一张截图和同一任务重复请求命中缓存。
2. Vision 调用超过 VISION_TIMEOUT 后可控失败。
3. MAX_CONCURRENT_VISION 生效，避免瞬时并发打满外部 API。
4. 记录 vision_latency_ms、cache_hit、provider、model。
```

### 阶段 4：兼容性请求改写

目标：将目标文本模型不支持的字段清理或转换，确保 Claude Code 不因为兼容差异中断。

验收：

```text
1. image block 被替换为 text block。
2. document/search_result/mcp_tool_use 等目标文本模型不支持字段有明确处理策略。
3. tool_use/tool_result 保持可用。
4. 改写前后 JSON 可在 debug 模式保存，但默认关闭且脱敏。
```

### 阶段 5：Streaming 生产稳定性

目标：Claude Code TUI 中保持流式输出体验。

验收：

```text
1. stream=true 时，输出不是等完整生成后一次性返回。
2. 客户端断开后，上游请求被取消。
3. 目标文本模型返回非 2xx 时，状态码和错误体可被 Claude Code 感知。
4. 长输出不会因为代理缓冲导致卡顿。
```

### 阶段 6：部署与运维

目标：本地 Docker 长期稳定运行，可诊断、可重启、可升级。

验收：

```text
1. docker compose up -d 后可稳定使用。
2. /health 返回进程状态，/ready 检查关键配置是否完整。
3. 日志结构化，包含 request_id、latency_ms、status、cache_hits。
4. 支持 graceful shutdown，已有请求尽量完成。
5. 配置错误时启动失败并给出清晰错误。
```

---

## 19. 性能与稳定性优先级

优先级从高到低：

1. 无图请求只做必要解析、模型映射和字段兼容处理，保证普通 Claude Code 体验最快。
2. 有图请求启用内存 LRU + 持久化缓存，减少 Vision 调用。
3. Vision 调用必须有超时、并发限制、最大图片大小限制。
4. SSE 必须逐块 Flush，并在客户端断开时取消上游请求。
5. 目标文本模型不支持字段必须有兼容清理策略。
6. 构建产物必须保持小：纯 Go、无 CGO、无重型框架、scratch 镜像。
7. 所有日志必须脱敏，默认不落盘图片和完整请求体。
8. 提供 request_id、latency_ms、cache_hit、vision_latency_ms 等指标。
9. 预留 provider fallback，但只在超时/限流/5xx 时触发，避免错误答案扩散。
10. 支持 debug 模式保存改写前后的 JSON，但默认关闭且脱敏。
11. Web UI、成本统计、按图片类型 prompt 等功能放到稳定性完成之后。

---

## 20. 风险点

### 20.1 Claude Code 图片格式变化

如果 Claude Code 后续调整 image block 格式，代理需要适配。

应对：

```text
保留 debug 模式，方便查看 request body 结构。
```

### 20.2 目标文本模型 Anthropic 兼容行为变化

目标文本模型的 Anthropic-compatible API 可能对字段支持不完整。

应对：

```text
代理应清理目标文本模型不支持的字段，例如 image block、部分 tool/mcp 字段。
```

### 20.3 Vision 模型输出不稳定

图片描述可能遗漏细节。

应对：

```text
使用结构化 prompt，并保留用户原始需求作为上下文。
```

### 20.4 流式响应兼容性

SSE 透传处理不当会导致 Claude Code 卡住。

应对：

```text
stream=true 必须作为生产路径优先实现和压测，不应作为后置能力。
```

---

## 21. 最终推荐实现路线

```text
第一版：Go + net/http + scratch Docker + 文本模型直通代理 + 稳定 SSE
第二版：Qwen-VL 默认视觉诊断 + 图片诊断改写 + 内存 LRU + bbolt 持久化缓存
第三版：字段兼容清理 + provider fallback + 并发限制 + 压测
第四版：ready check + graceful shutdown + debug 脱敏工具 + 小体积发布脚本
```

生产可用版本必须满足：

```text
1. 无图请求快速直通。
2. 有图请求可缓存、可超时、可取消、可限流。
3. SSE 流式输出稳定，不额外聚合完整响应。
4. 目标文本模型不支持字段不会导致 Claude Code 会话异常中断。
5. 日志可定位问题，但不泄露 API Key、base64 图片和完整敏感 prompt。
6. Docker 本地长期运行，支持健康检查、就绪检查和 graceful shutdown。
7. 最终产物是静态单二进制，Docker 镜像使用 scratch 或 distroless/static。
```

---

## 22. 一句话总结

这个项目的本质是：

```text
把 Claude Code 发给纯文本模型的 Anthropic 请求做一次“多模态降级转换”：
图片 → Vision 诊断上下文 → 纯文本模型推理。
```

在你的目标下，最佳技术路线是：

```text
Go + scratch Docker + 本地 Vision Gateway + 可配置 TEXT provider + 可配置 VISION provider
```
