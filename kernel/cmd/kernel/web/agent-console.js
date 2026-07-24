const byId = (id) => document.getElementById(id);

const state = {
  snapshot:null,
  bars:[],
  symbol:"SPY",
  days:5,
  rooms:[],
  room:null,
  messages:[],
  runID:null,
  pollToken:0,
  sending:false,
  environment:"",
  autonomy:null,
  replay:null,
};

async function request(path,options = {}) {
  const response = await fetch(path,{...options,cache:"no-store"});
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(payload?.error || `HTTP ${response.status}`);
    error.code = payload?.error_code || payload?.reason_code ||
      `http_${response.status}`;
    error.status = response.status;
    throw error;
  }
  return payload;
}

function text(id,value) {
  byId(id).textContent = value;
}

function money(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return "—";
  return new Intl.NumberFormat("en-US",{
    style:"currency",currency:"USD",minimumFractionDigits:2,maximumFractionDigits:2,
  }).format(number);
}

function compact(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return "—";
  return new Intl.NumberFormat("en-US",{notation:"compact",maximumFractionDigits:1}).format(number);
}

function when(value,withDate = false) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString("zh-CN",withDate ? {
    month:"numeric",day:"numeric",hour:"2-digit",minute:"2-digit",
  } : {hour:"2-digit",minute:"2-digit"});
}

function replayNumber(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return "—";
  return new Intl.NumberFormat("en-US",{
    maximumFractionDigits:2,
  }).format(number);
}

function localDateTimeValue(date) {
  const shifted = new Date(date.getTime()-date.getTimezoneOffset()*60000);
  return shifted.toISOString().slice(0,19);
}

function initializeReplayRange() {
  const end = new Date();
  const start = new Date(end.getTime()-24*60*60*1000);
  byId("replay-start").value = localDateTimeValue(start);
  byId("replay-end").value = localDateTimeValue(end);
}

function renderReplay(payload) {
  state.replay = {
    replay_id:payload.replay_id,
    generation:payload.generation,
    state:payload.state,
  };
  byId("replay-next").disabled = payload.state !== "active";
  text("replay-state",
    `${String(payload.state || "unknown").toUpperCase()} · GEN ${payload.generation || "—"}`);
  const observation = payload.observation;
  if (!observation) {
    text("replay-clock",payload.state === "complete" ? "回放完成" : "等待第一帧");
    return;
  }
  const metrics = observation.metrics || {};
  text("replay-clock",when(observation.source_timestamp || observation.available_at,true));
  text("replay-state",
    `${String(payload.state || "active").toUpperCase()} · AVAILABLE ${when(observation.available_at,true)} · GEN ${payload.generation}`);
  text("replay-spot",replayNumber(metrics.spot));
  text("replay-zero",replayNumber(metrics.zero_gamma));
  text("replay-call",replayNumber(metrics.major_pos_oi));
  text("replay-put",replayNumber(metrics.major_neg_oi));
}

async function createReplay() {
  clearError();
  const start = new Date(byId("replay-start").value);
  const end = new Date(byId("replay-end").value);
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) ||
      end < start || end > new Date()) {
    showError(new Error("请选择一个已经发生的有效回放时间段。"));
    return;
  }
  const button = byId("replay-create");
  button.disabled = true;
  try {
    const requestID = globalThis.crypto?.randomUUID ?
      `console-replay-${crypto.randomUUID()}` :
      `console-replay-${Date.now()}`;
    const payload = await request(
      "/agent/console/data-streams/gexbot/replays",
      {
        method:"POST",
        headers:{"Content-Type":"application/json"},
        body:JSON.stringify({
          request_id:requestID,
          symbol:"SPX",
          category:byId("replay-category").value,
          start_available_at:start.toISOString(),
          end_available_at:end.toISOString(),
          as_of:new Date().toISOString(),
        }),
      },
    );
    renderReplay(payload);
  } catch (error) {
    showError(error);
  } finally {
    button.disabled = false;
  }
}

async function advanceReplay() {
  if (!state.replay?.replay_id || state.replay.state !== "active") return;
  clearError();
  const button = byId("replay-next");
  button.disabled = true;
  try {
    const payload = await request(
      `/agent/console/data-streams/gexbot/replays/${encodeURIComponent(state.replay.replay_id)}/next`,
      {
        method:"POST",
        headers:{"Content-Type":"application/json"},
        body:JSON.stringify({generation:state.replay.generation}),
      },
    );
    renderReplay(payload);
  } catch (error) {
    showError(error);
  } finally {
    button.disabled = state.replay?.state !== "active";
  }
}

function showError(error) {
  const known = {
    portfolio_unavailable:"账户数据暂时不可用。",
    cortex_unavailable:"Cortex 暂时无法连接。",
    cortex_paper_candidates_unavailable:"Cortex 候选交易暂时不可用。",
    paper_candidate_review_requires_copilot:"请先切换到 Copilot，再批准或拒绝候选交易。",
    candidate_review_conflict:"候选交易已被处理，列表已刷新。",
    candidate_source_not_committed:"来源决策尚未成功提交，不能批准。",
    agent_room_paused:"这个对话已暂停，请在完整对话界面恢复。",
    autonomy_generation_conflict:"自治模式刚被其他操作修改，已重新读取最新状态。",
    live_autonomy_locked:"Live 环境目前仍强制 Observe。",
    moody_blues_replay_unavailable:"Moody Blues 历史回放暂时不可用。",
    moody_blues_generation_conflict:"回放游标已前进，请重新创建或恢复该回放。",
    moody_blues_cursor_invalid:"回放游标无效。",
  };
  text("console-error",known[error?.code] || error?.message || "请求失败");
}

function clearError() {
  text("console-error","");
}

function renderEnvironment(environment,autonomy) {
  state.environment = environment.selected;
  state.autonomy = autonomy;
  for (const button of byId("environment-switch").querySelectorAll("button")) {
    const value = button.dataset.environment;
    button.classList.toggle("active",value === environment.selected);
    button.disabled = value === "paper" ? !environment.paper_available : !environment.live_available;
    button.title = button.disabled ? `${value.toUpperCase()} 环境尚未接入这套 Console` : "";
  }
  for (const button of byId("autonomy-switch").querySelectorAll("button")) {
    const value = button.dataset.autonomy;
    button.classList.toggle("active",value === autonomy.selected);
    button.disabled = !autonomy.available.includes(value);
    button.title = button.disabled ? "该自治级别尚未通过安全边界验收" : "";
  }
  const execution = byId("execution-state");
  execution.className = `execution-state ${environment.execution_enabled ? "enabled" : "locked"}`;
  execution.querySelector("strong").textContent = environment.execution_enabled ? "ENABLED" : "LOCKED";
  text("kernel-mode",environment.kernel_mode.replaceAll("_"," ").toUpperCase());
  text("data-scope",`${environment.data_scope.toUpperCase()} DATA · ${environment.execution_enabled ? "execution enabled" : "execution locked"}`);
  text("composer-state",`${autonomy.selected.toUpperCase()} · ${environment.execution_enabled ? "受控执行" : "只读"}`);
}

function renderPortfolio(portfolio) {
  if (!portfolio.available) {
    text("portfolio-status","UNAVAILABLE");
    text("account-source",portfolio.error_code || "账户不可用");
    byId("positions-body").innerHTML = '<tr><td colspan="5" class="table-empty">没有可验证的账户数据。</td></tr>';
    return;
  }
  const account = portfolio.account || {};
  const positions = Array.isArray(portfolio.positions) ? portfolio.positions : [];
  const orders = Array.isArray(portfolio.open_orders) ? portfolio.open_orders : [];
  text("account-equity",account.equity_known ? money(account.equity) : "UNKNOWN");
  text("buying-power",money(account.buying_power));
  text("account-cash",account.cash_known ? money(account.cash) : "UNKNOWN");
  text("account-source",`${account.account_type || "account"} · ${portfolio.source || account.source || "provider"}`);
  text("account-as-of",when(portfolio.as_of,true));
  text("position-count",String(positions.length));
  text("order-count",`${orders.length} open orders`);
  text("portfolio-status",`${portfolio.source || account.source || "PROVIDER"} · ${when(portfolio.as_of)}`.toUpperCase());

  const body = byId("positions-body");
  body.replaceChildren();
  if (!positions.length) {
    const row = document.createElement("tr");
    row.innerHTML = '<td colspan="5" class="table-empty">当前账户没有持仓。</td>';
    body.append(row);
  } else {
    for (const position of positions) {
      const row = document.createElement("tr");
      const values = [
        position.symbol || "—",
        position.kind || "—",
        String(position.qty ?? "—"),
        position.avg_price_known ? money(position.avg_price) : "UNKNOWN",
        position.source || "—",
      ];
      for (const value of values) {
        const cell = document.createElement("td");
        cell.textContent = value;
        row.append(cell);
      }
      body.append(row);
    }
  }
  renderWatchlist(positions);
}

function operationSummary(operation) {
  const payload = operation.payload && typeof operation.payload === "object" ? operation.payload : {};
  const action = (payload.action || "decision").toUpperCase();
  const symbol = payload.symbol || payload.underlying || "PORTFOLIO";
  const qty = payload.qty ? ` × ${payload.qty}` : "";
  return `${action} ${symbol}${qty}`;
}

function renderActivity(activity) {
  const operations = activity.available && Array.isArray(activity.operations) ? activity.operations : [];
  const paperOrders = activity.available && Array.isArray(activity.paper_orders) ? activity.paper_orders : [];
  text("activity-count",`${operations.length + paperOrders.length} EVENTS`);
  const list = byId("operation-list");
  list.replaceChildren();
  if (!operations.length && !paperOrders.length) {
    const empty = document.createElement("div");
    empty.className = "table-empty";
    empty.textContent = activity.available
      ? "还没有 Agent 买卖或 Kernel 决策记录。"
      : "Kernel 操作记录暂时不可用。";
    list.append(empty);
    return;
  }
  for (const order of paperOrders.slice(0,8)) {
    const item = document.createElement("div");
    item.className = `operation-item ${order.state || ""}`;
    item.append(document.createElement("i"));
    const copy = document.createElement("div");
    copy.className = "operation-copy";
    const title = document.createElement("strong");
    title.textContent = `${String(order.side || "trade").toUpperCase()} ${order.symbol || "—"} × ${order.qty ?? "—"} @ ${money(order.fill_price)}`;
    const meta = document.createElement("span");
    const actor = order.actor_kind === "agent" ? "Cortex Agent" :
      order.actor_kind === "trigger" ? "Trigger Wake" : "User";
    meta.textContent = `${actor} · ${when(order.filled_at,true)} · PAPER · ${order.quote_source || "quote"}`;
    copy.append(title,meta);
    const status = document.createElement("span");
    status.className = "operation-state";
    status.textContent = (order.state || "UNKNOWN").replaceAll("_"," ").toUpperCase();
    item.append(copy,status);
    list.append(item);
  }
  for (const operation of operations.slice(0,8)) {
    const item = document.createElement("div");
    item.className = `operation-item ${operation.status || ""}`;
    item.append(document.createElement("i"));
    const copy = document.createElement("div");
    copy.className = "operation-copy";
    const title = document.createElement("strong");
    title.textContent = operationSummary(operation);
    const meta = document.createElement("span");
    meta.textContent = `${operation.proposer || "Agent"} · ${when(operation.ts,true)} · Class ${operation.class || "—"}`;
    copy.append(title,meta);
    const status = document.createElement("span");
    status.className = "operation-state";
    status.textContent = (operation.status || "UNKNOWN").replaceAll("_"," ").toUpperCase();
    item.append(copy,status);
    list.append(item);
  }
}

function renderCandidates(payload) {
  const items = payload?.available && Array.isArray(payload.items) ?
    payload.items : [];
  const proposed = items.filter((item) =>
    item.status === "proposed" && item.eligible);
  text("candidate-count",`${proposed.length} PROPOSED`);
  const list = byId("candidate-list");
  list.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "candidate-empty";
    empty.textContent = state.environment === "live"
      ? "Live 环境不接收 Paper Candidate。"
      : payload?.available
        ? "目前没有 Cortex 候选交易。"
        : "Cortex 候选交易暂时不可用。";
    list.append(empty);
    return;
  }
  for (const candidate of items.slice(0,5)) {
    const proposal = candidate.proposal || {};
    const item = document.createElement("div");
    const execution = candidate.execution || null;
    item.className = `candidate-item ${candidate.status || ""} ${execution?.outcome || ""}`;
    item.append(document.createElement("i"));
    const copy = document.createElement("div");
    copy.className = "candidate-copy";
    const title = document.createElement("strong");
    title.textContent = `${String(proposal.side || "review").toUpperCase()} ${proposal.symbol || "—"} × ${proposal.qty ?? "—"}`;
    const meta = document.createElement("span");
    const confidence = Number(proposal.confidence_bps);
    const confidenceText = Number.isFinite(confidence)
      ? `${(confidence / 100).toFixed(2)}% confidence`
      : "confidence unknown";
    meta.textContent = `${proposal.strategy_id || "strategy"} · ${confidenceText} · ${when(candidate.proposed_at,true)} · RUN ${String(candidate.run_id || "—").slice(0,8)}`;
    const thesis = document.createElement("span");
    thesis.className = "candidate-thesis";
    thesis.textContent = `THESIS ${proposal.thesis || "—"}`;
    const invalidation = document.createElement("span");
    invalidation.className = "candidate-thesis";
    invalidation.textContent = `INVALIDATION ${proposal.invalidation || "—"}`;
    copy.append(title,meta,thesis,invalidation);
    if (execution) {
      const executionMeta = document.createElement("span");
      executionMeta.className = "candidate-execution";
      if (execution.outcome === "succeeded") {
        executionMeta.textContent = `FILLED ${String(execution.authorization_kind || "").toUpperCase()} · ${money(execution.order?.fill_price)} · ${when(execution.recorded_at,true)}`;
      } else if (execution.outcome === "failed") {
        executionMeta.textContent = `FAILED ${execution.failure_code || "unknown"} · HTTP ${execution.http_status || "—"} · ${when(execution.recorded_at,true)}`;
      } else {
        executionMeta.textContent = `AUTHORIZED ${String(execution.authorization_kind || "").toUpperCase()} · waiting for receipt`;
      }
      copy.append(executionMeta);
    }
    if (candidate.status === "proposed" && candidate.eligible &&
        !execution &&
        state.environment === "paper" &&
        state.autonomy?.selected === "copilot") {
      const actions = document.createElement("div");
      actions.className = "candidate-actions";
      for (const [decision,label] of [
        ["approve","批准"],["reject","拒绝"],
      ]) {
        const button = document.createElement("button");
        button.type = "button";
        button.dataset.decision = decision;
        button.textContent = label;
        button.addEventListener("click",() =>
          reviewCandidate(candidate,decision,button));
        actions.append(button);
      }
      item.append(copy,actions);
    } else {
      const status = document.createElement("span");
      status.className = "candidate-state";
      status.textContent = execution?.outcome === "succeeded" ? "FILLED" :
        execution?.outcome === "failed" ? "FAILED" :
        execution ? "AUTHORIZED" : ({
          proposed:"REVIEW",
          approved:"APPROVED",
          rejected:"REJECTED",
          source_not_committed:"INVALID",
        }[candidate.status] || "UNKNOWN");
      item.append(copy,status);
    }
    list.append(item);
  }
}

async function reviewCandidate(candidate,decision,button) {
  if (!candidate?.candidate_id || !candidate?.generation ||
      state.environment !== "paper" ||
      state.autonomy?.selected !== "copilot") return;
  clearError();
  const actions = button.closest(".candidate-actions");
  for (const item of actions?.querySelectorAll("button") || []) {
    item.disabled = true;
  }
  try {
    await request(
      `/agent/console/candidates/${encodeURIComponent(candidate.candidate_id)}/review`,
      {
        method:"POST",
        headers:{"Content-Type":"application/json"},
        body:JSON.stringify({
          environment:"paper",
          expected_generation:candidate.generation,
          decision,
        }),
      },
    );
    await loadCandidates();
  } catch (error) {
    showError(error);
    await loadCandidates().catch(() => {});
  }
}

function renderTriggers(triggers) {
  const items = Array.isArray(triggers?.items) ? triggers.items : [];
  text("trigger-count",`${items.filter((item) => item.enabled).length} ACTIVE`);
  const list = byId("trigger-list");
  if (!items.length) {
    list.innerHTML = `
      <div class="empty-module">
        <span class="empty-orbit"><i></i></span>
        <strong>${triggers?.available ? "目前没有激活条件" : "Trigger Registry 尚未接入"}</strong>
        <p>${triggers?.available ? "数学条件达到阈值后，Agent 会在这里被唤醒。" : "这里不会伪造触发点；注册器上线后显示真实条件、阈值与触发记录。"}</p>
      </div>`;
    return;
  }
  list.replaceChildren();
  for (const trigger of items) {
    const row = document.createElement("div");
    row.className = "trigger-item";
    row.innerHTML = "<i></i>";
    const copy = document.createElement("div");
    copy.className = "trigger-copy";
    const title = document.createElement("strong");
    title.textContent = trigger.title || trigger.strategy_id || "Trigger";
    const detail = document.createElement("span");
    const comparator = {
      gte:"≥",lte:"≤",crosses_above:"↗ crosses",crosses_below:"↘ crosses",
    }[trigger.comparator] || trigger.comparator || "";
    const metric = (trigger.metric || "metric").replaceAll("_"," ");
    detail.textContent = trigger.condition || trigger.description ||
      `${trigger.symbol || ""} · ${metric} ${comparator} ${trigger.threshold ?? "—"} · ${trigger.cooldown_seconds || 0}s cooldown`;
    copy.append(title,detail);
    if (trigger.last_value != null && trigger.last_observed_at) {
      const reason = {
        threshold_not_met:"WAITING",
        threshold_met:"THRESHOLD MET",
        crossed:"CROSSED",
        no_prior_sample:"BASELINE",
        cooldown_suppressed:"COOLDOWN",
      }[trigger.last_reason_code] || String(trigger.last_reason_code || "").toUpperCase();
      const sample = document.createElement("span");
      sample.className = `trigger-sample ${trigger.last_reason_code || ""}`;
      sample.textContent = `CURRENT ${Number(trigger.last_value).toLocaleString("en-US",{maximumFractionDigits:4})} · ${when(trigger.last_observed_at)} · ${reason}`;
      copy.append(sample);
    }
    const stateLabel = document.createElement("span");
    stateLabel.className = "trigger-state";
    stateLabel.textContent = (trigger.state || "ARMED").toUpperCase();
    row.append(copy,stateLabel);
    list.append(row);
  }
}

function renderWatchlist(positions = []) {
  const symbols = [...new Set([state.symbol,...positions.map((item) => item.symbol).filter(Boolean),"SPY"])];
  const list = byId("watchlist");
  list.replaceChildren();
  for (const symbol of symbols.slice(0,10)) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = symbol;
    button.classList.toggle("active",symbol === state.symbol);
    button.addEventListener("click",() => selectSymbol(symbol));
    list.append(button);
  }
}

function drawChart() {
  const canvas = byId("price-chart");
  const bars = state.bars;
  if (!bars.length) return;
  const rect = canvas.getBoundingClientRect();
  const scale = Math.max(window.devicePixelRatio || 1,1);
  canvas.width = Math.round(rect.width * scale);
  canvas.height = Math.round(rect.height * scale);
  const ctx = canvas.getContext("2d");
  ctx.scale(scale,scale);
  const width = rect.width;
  const height = rect.height;
  const padding = {top:18,right:48,bottom:24,left:10};
  const plotWidth = width-padding.left-padding.right;
  const plotHeight = height-padding.top-padding.bottom;
  const highs = bars.map((bar) => Number(bar.high));
  const lows = bars.map((bar) => Number(bar.low));
  let high = Math.max(...highs);
  let low = Math.min(...lows);
  if (!Number.isFinite(high) || !Number.isFinite(low)) return;
  if (high === low) { high += 1; low -= 1; }
  const margin = (high-low)*.08;
  high += margin;
  low -= margin;
  const y = (price) => padding.top+(high-price)/(high-low)*plotHeight;
  const x = (index) => padding.left+(bars.length === 1 ? plotWidth/2 : index/(bars.length-1)*plotWidth);

  ctx.clearRect(0,0,width,height);
  ctx.lineWidth = 1;
  ctx.font = "8px Inter, sans-serif";
  ctx.textAlign = "right";
  ctx.textBaseline = "middle";
  for (let index = 0; index < 5; index += 1) {
    const value = high-(high-low)*(index/4);
    const py = y(value);
    ctx.strokeStyle = "#1d251f";
    ctx.beginPath();
    ctx.moveTo(padding.left,py);
    ctx.lineTo(width-padding.right+4,py);
    ctx.stroke();
    ctx.fillStyle = "#505b53";
    ctx.fillText(value.toFixed(2),width-4,py);
  }

  const gradient = ctx.createLinearGradient(0,padding.top,0,height-padding.bottom);
  gradient.addColorStop(0,"rgba(145,221,176,.24)");
  gradient.addColorStop(1,"rgba(145,221,176,0)");
  ctx.beginPath();
  bars.forEach((bar,index) => {
    const px = x(index);
    const py = y(Number(bar.close));
    if (index === 0) ctx.moveTo(px,py);
    else ctx.lineTo(px,py);
  });
  ctx.lineTo(x(bars.length-1),height-padding.bottom);
  ctx.lineTo(x(0),height-padding.bottom);
  ctx.closePath();
  ctx.fillStyle = gradient;
  ctx.fill();

  ctx.beginPath();
  bars.forEach((bar,index) => {
    const px = x(index);
    const py = y(Number(bar.close));
    if (index === 0) ctx.moveTo(px,py);
    else ctx.lineTo(px,py);
  });
  ctx.strokeStyle = "#91ddb0";
  ctx.lineWidth = 1.5;
  ctx.stroke();

  const last = bars[bars.length-1];
  const lastY = y(Number(last.close));
  ctx.setLineDash([3,3]);
  ctx.strokeStyle = "rgba(188,245,207,.4)";
  ctx.beginPath();
  ctx.moveTo(padding.left,lastY);
  ctx.lineTo(width-padding.right,lastY);
  ctx.stroke();
  ctx.setLineDash([]);
  ctx.fillStyle = "#bcf5cf";
  ctx.beginPath();
  ctx.arc(x(bars.length-1),lastY,3,0,Math.PI*2);
  ctx.fill();
}

async function loadMarket() {
  const empty = byId("chart-empty");
  empty.hidden = false;
  empty.querySelector("strong").textContent = "正在读取真实行情";
  try {
    const [barPayload,quote] = await Promise.all([
      request(`/agent/console/market/bars/${encodeURIComponent(state.symbol)}?days=${state.days}`),
      request(`/agent/console/market/quote/${encodeURIComponent(state.symbol)}`),
    ]);
    state.bars = Array.isArray(barPayload.items) ? barPayload.items : [];
    const highs = state.bars.map((bar) => Number(bar.high)).filter(Number.isFinite);
    const lows = state.bars.map((bar) => Number(bar.low)).filter(Number.isFinite);
    const volumes = state.bars.map((bar) => Number(bar.volume)).filter(Number.isFinite);
    text("chart-high",highs.length ? money(Math.max(...highs)) : "—");
    text("chart-low",lows.length ? money(Math.min(...lows)) : "—");
    text("chart-volume",volumes.length ? compact(volumes.reduce((sum,value) => sum+value,0)) : "—");
    text("chart-source",state.bars.at(-1)?.source || quote.source || "—");
    const mid = (Number(quote.bid)+Number(quote.ask))/2;
    text("quote-price",money(mid));
    text("quote-spread",`${money(quote.bid)} / ${money(quote.ask)} · ${when(quote.as_of)}`);
    if (!state.bars.length) throw new Error("没有返回可绘制的历史行情");
    empty.hidden = true;
    drawChart();
  } catch (error) {
    state.bars = [];
    text("quote-price","—");
    text("quote-spread","行情不可用");
    empty.hidden = false;
    empty.querySelector("strong").textContent = error.message || "行情暂时不可用";
  }
}

async function selectSymbol(symbol) {
  state.symbol = String(symbol || "SPY").toUpperCase();
  byId("symbol-input").value = state.symbol;
  byId("chat-symbol").value = state.symbol;
  renderWatchlist(state.snapshot?.portfolio?.positions || []);
  await loadMarket();
}

async function loadSnapshot() {
  clearError();
  const suffix = state.environment ?
    `?environment=${encodeURIComponent(state.environment)}` : "";
  const snapshot = await request(`/agent/console/snapshot${suffix}`);
  state.snapshot = snapshot;
  renderEnvironment(snapshot.environment,snapshot.autonomy);
  renderPortfolio(snapshot.portfolio);
  renderActivity(snapshot.activity);
}

async function loadTriggers() {
  try {
    const triggers = await request("/agent/console/triggers");
    renderTriggers(triggers);
  } catch (error) {
    renderTriggers({
      available:false,
      items:[],
      reason:"cortex_trigger_registry_unavailable",
    });
    throw error;
  }
}

async function loadCandidates() {
  try {
    const environment = state.environment || "paper";
    const candidates = await request(
      `/agent/console/candidates?environment=${encodeURIComponent(environment)}`,
    );
    renderCandidates(candidates);
  } catch (error) {
    renderCandidates({available:false,items:[]});
    throw error;
  }
}

function roomRunning(room) {
  return ["queued","running","waiting","canceling"].includes(room?.last_run_state);
}

function renderRoomPicker() {
  const select = byId("room-select");
  select.replaceChildren();
  const fresh = document.createElement("option");
  fresh.value = "";
  fresh.textContent = "新建对话";
  select.append(fresh);
  for (const room of state.rooms) {
    const option = document.createElement("option");
    option.value = room.conversation_id;
    option.textContent = room.title;
    option.selected = state.room?.conversation_id === room.conversation_id;
    select.append(option);
  }
}

function messageNode(role,content) {
  const node = document.createElement("article");
  node.className = `dock-message ${role}`;
  const label = document.createElement("span");
  label.textContent = role === "user" ? "YOU" : "CORTEX";
  const copy = document.createElement("p");
  copy.textContent = content || "没有返回文本。";
  node.append(label,copy);
  return node;
}

function renderMessages(pending = "") {
  const nodes = [];
  for (const entry of state.messages.slice(-12)) {
    if (entry.user_text) nodes.push(messageNode("user",entry.user_text));
    if (entry.assistant_text) nodes.push(messageNode("assistant",entry.assistant_text));
  }
  if (pending) nodes.push(messageNode("user",pending));
  if (!nodes.length) {
    const empty = document.createElement("div");
    empty.className = "dock-empty";
    empty.innerHTML = "<span>A</span><strong>对话在这里，决策在左边。</strong><p>你可以随时询问、纠正或加入交易流程。Agent 的回答与历史仍会永久保存。</p>";
    nodes.push(empty);
  }
  byId("message-list").replaceChildren(...nodes);
  requestAnimationFrame(() => {
    const list = byId("message-list");
    list.scrollTop = list.scrollHeight;
  });
  const busy = state.sending || Boolean(state.runID) || state.room?.state === "paused";
  byId("message-input").disabled = busy;
  byId("send-message").disabled = busy;
}

async function loadRooms() {
  const payload = await request("/agent/rooms");
  state.rooms = payload.rooms || [];
  renderRoomPicker();
}

async function selectRoom(id) {
  state.pollToken += 1;
  state.runID = null;
  if (!id) {
    state.room = null;
    state.messages = [];
    renderRoomPicker();
    renderMessages();
    return;
  }
  const payload = await request(`/agent/rooms/${encodeURIComponent(id)}`);
  state.room = payload.room;
  state.messages = payload.messages || [];
  renderRoomPicker();
  renderMessages();
  if (state.room.last_run_id) {
    const run = await request(`/agent/cortex-runs/${encodeURIComponent(state.room.last_run_id)}`);
    if (roomRunning(state.room)) {
      state.runID = state.room.last_run_id;
      renderRunState(run);
      pollRun(state.runID);
    }
  }
}

const stageLabels = {
  user_request_admitted:"请求进入 Cortex",
  intent_interpreter_completed:"意图解析完成",
  task_graph_round_started:"多 Agent 分支启动",
  task_graph_join_completed:"多 Agent 结果汇合",
  tool_call_authorized:"工具调用已授权",
  tool_receipt_succeeded:"工具结果已验证",
  decision_desk_completed:"Decision Desk 已完成",
};

function renderRunState(run) {
  const active = !["succeeded","failed","canceled"].includes(run?.status);
  byId("run-state").hidden = !active;
  if (active) {
    const event = (run.trace || []).at(-1);
    text("run-stage",stageLabels[event?.stage] || event?.stage?.replaceAll("_"," ") || "等待执行记录");
  }
}

async function pollRun(runID) {
  const token = ++state.pollToken;
  for (let attempt = 0; attempt < 540; attempt += 1) {
    if (token !== state.pollToken || state.runID !== runID) return;
    try {
      const run = await request(`/agent/cortex-runs/${encodeURIComponent(runID)}`);
      renderRunState(run);
      if (["succeeded","failed","canceled"].includes(run.status)) {
        state.runID = null;
        byId("run-state").hidden = true;
        if (state.room) await selectRoom(state.room.conversation_id);
        await Promise.all([loadRooms(),loadSnapshot()]);
        return;
      }
    } catch (error) {
      showError(error);
    }
    await new Promise((resolve) => setTimeout(resolve,1000));
  }
}

async function submitMessage(event) {
  event.preventDefault();
  if (state.sending || state.runID) return;
  const query = byId("message-input").value.trim();
  const symbol = byId("chat-symbol").value.trim().toUpperCase();
  if (!query) return;
  state.sending = true;
  clearError();
  renderMessages(query);
  try {
    const body = {mode:state.room?.mode || "research",symbol,query};
    if (state.room) {
      body.conversation_id = state.room.conversation_id;
      body.conversation_created_at = state.room.conversation_created_at;
    }
    const accepted = await request("/agent/room-requests",{
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify(body),
    });
    state.room = accepted.room;
    state.runID = accepted.id;
    state.messages.push({user_text:query,created_at:new Date().toISOString()});
    byId("message-input").value = "";
    await loadRooms();
    renderMessages();
    renderRunState({status:"running",trace:[]});
    pollRun(accepted.id);
  } catch (error) {
    showError(error);
    renderMessages();
  } finally {
    state.sending = false;
    renderMessages();
  }
}

async function loadHealth() {
  const indicator = byId("agent-status");
  const system = byId("system-dot");
  try {
    const health = await request("/agent/cortex-operations");
    indicator.className = `agent-status ${health.status}`;
    indicator.lastChild.textContent = health.status.toUpperCase();
    system.className = `system-dot ${health.status}`;
  } catch {
    indicator.className = "agent-status degraded";
    indicator.lastChild.textContent = "UNAVAILABLE";
    system.className = "system-dot degraded";
  }
}

async function restore() {
  try {
    await request("/agent/auth/session");
  } catch (error) {
    if (error.status === 401) {
      byId("login-screen").hidden = false;
      byId("login-password").focus();
      return;
    }
    showError(error);
    return;
  }
  byId("login-screen").hidden = true;
  const results = await Promise.allSettled([
    loadSnapshot().then(loadCandidates),
    loadTriggers(),loadRooms(),loadHealth(),loadMarket(),
  ]);
  const failure = results.find((result) => result.status === "rejected");
  if (failure) showError(failure.reason);
  if (state.rooms.length) await selectRoom(state.rooms[0].conversation_id);
  else renderMessages();
}

byId("symbol-input").addEventListener("change",(event) => {
  const value = event.target.value.toUpperCase().replace(/[^A-Z0-9.^_-]/g,"");
  if (value) selectSymbol(value);
});
byId("symbol-input").addEventListener("input",(event) => {
  event.target.value = event.target.value.toUpperCase().replace(/[^A-Z0-9.^_-]/g,"");
});
byId("chat-symbol").addEventListener("input",(event) => {
  event.target.value = event.target.value.toUpperCase().replace(/[^A-Z0-9.^_-]/g,"");
});
byId("refresh-console").addEventListener("click",() => Promise.allSettled([
  loadSnapshot().then(loadCandidates),loadTriggers(),loadHealth(),loadMarket(),
]));
byId("replay-create").addEventListener("click",createReplay);
byId("replay-next").addEventListener("click",advanceReplay);
for (const button of byId("environment-switch").querySelectorAll("button")) {
  button.addEventListener("click",async () => {
    if (button.disabled || button.dataset.environment === state.environment) return;
    state.environment = button.dataset.environment;
    clearError();
    try {
      await loadSnapshot();
      await loadCandidates();
    } catch (error) {
      showError(error);
    }
  });
}
for (const button of byId("autonomy-switch").querySelectorAll("button")) {
  button.addEventListener("click",async () => {
    const mode = button.dataset.autonomy;
    if (button.disabled || mode === state.autonomy?.selected ||
        !state.environment || !state.autonomy?.generation) return;
    clearError();
    try {
      await request(`/agent/console/autonomy/${encodeURIComponent(state.environment)}`,{
        method:"PUT",
        headers:{"Content-Type":"application/json"},
        body:JSON.stringify({
          expected_generation:state.autonomy.generation,
          mode,
        }),
      });
      await loadSnapshot();
      await loadCandidates();
    } catch (error) {
      showError(error);
      await loadSnapshot().catch(() => {});
      await loadCandidates().catch(() => {});
    }
  });
}
byId("room-select").addEventListener("change",(event) => selectRoom(event.target.value).catch(showError));
byId("new-room").addEventListener("click",() => selectRoom(""));
byId("composer").addEventListener("submit",submitMessage);
for (const button of document.querySelectorAll("[data-days]")) {
  button.addEventListener("click",() => {
    state.days = Number(button.dataset.days);
    document.querySelectorAll("[data-days]").forEach((item) => item.classList.toggle("active",item === button));
    loadMarket();
  });
}
window.addEventListener("resize",() => {
  if (state.bars.length) drawChart();
});
byId("login-form").addEventListener("submit",async (event) => {
  event.preventDefault();
  text("login-error","");
  try {
    await request("/agent/auth/login",{
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({password:byId("login-password").value}),
    });
    byId("login-password").value = "";
    await restore();
  } catch (error) {
    text("login-error",error.message || "登录失败");
  }
});

initializeReplayRange();
restore();
setInterval(() => {
  if (byId("login-screen").hidden) {
    Promise.allSettled([loadCandidates(),loadTriggers(),loadHealth(),loadMarket()]);
  }
},15000);
