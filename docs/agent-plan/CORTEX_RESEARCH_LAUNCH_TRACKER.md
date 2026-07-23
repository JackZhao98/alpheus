# Cortex + Research 上线进度

> 范围：Cortex 的用户请求、Conversation、Run/Task/Worker、受控工具与
> Research Gateway / Moody Blues / Provider。**不包含交易执行、下单、资金效果
> 或 Live 授权。** 本表只在构建、迁移、容器健康和端到端验证均有证据后标记
> “可上线”。

**上线判定（2026-07-23）：当前只读 Cortex + Research 切片已达到可上线。**
范围是持久化对话、六个专业 Agent、37 条收据化只读/只读预检 Tool、
Moody Blues `live` / `as_of` / replay 和 Agent Lab 两层验收。旧
`agent-runtime` 部署与 Kernel legacy query 写入路径已经退役；历史
`agent_query_job` 仅保留只读审计访问。

| 工作项 | 当前状态 | 上线验收 |
|---|---|---|
| Cortex 输入、Conversation 与多轮上下文 | 已部署并持久化 | 重启后连续对话、刷新历史、同一 subject 隔离均通过 |
| Run / Task / Attempt / Turn / Artifact | canonical 执行链已部署 | 成功、失败、超时恢复及 Trace 均由数据库记录重建 |
| Intent → Scout / Desk 协作 | 已部署并实测 | 真实 Run 能显示 handoff、Scout memo、Desk continuation |
| Cortex 工具授权与收据 | 37 条只读 / 只读预检 Tool 已接入 | Tool 精准测试可逐项运行；真实抽测均出现授权与 receipt |
| 专业 Agent 路由 | 6 个 Specialist 已部署 | Market / Fundamental / Options / Position / Catalyst / Discovery 均有真实 handoff、持久化 Turn 和 Desk continuation |
| Research Gateway | 已有 Web Fetch 与 GEXBOT 受控入口 | 服务健康、权限最小化、失败不泄露凭据或原始数据 |
| Moody Blues Provider 目录 | 已部署并实测 | 目录准确声明每个 Provider 的 `live` / `as_of` / `replay` 能力 |
| Moody Blues GEXBOT 采集状态 | 已迁移、部署并实测 | 显示三条 SPX 序列覆盖、最新 observed/available 时间；不泄露 raw 数据 |
| GEXBOT 历史 `as_of` / replay | 已部署并实测 | 秒级时间围栏正确、微秒规范化、仅返回 `available_at <= as_of` 的数据 |
| GEXBOT 官方按需读数 | 已部署并实测 | `market_gexbot_live` 独立于历史 Tool；永久保存 raw Blob、Evidence、Receipt，并区分 `source_timestamp` 与 `fetched_at` |
| Kernel / Robinhood 只读工具批次 | 34/34 已接入 Cortex | 1 条财报专用桥；33 条使用严格 Tool/source/参数白名单的通用只读桥 |
| Agent Lab 验收界面 | 已部署并通过真实网页交互 | 用户可看见 Conversation、Trace、Tool receipt、Provider 数据时间边界；阶段 A 精准 Tool 与阶段 B 自主意图路线分开展示 |
| 旧 agent-runtime 退役 | 已完成 | Compose 无该服务；`POST /agent/query` 返回 410；旧 job 不再恢复或执行，14 条历史记录仍可读 |

## 已记录的部署验证（2026-07-23）

- `research-gateway`、`gexbot-provider`、`cortex-input`、`cortex-worker` 与
  `kernel` 镜像已成功构建，服务已强制重建且健康。
- 数据库 migration `0048_moody_blues_gexbot_collection_status` 已实际应用。
- Provider 目录、GEXBOT 状态、历史 `as_of` 和 generation 保护的 replay 已对
  真实归档数据验证；三条 SPX 序列均有数据。最新观测为
  `2026-07-22T19:59:30Z`，不是伪造的“实时”读数。
- 历史 GEX Run `f4aa847c-e7b1-42ad-a293-9093da1d376f` 已验证
  Run → `research_gexbot_as_of` authorization → receipt → Desk Artifact。
- 财报 Run `e025fff6-706e-48f9-abc7-da0655ca2e33` 已验证
  Intent → `kernel_earnings_results` authorization → Kernel / Robinhood MCP
  → 持久化 receipt → Desk Artifact；返回 TSLA 2026 Q2 标准化 EPS 与时间戳。
- 重启后的三 Tool 精准验收全部成功：
  `research_web_fetch` Run `3117421f-324a-4b89-9f5e-dbfbf2a85812`、
  `research_gexbot_as_of` Run `e6ecf84a-d1d0-4981-8364-5ecd93377a6f`、
  `kernel_earnings_results` Run `192d9965-6d4e-4dbf-b0a7-16e4ee54f23a`。
- GEX 时间语义加固 Run `75bf8fcb-e237-4f02-aa42-7471113dedc8`
  明确区分实际 `observed_at`、首次 `available_at` 与请求截止 `as_of`。
- 真实网页意图路线验收已通过：财报自然语言路线 Run
  `168a9741-6668-4d4f-bb53-5b1e56b84526`；完整
  Intent → Scout → child Task / memo → Desk continuation 路线 Run
  `47557a5a-fa86-43b6-b8ec-e114ed671981`。浏览器控制台无错误。
- 通用 Kernel-read 真实 Provider 验收已通过：
  `kernel_portfolio` Run `b4557073-4bd6-4b95-85a2-f50d3bf94c73`、
  `kernel_equity_quotes` Run `8819eb56-b071-43af-8cc1-cbae5869f692`、
  `kernel_search` Run `e02b29f0-527a-404b-ab3c-2db7f7c9f5ce`、
  `kernel_accounts` Run `581e5e0b-a928-489e-9009-8e43e7d37602`。
  四条均完成 Intent → Tool 授权 → Kernel / Robinhood MCP → Receipt → Desk。
- Agent Lab 已用真实浏览器验收为 37/37 已启用；需要 Provider UUID 的 5 行
  会明确要求先从对应前置只读工具取得真实 ID，不允许 LLM 猜测。
- 六条专业角色路线均已通过真实 Run：
  Market `5eb4b178-8f39-4a5c-b6dd-db836df7a1df`、Fundamental
  `7123f619-67df-4149-9faa-566d01ef2d07`、Options
  `1f586ba8-c19e-4783-ba8a-0c8ea7244c87`、Position
  `d20c1ecd-5e2f-49b2-ab99-05199943f795`、Catalyst
  `fc6ff5b4-0869-4695-9da6-d65f94f82331`、Discovery
  `5f3e1420-6a45-4803-a93d-538f7fcf7152`。
- 官方 GEX Live Run `edf5bb71-51c2-4df6-8ded-17b890f13d51` 已验证
  Intent → Options Scout → `market_gexbot_live` authorization → receipt
  → Specialist memo → Decision Desk；回答明确把
  `2026-07-22T20:00:00Z` 的来源时间和
  `2026-07-23T10:02:47.922246Z` 的抓取时间分开。
- Agent Lab 当前为 37/37 已启用；`market_gexbot_live` 有独立精准测试行。
- 4,215 条旧 GEX 观察值已完成一次性导入，`gexbot-legacy-import` 已从
  Compose 删除；导入源码仅作为灾难恢复工具保留，不再生成容器。

## 本次 Moody Blues 接口

| 入口 | 用途 | 访问边界 |
|---|---|---|
| `GET :8300/internal/v1/moody-blues/providers` | Provider 的能力、采集策略、时间语义目录 | Cortex 内部令牌 |
| `GET :8300/internal/v1/moody-blues/providers/gexbot-classic/status` | 三类 SPX 数据的覆盖与最新时间 | Cortex 内部令牌；无原始数据 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/as-of` | GEXBOT 历史时间截点 | Cortex 内部令牌；只读 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/live` | 官方 API 按需读取并归档 | Cortex 内部令牌；只读；不得把抓取时间冒充市场数据时间 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/replays` | 创建受 generation 保护的历史回放游标 | Cortex 内部令牌；只读 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/replays/{id}/next` | 消费下一条历史观察值 | Cortex 内部令牌；只读 |

旧 `/internal/v1/gexbot/*` 路径会暂时保留为兼容别名，直到所有内部调用迁移。
