# Cortex 工具注册进度台账

> 状态：**当前只读工具批次已完成 — 2026-07-23。** 这是把现有服务能力登记为
> Cortex 可见工具的唯一进度台账。这里的“已注册”不等于“Agent 可以调用”：
> 每个工具仍必须具备审查后的契约、Control 授权、预算、收据、角色授予和
> 验收测试，才可启用。

## 状态说明

- **Cortex 已启用**：真实 Cortex Run 可以授权调用，Trace 会显示收据。
- **仅 Kernel 可用**：有经认证的 Kernel HTTP 接口，但 Cortex Worker 不能调用。
- **候选工具**：已纳入注册范围的受限只读 MCP 能力，尚不是 Cortex Tool。
- **排除**：外部写入或资金效果；绝不是普通 Agent Tool。
- **目标 Agent**：计划中的最终角色授予，不表示该角色已经部署。目前真正可
  执行的只有 `intent`、`scout`、`desk`。

## 总进度

| 工作流 | 总数 | 已注册 | 已启用给 Cortex | 状态 |
|---|---:|---:|---:|---|
| Cortex 跨平面只读工具 | 36 | 36 | 36 | 全部已启用；Agent Lab 显示 36 已启用 / 0 待接入 |
| Robinhood MCP 安全只读工具 | 34 | 34 | 34 | 1 条财报专用桥 + 33 条审查后的通用只读桥 |
| Robinhood MCP 受管资金 mutation | 4 | 0 | 0 | 永久排除，不进入 Agent Tools |
| 其他 Robinhood MCP 写工具 | 11 | 0 | 0 | 已排除；不在 Kernel allowlist |
| 旧 Kernel / Research 只读连接器 | 2 | 0 | 0 | 需要迁移或替换；不是 Cortex Tool |

## 注册路线图

| 编号 | 交付物 | 验收条件 | 进度 |
|---|---|---|---|
| R0 | 完整库存与排除清单 | 已分类全部 49 个上游 MCP 工具，并纳入现有 Cortex/Research 工具 | 已完成 |
| R1 | Tool Registry 记录与通用 Kernel-read 收据契约 | 每个工具具备稳定 ID、版本、描述、输入/输出 schema、来源/新鲜度、效果等级、角色范围和预算 | **已完成**：33 条通用 Kernel-read Tool 使用严格 Tool/source 配对、参数白名单、不可变 intent/evidence/receipt |
| R2 | `kernel_earnings_results` 第一条完整链路 | Intent 可提出需求；Control 授权受限 symbol 读取；Kernel 只调用 `get_earnings_results`；Desk 获得持久化、带时间戳的收据 | **已完成**：`0046` / `0047` / `0049` 已迁移；真实 Run `e025fff6-706e-48f9-abc7-da0655ca2e33` 获得 Tool 授权与成功收据，Desk 仅据收据回答。 |
| R3 | 基本面与催化剂工具批次 | 接入财报日历、基本面和财务数据，并绑定来源/新鲜度契约 | **已完成接入** |
| R4 | 市场与发现工具批次 | 接入报价、历史、技术指标、指数、搜索/扫描/自选列表，且输入均受限 | **已完成接入** |
| R5 | 期权数据工具批次 | Chain、合约、报价和账户级期权读取按角色及账户范围隔离 | **已完成接入**；需要 provider UUID 的工具仍要求请求提供真实 ID |
| R6 | 组合上下文工具批次 | 账户、持仓、订单、税务 lot、PnL 必须绑定账户 | **已完成接入**；账户由 Kernel 永久绑定值注入，LLM 不能传入 |
| R7 | 只读预检工具批次 | Review 工具绝不创建订单，要求明确 proposal 上下文 | **已完成接入**；只返回模拟预检，不具备下单入口 |
| R8 | Trace 与网页证据展示 | 显示工具授权、执行收据、来源和时间 | **已完成**：Agent Lab 阶段 A 显示 36/36 已启用，阶段 B 继续单独验证自主意图路线；5 个 UUID 型测试会先要求填入前置只读工具返回的真实 ID |
| R9 | 能力与角色授予验收 | 未获授权的 Worker 无法直接调用上游 MCP | **已完成当前运行角色边界**：Intent 每次最多提出 1 个 Tool，Control 授权，Desk 只消费收据；未来专业 Agent 仍需独立授予 |

## 当前已启用的 Cortex 工具

| Tool ID | 入口端口与路径 | Provider 路径 | 描述 | 当前 Agent 范围 | 状态 |
|---|---|---|---|---|---|
| `research_web_fetch` | Cortex Input `:8400` `/internal/v1/tool-calls/web-fetch` | Research Gateway `:8300` `/internal/v1/cortex-tools/web-fetch` | 抓取用户不可变文本中一个明确的公开 HTTP(S) URL，返回有界且不可信的页面证据；它不是搜索。 | Intent → Desk；Scout 未获授权 | 已启用 |
| `research_gexbot_as_of` | Cortex Input `:8400` `/internal/v1/tool-calls/gexbot-as-of` | Research Gateway `:8300` → GEXBOT Provider `:8500` | 按时间围栏读取一个 SPX GEX 历史序列，返回标准化历史证据。 | Intent 提议，Desk 消费；Scout 未获授权 | 已启用 |
| `kernel_earnings_results` | Cortex Input `:8400` `/internal/v1/tool-calls/kernel-earnings-results` | Kernel `:8100` `/internal/v1/cortex-tools/earnings-results` → Robinhood MCP `get_earnings_results` | 只读取一个明确股票代码的标准化财报结果：EPS estimate / actual、报告日期、盘前/盘后和确认状态；丢弃上游 guide、通用 MCP 参数、账户和凭据。 | Intent 提议，Decision Desk 消费；Scout 未获授权 | 已启用；真实收据已验证 |

## Robinhood MCP 安全只读工具（Cortex 已启用）

以下 34 项均已成为 Cortex Tool。`kernel_earnings_results` 保留专用桥；
其余 33 项通过 `POST :8400/internal/v1/tool-calls/kernel-read` 授权后，调用
`POST :8100/internal/v1/cortex-tools/read`。通用 MCP 接口和 Robinhood session
没有暴露给 LLM。每个 Tool ID 只能映射到一个固定上游函数和参数白名单。

| MCP 工具 | 分类 | 有界描述 | 目标 Agent | 当前状态 |
|---|---|---|---|---|
| `get_accounts` | 组合 | 已绑定账户的身份和账户事实 | Position Manager | Cortex 已启用 |
| `get_earnings_calendar` | 催化剂 | 指定股票近期财报日期 | 基本面 Scout | Cortex 已启用 |
| `get_earnings_results` | 催化剂 | 指定股票已发布财报结果 | Intent → Decision Desk | 已通过窄桥接对 Cortex 启用 |
| `get_equity_fundamentals` | 基本面 | Provider 基本面和估值字段 | 基本面 Scout | Cortex 已启用 |
| `get_financials` | 基本面 | 指定股票财务报表数据 | 基本面 Scout | Cortex 已启用 |
| `get_equity_historicals` | 市场 | 有界历史股价 K 线 | 市场 Scout | Cortex 已启用 |
| `get_equity_price_book` | 市场 | 股票 bid/ask 与盘口快照 | 市场 Scout | Cortex 已启用 |
| `get_equity_quotes` | 市场 | 当前股票报价快照 | 市场 Scout → Decision Desk | Cortex 已启用 |
| `get_equity_technical_indicators` | 市场 | 在限定区间内计算一个明确技术指标 | 市场 Scout | Cortex 已启用 |
| `get_equity_tradability` | 市场 | 可交易性和市场状态事实 | Decision Desk | Cortex 已启用 |
| `get_indexes` | 市场 | 解析指数 symbol 与 provider ID | 市场 Scout | Cortex 已启用 |
| `get_index_quotes` | 市场 | 当前指数报价快照 | 市场 Scout | Cortex 已启用 |
| `get_option_chains` | 期权 | 标的的期权链元数据 | 期权 Scout | Cortex 已启用 |
| `get_option_instruments` | 期权 | 精确期权合约 ID 与条款 | 期权 Scout | Cortex 已启用 |
| `get_option_quotes` | 期权 | 按合约 ID 读取有界期权报价快照 | 期权 Scout | Cortex 已启用 |
| `get_option_watchlist` | 期权 | 只读期权自选列表内容 | 期权 Scout | Cortex 已启用 |
| `get_option_level_upgrade_info` | 账户/期权 | 已绑定账户的期权资格信息 | Position Manager | Cortex 已启用 |
| `get_equity_positions` | 组合 | 已绑定账户的股票持仓 | Position Manager | Cortex 已启用 |
| `get_option_positions` | 组合 | 已绑定账户的期权持仓 | Position Manager | Cortex 已启用 |
| `get_equity_orders` | 组合 | 已绑定账户的股票订单历史与状态 | Position Manager | Cortex 已启用 |
| `get_option_orders` | 组合 | 已绑定账户的期权订单历史与状态 | Position Manager | Cortex 已启用 |
| `get_equity_tax_lots` | 组合 | 已绑定账户的股票 tax lots | Position Manager | Cortex 已启用 |
| `get_portfolio` | 组合 | 已绑定账户的组合汇总 | Position Manager → Decision Desk | Cortex 已启用 |
| `get_pnl_trade_history` | 组合 | 有界的已实现交易 P&L 历史 | Position Manager | Cortex 已启用 |
| `get_realized_pnl` | 组合 | 有界的已实现 P&L 汇总 | Position Manager | Cortex 已启用 |
| `get_popular_watchlists` | 发现 | 公开热门自选列表元数据 | Scout | Cortex 已启用 |
| `get_watchlists` | 发现 | 已绑定账户或公开自选列表元数据 | Scout 或 Position Manager，按范围 | Cortex 已启用 |
| `get_watchlist_items` | 发现 | 一个明确自选列表 ID 的内容 | Scout 或 Position Manager，按范围 | Cortex 已启用 |
| `get_scanner_filter_specs` | 发现 | 有效的 Scanner filter 定义 | Scout | Cortex 已启用 |
| `get_scans` | 发现 | 可用 Scanner 定义 | Scout | Cortex 已启用 |
| `run_scan` | 发现 | 执行一个获准且有界的 Scanner ID | Scout | Cortex 已启用 |
| `search` | 发现 | 把资产名称/股票代码解析为 provider ID | Scout | Cortex 已启用 |
| `review_equity_order` | 预检 | 模拟并校验一份股票订单，不会创建订单 | Decision Desk，仅明确预检 | Cortex 已启用；仅模拟预检 |
| `review_option_order` | 预检 | 模拟并校验一份期权订单，不会创建订单 | Decision Desk，仅明确预检 | Cortex 已启用；仅模拟预检 |

## Robinhood MCP 写入面：排除出普通 Agent Tools

| MCP 工具 | 类别 | 当前入口 | 规则 | 状态 |
|---|---|---|---|---|
| `place_equity_order` | 资金 mutation | 仅 Kernel execution provider | 不作为 Agent Tool；必须走独立 operation、policy、确认流程 | 已排除 |
| `place_option_order` | 资金 mutation | 仅 Kernel execution provider | 不作为 Agent Tool；期权执行仍关闭 | 已排除 |
| `cancel_equity_order` | 资金 mutation | 仅 Kernel execution provider | 不作为 Agent Tool；仅 Kernel 对账或授权 operation 流程 | 已排除 |
| `cancel_option_order` | 资金 mutation | 仅 Kernel execution provider | 不作为 Agent Tool；期权执行仍关闭 | 已排除 |
| `add_option_to_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `add_to_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `create_scan` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `create_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `follow_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `remove_from_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `remove_option_from_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `unfollow_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `update_scan_config` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `update_scan_filters` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |
| `update_watchlist` | 外部写入 | 无 | 不在 Kernel allowlist；不注册 | 已排除 |

## 需要迁移或替换的旧只读连接器

| 能力 | 入口端口与路径 | Provider 路径 | 描述 | 目标 Agent | 当前状态 |
|---|---|---|---|---|---|
| Brave Web Search | Kernel `:8100` `GET /research/search` | Research Gateway `:8300` `POST /v1/web/search` | 有界 Brave 搜索；目前 Key 由 Kernel 为单次调用解密。 | Data Desk / Scout | 旧接口；非 Cortex Tool；尚无记录的真实 provider smoke |
| Robinhood Research News | Kernel `:8100` `GET /research/news/{symbol}` | Research Gateway `:8300` `POST /v1/robinhood/news` | 用单独导入的二级凭据抓取最多 20 条标准化 Robinhood 新闻标题。 | 基本面 Scout / 催化剂 Scout | 旧接口；非 Cortex Tool |

## 最终交付表

端口说明：Research Tool 由 Cortex Control `:8400` 授权后进入 Research
Gateway `:8300`；GEX 再进入 Provider `:8500`。财报专用工具进入 Kernel
`:8100/internal/v1/cortex-tools/earnings-results`；其余 Kernel 工具统一进入
`:8100/internal/v1/cortex-tools/read`，但仍按 Tool ID 固定上游函数和参数白名单。

| Tool ID | 端口链路 | 上游工具 / Provider | 描述 | 归属 Agent | 状态 |
|---|---|---|---|---|---|
| `research_web_fetch` | `:8400 → :8300` | Research `web-fetch` | 读取一个明确公开 URL 的有界、不可信证据 | Intent → Decision Desk | 已启用 |
| `research_gexbot_as_of` | `:8400 → :8300 → :8500` | GEXBOT Provider | 按 `as_of` 围栏读取 SPX GEX 历史快照 | Intent → Decision Desk | 已启用 |
| `kernel_accounts` | `:8400 → :8100` | `get_accounts` | 绑定账户的身份、类型与状态 | Position Manager / Desk | 已启用 |
| `kernel_earnings_calendar` | `:8400 → :8100` | `get_earnings_calendar` | 有界的近期财报日历 | Fundamental Scout / Desk | 已启用 |
| `kernel_earnings_results` | `:8400 → :8100`（专用桥） | `get_earnings_results` | 标准化 EPS 与报告日期事实 | Intent → Decision Desk | 已启用 |
| `kernel_equity_fundamentals` | `:8400 → :8100` | `get_equity_fundamentals` | 股票基本面与估值字段 | Fundamental Scout / Desk | 已启用 |
| `kernel_financials` | `:8400 → :8100` | `get_financials` | 有界财务报表数据 | Fundamental Scout / Desk | 已启用 |
| `kernel_equity_historicals` | `:8400 → :8100` | `get_equity_historicals` | 有界历史股价 K 线 | Market Scout / Desk | 已启用 |
| `kernel_equity_price_book` | `:8400 → :8100` | `get_equity_price_book` | bid、ask 与盘口快照 | Market Scout / Desk | 已启用 |
| `kernel_equity_quotes` | `:8400 → :8100` | `get_equity_quotes` | 股票报价快照 | Market Scout → Desk | 已启用 |
| `kernel_equity_technical_indicators` | `:8400 → :8100` | `get_equity_technical_indicators` | 指定区间的单一技术指标 | Market Scout / Desk | 已启用 |
| `kernel_equity_tradability` | `:8400 → :8100` | `get_equity_tradability` | 可交易性与市场状态 | Decision Desk | 已启用 |
| `kernel_indexes` | `:8400 → :8100` | `get_indexes` | 指数 symbol 与 Provider ID 解析 | Market Scout / Desk | 已启用 |
| `kernel_index_quotes` | `:8400 → :8100` | `get_index_quotes` | 指数报价快照 | Market Scout / Desk | 已启用 |
| `kernel_option_chains` | `:8400 → :8100` | `get_option_chains` | 标的期权链元数据 | Options Scout / Desk | 已启用 |
| `kernel_option_instruments` | `:8400 → :8100` | `get_option_instruments` | 期权合约 ID 与条款 | Options Scout / Desk | 已启用 |
| `kernel_option_quotes` | `:8400 → :8100` | `get_option_quotes` | 指定期权合约报价 | Options Scout / Desk | 已启用 |
| `kernel_option_watchlist` | `:8400 → :8100` | `get_option_watchlist` | 现有期权自选列表快照 | Options Scout / Desk | 已启用 |
| `kernel_option_level_upgrade_info` | `:8400 → :8100` | `get_option_level_upgrade_info` | 绑定账户的期权资格 | Position Manager / Desk | 已启用 |
| `kernel_equity_positions` | `:8400 → :8100` | `get_equity_positions` | 绑定账户股票持仓 | Position Manager / Desk | 已启用 |
| `kernel_option_positions` | `:8400 → :8100` | `get_option_positions` | 绑定账户期权持仓 | Position Manager / Desk | 已启用 |
| `kernel_equity_orders` | `:8400 → :8100` | `get_equity_orders` | 股票订单历史与状态，只读 | Position Manager / Desk | 已启用 |
| `kernel_option_orders` | `:8400 → :8100` | `get_option_orders` | 期权订单历史与状态，只读 | Position Manager / Desk | 已启用 |
| `kernel_equity_tax_lots` | `:8400 → :8100` | `get_equity_tax_lots` | 股票 tax lots | Position Manager / Desk | 已启用 |
| `kernel_portfolio` | `:8400 → :8100` | `get_portfolio` | 绑定账户组合汇总 | Position Manager → Desk | 已启用 |
| `kernel_pnl_trade_history` | `:8400 → :8100` | `get_pnl_trade_history` | 有界已实现交易 P&L 历史 | Position Manager / Desk | 已启用 |
| `kernel_realized_pnl` | `:8400 → :8100` | `get_realized_pnl` | 有界已实现 P&L 汇总 | Position Manager / Desk | 已启用 |
| `kernel_popular_watchlists` | `:8400 → :8100` | `get_popular_watchlists` | 公开热门自选列表元数据 | Scout / Desk | 已启用 |
| `kernel_watchlists` | `:8400 → :8100` | `get_watchlists` | 自选列表名称与 ID | Scout / Position Manager / Desk | 已启用 |
| `kernel_watchlist_items` | `:8400 → :8100` | `get_watchlist_items` | 指定自选列表 ID 的内容 | Scout / Position Manager / Desk | 已启用 |
| `kernel_scanner_filter_specs` | `:8400 → :8100` | `get_scanner_filter_specs` | Scanner 筛选字段定义 | Scout / Desk | 已启用 |
| `kernel_scans` | `:8400 → :8100` | `get_scans` | 可用 Scanner 定义 | Scout / Desk | 已启用 |
| `kernel_run_scan` | `:8400 → :8100` | `run_scan` | 执行一个明确的 Scanner ID | Scout / Desk | 已启用 |
| `kernel_search` | `:8400 → :8100` | `search` | 名称或代码到 Provider ID 的解析 | Scout / Desk | 已启用 |
| `kernel_review_equity_order` | `:8400 → :8100` | `review_equity_order` | 股票订单模拟预检，绝不创建订单 | Decision Desk | 已启用（仅预检） |
| `kernel_review_option_order` | `:8400 → :8100` | `review_option_order` | 期权订单模拟预检，绝不创建订单 | Decision Desk | 已启用（仅预检） |

所有行都产生不可变 Tool intent、Evidence、Receipt 和 Trace。任何资金 mutation
或第三方写入仍不作为普通 Agent Tool。
