# Cortex + Research 上线进度

> 范围：Cortex 的用户请求、Conversation、Run/Task/Worker、受控工具与
> Research Gateway / Moody Blues / Provider。**不包含交易执行、下单、资金效果
> 或 Live 授权。** 本表只在构建、迁移、容器健康和端到端验证均有证据后标记
> “可上线”。

**上线判定（2026-07-23）：当前只读 Cortex + Research 切片已达到可上线。**
范围是持久化对话、六个专业 Agent、37 条收据化只读/只读预检 Tool、
Moody Blues `live` / `as_of` / replay 和 Agent Lab 两层验收。旧
`agent-runtime` 部署与 Kernel legacy query 写入路径已经退役；历史
`agent_query_job` 仅保留只读审计访问。最新重启验收后 Cortex 与 Research
当前健康状态均为 `healthy`。

| 工作项 | 当前状态 | 上线验收 |
|---|---|---|
| Cortex 输入、Conversation 与多轮上下文 | 已部署并持久化 | 重启后连续对话、刷新历史、同一 subject 隔离均通过 |
| Run / Task / Attempt / Turn / Artifact | canonical 执行链已部署 | 成功、失败、超时恢复、用户取消及 Trace 均由数据库记录重建 |
| Intent → Scout / Desk 协作 | 已部署并实测 | 真实 Run 能显示 handoff、Scout memo、Desk continuation |
| Cortex 工具授权与收据 | 37 条只读 / 只读预检 Tool 已接入 | Tool 精准测试可逐项运行；真实抽测均出现授权与 receipt |
| 专业 Agent 路由 | 6 个 Specialist 已部署 | Market / Fundamental / Options / Position / Catalyst / Discovery 均有真实 handoff、持久化 Turn 和 Desk continuation |
| Research Gateway | 已有 Web Fetch 与 GEXBOT 受控入口 | 服务健康、权限最小化、失败不泄露凭据或原始数据 |
| Moody Blues Provider 目录 | 已部署并实测 | 目录准确声明每个 Provider 的 `live` / `as_of` / `replay` 能力 |
| Moody Blues GEXBOT 采集状态 | 已迁移、部署并修复时区依赖 | 显示三条 SPX 序列覆盖、最新 observed/available 时间；配置采集器缺失 New York 时区时拒绝伪健康；不泄露 raw 数据 |
| GEXBOT 历史 `as_of` / replay | 已部署并实测 | 秒级时间围栏正确、微秒规范化、仅返回 `available_at <= as_of` 的数据 |
| GEXBOT 官方按需读数 | 已部署并实测 | `market_gexbot_live` 独立于历史 Tool；永久保存 raw Blob、Evidence、Receipt，并区分 `source_timestamp` 与 `fetched_at` |
| Kernel / Robinhood 只读工具批次 | 34/34 已接入 Cortex | 1 条财报专用桥；33 条使用严格 Tool/source/参数白名单的通用只读桥 |
| Agent Lab 验收界面 | 已部署并通过真实网页交互 | 用户可看见 Conversation、Trace、Tool receipt、Provider 数据时间边界、系统健康与恢复；阶段 A 精准 Tool 与阶段 B 自主意图路线分开展示；运行中 Run 可由本人取消 |
| 并行多 Agent TaskGraph | 两轮自适应真实图已部署 | 每轮由模型提出无权限提案，Control 重新准入 2–4 条独立分支；并行 Worker、Join、Decision Desk 与下一轮均由数据库状态驱动 |
| 旧 agent-runtime 退役 | 已完成 | Compose 无该服务；`POST /agent/query` 返回 410；旧 job 不再恢复或执行，14 条历史记录仍可读 |

## 已记录的部署验证（2026-07-23）

- `research-gateway`、`gexbot-provider`、`cortex-input`、`cortex-worker` 与
  `kernel` 镜像已成功构建，服务已强制重建且健康。
- 数据库 migration `0048_moody_blues_gexbot_collection_status` 已实际应用。
- Provider 目录、GEXBOT 状态、历史 `as_of` 和 generation 保护的 replay 已对
  真实归档数据验证；三条 SPX 序列均有数据。
- 2026-07-23 的定时采集曾因 Provider Alpine 镜像缺少 `tzdata` 而静默停机。
  修复后，配置采集器会在无法加载 `America/New_York` 时拒绝启动；容器已
  重建并明确记录 09:00–16:00 ET 窗口启用。三条官方收盘快照已补采，
  `source_timestamp` 均为 `2026-07-23T20:00:00Z`，实际
  `fetched_at` / `available_at` 约为 `2026-07-24T00:20:18Z`。当天缺失的
  盘中 30 秒序列无法重建，本表不声称已经回填。
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
- 两轮自适应 TaskGraph Run
  `94a2760e-3914-4906-a639-a7680a225cc9` 已完成两次四路并行、两次
  `all_required` Join 与最终合成。Decision Desk 的 `refine` 只能提交
  2–4 条无权限分支提案；Control 会重新验证剩余轮次、deadline、预算、
  角色、Tool 所有权和输出契约后再创建下一轮。
- Agent Lab 已用真实浏览器从
  `/agent-lab?run=94a2760e-3914-4906-a639-a7680a225cc9` 恢复持久化 Run，
  同时显示第 1、2 轮的全部节点、Tool、Join 和“Decision Desk 发起第 2
  轮核验”转换事件，不依赖页面内存。
- Agent Lab 用户取消 Run
  `c258fcac-350e-42bc-b1a4-4eecefaa3ece` 已通过真实按钮验收。页面只在
  Run 活跃时显示取消按钮，最终显示“已取消 · 资源已回收”；持久化 Trace
  包含同一 request ID 的 `run_cancel_requested` 和 `run_canceled`。
  进行中的 Turn / Attempt、Task、Session 和并发槽均收敛，稍后返回的模型
  结果不能覆盖终态；同一取消请求精确重放返回相同响应。
- 严格部分失败 Run `1d418675-071e-44b1-9a21-11fc69035b90` 已证明第二轮
  必需 Tool 分支失败会让整个 Run `dead_lettered`；过期树
  `0f55b29f-1bc8-4411-a096-49eb20be9e7d` 已由恢复器收敛为终态。
- 精确重放 Run `d1d1b962-1b8c-4474-9757-c1ad11c93676` 已证明同一
  UserRequest 的重复提交返回同一 Run、root Task 和 request digest，并最终
  成功，不会重复创建执行树。
- Moody Blues replay `bf41d3b3-33d7-590a-9c30-a0217903d4e1` 已从
  generation 1 消费至 generation 2；旧 generation 重放返回 HTTP 409。
- Command Console 数据流条已用真实 Provider replay
  `4bb2d472-c614-5240-8be8-2eb8f6c4e6d5` 验收：generation 1→2，
  页面显示 Spot `7498.48`、Zero Gamma `7519.74`、Call Wall `7600`、
  Put Wall `7500` 和分离的 source / available 时间；响应不含 raw Blob
  元数据。页面内 60× 自动播放已从 generation 1 连续推进至 generation 5，
  并可完成至 generation 12；10× 回放在 generation 2 暂停后跨越一个调度
  周期仍保持 generation 2。自动播放仅在页面存活期间运行，不声称是服务端
  后台流。
- 独立模拟源 `moody_blues_replay` 已完成每帧
  `gex_compact_v1 → Trigger Sample → Occurrence → Cortex Wake` 验收。
  Replay `f5299c05-a4f9-5658-91ea-f41d694998dc` 的 generation 2 绑定
  normalized digest
  `bdfa5672719897557cafcc523886e1436e005a8aebfec5e73bc43edf99867e84`，
  页面显示 Wake Run `9c8a5503-58c1-4728-8376-b9e1a82d0080`。验收 Trigger
  已暂停，Paper / Observe / Execution Locked 已恢复，Effect Authorization
  数量为 0。
- Agent Platform 的 `go test -race ./...` 与 `go vet ./...`、139 个 Agent
  migration 文件对应 143 条 ledger 记录的幂等回放均通过。暂停验收 Trigger
  会按设计立即撤销旧 generation 的 Runtime Authority；两个故意留在处理中
  的验收 Run 因此停止领取后续分支并等待 deadline recovery。下一切片增加
  authority-revoked Run 的即时收敛，避免 Worker 重试刷屏。
- `scripts/verify-cortex-research-operations.sh --restart` 已真实重启五个应用
  服务并通过：六个必需服务健康、旧写入口仍为 410、Cortex 六项当前风险为
  0、Research 三条序列新鲜、18 条过期 Run 恢复证据、14 条 Tool 恢复事件
  和 5 条用户取消记录均在数据库中保留。
- `cortex-input`、`cortex-worker`、`db`、`gexbot-provider`、`kernel`、
  `research-gateway` 六个必需服务均运行；对外入口健康，内部 Worker 保持
  无公开端口。

## 本次 Moody Blues 接口

| 入口 | 用途 | 访问边界 |
|---|---|---|
| `GET :8300/internal/v1/moody-blues/providers` | Provider 的能力、采集策略、时间语义目录 | Cortex 内部令牌 |
| `GET :8300/internal/v1/moody-blues/providers/gexbot-classic/status` | 三类 SPX 数据的覆盖与最新时间 | Cortex 内部令牌；无原始数据 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/as-of` | GEXBOT 历史时间截点 | Cortex 内部令牌；只读 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/live` | 官方 API 按需读取并归档 | Cortex 内部令牌；只读；不得把抓取时间冒充市场数据时间 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/replays` | 创建受 generation 保护的历史回放游标 | Cortex 内部令牌；只读 |
| `POST :8300/internal/v1/moody-blues/providers/gexbot-classic/replays/{id}/next` | 消费下一条历史观察值 | Cortex 内部令牌；只读 |
| `POST :8400/v1/data-streams/gexbot/replays` | 为 Console 创建受控历史数据流 | Kernel 持有 Cortex 服务令牌；浏览器不接触 Research / Provider 凭据 |
| `POST :8400/v1/data-streams/gexbot/replays/{id}/next` | 按 generation 推进一帧并移除 raw Blob 元数据 | Kernel 持有 Cortex 服务令牌；旧 generation 返回冲突 |

回放推进响应可附带 `trigger_evaluations`：只公开 Trigger ID、metric、Sample、
Occurrence 和 Wake Run，不公开 Provider raw Blob。只有显式注册为
`moody_blues_replay` 的 Trigger 会消费历史帧；`research_gexbot` 继续只消费
新鲜的 Live 归档。

旧 `/internal/v1/gexbot/*` 路径会暂时保留为兼容别名，直到所有内部调用迁移。

## 上线清单：并行多 Agent TaskGraph

> 受控单链仍保留用于简单问题；多角色请求已经可以进入首轮并行 TaskGraph。
> 下面的进度不计入 37 Tool 上线完成度。

| 顺序 | 工作项 | 完成条件 | 状态 |
|---:|---|---|---|
| P1 | 冻结 TaskGraph / dependency / join 契约 | 计划可表达多个并行子 Task、依赖边、最大并发、deadline、预算及不可变输出契约；模型不能自行扩大权限 | 已完成：独立 frozen v1 契约、Schema、golden、DAG/Join/权限/预算校验全部通过 |
| P2 | Control 批量 admission 与 fan-out | 一次已验证计划原子创建多个独立子 Task；每个分支绑定唯一角色、Tool grant、预算和父 Run | 已完成：Control-only 原子命令、精确重放、三节点真实数据库探针及全回滚失败路径通过 |
| P3 | Scheduler 并行调度 | 不同 Specialist 可同时 claim/执行；同一 Task 仍只有一个有效 lease，重复投递不重复调用 Tool | 已完成：4 条 Worker lane、逐节点 Session/Blob ACL、Graph 独立原子并发槽、效果为 none 的 Specialist 并行执行；带 Tool 节点继续冻结等待专用执行边界 |
| P4 | Join Barrier / fan-in | 支持 `all_required`、`minimum_success`、部分失败和严格终态；Join 只读取已提交 Artifact | 已完成：Control-only Join 解析、下游 Blob/ACL、Desk fan-in、失败收敛、结果血缘及成功/失败数据库验收通过 |
| P5 | Tool 并行节点与 Moody Blues 预处理上下文 | 每个带 Tool 节点只执行准入时冻结的一项只读 Tool；回放数据先经过统一 normalize/精简框架再交给 Agent | 已完成：双 Turn 参数/证据边界、精确 grant、全部现有只读 Tool 分派、错误 Tool 拒绝及 `gex_compact_v1` 已验证 |
| P6 | 多阶段自适应研究与 DAG Trace | Desk 可在有界轮次内提出下一批子链路；网页显示真实分叉、等待、失败、汇合和下一轮 | 已完成：最多两轮；每轮 Control 重新准入；持久化 DAG Trace 与 Agent Lab 全轮次恢复视图均通过真实 Run |
| P7 | 故障与上线验收 | 通过并发、重复、崩溃恢复、慢分支、部分失败、预算耗尽和真实多角色端到端测试 | 已完成（只读上线范围）：并发、精确重放、过期恢复、工具恢复、用户取消、五服务重启、严格部分失败、预算/轮次围栏、两轮真实端到端、数据库终态不变量及全量 race/vet 均通过 |

P1–P7 已在本表定义的**只读 Cortex + Research** 范围内完成。此结论不包含
交易下单、资金效果、Live 权限或完整 AP1 正式 stage seal；这些能力必须作为
独立效果化项目重新做权限、确认、幂等、未知结果恢复和上线验收，不能由本次
只读上线结论隐式开启。
