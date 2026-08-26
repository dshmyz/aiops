# Capability Console Demo Data

This folder contains local demo data for testing the Swagger import,
Capability review, publish, and AI preflight workflow.

## Start The Mock Middleware API

```bash
node examples/mock-middleware-api.js
```

It listens on `http://127.0.0.1:19090` and exposes:

- `GET /v3/api-docs`
- `GET /api/minio/{cluster}/buckets/{bucket}/capacity`
- `GET /api/glusterfs/{cluster}/volumes/{volume}/status`
- `GET /api/kafka/{cluster}/consumer-groups/{group}/lag`
- `POST /api/kafka/{cluster}/topics/{topic}/retention`

## Fast AI Runtime Test

Start the Copilot API with the ready published demo capabilities:

```bash
COPILOT_DATABASE_DRIVER=sqlite \
COPILOT_CAPABILITIES_DIR=./examples/capabilities \
COPILOT_JWT_HMAC_SECRET=dev-secret \
go run ./cmd/copilot-api
```

Start the Vue Capability Console:

```bash
cd apps/capability-console
npm run dev
```

Open `http://127.0.0.1:5174/`. The Vue console opens on `AI 运维助手` by
default; use this entry for natural-language middleware questions. Switch to
`能力接入管理` to select a published demo capability and run AI preflight, or
to import Swagger, review generated Capability drafts, and publish reviewed
read tools.

Try these prompts:

```text
查询 m1 archive bucket 的 minio 容量
查询 g1 data volume 的 glusterfs 状态
查询 k1 payments consumer_group 的 kafka 延迟
```

## Swagger Import Test

Use this URL in the console:

```text
http://127.0.0.1:19090/v3/api-docs
```

Use this backend Base URL:

```text
http://127.0.0.1:19090
```

In `能力接入管理`, use `预览 API` first. Previewing the Swagger URL analyzes
candidate APIs but does not create Capability drafts yet. Recommended read
candidates are selected by default; write or risky candidates stay unselected
unless you explicitly choose them.

After reviewing the candidate list, click `生成 Capability 草稿`. Only selected
candidates become discovered drafts. The existing `本次导入` workbench then
shows the saved drafts so you can open them in the review panel, adjust output
mappings, test, publish safe reads, and run AI preflight.
