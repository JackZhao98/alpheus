# alpheus — 期权账户 agentic 交易系统（Go）

> 名字来自枪虾（Alpheus）与虾虎鱼的共生：枪虾近乎全盲，负责挖洞守家；
> 虾虎鱼是它的眼睛。这里 kernel 是那只枪虾——不看行情、不做判断、
> 只保证家不被炸；LLM 是虾虎鱼。枪虾极少出手，出手是海里最响的一击。

架构先行，prompt 全留白。显式初始化数据库策略后，整条流水线
（调度 → 认知 → 风控分级 → 执行/影子 → journal）在零 prompt、零真实券商
的状态下就能端到端跑通。全部 Go，方便从 tofi 平移 session/inbox/heartbeat
等已验证的机制。

## 项目文档

- [`docs/plan/INDEX.md`](docs/plan/INDEX.md) — 冻结计划的唯一进度表、阶段索引与 AI 阅读入口。
- [`docs/PLAN.md`](docs/PLAN.md) — 兼容旧链接的计划入口。
- [`docs/AUDIT.md`](docs/AUDIT.md) — 面向运行系统的黑盒审计章程。

## 三条不变量（违反任何一条 = 架构被破坏）

1. **数字规则永远在 kernel，不在 prompt。** 人类策略由数据库中的不可变
   revision/head 授权，`internal/risk` 的 if 语句强制执行；`limits.yaml` 只
   是 fresh database 的显式导入材料，不是运行时兜底。
2. **agent 永远见不到券商。** 券商凭证只存在于 kernel 的 broker adapter 层；
   agent 只能调 kernel 的 HTTP API。
3. **合同在代码里，措辞在配置里。** 每个角色的输出 schema 定义在
   `agent-runtime/internal/contracts`（struct + Validate），代码强制校验；
   prompt 只负责让模型"想得好"，不负责让系统"不出错"。

## 跑起来

```bash
cp .env.example .env
docker compose up -d db
docker compose build kernel
docker compose run --rm kernel /kernel kernel-policy \
  --file=/limits.yaml --expected-generation=0 \
  --recorded-by=dev:local --reason='initial local policy'
docker compose up --build
./scripts/smoke.sh        # 手动过一遍四条审批路径
docker compose logs -f agent-runtime   # 看 stub 的影子提案被 Class-B 放行
```

`kernel-policy` 是 fresh database 的一次性显式 bootstrap。正常 Kernel
启动不会读取 `limits.yaml`，已有 head 时也不会用文件兜底；后续修改策略
必须携带当前 `--expected-generation`，并留下操作者和原因。

默认 `BROKER=fake` + `COGNITION=stub`：stub 认知会周期性提交一笔影子
SPY 期权提案，日志里能看到它被清单自动放行并写入 journal——
"prompt 还没写，系统已经在测"。没有 Swagger（Go 标准库），
`scripts/smoke.sh` 就是 Day-1 的手动测试台。

## 运行模式与认证

`TRADING_MODE` 默认是 `sim`；此外支持 `shadow`（所有 operation 强制影子）、
`read_only`（所有写端点 405）和 `live`。非 sim 模式必须配置彼此不同的
`RUNTIME_TOKEN`、`ADMIN_TOKEN`、`KERNEL_TOKEN`。Runtime Token 可以提案和写
journal/blackboard，但不能审批自己的 Class-C；reviewer 身份只取自认证主体。
`live` 还必须同时满足 `LIVE_TRADING_ENABLED=true`、精确的
`LIVE_ACCOUNT_ID`，且拒绝 FakeBroker，否则进程在启动阶段直接退出。

`POST /halt` 是 Admin-only 的全局 kill switch：它阻止 live/shadow 两本账的
所有新开仓，但经过持仓验证的平仓和撤单仍保持 Class-A 快路径。状态和原因
持久化在事件流中，重启后继续生效。

### Robinhood 只读 Provider（M8A）

生产读取和执行已在 Go 类型边界拆开。Robinhood 的账户 Provider 没有任何
下单或撤单方法，MCP client 也只接受提交到仓库并经审阅的只读工具 allowlist。
`read_only` 不挂载写端点；`shadow` 使用相同的生产账户和行情，但所有提案仍
只进入影子账本。真实执行能力继续留在 M11。

首次连接会把 OAuth 状态写到仓库外的 0600 文件，随后显式生成无敏感信息的
工具 schema snapshot：

```bash
cd kernel
go run ./cmd/rh-mcp -action auth \
  -token-file "$HOME/.config/alpheus/rh-oauth.json"
go run ./cmd/rh-mcp -action discover \
  -token-file "$HOME/.config/alpheus/rh-oauth.json" \
  -out ../docs/rh_mcp_capabilities.json
go run ./cmd/rh-mcp -action accounts \
  -token-file "$HOME/.config/alpheus/rh-oauth.json"
# After the owner explicitly chooses the masked Agentic account:
go run ./cmd/rh-mcp -action bind \
  -token-file "$HOME/.config/alpheus/rh-oauth.json" \
  -account-last4 0000 \
  -binding-file "$HOME/.config/alpheus/live-account-id"
go run ./cmd/rh-mcp -action capture \
  -token-file "$HOME/.config/alpheus/rh-oauth.json" \
  -binding-file "$HOME/.config/alpheus/live-account-id" \
  -private-dir "$HOME/.config/alpheus/discovery"
```

使用 `BROKER=robinhood` 时，即使是 `read_only` / `shadow` 也必须显式设置
`LIVE_ACCOUNT_ID` 或指向 0600 文件的 `LIVE_ACCOUNT_ID_FILE`，Alpheus 不会选
“默认”或“第一个”账户，也不允许两者同时设置。同时设置
`RH_MCP_TOKEN_FILE` 和 `RH_MCP_CAPABILITIES_FILE`。启动时 kernel 会比较
线上工具和已提交 snapshot；任何必需工具缺失、改名或 schema 不兼容都拒绝
启动。token、原始 Provider payload 和账户号不会进入日志、事件、数据库
payload 或 API 响应。

Docker 运行真实只读 Provider 时使用单独的 secret-volume override；不要把
宿主机绝对路径原样当作容器路径：

```bash
export RH_MCP_SECRET_DIR="$HOME/.config/alpheus"
docker compose -f docker-compose.yml -f docker-compose.robinhood.yml up --build
```

override 只挂载包含 `rh-oauth.json` 的目录，不会在 sim 启动时制造空 secret
文件或把 OAuth 状态打进镜像。

新增的只读入口如下；非 sim 模式全部要求读权限 token：

```text
GET /market/quote/{symbol}
GET /market/chain/{underlying}?expiry=YYYY-MM-DD&window_pct=15
GET /market/expirations/{underlying}
GET /market/bars/{symbol}?days=30
GET /market/movers?dir=up&n=10
GET /market/hours
GET /provider/status
```

kernel 会在调用 Provider 前把 chain 窗口截到 15 个百分点、bars 截到 30 天、
movers 截到 10 个。过期、未来时间戳、锁盘、交叉盘、非正数、不完整或匹配
歧义的报价一律 fail closed。

当前认证后的 Robinhood 工具目录没有 market-hours、movers 或独立的
expirations 工具，因此生产实现不会猜工具名：前两者明确 fail closed，到期日由
`get_option_chains` 的已验证字段提供。期权 instrument 只有在链与合约共同证明
固定 tick、整数数量、标准 multiplier=100 且没有调整现金交付时才可用；股票
instrument 因缺少跨订单类型的精确 tick/数量增量字段仍然 fail closed。
已核实的 Provider 字段与 M3D buying-power 决策见
[`docs/rh_mcp_facts.md`](docs/rh_mcp_facts.md)。

### Trading Cockpit（M8B + M7，已落地）

kernel 已内嵌一个无构建步骤的只读驾驶舱：

```text
http://127.0.0.1:8100/
http://127.0.0.1:8100/cockpit
```

Agent 测试已从 Cockpit 拆到独立页面：

```text
http://127.0.0.1:8100/agent-lab
```

设置 `AGENT_WEB_PASSWORD`（至少 12 字节）和独立的
`AGENT_WEB_SESSION_KEY`（至少 32 字节）后，用户只需输入密码；服务端签发
12 小时 HttpOnly/SameSite 会话，不必把机器 token 复制进浏览器。输入股票代码
和问题后，默认 Auto workflow 先由类型化 Intent Interpreter 在代码提供的当前
能力清单中选择 Scout-only 或 Team；虚构、重复或缺失的 capability 会失败关闭。
Team workflow 先让 Scout 基于最新标准化报价与 30 日 bars
产生类型化证据简报，再让只读 Decision Desk 返回 `WAIT/PASS`、理由和观察条件；
也可手动选择 Team 或 Scout-only 以少一次模型调用。只读 Desk 若尝试 `PROPOSE`、非空 proposals 或 blackboard
写入，代码会直接拒绝整个结果。Agent Lab 另有 `OpenAI API Token` 密码框：token 只保留在当前页面
内存并随手动查询有界转发，不写数据库、日志、cookie 或浏览器存储；刷新即清除。
该手动路径固定使用 `gpt-5.6-sol`，不会把后台定时 Agent 从 `stub` 切成真实模型。
查询任务异步写入 `agent_query_job`，页面轮询 queued/running/terminal 状态与持久化
结果；模型 token 不进入该表。`POST /agent/query` 是只读路径，不能创建 operation。
当前页面由本地 HTTP 提供，
只应在可信 LAN 使用；跨不可信网络前必须先加 HTTPS。

它显示运行模式、Provider/snapshot 状态、脱敏账户、资金、live/shadow
双账本、持仓、行情、外部订单/成交诊断，以及带 `(ts,id)` 游标的最近操作。
Live MCP Tool Lab 另外列出 34 个经审阅的无状态变更工具（32 个数据查询和
2 个订单预检模拟），显示提交快照里的输入 schema，并允许手工填写 JSON
参数。账户参数由 kernel 固定注入；15 个下单、撤单、watchlist/scanner
写工具在服务端 allowlist 中不存在。
非 sim 模式只在当前标签页内存里保存 read-capable token。需要人工控制时，页面
才单独验证并在内存中保存 Admin Token；刷新即清除，不使用 cookie、URL 或
localStorage。Class-C 面板展示失败检查、申报/推导风险、数量、multiplier、持久化
价格上限和最新 sane quote，可 Approve/Reject；控制面还提供二次确认的全局 Halt、
只针对当前 ledger/reason 的 Breaker Resume，以及不可操作的 stale/unknown attempt
和 held reservation 警告。所有 mutation 都要求精确匹配 `CONSOLE_ORIGIN`，
`read_only` 部署则结构性禁用控制。页面仍没有直接下单、撤单、改单、释放预留或
重试不确定 broker effect 的按钮。

MCP Lab 返回值在服务端完成大小限制、JSON 解码、账户/secret 字段脱敏和重新编码，
不透传原始 transport payload。所有外部/存储文本都通过 `textContent` 渲染，页面
带不允许 inline script 的 CSP。

`internal/risk/risk_test.go` 已带五条路径用例（A / B / C / 两种 REJECT），
`go test ./...` 可跑；这是
[`Phase 4` 的 Milestone 9](docs/plan/05_PRELIVE_AND_LIVE.md) 风险测试扩展的种子。

M9 pre-live fault-injection certification 已落地。`./scripts/certify-m9.sh`
会在独立 FakeBroker/PostgreSQL Compose project 中运行 race/vet、96.6% risk
coverage、四类并发容量闸门、完整交易日幂等重放、暂停/替换数据库和 crash-window
恢复，并在退出时删除测试栈和数据卷；证据矩阵见
[`docs/m9_certification.md`](docs/m9_certification.md)。它不会连接或改变当前
Robinhood `read_only` 部署。

M10 的 cognition transport 同时支持 OpenAI Responses API 和 Anthropic；默认仍是
`COGNITION=stub`。Scout 已有最小只读 prompt，其他交易角色的 prompt 槽位仍按
冻结计划保持空白。启用 `llm` 时必须在启动前选择 `LLM_PROVIDER`，提供对应的
API key、`DECIDER_MODEL` 和 `MONITOR_MODEL`；OpenAI 当前配置示例使用
`gpt-5.6-sol`。每个槽位先受
字节上限约束，完整请求再通过 token-count API 对
`SESSION_TOKEN_BUDGET` 做精确检查，超限拒绝且不生成。模型只能用单个强制
JSON-schema tool 返回 contract，本地严格解码和 Validate 失败最多重试一次。blackboard 和
lessons 始终是不可信 user data，绝不进入 system prompt。使用量 telemetry
只包含 role/model/token/latency/status 元数据，由 `RUNTIME_TOKEN` 写入 kernel
事件；`read_only` 模式下该端点仍为 405。

两个 go.mod 里有一条指向 GitHub 镜像的 `replace gopkg.in/yaml.v3`——
无害，保留或删除均可（正常网络下 `go mod tidy` 两者等价）。

## 审批分级（`kernel/internal/risk`）

- **A 减风险**（平仓/撤单/收紧止损）：零审批即刻执行——止损路径只有一跳。
  live 平仓进入 A 之前必须匹配真实持仓，数量不得超过持仓；订单方向由
  kernel 根据持仓正负推导（平多卖 bid、平空买 ask），不会采用 payload
  中的 `side`。live 平仓在 PostgreSQL 的 `(ledger,symbol)` 事务锁内扣除
  已持有的 close reservation，并把 operation、reservation 和带稳定 client id
  的 execution attempt 一次提交后才触达 broker；不再依赖单进程 mutex。
  崩溃恢复会重新核对仓位方向和其他 reservation。M2.9 起，每笔 durable fill
  与 close reservation 的数量扣减在同一事务完成；超时或结果不明时，未被完整
  证明的剩余 reservation 继续保持 held（fail closed）。撤单同样先写 attempt；
  原生止损单上线前，`tighten_stop` 只更新 operation payload 与 journal。
- **B 合规新仓**：清单全过（预算/总敞口/日单数/白名单/流动性/计划完整）
  → 代码自动放行，不经过任何 LLM。额度按**净值百分比**计算，
  agent 赚得越多绝对额度自动越大；live 与 shadow 使用相同清单、独立的
  市场日日内交易计数。每个获准 open 都先写不可撤销的 `trade_grant`；即使
  broker 拒绝也消耗当日槽位，避免失败循环绕过上限。PostgreSQL 事务锁会串行化
  `count → resources → classify → grant → reservation → attempt`。M3A 使用跨市场日
  稳定的 per-ledger 锁；总开仓风险等于已成交 exposure lots 加仍 held 的 open
  reservations，挂单不会制造风险或购买力的空窗。live fill 在同一事务里把预留
  转成 exposure lot；shadow 则原子写 synthetic order/fill、独立 paper 资金与
  持仓，从不调用 broker。M3C 按 durable FIFO close allocation 计算成本基础
  已实现 PnL（含费用与期权 multiplier）；live 同时读取 Robinhood 当日已实现
  PnL，始终采用本地/Provider 中更亏损的值。超过对账容差、触及日亏阈值或达到
  连亏天数都会按 ledger 独立熔断；`POST /breaker/resume` 只接受 Admin Token，
  override 只在当前市场日有效。Cockpit 的 live/shadow 卡片显示 PnL、日亏阈值、
  连亏天数与 breaker 状态（进攻档宪法见 `kernel/limits.yaml`）。
- **C 例外**：清单未过但不违反绝对项 → `pending_review`，
  交 reviewer（不同家族模型）或人一键裁决（`POST /operations/{id}/review`）。
  Admin 批准时会锁住待审行，再取账本锁和最新账户/行情，重新执行所有绝对项；
  `approved`、trade grant、open reservation、execution attempt 与 typed order
  在同一事务提交，随后才执行。熔断、坏行情或购买力不足会 409 且继续保持
  `pending_review`，只有超过 1800 秒的提案会原子转成终态 `expired`。
- **REJECT 绝对项**：熔断中、任何单腿 `open + sell`、风险声明不实、
  行情/合约/购买力依赖不可信 → 直接死亡。

95% 的正常交易全自动，审批 LLM 只碰真正的例外。

M2.8 的 `proposal_ttl_sec` 为人工批准的 1800 秒。待审 Class-C 超时后不能再
批准；进程若在 attempt 提交后、券商调用前崩溃，reconciler 会重新取行情/
仓位并跑完整 gate。已授予的 trade grant 保留，只有取得终态证明的未成交资源
预留才释放。

## 三个 FILL POINT（按顺序填）

1. `agent-runtime/roles/*.yaml` 的 `prompt_slots`——四个角色
   （desk_master / scout / position_manager / coach），每个槽位有 TODO
   注释。每次改动 prompt 必须 bump `version`，journal 把每笔交易绑定到
   当时的 prompt 版本，之后可以像策略一样对 prompt 做 A/B。
2. `agent-runtime/internal/cognition/llm.go`——真实 LLM 调用；OpenAI 使用
   Responses API，Anthropic 使用官方 Go SDK：渲染非空槽位 + context →
   结构化输出（JSON schema）→ Validate 失败带错误重试一次。
   按 `model_tier` 路由 DECIDER_MODEL（贵）/ MONITOR_MODEL（便宜）——
   $300 账户上推理费必须远小于账户本身。
3. `kernel/internal/broker/robinhood.go`——真实券商，走 Robinhood MCP
   （官方 SDK `github.com/modelcontextprotocol/go-sdk`）。只读方法
   （账户/持仓/行情）在 M8A 提前接；真实下单仍严格留到 M11。
   凭证只活在这一层。

## Session 与状态

Session 无状态、随用随弃，命名
`{role}/{date}/{trigger}/{occurrence_id}/{seq}`。
状态全部外置：postgres 事件表（审计）、operations/orders/fills（交易）、
blackboard（当日共享作战图）、journal + lessons（学习）。任何 session
挂了重启零损失。调度骨架在 `kernel/internal/watchdog`（固定 cron，
含 9:12 开盘决策，时区随 TZ_MARKET，tzdata 已内嵌），agent 自调度只是
加密度的优化——骨架由代码保证，永不缺席。

## 学习闭环

开仓时 journal 锁定假设（setup/论点/失效条件/计划退出），coach 每晚补
结果归因（盈亏/滑点/规则遵守/错误分类：选择错/时机错/执行错/纯方差），
产出带置信度和适用条件的 lessons；`GET /lessons` 的 top-5 由 assemble
自动注入每次决策的 context——经验不是躺在库里，是喂进嘴里。
每周 coach 汇总 per-setup 统计（直接在 postgres 用 SQL 算），范围内调参
自动生效，越界建议升级人工。

## 策略测试路径（fake broker 的三重身份）

fake adapter = Robinhood 没有的模拟盘 = 集成测试靶 = 回测场
（把历史行情灌进 `POST /sim/quote` 即可重放）。新 playbook 的生命周期：
影子模式跑 2–4 周（`shadow: true`，journal 记录、永不触达券商）→
期望值为正且样本足够 → 人工批准 → 最小尺寸实盘 → 正常尺寸。

## 骨架刻意没做的事

角色 prompt 内容仍为空且由人编写；M10 只完成了 LLM transport、contract 和
预算边界，默认不会产生真实模型调用。C 级裁决仍留给带 Admin Token 的人。
inbox/watchlist 注入（assemble 有 TODO）、watchdog 的漏跑 heartbeat 修复任务，
以及 M11 之前明确禁止的 Robinhood 生产写能力也尚未实现。
券商原生止损单也尚未实现；当前 `tighten_stop` 只留下可审计的新止损记录。
这些都有明确的挂载点，但骨架的任务是把边界立住。
