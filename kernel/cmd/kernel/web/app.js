let readToken = "";
let adminToken = "";
let operationCursor = "";
let refreshPromise = null;
let mcpTools = [];
let mcpToolsLoaded = false;
let controlCapabilities = {admin:false, mutations_enabled:false, mode:"unknown"};
let latestState = null;
let haltArmed = false;
let pendingOperations = [];

const byId = (id) => document.getElementById(id);
const setText = (id, value) => { byId(id).textContent = value == null || value === "" ? "—" : String(value); };
const money = (value) => value == null ? "—" : new Intl.NumberFormat("en-US", {style:"currency", currency:"USD", maximumFractionDigits:2}).format(Number(value));
const when = (value) => value ? new Date(value).toLocaleString() : "—";
const setPanelError = (id, value) => { setText(id, value); byId(id).classList.toggle("hidden", !value); };

async function apiWithToken(path, options = {}, token = readToken) {
  const headers = {...(options.headers || {})};
  if (token) headers.Authorization = `Bearer ${token}`;
  const response = await fetch(path, {...options, headers, cache:"no-store"});
  const payload = await response.json().catch(() => null);
  if (response.status === 401) throw new Error("AUTH_REQUIRED");
  if (!response.ok) {
    const error = new Error(payload?.error || `HTTP_${response.status}`);
    error.status = response.status;
    throw error;
  }
  return payload;
}

async function api(path, options = {}) { return apiWithToken(path, options, readToken); }

async function controlAPI(path, body) {
  if (!adminToken) throw new Error("ADMIN_REQUIRED");
  try {
    return await apiWithToken(path, {
      method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify(body)
    }, adminToken);
  } catch (error) {
    if (error.message === "AUTH_REQUIRED") {
      adminToken = "";
      renderControlAccess();
    }
    throw error;
  }
}

function renderLedger(prefix, ledger, asOf) {
  setText(`${prefix}-count`, ledger?.trades_today ?? 0);
  setText(`${prefix}-risk`, `Open risk ${money(ledger?.open_risk ?? 0)}`);
  setText(`${prefix}-pnl`, `Realized PnL ${money(ledger?.realized_pnl ?? 0)} · daily floor -${money(ledger?.daily_loss_limit ?? 0)}`);
  setText(`${prefix}-streak`, `Consecutive loss days ${ledger?.consecutive_loss_days ?? 0}`);
  setText(`${prefix}-halt`, ledger?.halted ? `HALTED · ${ledger.halt_reason || "operator"}` : "READY · no active breaker");
  byId(`${prefix}-halt`).style.color = ledger?.halted ? "var(--red)" : "var(--green)";
  setText(`${prefix}-asof`, `kernel_db · ${when(asOf)} · current`);
}

function renderList(containerId, emptyId, countId, items, formatter) {
  const container = byId(containerId);
  const nodes = (items || []).map((item) => {
    const formatted = formatter(item);
    const row = document.createElement("div"); row.className = "list-item";
    const title = document.createElement("strong"); title.textContent = formatted.title;
    const detail = document.createElement("span"); detail.textContent = formatted.detail;
    row.append(title);
    if (formatted.origin) row.append(originBadge(formatted.origin));
    row.append(detail); return row;
  });
  container.replaceChildren(...nodes);
  setText(countId, nodes.length);
  byId(emptyId).classList.toggle("hidden", nodes.length > 0);
}

function originBadge(origin, label = origin) {
  const known = new Set(["alpheus", "external", "ambiguous", "mixed"]);
  const normalized = known.has(origin) ? origin : "ambiguous";
  const badge = document.createElement("span");
  badge.className = `origin-badge origin-${normalized}`;
  badge.textContent = String(label || normalized).toUpperCase();
  return badge;
}

function brokerOriginKey(family, objectKey) { return `${family}\u0000${objectKey}`; }
function exposureKey(kind, symbol) { return `${kind}\u0000${symbol}`; }

async function renderPositions(positions, origins = {}, coexistencePositions = []) {
  const body = byId("positions-body");
  const exposure = {};
  coexistencePositions.forEach((position) => { exposure[exposureKey(position.kind, position.symbol)] = position; });
  const rows = await Promise.all((positions || []).map(async (position) => {
    let quote = null;
    try { quote = await api(`/market/quote/${encodeURIComponent(position.symbol)}`); } catch (_) {}
    const row = document.createElement("tr");
    const allocation = exposure[exposureKey(position.kind, position.symbol)];
    const origin = origins[brokerOriginKey("positions", position.position_id)] || {origin:allocation?.observed_origin || "ambiguous", evidence:allocation?.origin_evidence || "unavailable"};
    const values = [position.symbol, position.kind];
    values.forEach((value) => { const cell = document.createElement("td"); cell.textContent = String(value); row.append(cell); });
    const exposureCell = document.createElement("td");
    exposureCell.append(originBadge(allocation?.exposure_origin || "ambiguous", allocation?.exposure_origin || "pending"));
    const exposureDetail = document.createElement("small");
    exposureDetail.textContent = allocation ? `tracked ${allocation.tracked_qty} · external ${allocation.external_qty}` : "awaiting reconciliation";
    exposureCell.append(exposureDetail); row.append(exposureCell);
    const originCell = document.createElement("td"); originCell.append(originBadge(origin.origin)); row.append(originCell);
    [position.qty, position.multiplier, money(position.avg_price), quote ? `${money(quote.bid)} / ${money(quote.ask)}` : "Unavailable", quote ? `FRESH · ${quote.source} · ${when(quote.as_of)} · ${origin.evidence}` : `STALE/ERROR · fail closed · ${origin.evidence}`].forEach((value) => {
      const cell = document.createElement("td"); cell.textContent = String(value); row.append(cell);
    });
    return row;
  }));
  body.replaceChildren(...rows);
  setText("position-count", rows.length);
  byId("positions-empty").classList.toggle("hidden", rows.length > 0);
}

function renderBrokerCoexistence(view = {}) {
  const reconciliation = view.reconciliation || {};
  const state = reconciliation.state || "uninitialized";
  const stateBadge = byId("reconciliation-state");
  stateBadge.className = `badge reconciliation-${state}`;
  setText("reconciliation-state", state);
  setText("broker-observed-generation", reconciliation.observed_generation || "—");
  setText("broker-reconciled-generation", reconciliation.reconciled_generation || "—");
  setText("broker-observed-at", when(reconciliation.observed_at));
  renderList("external-change-list", "external-change-empty", "external-change-count", view.external_changes || [], (change) => ({
    title:`${change.change_kind} · ${change.symbol} / ${change.kind}`,
    origin:change.origin || "ambiguous",
    detail:`provider ${change.provider_qty_before ?? "—"} → ${change.provider_qty_after} · tracked ${change.tracked_qty_before} → ${change.tracked_qty_after} · adjusted ${change.adjusted_tracked_qty} · attribution ${change.attribution_status} · observation ${change.observation_generation} · ${when(change.created_at)}`
  }));
  renderList("invalidation-list", "invalidation-empty", "invalidation-count", view.invalidated_operations || [], (invalidation) => ({
    title:`${invalidation.operation_status} · operation ${invalidation.operation_id}`,
    origin:"ambiguous",
    detail:`${invalidation.reason} · observation ${invalidation.observation_generation} · ${when(invalidation.created_at)}`
  }));
  setPanelError("coexistence-error", "");
}

function operationIntent(operation) {
  const payload = operation.payload || {};
  const action = payload.action || "unknown";
  const symbol = payload.symbol || payload.underlying || "—";
  const side = payload.side ? ` ${payload.side}` : "";
  const qty = payload.qty == null ? "" : ` ${payload.qty}`;
  return `${action}${side}${qty} · ${symbol}`;
}

async function loadOperations(reset) {
  const cursor = reset ? "" : operationCursor;
  const query = cursor ? `?limit=25&cursor=${encodeURIComponent(cursor)}` : "?limit=25";
  const page = await api(`/operations${query}`);
  const rows = (page.operations || []).map((operation) => {
    const row = document.createElement("tr");
    const values = [when(operation.ts), operation.id, operation.class, operation.status, operationIntent(operation), operation.proposer];
    values.forEach((value) => { const cell = document.createElement("td"); cell.textContent = String(value || "—"); row.append(cell); });
    return row;
  });
  const body = byId("operations-body");
  if (reset) body.replaceChildren(...rows); else body.append(...rows);
  operationCursor = page.next_cursor || "";
  byId("operations-more").classList.toggle("hidden", !operationCursor);
  byId("operations-empty").classList.toggle("hidden", body.children.length > 0);
  setText("operation-source", page.source);
  setText("operations-asof", `As of ${when(page.as_of)}`);
  setPanelError("operations-error", "");
}

function fact(label, value, className = "") {
  const item = document.createElement("div");
  if (className) item.className = className;
  const name = document.createElement("span"); name.textContent = label;
  const content = document.createElement("strong"); content.textContent = value == null || value === "" ? "—" : String(value);
  item.append(name, content);
  return item;
}

function renderControlAccess(rerenderReviews = true) {
  const enabled = Boolean(controlCapabilities.mutations_enabled);
  byId("controls-disabled").classList.toggle("hidden", enabled);
  byId("control-unlock").classList.toggle("hidden", !enabled || Boolean(adminToken));
  byId("control-actions").classList.toggle("hidden", !enabled || !adminToken);
  setText("control-state", !enabled ? `${controlCapabilities.mode} · DISABLED` : adminToken ? "ADMIN ENABLED" : "ADMIN LOCKED");
  renderBreakerActions();
  if (rerenderReviews) {
    renderPendingReviews();
    loadControlWarnings().catch((error) => setPanelError("warnings-error", error.message));
  }
}

async function loadControlCapabilities() {
  controlCapabilities = await api("/auth/capabilities");
  if (!controlCapabilities.mutations_enabled) adminToken = "";
  renderControlAccess(false);
}

function reviewActionControls(operation) {
  if (!adminToken || !controlCapabilities.mutations_enabled) return null;
  const controls = document.createElement("div"); controls.className = "review-actions";
  const rationale = document.createElement("input");
  rationale.type = "text"; rationale.maxLength = 500; rationale.placeholder = "Rationale (optional)";
  rationale.autocomplete = "off"; rationale.spellcheck = false;
  const approve = document.createElement("button"); approve.type = "button"; approve.textContent = "Approve"; approve.className = "approve";
  const reject = document.createElement("button"); reject.type = "button"; reject.textContent = "Reject"; reject.className = "danger secondary-danger";
  const decide = async (verdict) => {
    approve.disabled = true; reject.disabled = true;
    setPanelError("pending-error", "");
    try {
      const result = await controlAPI(`/operations/${encodeURIComponent(operation.id)}/review`, {
        verdict, rationale:rationale.value.trim()
      });
      const ids = [`operation ${result.operation_id || operation.id}`];
      if (result.attempt_id) ids.push(`attempt ${result.attempt_id}`);
      setText("control-result", `${verdict.toUpperCase()} · ${ids.join(" · ")} · ${result.status || "recorded"}`);
      await refreshAfterControl();
    } catch (error) {
      setPanelError("pending-error", `Review refused: ${error.message}`);
      approve.disabled = false; reject.disabled = false;
    }
  };
  approve.addEventListener("click", () => decide("approved"));
  reject.addEventListener("click", () => decide("rejected"));
  controls.append(rationale, approve, reject);
  return controls;
}

async function pendingReviewCard(operation) {
  const payload = operation.payload || {};
  const verdict = operation.verdict || {};
  const card = document.createElement("article"); card.className = "review-card";
  const head = document.createElement("div"); head.className = "review-card-head";
  const title = document.createElement("strong"); title.textContent = `${payload.action || "operation"} · ${payload.symbol || payload.underlying || "—"}`;
  const id = document.createElement("code"); id.textContent = operation.id;
  head.append(title, id);

  let quoteText = "Unavailable · approval fails closed";
  const symbol = payload.symbol || payload.underlying;
  if (symbol) {
    try {
      const quote = await api(`/market/quote/${encodeURIComponent(symbol)}`);
      quoteText = `${money(quote.bid)} / ${money(quote.ask)} · ${quote.source || "provider"} · ${when(quote.as_of)}`;
    } catch (_) {}
  }

  const facts = document.createElement("div"); facts.className = "review-facts";
  facts.append(
    fact("Quantity", payload.qty ?? "—"),
    fact("Multiplier", payload.multiplier ?? "—"),
    fact("Declared max risk", payload.max_risk_usd == null ? "Not declared" : money(payload.max_risk_usd)),
    fact("Kernel derived risk", money(payload.derived_max_risk)),
    fact("Approved price cap", money(payload.approved_price_cap)),
    fact("Latest sane bid / ask", quoteText)
  );

  const checks = document.createElement("div"); checks.className = "check-list";
  const entries = Object.entries(verdict.checks || {});
  if (entries.length === 0) {
    const missing = document.createElement("span"); missing.className = "check fail"; missing.textContent = "CHECK SNAPSHOT UNAVAILABLE"; checks.append(missing);
  } else {
    entries.sort(([left], [right]) => left.localeCompare(right)).forEach(([name, passed]) => {
      const check = document.createElement("span"); check.className = `check ${passed ? "pass" : "fail"}`;
      check.textContent = `${passed ? "PASS" : "FAIL"} · ${name}`; checks.append(check);
    });
  }
  const plan = document.createElement("p"); plan.className = "review-plan";
  plan.textContent = `Plan · stop ${payload.plan?.stop || "—"} · invalidation ${payload.plan?.invalidation || "—"} · time ${payload.plan?.time_stop || "—"} · target ${payload.plan?.target || "—"}`;
  card.append(head, facts, checks, plan);
  const controls = reviewActionControls(operation);
  if (controls) card.append(controls);
  return card;
}

async function loadPendingReviews() {
  const page = await api("/operations?status=pending_review&limit=100");
  pendingOperations = page.operations || [];
  await renderPendingReviews();
  setPanelError("pending-error", "");
}

async function renderPendingReviews() {
  const list = byId("pending-list");
  const cards = await Promise.all(pendingOperations.map((operation) => pendingReviewCard(operation)));
  list.replaceChildren(...cards);
  setText("pending-count", cards.length);
  byId("pending-empty").classList.toggle("hidden", cards.length > 0);
}

async function loadControlWarnings() {
  const snapshot = await api("/control/warnings");
  const nodes = (snapshot.warnings || []).map((warning) => {
    const row = document.createElement("div"); row.className = "warning-item";
    const title = document.createElement("strong"); title.textContent = `${warning.kind} · ${warning.state}`;
    const detail = document.createElement("span");
    detail.textContent = `${warning.ledger || "—"} · ${warning.symbol || "—"} · operation ${warning.operation_id} · ${warning.detail || "operator inspection required"}${warning.provider_error_code ? ` · ${warning.provider_error_code}` : ""}${warning.candidate_broker_order_id ? ` · candidate ${warning.candidate_broker_order_id}` : ""} · ${when(warning.created_at)}`;
    const id = document.createElement("code"); id.textContent = warning.id;
    row.append(title, detail, id);
    if (warning.kind === "execution_attempt" && warning.state === "unknown" && warning.candidate_broker_order_id && adminToken && controlCapabilities.mode === "live") {
      const adopt = document.createElement("button"); adopt.type = "button"; adopt.className = "danger secondary-danger candidate-adopt";
      adopt.textContent = "Arm candidate adoption";
      let armed = false;
      let disarmTimer = null;
      adopt.addEventListener("click", async () => {
        if (!armed) {
          armed = true;
          adopt.textContent = `Confirm ${warning.candidate_broker_order_id.slice(-8)}`;
          disarmTimer = window.setTimeout(() => { armed = false; adopt.textContent = "Arm candidate adoption"; }, 8000);
          return;
        }
        window.clearTimeout(disarmTimer);
        adopt.disabled = true;
        setPanelError("warnings-error", "");
        try {
          await controlAPI(`/execution-attempts/${encodeURIComponent(warning.id)}/adopt-candidate`, {
            confirm_attempt_id:warning.id, confirm_broker_order_id:warning.candidate_broker_order_id
          });
          setText("control-result", `Adopted exact broker candidate ${warning.candidate_broker_order_id}.`);
          await refresh();
        } catch (error) {
          armed = false;
          adopt.disabled = false;
          adopt.textContent = "Arm candidate adoption";
          setPanelError("warnings-error", error.message);
        }
      });
      row.append(adopt);
    }
    return row;
  });
  byId("warning-list").replaceChildren(...nodes);
  setText("warning-count", nodes.length);
  byId("warnings-empty").classList.toggle("hidden", nodes.length > 0);
  setText("warnings-asof", `${snapshot.source || "kernel_db"} · ${when(snapshot.as_of)}`);
  setPanelError("warnings-error", "");
}

function renderBreakerActions() {
  if (!byId("breaker-actions")) return;
  const container = byId("breaker-actions");
  const resumable = new Set(["daily_loss", "loss_streak", "pnl_divergence"]);
  const actions = [];
  if (adminToken && latestState?.day) {
    ["live", "shadow"].forEach((ledger) => {
      const state = latestState.day[ledger];
      if (!state?.halted || !resumable.has(state.halt_reason)) return;
      const button = document.createElement("button"); button.type = "button"; button.className = "secondary";
      button.textContent = `Resume ${ledger} · ${state.halt_reason}`;
      button.addEventListener("click", async () => {
        button.disabled = true; setPanelError("control-error", "");
        try {
          const result = await controlAPI("/breaker/resume", {ledger, reason:state.halt_reason});
          setText("control-result", `BREAKER RESUMED · event ${result.event_id} · ${result.ledger} / ${result.override_reason}`);
          await refreshAfterControl();
        } catch (error) {
          setPanelError("control-error", `Resume refused: ${error.message}`); button.disabled = false;
        }
      });
      actions.push(button);
    });
  }
  container.replaceChildren(...actions);
  byId("breaker-empty").classList.toggle("hidden", actions.length > 0);
}

async function refreshAfterControl() {
  const pendingRefresh = refreshPromise;
  if (pendingRefresh) await pendingRefresh;
  await refresh();
}

async function renderState(state) {
  latestState = state;
  setText("mode-badge", state.mode);
  setText("account-type", state.account.account_type); setText("account-source", state.account.source);
  setText("equity", money(state.account.equity)); setText("buying-power", money(state.account.buying_power));
  const brokerObservation = state.broker_observation || {};
  setText("provider-cash", state.account.cash_known ? money(state.account.cash) : "Unknown"); setText("account-asof", `As of ${when(state.account.as_of)} · observation ${brokerObservation.generation || "—"}`);
  const gate = state.live_execution_gate || {};
  setText("mutation-gate", gate.unknown_attempt_id ? `LATCHED · ${gate.unknown_attempt_id}` : gate.active_attempt_id ? `ACTIVE · ${gate.active_attempt_id}` : "READY");
  renderLedger("live", state.day.live, state.as_of); renderLedger("shadow", state.day.shadow, state.as_of);
  const origins = {};
  (state.broker_objects || []).forEach((object) => { origins[brokerOriginKey(object.family, object.object_key)] = {origin:object.origin || "ambiguous", evidence:object.origin_evidence || "unavailable"}; });
  renderBrokerCoexistence(state.broker_coexistence);
  await renderPositions(state.positions, origins, state.broker_coexistence?.positions || []);
  renderList("orders-list", "orders-empty", "order-count", state.open_orders, (o) => { const origin = origins[brokerOriginKey("orders", o.broker_order_id)] || {origin:"ambiguous", evidence:"unavailable"}; return {title:`${o.side} ${o.symbol} · ${o.state}`, origin:origin.origin, detail:`${o.qty} @ ${money(o.limit_price)} · ${o.source} · ${origin.evidence} · ${when(o.as_of)}`}; });
  renderList("fills-list", "fills-empty", "fill-count", state.recent_fills, (f) => { const origin = origins[brokerOriginKey("fills", f.fill_id)] || {origin:"ambiguous", evidence:"separate fill observation"}; return {title:`${f.side} ${f.symbol} · ${f.qty}`, origin:origin.origin, detail:`${money(f.price)} · ${f.source} · ${origin.evidence} · ${when(f.as_of)}`}; });
  renderBreakerActions();
  ["account-error", "positions-error", "orders-error", "fills-error"].forEach((id) => setPanelError(id, ""));
}

function renderProvider(provider) {
  setText("account-mask", provider.account); setText("provider-name", provider.source);
  setText("provider-state", provider.connected ? "connected" : "degraded"); setText("snapshot-version", provider.snapshot_version);
  setText("schema-parity", provider.schema_drift ? "FAIL CLOSED" : "PASS"); setText("contract-state", provider.schema_drift ? "drift" : "parity");
  setText("last-read", when(provider.last_successful_read)); setText("provider-error", provider.last_error || "None");
}

function renderHours(hours) {
  setText("market-state", hours.is_open ? "Market open" : "Market closed");
  setText("market-hours", hours.opens_at ? `${when(hours.opens_at)} → ${when(hours.closes_at)}` : "Session schedule unavailable");
  setText("hours-asof", `${hours.source || "provider"} · ${when(hours.as_of)} · current`); setPanelError("hours-error", "");
}

const queryFields = {
  quote: ["symbol"],
  bars: ["symbol", "days"],
  expirations: ["symbol"],
  chain: ["symbol", "expiry", "window"],
  state: [],
  provider: []
};

function querySymbol() {
  const symbol = byId("query-symbol").value.trim().toUpperCase();
  if (!symbol || !/^[A-Z0-9._-]+$/.test(symbol)) throw new Error("Enter a valid symbol.");
  return symbol;
}

function boundedQueryNumber(id, minimum, maximum, integer) {
  const raw = byId(id).value.trim();
  const value = Number(raw);
  if (raw === "" || !Number.isFinite(value) || value < minimum || value > maximum || (integer && !Number.isInteger(value))) {
    throw new Error(`Enter a value from ${minimum} to ${maximum}.`);
  }
  return value;
}

function queryPath() {
  const type = byId("query-type").value;
  switch (type) {
    case "quote": return `/market/quote/${encodeURIComponent(querySymbol())}`;
    case "bars": return `/market/bars/${encodeURIComponent(querySymbol())}?days=${boundedQueryNumber("query-days", 1, 30, true)}`;
    case "expirations": return `/market/expirations/${encodeURIComponent(querySymbol())}`;
    case "chain": {
      const expiry = byId("query-expiry").value;
      if (!/^\d{4}-\d{2}-\d{2}$/.test(expiry)) throw new Error("Choose an expiration date.");
      const windowPct = boundedQueryNumber("query-window", 0, 15, false);
      return `/market/chain/${encodeURIComponent(querySymbol())}?expiry=${encodeURIComponent(expiry)}&window_pct=${encodeURIComponent(windowPct)}`;
    }
    case "state": return "/state";
    case "provider": return "/provider/status";
    default: throw new Error("Unsupported query.");
  }
}

function updateQueryForm() {
  const visible = new Set(queryFields[byId("query-type").value] || []);
  document.querySelectorAll("[data-query-field]").forEach((field) => {
    field.classList.toggle("hidden", !visible.has(field.dataset.queryField));
  });
  const symbolLabel = byId("query-type").value === "quote" || byId("query-type").value === "bars" ? "Symbol" : "Underlying";
  document.querySelector('[for="query-symbol"] span').textContent = symbolLabel;
  try {
    setText("query-request", `GET ${queryPath()}`);
    setPanelError("query-error", "");
  } catch (error) {
    setText("query-request", "Complete the required parameters");
  }
}

async function runManualQuery() {
  const path = queryPath();
  const started = performance.now();
  byId("query-run").disabled = true;
  setText("query-status", "QUERYING");
  setPanelError("query-error", "");
  try {
    const result = await api(path);
    byId("query-result").textContent = JSON.stringify(result, null, 2);
    setText("query-request", `GET ${path}`);
    setText("query-status", `OK · ${Math.round(performance.now() - started)} MS`);
  } catch (error) {
    byId("query-result").textContent = "No result returned.";
    setText("query-status", "FAILED CLOSED");
    setPanelError("query-error", error.message === "AUTH_REQUIRED" ? "Enter a valid read token above." : `Query ${error.message}`);
  } finally {
    byId("query-run").disabled = false;
  }
}

function selectedMCPTool() {
  return mcpTools.find((tool) => tool.name === byId("mcp-tool").value) || null;
}

function renderMCPTool(resetArgs) {
  const tool = selectedMCPTool();
  if (!tool) return;
  setText("mcp-category", tool.category);
  setText("mcp-account-rule", tool.requires_account ? "BOUND ACCOUNT AUTO-INJECTED" : "NO ACCOUNT ARGUMENT");
  setText("mcp-description", tool.description);
  byId("mcp-schema").textContent = JSON.stringify(tool.input_schema, null, 2);
  if (resetArgs) byId("mcp-args").value = JSON.stringify(tool.example_args || {}, null, 2);
  setText("mcp-request", `POST /mcp/read-query · ${tool.name}`);
  setText("mcp-status", "READY");
  setPanelError("mcp-error", "");
}

async function loadMCPTools() {
  const catalog = await api("/mcp/read-tools");
  mcpTools = catalog.tools || [];
  const select = byId("mcp-tool");
  const options = mcpTools.map((tool) => {
    const option = document.createElement("option");
    option.value = tool.name;
    option.textContent = `${tool.category} · ${tool.name}`;
    return option;
  });
  select.replaceChildren(...options);
  select.disabled = mcpTools.length === 0;
  byId("mcp-run").disabled = mcpTools.length === 0;
  byId("mcp-reset").disabled = mcpTools.length === 0;
  setText("mcp-count", `${catalog.safe_tools} SAFE / ${catalog.blocked_mutations} BLOCKED`);
  setText("mcp-state", catalog.source === "robinhood-mcp" ? "LIVE READY" : "UNAVAILABLE");
  mcpToolsLoaded = mcpTools.length > 0;
  renderMCPTool(true);
}

function parsedMCPArgs() {
  let args;
  try { args = JSON.parse(byId("mcp-args").value); }
  catch (_) { throw new Error("Arguments must be valid JSON."); }
  if (args === null || Array.isArray(args) || typeof args !== "object") throw new Error("Arguments must be a JSON object.");
  return args;
}

async function runMCPQuery() {
  const tool = selectedMCPTool();
  if (!tool) throw new Error("Choose a Live MCP tool.");
  const started = performance.now();
  byId("mcp-run").disabled = true;
  setText("mcp-status", "QUERYING LIVE");
  setPanelError("mcp-error", "");
  try {
    const result = await api("/mcp/read-query", {
      method:"POST", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({tool:tool.name, args:parsedMCPArgs()})
    });
    byId("mcp-result").textContent = JSON.stringify(result, null, 2);
    setText("mcp-status", `OK · ${Math.round(performance.now() - started)} MS`);
  } catch (error) {
    byId("mcp-result").textContent = "No result returned.";
    setText("mcp-status", "FAILED CLOSED");
    setPanelError("mcp-error", error.message === "AUTH_REQUIRED" ? "Enter a valid read token above." : error.message);
  } finally {
    byId("mcp-run").disabled = false;
  }
}

function refresh() {
  if (refreshPromise) return refreshPromise;
  refreshPromise = (async () => {
    const requests = [
      api("/state"), api("/provider/status"), api("/market/hours"), loadOperations(true),
      mcpToolsLoaded ? Promise.resolve(null) : loadMCPTools(), loadControlCapabilities(),
      loadPendingReviews(), loadControlWarnings()
    ];
    const results = await Promise.allSettled(requests);
    const authRequired = results.some((result) => result.status === "rejected" && result.reason.message === "AUTH_REQUIRED");
    const fulfilled = results.filter((result) => result.status === "fulfilled").length;
    byId("auth-panel").classList.toggle("hidden", !authRequired);
    byId("connection-dot").className = fulfilled > 0 ? "dot ok" : "dot bad";
    setText("connection-label", authRequired ? "Read token required" : fulfilled > 0 ? "Kernel connected" : "Kernel unavailable");
    if (results[0].status === "fulfilled") await renderState(results[0].value);
    else if (!authRequired) ["account-error", "positions-error", "orders-error", "fills-error"].forEach((id) => setPanelError(id, `State ${results[0].reason.message}`));
    if (results[1].status === "fulfilled") renderProvider(results[1].value);
    else if (!authRequired) setText("provider-error", `Provider ${results[1].reason.message}`);
    if (results[2].status === "fulfilled") renderHours(results[2].value);
    else if (!authRequired) setPanelError("hours-error", `Hours ${results[2].reason.message}`);
    if (results[3].status === "rejected" && !authRequired) setPanelError("operations-error", `Operations ${results[3].reason.message}`);
    if (results[4].status === "rejected" && !authRequired) {
      setText("mcp-state", "UNAVAILABLE");
      setPanelError("mcp-error", results[4].reason.message);
    }
    if (results[5].status === "rejected" && !authRequired) setPanelError("control-error", `Control status ${results[5].reason.message}`);
    if (results[6].status === "rejected" && !authRequired) setPanelError("pending-error", `Pending review ${results[6].reason.message}`);
    if (results[7].status === "rejected" && !authRequired) setPanelError("warnings-error", `Warnings ${results[7].reason.message}`);
    setText("refresh-time", `Refreshed ${new Date().toLocaleTimeString()}`);
  })().finally(() => { refreshPromise = null; });
  return refreshPromise;
}

byId("auth-form").addEventListener("submit", async (event) => {
  event.preventDefault(); readToken = byId("read-token").value; byId("read-token").value = "";
  setText("auth-error", "");
  const pendingRefresh = refreshPromise;
  if (pendingRefresh) await pendingRefresh;
  await refresh();
  if (!byId("auth-panel").classList.contains("hidden")) { readToken = ""; setText("auth-error", "Token rejected."); }
});

byId("admin-auth-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const candidate = byId("admin-token").value.trim();
  byId("admin-token").value = "";
  setText("admin-auth-error", "");
  try {
    const capabilities = await apiWithToken("/auth/capabilities", {}, candidate);
    if (!capabilities.admin || !capabilities.mutations_enabled) throw new Error("Admin controls are unavailable for this token or mode.");
    adminToken = candidate;
    controlCapabilities = capabilities;
    setText("control-result", "Admin controls unlocked in memory for this tab.");
    renderControlAccess();
  } catch (error) {
    adminToken = "";
    setText("admin-auth-error", error.message === "AUTH_REQUIRED" ? "Admin Token rejected." : error.message);
    renderControlAccess();
  }
});

byId("halt-reason").addEventListener("input", () => {
  haltArmed = false;
  byId("halt-button").textContent = "Arm global halt";
  byId("halt-confirmation").classList.add("hidden");
});

byId("halt-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const reason = byId("halt-reason").value.trim();
  setPanelError("control-error", "");
  if (!reason) {
    setPanelError("control-error", "Global halt requires a non-empty reason.");
    return;
  }
  if (!haltArmed) {
    haltArmed = true;
    byId("halt-button").textContent = "Confirm global halt";
    byId("halt-confirmation").classList.remove("hidden");
    return;
  }
  byId("halt-button").disabled = true;
  try {
    const result = await controlAPI("/halt", {reason});
    const audit = result.event_id ? `event ${result.event_id}` : "existing halt state";
    setText("control-result", `GLOBAL HALT ACTIVE · ${audit} · ${result.reason}`);
    byId("halt-reason").value = "";
    haltArmed = false;
    byId("halt-button").textContent = "Arm global halt";
    byId("halt-confirmation").classList.add("hidden");
    await refreshAfterControl();
  } catch (error) {
    setPanelError("control-error", `Halt refused: ${error.message}`);
  } finally {
    byId("halt-button").disabled = false;
  }
});

byId("operations-more").addEventListener("click", async () => {
  byId("operations-more").disabled = true;
  try { await loadOperations(false); } catch (error) { setPanelError("operations-error", `Operations ${error.message}`); } finally { byId("operations-more").disabled = false; }
});

byId("query-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try { await runManualQuery(); } catch (error) { setText("query-status", "CHECK INPUT"); setPanelError("query-error", error.message); }
});

byId("query-clear").addEventListener("click", () => {
  byId("query-result").textContent = "Run a query to inspect normalized provider data.";
  setText("query-status", "READY");
  setPanelError("query-error", "");
});

byId("query-type").addEventListener("change", updateQueryForm);
["query-symbol", "query-days", "query-expiry", "query-window"].forEach((id) => byId(id).addEventListener("input", updateQueryForm));

byId("mcp-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try { await runMCPQuery(); } catch (error) { setText("mcp-status", "CHECK INPUT"); setPanelError("mcp-error", error.message); }
});
byId("mcp-tool").addEventListener("change", () => renderMCPTool(true));
byId("mcp-reset").addEventListener("click", () => renderMCPTool(true));

updateQueryForm();
refresh();
setInterval(refresh, 15000);
