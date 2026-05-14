# cc-vision-gateway

[中文文档](README.zh-CN.md)

A tiny production-ready vision gateway for Claude Code and text-only models.

It accepts Anthropic-compatible Claude Code requests, maps Claude-style model
names to real upstream text models, converts image blocks into Qwen-VL
diagnostic context, and forwards the rewritten request to the target model.

## Quick Start

```bash
cp .env.example .env
# edit TEXT_API_KEY and VISION_API_KEY
docker compose up -d --build
```

Configure Claude Code:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
export ANTHROPIC_AUTH_TOKEN=local-anything
export ANTHROPIC_MODEL=claude-opus-4-7
claude
```

Claude Code app/Desktop may only accept Claude-style model names. In that case
choose a Claude Code model alias from `config/model-map.json`, such as
`claude-opus-4-7`, `claude-sonnet-4-6`, or `claude-haiku-4-5`; the gateway maps
it to the real upstream model.

DeepSeek V4 Pro supports a larger upstream context than many Claude aliases,
but Claude Code's visible context window is client-side model metadata. If the
app forces Claude-style names, the UI may still show Claude's context window
even though the upstream request is routed to `deepseek-v4-pro`.

Health check:

```bash
curl http://127.0.0.1:8787/health
```

List client-facing model aliases:

```bash
curl http://127.0.0.1:8787/v1/models
```

Run smoke checks:

```bash
BASE_URL=http://127.0.0.1:8787 SKIP_MESSAGE=true ./scripts/smoke.sh
```

With real API keys configured, omit `SKIP_MESSAGE=true` to also verify a
non-image `/v1/messages` request.

Run image plumbing smoke test:

```bash
./scripts/smoke_image.sh
```

## Current Scope

- Text upstream: `anthropic_compatible` providers, default DeepSeek.
- Image requests use `TEXT_IMAGE_ROUTING=auto`: simple visual questions go to
  `TEXT_IMAGE_FAST_MODEL`, code/debug/layout tasks go to
  `TEXT_IMAGE_STRONG_MODEL`.
- Vision upstream: OpenAI-compatible Qwen-VL endpoint.
- Cache: bbolt, storing vision diagnosis text only.
- Streaming: upstream response is copied in chunks and flushed.

OpenAI-compatible text-provider conversion is intentionally left for the next
phase because tool-call and SSE event mapping need dedicated compatibility
tests.

See [docs/design.md](docs/design.md) for the implementation design and tradeoffs.
