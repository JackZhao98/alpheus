const byId = (id) => document.getElementById(id);

async function request(path, options = {}) {
  const response = await fetch(path, {...options, cache:"no-store"});
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(payload?.error || `HTTP_${response.status}`);
    error.code = payload?.error_code || `http_${response.status}`;
    throw error;
  }
  return payload;
}

function formatError(error) {
  return error?.code ? `[${error.code}] ${error.message}` : error.message;
}

let currentConversation = null;
let conversationEntries = [];

// This operator-facing catalog mirrors the reviewed Cortex allowlist. Runtime
// authorization remains server-side and every execution still requires an
// immutable intent plus a durable receipt.
const toolPrecisionTests = [
  {
    id: "research_web_fetch", state: "enabled", symbol: "SPY", source: "Research Gateway", selector: "受控 URL 规则",
    roles: "Intent → Decision Desk", description: "读取一个明确的公开网页，作为有界、不可信证据。",
    prompt: "请先读取这个公开投资者教育网页，再提取其中两条关于投资风险的原文事实；不得凭记忆回答或使用其他来源：https://www.investor.gov/introduction-investing",
  },
  {
    id: "research_gexbot_as_of", state: "enabled", symbol: "SPX", source: "GEXBOT Provider", selector: "LLM Intent",
    roles: "Intent → Options Scout → Decision Desk", description: "按 as_of 时间围栏读取一条 SPX GEX 历史快照。",
    prompt: "请读取当前可用的一条 SPX GEX Full 历史快照；分别报告实际 observed_at、首次 available_at 和请求 as_of 截止时间，不要把截止时间当作观测时间，也不要补充实时行情。",
  },
  {
    id: "market_gexbot_live", state: "enabled", symbol: "SPX", source: "GEXBOT 官方 API", selector: "LLM Intent",
    roles: "Intent → Options Scout → Decision Desk", description: "按需读取最新一条官方 SPX GEX 响应，并永久保存原始 Blob 与执行收据。",
    prompt: "请调用官方 GEXBot Live API，读取最新一条 SPX GEX Full 数据；必须分别报告 provider 的 source_timestamp 和本次 fetched_at，并明确两者不是同一个时间，只依据工具收据回答。",
  },
  {
    id: "kernel_accounts", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的身份和账户事实。", prompt: "请读取我已绑定经纪账户的基本账户事实，只列账户类型与状态。",
  },
  {
    id: "kernel_earnings_calendar", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Fundamental Scout",
    description: "指定股票近期财报日期。", prompt: "请查询 AAPL 接下来最近一次财报的日期与盘前或盘后时间。",
  },
  {
    id: "kernel_earnings_results", state: "enabled", symbol: "TSLA", source: "Kernel → Robinhood MCP", selector: "LLM Intent",
    roles: "Intent → Decision Desk", description: "一个股票代码的标准化已发布财报结果。",
    prompt: "请精确调用已安装的只读财报结果工具，读取 TSLA 最近已公布季度的 EPS 实际值、预期值和报告日期；只依据工具收据回答。",
  },
  {
    id: "kernel_equity_fundamentals", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Fundamental Scout",
    description: "股票基本面和估值字段。", prompt: "请读取 MSFT 的基本面与估值字段，并只列出 provider 返回的核心字段。",
  },
  {
    id: "kernel_financials", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Fundamental Scout",
    description: "一只股票的有界财务报表数据。", prompt: "请读取 NVDA 最近可用的财务报表数据，只概括营收、净利润和经营现金流字段。",
  },
  {
    id: "kernel_equity_historicals", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Market Scout",
    description: "有界历史股价 K 线。", prompt: "请读取 AAPL 最近 20 个交易日的日线历史价格，用于观察区间走势。",
  },
  {
    id: "kernel_equity_price_book", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Market Scout",
    description: "股票 bid、ask 与盘口快照。", prompt: "请读取 SOFI 当前的 bid、ask 与盘口快照，只报告可得报价字段。",
  },
  {
    id: "kernel_equity_quotes", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Market Scout → Decision Desk",
    description: "股票当前报价快照。", prompt: "请读取 AMD 当前股票报价快照，包括最新价、涨跌和时间戳。",
  },
  {
    id: "kernel_equity_technical_indicators", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Market Scout",
    description: "指定区间内的单一技术指标。", prompt: "请计算 SPY 最近 20 个交易日的 RSI 技术指标，并说明所用区间。",
  },
  {
    id: "kernel_equity_tradability", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Decision Desk",
    description: "股票可交易性和市场状态。", prompt: "请读取 GME 当前是否可交易以及市场状态事实。",
  },
  {
    id: "kernel_indexes", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Market Scout",
    description: "指数 symbol 到 provider 标识的解析。", prompt: "请解析 ^SPX 对应的指数 provider 标识和基本指数事实。",
  },
  {
    id: "kernel_index_quotes", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Market Scout",
    description: "指数当前报价快照；需要先由 kernel_indexes 取得真实 UUID。", prompt: "请只调用指数报价工具，读取 instrument UUID {{INDEX_UUID}} 的当前指数报价快照及时间戳；不要猜测或替换这个 UUID。",
  },
  {
    id: "kernel_option_chains", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Options Scout",
    description: "标的的期权链元数据。", prompt: "请读取 SPY 期权链的可用到期日与行权价范围元数据。",
  },
  {
    id: "kernel_option_instruments", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Options Scout",
    description: "精确期权合约 ID 与条款。", prompt: "请读取 SPY 下一周到期、接近平值的一张看涨期权的合约标识与条款。",
  },
  {
    id: "kernel_option_quotes", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Options Scout",
    description: "有界期权合约报价快照；需要先由 kernel_option_instruments 取得真实 UUID。", prompt: "请只调用期权报价工具，读取 option instrument UUID {{OPTION_UUID}} 的 bid、ask、最新价和时间戳；不要猜测或替换这个 UUID。",
  },
  {
    id: "kernel_option_watchlist", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Options Scout",
    description: "现有期权自选列表快照。", prompt: "请读取现有期权自选列表中的合约，不要修改任何自选列表。",
  },
  {
    id: "kernel_option_level_upgrade_info", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的期权资格事实。", prompt: "请读取已绑定账户当前的期权资格信息，只报告资格事实。",
  },
  {
    id: "kernel_equity_positions", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的股票持仓。", prompt: "请读取我已绑定账户的股票持仓，只列代码、数量和市值字段。",
  },
  {
    id: "kernel_option_positions", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的期权持仓。", prompt: "请读取我已绑定账户的期权持仓，只列合约、数量与可得市值字段。",
  },
  {
    id: "kernel_equity_orders", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的股票订单历史与状态。", prompt: "请读取我已绑定账户最近的股票订单及其状态；不要创建或修改订单。",
  },
  {
    id: "kernel_option_orders", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的期权订单历史与状态。", prompt: "请读取我已绑定账户最近的期权订单及其状态；不要创建或修改订单。",
  },
  {
    id: "kernel_equity_tax_lots", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "已绑定账户的股票 tax lots。", prompt: "请读取我已绑定账户中 AAPL 的股票 tax lots，只列取得日期、数量和成本基础字段。",
  },
  {
    id: "kernel_portfolio", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager → Decision Desk",
    description: "已绑定账户的组合汇总。", prompt: "请读取我已绑定账户的组合汇总，只报告总市值、现金与可得的当日变化字段。",
  },
  {
    id: "kernel_pnl_trade_history", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "有界已实现交易 P&L 历史。", prompt: "请读取我已绑定账户最近 30 天的已实现交易盈亏历史。",
  },
  {
    id: "kernel_realized_pnl", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Position Manager",
    description: "有界已实现 P&L 汇总。", prompt: "请读取我已绑定账户今年截至目前的已实现盈亏汇总。",
  },
  {
    id: "kernel_popular_watchlists", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout",
    description: "公开热门自选列表元数据。", prompt: "请读取当前公开热门自选列表的名称和元数据。",
  },
  {
    id: "kernel_watchlists", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout / Position Manager",
    description: "公开或已绑定账户的自选列表元数据。", prompt: "请读取我已绑定账户中的自选列表名称和标识，不要修改它们。",
  },
  {
    id: "kernel_watchlist_items", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout / Position Manager",
    description: "一个明确自选列表 ID 的内容；需要先由 kernel_watchlists 取得真实 UUID。", prompt: "请只调用自选列表内容工具，读取 list UUID {{WATCHLIST_UUID}} 的成分，只列其中的资产代码；不要猜测或替换这个 UUID。",
  },
  {
    id: "kernel_scanner_filter_specs", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout",
    description: "有效的 Scanner filter 定义。", prompt: "请读取可用股票扫描器的筛选字段定义和允许值。",
  },
  {
    id: "kernel_scans", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout",
    description: "可用 Scanner 定义。", prompt: "请列出当前可用的股票扫描器定义及其标识。",
  },
  {
    id: "kernel_run_scan", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout",
    description: "执行一个获准且有界的 Scanner；需要先由 kernel_scans 取得真实 UUID。", prompt: "请只调用运行扫描器工具，执行 scan UUID {{SCAN_UUID}}，并仅返回前 10 个结果；不要猜测或替换这个 UUID。",
  },
  {
    id: "kernel_search", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Scout",
    description: "资产名称或股票代码到 provider 标识的解析。", prompt: "请搜索“Tesla”并返回对应资产的代码和 provider 标识。",
  },
  {
    id: "kernel_review_equity_order", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Decision Desk",
    description: "股票订单模拟预检，不会创建订单。", prompt: "请只调用股票订单预检工具，模拟检查一份 regular_hours、GFD、以 $180.00 限价买入 1 股 AAPL 的订单；quantity 使用字符串“1”。只报告校验结果，绝对不要创建订单。",
  },
  {
    id: "kernel_review_option_order", state: "enabled", source: "Kernel → Robinhood MCP", roles: "Decision Desk",
    description: "期权订单模拟预检；需要先由 kernel_option_instruments 取得真实 UUID，不会创建订单。", prompt: "请只调用期权订单预检工具，模拟检查买入开仓 1 张 SPY 单腿合约：option UUID {{OPTION_UUID}}，side=buy，position_effect=open，quantity 字符串“1”，type=market，time_in_force=gfd，market_hours=regular_hours，underlying_type=equity。只报告校验结果，绝对不要创建订单。",
  },
];

const intentRouteTests = [
  {
    id: "route_market_scout", symbol: "AAPL", label: "市场数据 → Market Scout → Decision Desk",
    prompt: "读取 AAPL 当前可用的股票报价，告诉我买价、卖价和数据时间；不要凭常识补数字。",
    expectedStages: ["intent_interpreter_completed", "handoff_to_market_scout", "tool_call_authorized", "tool_receipt_succeeded", "market_scout_completed", "decision_desk_completed"],
    expectedToolID: "kernel_equity_quotes",
  },
  {
    id: "route_fundamental_scout", symbol: "AAPL", label: "基本面 → Fundamental Scout → Decision Desk",
    prompt: "读取 AAPL 的基本面和估值字段，选出三个系统确实返回的指标并解释；缺失字段不要补。",
    expectedStages: ["intent_interpreter_completed", "handoff_to_fundamental_scout", "tool_call_authorized", "tool_receipt_succeeded", "fundamental_scout_completed", "decision_desk_completed"],
    expectedToolID: "kernel_equity_fundamentals",
  },
  {
    id: "route_options_scout", symbol: "SPX", label: "历史 GEX → Options Scout → Decision Desk",
    prompt: "系统最近存下来的 SPX Full gamma 状态是什么？告诉我实际采样时间、spot 和 zero gamma；不要冒充实时行情。",
    expectedStages: ["intent_interpreter_completed", "handoff_to_options_scout", "tool_call_authorized", "tool_receipt_succeeded", "options_scout_completed", "decision_desk_completed"],
    expectedToolID: "research_gexbot_as_of",
  },
  {
    id: "route_position_manager", symbol: "SPY", label: "账户组合 → Position Manager → Decision Desk",
    prompt: "读取我已绑定账户的投资组合摘要，只解释系统实际返回的权益、现金或回报字段，不要猜测账户号。",
    expectedStages: ["intent_interpreter_completed", "handoff_to_position_manager", "tool_call_authorized", "tool_receipt_succeeded", "position_manager_completed", "decision_desk_completed"],
    expectedToolID: "kernel_portfolio",
  },
  {
    id: "route_catalyst_scout", symbol: "TSLA", label: "财报事实 → Catalyst Scout → Decision Desk",
    prompt: "TSLA 最近一次公布的季度 EPS 到底是超预期还是低于预期？只使用系统已有的可信数据，不确定的指标不要补。",
    expectedStages: ["intent_interpreter_completed", "handoff_to_catalyst_scout", "tool_call_authorized", "tool_receipt_succeeded", "catalyst_scout_completed", "decision_desk_completed"],
    expectedToolID: "kernel_earnings_results",
  },
  {
    id: "route_discovery_scout", symbol: "SPY", label: "明确 URL → Discovery Scout → Decision Desk",
    prompt: "这个页面主要说了什么？只按页面内容概括一条事实，并给出来源：https://example.com",
    expectedStages: ["intent_interpreter_completed", "handoff_to_discovery_scout", "tool_call_authorized", "tool_receipt_succeeded", "discovery_scout_completed", "decision_desk_completed"],
    expectedToolID: "research_web_fetch",
  },
  {
    id: "route_scout_collaboration", symbol: "SOFI", label: "开放研究问题 → Scout → Decision Desk",
    prompt: "先让合适的研究角色梳理 SOFI 当前最值得进一步验证的三件事，再由 Decision Desk 汇总；没有实时证据的地方必须明确写出来。",
    expectedStages: ["intent_interpreter_completed", "handoff_to_scout", "scout_task_admitted", "scout_research_completed", "desk_continuation_ready", "decision_desk_completed"],
  },
];

let toolPrecisionTestRunning = false;
let intentRouteTestRunning = false;

function renderConversation() {
  const target = byId("conversation");
  const count = conversationEntries.length;
  byId("conversation-count").textContent = `${count} ${count === 1 ? "EXCHANGE" : "EXCHANGES"}`;
  if (!count) {
    target.replaceChildren(Object.assign(document.createElement("p"), {
      className: "field-note",
      textContent: "后续问题会沿用同一个 Cortex Conversation；历史来自已持久化的 UserRequest 和回答 Artifact。",
    }));
    return;
  }
  const nodes = [];
  for (const entry of conversationEntries) {
    for (const [role, text] of [["user", entry.user_text], ["assistant", entry.assistant_text]]) {
      const message = document.createElement("div");
      message.className = `conversation-message ${role}`;
      const label = document.createElement("strong");
      label.textContent = role === "user" ? "YOU" : "CORTEX";
      const body = document.createElement("div");
      body.textContent = text;
      message.append(label, body);
      nodes.push(message);
    }
  }
  target.replaceChildren(...nodes);
  target.scrollTop = target.scrollHeight;
}

function setConversation(conversation) {
  currentConversation = conversation;
  const status = byId("conversation-status");
  if (!conversation) {
    status.textContent = "新对话：第一条消息会创建一个永久 Cortex Conversation。";
    return;
  }
  status.textContent = `当前 Conversation：${conversation.id.slice(-12)} · 后续消息会带入最近已确认的上下文。`;
  const url = new URL(window.location.href);
  url.searchParams.set("conversation", conversation.id);
  url.searchParams.set("conversation_created_at", conversation.createdAt);
  history.replaceState(null, "", url);
}

async function restoreConversation() {
  const url = new URL(window.location.href);
  const id = url.searchParams.get("conversation");
  const createdAt = url.searchParams.get("conversation_created_at");
  if (!id || !createdAt) return;
  const data = await request(`/agent/cortex-conversations/${encodeURIComponent(id)}`);
  conversationEntries = Array.isArray(data?.entries) ? data.entries : [];
  setConversation({id, createdAt});
  renderConversation();
}

function renderTrace(job) {
  const trace = Array.isArray(job?.trace) ? job.trace : [];
  byId("trace-status").textContent = job?.id
    ? String(job.status || "unknown").toUpperCase()
    : "NO JOB";
  const summary = {
    job_id: job?.id,
    status: job?.status,
    workflow: job?.workflow,
    symbol: job?.symbol,
    attempt: job?.attempt,
    error_code: job?.error_code || undefined,
    trace: trace.map((event) => ({
      sequence: event.sequence,
      at: event.created_at,
      attempt: event.attempt,
      stage: event.stage,
      state: event.state,
      target_role: event.target_role,
      tool_call_id: event.tool_call_id,
      tool_id: event.tool_id,
      receipt_id: event.receipt_id,
      error_code: event.error_code || undefined,
    })),
  };
  byId("trace").textContent = JSON.stringify(summary, null, 2);
}

async function restoreSession() {
  try {
    await request("/agent/auth/session");
    await refreshCredentialStatus();
	  await refreshRobinhoodConnection();
    await restoreConversation();
  } catch (error) {
    byId("query-error").textContent = formatError(error);
  }
}

byId("new-conversation").addEventListener("click", () => {
  currentConversation = null;
  conversationEntries = [];
  const url = new URL(window.location.href);
  url.searchParams.delete("conversation");
  url.searchParams.delete("conversation_created_at");
  history.replaceState(null, "", url);
  setConversation(null);
  renderConversation();
  byId("question").focus();
});

function setRobinhoodStatus(message, connectLabel = "Connect Robinhood") {
  byId("robinhood-status").textContent = message;
  byId("connect-robinhood").textContent = connectLabel;
}

async function refreshRobinhoodConnection() {
  const picker = byId("robinhood-account-picker");
  picker.hidden = true;
  const connection = await request("/agent/robinhood/connection");
  if (!connection?.enabled) {
    setRobinhoodStatus("当前 Kernel 未启用 Robinhood。", "Robinhood unavailable");
    byId("connect-robinhood").disabled = true;
    return;
  }
  byId("connect-robinhood").disabled = false;
  if (connection.status === "disconnected") {
    setRobinhoodStatus("尚未连接。", "Connect Robinhood");
    return;
  }
  if (connection.status === "connected") {
    setRobinhoodStatus(`已连接并绑定 ${connection.account || "账户"}；只读数据已就绪。`, "Reconnect Robinhood");
    return;
  }
  setRobinhoodStatus("已授权；请明确选择一个活跃的 Agentic Trading 账户。", "Reconnect Robinhood");
  const accounts = await request("/agent/robinhood/accounts");
  const eligible = (accounts?.accounts || []).filter((account) =>
    account.agentic_allowed && account.state === "active" && !account.deactivated && !account.permanently_deactivated
  );
  if (!eligible.length) {
    byId("robinhood-status").textContent = "已授权，但未发现可用的活跃 Agentic Trading 账户。";
    return;
  }
  const select = byId("robinhood-account");
  select.replaceChildren(...eligible.map((account) => {
    const option = document.createElement("option");
    option.value = account.masked_account;
    option.textContent = `${account.masked_account}${account.nickname ? ` · ${account.nickname}` : ""} · ${account.brokerage_account_type}`;
    return option;
  }));
  picker.hidden = false;
}

async function refreshCredentialStatus() {
  const payload = await request("/agent/secrets");
  const configured = Boolean(payload?.configured?.openai);
  byId("openai-status").textContent = configured ? "已加密保存在数据库中。" : "尚未配置。";
  const braveConfigured = Boolean(payload?.configured?.brave);
  byId("brave-status").textContent = braveConfigured ? "已加密保存在数据库中。" : "尚未配置。";
  const gexbotConfigured = Boolean(payload?.configured?.gexbot);
  byId("gexbot-status").textContent = gexbotConfigured ? "已加密保存在数据库中。" : "尚未配置。";
  const robinhoodConfigured = Boolean(payload?.configured?.robinhood_research);
  byId("robinhood-research-status").textContent = robinhoodConfigured ? "已加密保存在数据库中。" : "尚未配置。";
  return configured;
}

byId("save-openai").addEventListener("click", async () => {
  const value = byId("openai-token").value.trim();
  byId("query-error").textContent = "";
  if (!value) {
    byId("query-error").textContent = "请输入 OpenAI API Token。";
    return;
  }
  byId("save-openai").disabled = true;
  try {
    await request("/agent/secrets/openai", {
      method:"PUT", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({value})
    });
    byId("openai-token").value = "";
    await refreshCredentialStatus();
  } catch (error) {
    byId("query-error").textContent = error.message;
  } finally {
    byId("save-openai").disabled = false;
  }
});

byId("save-brave").addEventListener("click", async () => {
  const value = byId("brave-token").value.trim();
  byId("query-error").textContent = "";
  if (!value) {
    byId("query-error").textContent = "请输入 Brave Search API Key。";
    return;
  }
  byId("save-brave").disabled = true;
  try {
    await request("/agent/secrets/brave", {
      method:"PUT", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({value})
    });
    byId("brave-token").value = "";
    await refreshCredentialStatus();
  } catch (error) {
    byId("query-error").textContent = error.message;
  } finally {
    byId("save-brave").disabled = false;
  }
});

byId("save-gexbot").addEventListener("click", async () => {
  const value = byId("gexbot-token").value.trim();
  byId("query-error").textContent = "";
  if (!value) {
    byId("query-error").textContent = "请输入 GEXBot API Key。";
    return;
  }
  byId("save-gexbot").disabled = true;
  try {
    await request("/agent/secrets/gexbot", {
      method:"PUT", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({value})
    });
    byId("gexbot-token").value = "";
    await refreshCredentialStatus();
  } catch (error) {
    byId("query-error").textContent = error.message;
  } finally {
    byId("save-gexbot").disabled = false;
  }
});

byId("save-robinhood-research").addEventListener("click", async () => {
  const file = byId("robinhood-research-token").files?.[0];
  byId("query-error").textContent = "";
  if (!file || file.size > 4000) {
    byId("query-error").textContent = "请选择有效且小于 4KB 的 credentials.json。";
    return;
  }
  byId("save-robinhood-research").disabled = true;
  try {
    const value = JSON.stringify(JSON.parse(await file.text()));
    await request("/agent/secrets/robinhood_research", {
      method:"PUT", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({value})
    });
    byId("robinhood-research-token").value = "";
    await refreshCredentialStatus();
  } catch (error) {
    byId("query-error").textContent = error.message;
  } finally {
    byId("save-robinhood-research").disabled = false;
  }
});

byId("connect-robinhood").addEventListener("click", async () => {
  byId("query-error").textContent = "";
  byId("connect-robinhood").disabled = true;
  try {
    const connection = await request("/agent/robinhood/connect", {method:"POST"});
    if (!connection?.authorization_url) throw new Error("Robinhood authorization URL unavailable");
    window.location.assign(connection.authorization_url);
  } catch (error) {
    byId("query-error").textContent = formatError(error);
    byId("connect-robinhood").disabled = false;
  }
});

byId("bind-robinhood").addEventListener("click", async () => {
  const maskedAccount = byId("robinhood-account").value;
  if (!maskedAccount) return;
  byId("query-error").textContent = "";
  byId("bind-robinhood").disabled = true;
  try {
    await request("/agent/robinhood/bind", {
      method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify({masked_account: maskedAccount})
    });
    await refreshRobinhoodConnection();
  } catch (error) {
    byId("query-error").textContent = formatError(error);
  } finally {
    byId("bind-robinhood").disabled = false;
  }
});

const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

function createToolTestText(tag, className, value) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = value;
  return node;
}

function createToolTestRow(test) {
  const row = document.createElement("article");
  row.className = `tool-test-row ${test.state === "enabled" ? "enabled" : "candidate"}`;
  row.dataset.toolTest = test.id;

  const head = document.createElement("div");
  head.className = "tool-test-row-head";
  const title = document.createElement("div");
  const toolID = createToolTestText("code", "tool-test-id", test.id);
  const description = createToolTestText("p", "field-note", test.description);
  title.append(toolID, description);
  const lifecycle = createToolTestText("span", `tool-test-badge ${test.state}`, test.state === "enabled" ? "CORTEX 已启用" : "CORTEX 未启用");
  head.append(title, lifecycle);

  const selector = test.selector || "LLM Intent";
  const metadata = createToolTestText("p", "tool-test-meta", `${test.source} · 选择方：${selector} · 计划角色：${test.roles}`);
  const promptLabel = createToolTestText("label", "tool-test-prompt-label", test.state === "enabled" ? "测试提示词（可编辑）" : "计划测试提示词");
  const prompt = document.createElement("textarea");
  prompt.className = "tool-test-prompt";
  prompt.rows = 3;
  prompt.value = test.prompt;
  prompt.setAttribute("aria-label", `${test.id} 测试提示词`);
  prompt.readOnly = test.state !== "enabled";

  const controls = document.createElement("div");
  controls.className = "tool-test-controls";
  const expected = createToolTestText("span", "tool-test-expected", `预期 Tool ID：${test.id}`);
  const state = createToolTestText("strong", "tool-test-state", test.state === "enabled" ? "尚未运行" : "尚未授予 Cortex" );
  state.dataset.toolTestState = "true";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "secondary tool-test-run";
  button.textContent = test.state === "enabled" ? "运行精准测试" : "未启用";
  button.disabled = test.state !== "enabled";
  if (test.state !== "enabled") button.title = "必须先完成 Cortex bridge、授权和收据链，才能运行。";
  if (test.state === "enabled") {
    button.addEventListener("click", () => runToolPrecisionTest(test, row, prompt, button));
  }
  controls.append(expected, state, button);

  const detail = document.createElement("pre");
  detail.className = "tool-test-result hidden";
  detail.dataset.toolTestResult = "true";
  row.append(head, metadata, promptLabel, prompt, controls, detail);
  return row;
}

function renderToolPrecisionTests() {
  const enabled = toolPrecisionTests.filter((test) => test.state === "enabled");
  const candidates = toolPrecisionTests.filter((test) => test.state !== "enabled");
  byId("tool-test-count").textContent = `${enabled.length} 已启用 / ${candidates.length} 待接入`;
  byId("tool-test-active").replaceChildren(...enabled.map(createToolTestRow));
  byId("tool-test-candidates").replaceChildren(...candidates.map(createToolTestRow));
}

function routeTestVerdict(job, test) {
  const trace = Array.isArray(job?.trace) ? job.trace : [];
  let cursor = -1;
  const matchedStages = [];
  for (const expected of test.expectedStages) {
    const index = trace.findIndex((event, candidate) => candidate > cursor && event.stage === expected);
    if (index < 0) break;
    cursor = index;
    matchedStages.push(expected);
  }
  const authorized = test.expectedToolID
    ? trace.find((event) => event.stage === "tool_call_authorized" && event.tool_id === test.expectedToolID)
    : null;
  const matchingReceipt = authorized && trace.find((event) =>
    event.stage === "tool_receipt_succeeded" && event.tool_call_id === authorized.tool_call_id
  );
  const routeComplete = matchedStages.length === test.expectedStages.length;
  const toolComplete = !test.expectedToolID || Boolean(authorized && matchingReceipt);
  const passed = job?.status === "succeeded" && routeComplete && toolComplete;
  return {
    state: passed ? "passed" : "failed",
    label: passed ? "通过：Cortex 自主选择了预期路线。" : "未通过：Run 没有完整走过预期路线。",
    matchedStages,
    routeComplete,
    toolComplete,
  };
}

function renderRouteTestRun(row, job, test, message) {
  const state = row.querySelector("[data-route-test-state]");
  const result = row.querySelector("[data-route-test-result]");
  const inFlight = job?.status === "queued" || job?.status === "running";
  const verdict = job && !inFlight ? routeTestVerdict(job, test) : null;
  state.className = `tool-test-state ${verdict?.state || "running"}`;
  state.textContent = message || verdict?.label || "运行中：等待 Cortex 路线 Trace…";
  if (!job || inFlight) return;
  result.classList.remove("hidden");
  result.textContent = JSON.stringify({
    run_id: job.id,
    cortex_state: job.status,
    expected_route: test.expectedStages,
    expected_tool_id: test.expectedToolID || null,
    matched_route: verdict.matchedStages,
    route_complete: verdict.routeComplete,
    tool_receipt_complete: verdict.toolComplete,
    test_verdict: verdict.state,
    trace: job.trace || [],
  }, null, 2);
}

async function waitForRouteTest(job, test, row) {
  const deadline = Date.now() + 540000;
  while (job.status === "queued" || job.status === "running") {
    if (Date.now() >= deadline) throw new Error("路线测试仍在运行，请稍后查看该 Run 的 Trace。");
    renderRouteTestRun(row, job, test);
    await wait(750);
    job = await request(`/agent/cortex-runs/${encodeURIComponent(job.id)}`);
  }
  renderRouteTestRun(row, job, test);
  return job;
}

async function runIntentRouteTest(test, row, prompt, button) {
  if (intentRouteTestRunning) return;
  const query = prompt.value.trim();
  if (!query) {
    renderRouteTestRun(row, null, test, "测试提示词不能为空。");
    return;
  }
  intentRouteTestRunning = true;
  button.disabled = true;
  row.querySelector("[data-route-test-result]").classList.add("hidden");
  try {
    renderRouteTestRun(row, {status:"queued", trace:[]}, test);
    let job = await request("/agent/cortex-requests", {
      method:"POST", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({workflow:"auto", symbol:test.symbol || "SPY", query})
    });
    job = await waitForRouteTest(job, test, row);
    if (job.status !== "succeeded") {
      renderRouteTestRun(row, job, test, `Cortex Run 未成功结束：${job.error_code || "unknown"}`);
    }
  } catch (error) {
    renderRouteTestRun(row, null, test, `测试提交或轮询失败：${formatError(error)}`);
  } finally {
    intentRouteTestRunning = false;
    button.disabled = false;
  }
}

function createRouteTestRow(test) {
  const row = document.createElement("article");
  row.className = "tool-test-row enabled";
  row.dataset.routeTest = test.id;
  const head = document.createElement("div");
  head.className = "tool-test-row-head";
  const title = document.createElement("div");
  title.append(
    createToolTestText("code", "tool-test-id", test.id),
    createToolTestText("p", "field-note", test.label),
  );
  head.append(title, createToolTestText("span", "tool-test-badge enabled", "可运行"));
  const metadata = createToolTestText(
    "p",
    "tool-test-meta",
    `预期路线：${test.expectedStages.join(" → ")}${test.expectedToolID ? ` · 预期 Tool：${test.expectedToolID}` : ""}`,
  );
  const promptLabel = createToolTestText("label", "tool-test-prompt-label", "自然语言意图（可编辑）");
  const prompt = document.createElement("textarea");
  prompt.className = "tool-test-prompt";
  prompt.rows = 3;
  prompt.value = test.prompt;
  prompt.setAttribute("aria-label", `${test.label} 测试提示词`);
  const controls = document.createElement("div");
  controls.className = "tool-test-controls";
  const expected = createToolTestText("span", "tool-test-expected", `股票：${test.symbol}`);
  const state = createToolTestText("strong", "tool-test-state", "尚未运行");
  state.dataset.routeTestState = "true";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "secondary tool-test-run";
  button.textContent = "运行路线测试";
  button.addEventListener("click", () => runIntentRouteTest(test, row, prompt, button));
  controls.append(expected, state, button);
  const detail = document.createElement("pre");
  detail.className = "tool-test-result hidden";
  detail.dataset.routeTestResult = "true";
  row.append(head, metadata, promptLabel, prompt, controls, detail);
  return row;
}

function renderIntentRouteTests() {
  byId("route-test-count").textContent = `6 个专业角色 + 1 条协作路线`;
  byId("route-test-list").replaceChildren(...intentRouteTests.map(createRouteTestRow));
}

function toolPrecisionVerdict(job, expectedToolID) {
  const trace = Array.isArray(job?.trace) ? job.trace : [];
  const authorized = trace.find((event) => event.stage === "tool_call_authorized" && event.tool_id === expectedToolID);
  const matchingReceipt = authorized && trace.find((event) =>
    event.stage === "tool_receipt_succeeded" && event.tool_call_id === authorized.tool_call_id
  );
  const authorizedToolIDs = [...new Set(trace
    .filter((event) => event.stage === "tool_call_authorized" && typeof event.tool_id === "string")
    .map((event) => event.tool_id))];
  if (job?.status === "succeeded" && authorized && matchingReceipt) {
    return {state:"passed", label:"通过：预期工具已获授权，且收到对应执行收据。", authorized, matchingReceipt, authorizedToolIDs};
  }
  if (authorized && matchingReceipt) {
    return {state:"failed", label:"工具已执行并收到收据，但 Cortex Run 未成功结束。", authorized, matchingReceipt, authorizedToolIDs};
  }
  if (authorized && !matchingReceipt) {
    return {state:"failed", label:"未通过：预期工具获授权，但未获得对应执行收据。", authorized, matchingReceipt, authorizedToolIDs};
  }
  return {state:"failed", label:"未通过：Trace 未显示预期 Tool ID 获得授权。", authorized, matchingReceipt, authorizedToolIDs};
}

function renderToolTestRun(row, job, expectedToolID, message) {
  const state = row.querySelector("[data-tool-test-state]");
  const result = row.querySelector("[data-tool-test-result]");
  const inFlight = job?.status === "queued" || job?.status === "running";
  const verdict = job && !inFlight ? toolPrecisionVerdict(job, expectedToolID) : null;
  state.className = `tool-test-state ${verdict?.state || "running"}`;
  state.textContent = message || verdict?.label || "运行中：等待 Cortex Trace…";
  if (!job || inFlight) return;
  const trace = Array.isArray(job.trace) ? job.trace : [];
  result.classList.remove("hidden");
  result.textContent = JSON.stringify({
    run_id: job.id,
    cortex_state: job.status,
    expected_tool_id: expectedToolID,
    authorized_tool_ids: verdict.authorizedToolIDs,
    expected_tool_authorized: Boolean(verdict.authorized),
    receipt_for_expected_call: Boolean(verdict.matchingReceipt),
    test_verdict: verdict.state,
    relevant_trace: trace.filter((event) => event.stage === "tool_call_authorized" || event.stage === "tool_receipt_succeeded"),
  }, null, 2);
}

async function waitForToolPrecisionTest(job, test, row) {
  const deadline = Date.now() + 540000;
  while (job.status === "queued" || job.status === "running") {
    if (Date.now() >= deadline) throw new Error("工具测试仍在运行，请稍后查看该 Run 的 Trace。");
    renderToolTestRun(row, job, test.id);
    await wait(750);
    job = await request(`/agent/cortex-runs/${encodeURIComponent(job.id)}`);
  }
  renderToolTestRun(row, job, test.id);
  return job;
}

async function runToolPrecisionTest(test, row, prompt, button) {
  if (toolPrecisionTestRunning) return;
  const query = prompt.value.trim();
  if (!query) {
    renderToolTestRun(row, null, test.id, "测试提示词不能为空。");
    return;
  }
  if (query.includes("{{")) {
    renderToolTestRun(row, null, test.id, "请先用说明中的前置只读工具取得真实 UUID，并替换提示词占位符。");
    return;
  }
  toolPrecisionTestRunning = true;
  button.disabled = true;
  const result = row.querySelector("[data-tool-test-result]");
  result.classList.add("hidden");
  try {
    renderToolTestRun(row, {status:"queued", trace:[]}, test.id);
    let job = await request("/agent/cortex-requests", {
      method:"POST", headers:{"Content-Type":"application/json"},
      // Deliberately omit conversation fields: a precision test is isolated
      // from the user's normal Agent Lab conversation and its context.
      body:JSON.stringify({workflow:"auto", symbol:test.symbol, query})
    });
    job = await waitForToolPrecisionTest(job, test, row);
    if (job.status !== "succeeded") {
      renderToolTestRun(row, job, test.id, `Cortex Run 未成功结束：${job.error_code || "unknown"}`);
    }
  } catch (error) {
    renderToolTestRun(row, null, test.id, `测试提交或轮询失败：${formatError(error)}`);
  } finally {
    toolPrecisionTestRunning = false;
    button.disabled = false;
  }
}

async function waitForAgentQuery(job) {
  const deadline = Date.now() + 540000;
  while (job.status === "queued" || job.status === "running") {
    if (Date.now() >= deadline) throw new Error("Agent Team 仍在运行，请稍后重试。");
    byId("status").textContent = job.status === "queued" ? "QUERY QUEUED" : "AGENTS WORKING";
    renderTrace(job);
    await wait(750);
    job = await request(`/agent/cortex-runs/${encodeURIComponent(job.id)}`);
  }
  renderTrace(job);
  return job;
}

byId("query-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const symbol = byId("symbol").value.trim().toUpperCase();
  const query = byId("question").value.trim();
  if (!/^[A-Z0-9.-]{1,16}$/.test(symbol) || !query) {
    byId("query-error").textContent = "请输入有效股票代码和问题。";
    return;
  }
  byId("run").disabled = true;
  byId("status").textContent = "CORTEX WORKING";
  byId("query-error").textContent = "";
  byId("result").textContent = "Awaiting dispatcher…";
  renderTrace(null);
  try {
    let job = await request("/agent/cortex-requests", {
      method:"POST", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({workflow:"auto", symbol, query,
        conversation_id: currentConversation?.id,
        conversation_created_at: currentConversation?.createdAt})
    });
    if (!job.conversation_id || !job.conversation_created_at) throw new Error("Cortex conversation was not accepted");
    setConversation({id: job.conversation_id, createdAt: job.conversation_created_at});
    job = await waitForAgentQuery(job);
    if (job.status !== "succeeded") throw new Error(job.error_code || "agent_query_failed");
    const result = job.result;
    byId("result").textContent = JSON.stringify(result, null, 2);
    if (typeof result?.answer === "string" && result.answer.trim()) {
      conversationEntries.push({user_text: `Symbol: ${symbol}\n\n${query}`, assistant_text: result.answer});
      conversationEntries = conversationEntries.slice(-6);
      renderConversation();
    }
    if (result.workflow === "ask_user") {
      byId("status").textContent = "NEEDS YOUR INPUT · NO OPERATION";
      byId("question").value = `${query}\n\n补充回答：`;
      byId("question").focus();
    } else {
      byId("status").textContent = result.cognition === "stub"
        ? "STUB PASS · MODEL NOT CONNECTED"
        : `COMPLETE · ${String(result.model || "OPENAI").toUpperCase()} · NO OPERATION`;
    }
  } catch (error) {
    byId("result").textContent = "No result returned.";
    byId("status").textContent = "FAILED CLOSED";
    byId("query-error").textContent = formatError(error);
  } finally {
    byId("run").disabled = false;
  }
});

renderToolPrecisionTests();
renderIntentRouteTests();
restoreSession();
