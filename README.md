# alpheus — 期权账户 agentic 交易系统（Go）

> 名字来自枪虾（Alpheus）与虾虎鱼的共生：枪虾近乎全盲，负责挖洞守家；
> 虾虎鱼是它的眼睛。这里 kernel 是那只枪虾——不看行情、不做判断、
> 只保证家不被炸；LLM 是虾虎鱼。枪虾极少出手，出手是海里最响的一击。

架构先行，prompt 全留白。`docker compose up` 之后整条流水线
（调度 → 认知 → 风控分级 → 执行/影子 → journal）在零 prompt、零真实券商
的状态下就能端到端跑通。全部 Go，方便从 tofi 平移 session/inbox/heartbeat
等已验证的机制。

## 三条不变量（违反任何一条 = 架构被破坏）

1. **数字规则永远在 kernel，不在 prompt。** `kernel/limits.yaml` 是宪法，
   由 `internal/risk` 的 if 语句强制执行。改它是 Class-D 操作：只有人能改。
2. **agent 永远见不到券商。** 券商凭证只存在于 kernel 的 broker adapter 层；
   agent 只能调 kernel 的 HTTP API。
3. **合同在代码里，措辞在配置里。** 每个角色的输出 schema 定义在
   `agent-runtime/internal/contracts`（struct + Validate），代码强制校验；
   prompt 只负责让模型"想得好"，不负责让系统"不出错"。

## 跑起来

```bash
cp .env.example .env
docker compose up --build
./scripts/smoke.sh        # 手动过一遍四条审批路径
docker compose logs -f agent-runtime   # 看 stub 的影子提案被 Class-B 放行
```

默认 `BROKER=fake` + `COGNITION=stub`：stub 认知会周期性提交一笔影子
SPY 期权提案，日志里能看到它被清单自动放行并写入 journal——
"prompt 还没写，系统已经在测"。没有 Swagger（Go 标准库），
`scripts/smoke.sh` 就是 Day-1 的手动测试台。

`internal/risk/risk_test.go` 已带五条路径用例（A / B / C / 两种 REJECT），
`go test ./...` 可跑；这是 ROADMAP Phase 1 单测任务的种子。

两个 go.mod 里有一条指向 GitHub 镜像的 `replace gopkg.in/yaml.v3`——
无害，保留或删除均可（正常网络下 `go mod tidy` 两者等价）。

## 审批分级（`kernel/internal/risk`）

- **A 减风险**（平仓/撤单/收紧止损）：零审批即刻执行——止损路径只有一跳。
- **B 合规新仓**：清单全过（预算/总敞口/日单数/白名单/流动性/计划完整）
  → 代码自动放行，不经过任何 LLM。额度按**净值百分比**计算，
  agent 赚得越多绝对额度自动越大（进攻档宪法见 `kernel/limits.yaml`）。
- **C 例外**：清单未过但不违反绝对项 → `pending_review`，
  交 reviewer（不同家族模型）或人一键裁决（`POST /operations/{id}/review`）。
- **REJECT 绝对项**：熔断中、裸卖期权 → 直接死亡。

95% 的正常交易全自动，审批 LLM 只碰真正的例外。

## 三个 FILL POINT（按顺序填）

1. `agent-runtime/roles/*.yaml` 的 `prompt_slots`——四个角色
   （desk_master / scout / position_manager / coach），每个槽位有 TODO
   注释。每次改动 prompt 必须 bump `version`，journal 把每笔交易绑定到
   当时的 prompt 版本，之后可以像策略一样对 prompt 做 A/B。
2. `agent-runtime/internal/cognition/llm.go`——真实 LLM 调用，官方 SDK
   `github.com/anthropics/anthropic-sdk-go`：渲染非空槽位 + context →
   结构化输出（JSON schema）→ Validate 失败带错误重试一次。
   按 `model_tier` 路由 DECIDER_MODEL（贵）/ MONITOR_MODEL（便宜）——
   $300 账户上推理费必须远小于账户本身。
3. `kernel/internal/broker/robinhood.go`——真实券商，走 Robinhood MCP
   （官方 SDK `github.com/modelcontextprotocol/go-sdk`）。只读方法
   （账户/持仓/行情）可以 Phase 1–2 提前接；下单留在 Phase 4 的
   gate 之后。凭证只活在这一层。

## Session 与状态

Session 无状态、随用随弃，命名 `{role}/{date}/{trigger}/{seq}`。
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

reviewer 角色接入（C 级裁决现在留给人 curl）、marketdata 门面与 MCP
只读接入（Phase 1，见 ROADMAP）、inbox/watchlist 注入（assemble 有
TODO）、C 级批准后的执行路径、订单重挂状态机接线、熔断的实时计算
（dayState 有 TODO）、watchdog → runtime 的 /wake 通道、任何 UI。
这些都有明确的挂载点，但骨架的任务是把边界立住。
