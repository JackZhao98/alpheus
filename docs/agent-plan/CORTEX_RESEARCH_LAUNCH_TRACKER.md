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
| 并行多 Agent TaskGraph | 首轮真实图已部署 | Intent 生成无权限提案，Control 准入 2–4 条独立分支；并行 Worker、Join 和 Decision Desk 均由数据库状态驱动 |
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
- 无 Tool 的四角色真实 TaskGraph Run
  `3d2bbe7e-85ce-48ae-9e74-f2a1002e19ee` 已完成 Market /
  Fundamental / Options / Catalyst 四路并行、`all_required` Join、
  Decision Desk 和最终 Artifact。四个分支在数据库中同时运行，不是线性
  handoff。
- Agent Lab 浏览器验收 Run
  `dbe27a81-68ee-48bc-af69-d333c6bdd703` 已从四条 `running` 分支观察到
  四条完成、Join 放行、Decision Desk 完成和 Run `succeeded`。折叠面板显示
  轮次、节点数、最大并发、角色、Tool、Join 策略及最终状态；原始 Trace
  同时保留精确 graph/task/turn/artifact ID。

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

## 下一阶段 TODO：并行多 Agent TaskGraph

> 受控单链仍保留用于简单问题；多角色请求已经可以进入首轮并行 TaskGraph。
> 下面的进度不计入 37 Tool 上线完成度。

| 顺序 | 工作项 | 完成条件 | 状态 |
|---:|---|---|---|
| P1 | 冻结 TaskGraph / dependency / join 契约 | 计划可表达多个并行子 Task、依赖边、最大并发、deadline、预算及不可变输出契约；模型不能自行扩大权限 | 已完成：独立 frozen v1 契约、Schema、golden、DAG/Join/权限/预算校验全部通过 |
| P2 | Control 批量 admission 与 fan-out | 一次已验证计划原子创建多个独立子 Task；每个分支绑定唯一角色、Tool grant、预算和父 Run | 已完成：Control-only 原子命令、精确重放、三节点真实数据库探针及全回滚失败路径通过 |
| P3 | Scheduler 并行调度 | 不同 Specialist 可同时 claim/执行；同一 Task 仍只有一个有效 lease，重复投递不重复调用 Tool | 已完成：4 条 Worker lane、逐节点 Session/Blob ACL、Graph 独立原子并发槽、效果为 none 的 Specialist 并行执行；带 Tool 节点继续冻结等待专用执行边界 |
| P4 | Join Barrier / fan-in | 支持 `all_required`、`minimum_success`、部分失败和严格终态；Join 只读取已提交 Artifact | 已完成：Control-only Join 解析、下游 Blob/ACL、Desk fan-in、失败收敛、结果血缘及成功/失败数据库验收通过 |
| P5 | Tool 并行节点与 Moody Blues 预处理上下文 | 每个带 Tool 节点只执行准入时冻结的一项只读 Tool；回放数据先经过统一 normalize/精简框架再交给 Agent | 已完成：双 Turn 参数/证据边界、精确 grant、全部现有只读 Tool 分派、错误 Tool 拒绝及 `gex_compact_v1` 已验证 |
| P6 | 多阶段自适应研究与 DAG Trace | Desk 可在有界轮次内提出下一批子链路；网页显示真实分叉、等待、失败、汇合和下一轮 | 进行中：首轮 2–4 分支规划、真实并行、Join、持久化 DAG Trace 和折叠网页已完成；只剩受控下一轮 |
| P7 | 故障与上线验收 | 通过并发、重复、崩溃恢复、慢分支、部分失败、预算耗尽和真实多角色端到端测试 | 已开始：真实四路成功与严格分支失败已验证；完整恢复矩阵待完成 |

下一项实际开发任务是 **P6 剩余项：受控的下一轮自适应研究**。首轮 Intent
图规划、Control 准入、四路并行、Join、Decision Desk、持久化 DAG Trace 与
Agent Lab 折叠视图已经真实跑通。下一轮不能由模型直接追加 Task；Decision
Desk 只能提交无权限 proposal，Control 必须重新检查剩余轮次、总预算、
deadline、角色与只读 Tool 快照，再原子创建下一轮或确定结束。完成后进入
P7 的崩溃恢复、重复投递、慢分支、部分失败和预算耗尽矩阵。
