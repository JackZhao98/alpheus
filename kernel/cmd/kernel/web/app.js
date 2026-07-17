let readToken = "";
let operationCursor = "";
let refreshPromise = null;
let mcpTools = [];
let mcpToolsLoaded = false;

const byId = (id) => document.getElementById(id);
const setText = (id, value) => { byId(id).textContent = value == null || value === "" ? "—" : String(value); };
const money = (value) => value == null ? "—" : new Intl.NumberFormat("en-US", {style:"currency", currency:"USD", maximumFractionDigits:2}).format(Number(value));
const when = (value) => value ? new Date(value).toLocaleString() : "—";
const setPanelError = (id, value) => { setText(id, value); byId(id).classList.toggle("hidden", !value); };

async function api(path, options = {}) {
  const headers = {...(options.headers || {})};
  if (readToken) headers.Authorization = `Bearer ${readToken}`;
  const response = await fetch(path, {...options, headers, cache:"no-store"});
  if (response.status === 401) throw new Error("AUTH_REQUIRED");
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw new Error(payload?.error || `HTTP_${response.status}`);
  return payload;
}

function renderLedger(prefix, ledger, asOf) {
  setText(`${prefix}-count`, ledger?.trades_today ?? 0);
  setText(`${prefix}-risk`, `Open risk ${money(ledger?.open_risk ?? 0)}`);
  setText(`${prefix}-halt`, ledger?.halted ? `HALTED · ${ledger.halt_reason || "operator"}` : "READY · no active breaker");
  byId(`${prefix}-halt`).style.color = ledger?.halted ? "var(--red)" : "var(--green)";
  setText(`${prefix}-asof`, `kernel_db · ${when(asOf)} · current`);
}

function renderList(containerId, emptyId, countId, items, formatter) {
  const container = byId(containerId);
  const nodes = (items || []).map((item) => {
    const row = document.createElement("div"); row.className = "list-item";
    const title = document.createElement("strong"); title.textContent = formatter(item).title;
    const detail = document.createElement("span"); detail.textContent = formatter(item).detail;
    row.append(title, detail); return row;
  });
  container.replaceChildren(...nodes);
  setText(countId, nodes.length);
  byId(emptyId).classList.toggle("hidden", nodes.length > 0);
}

async function renderPositions(positions) {
  const body = byId("positions-body");
  const rows = await Promise.all((positions || []).map(async (position) => {
    let quote = null;
    try { quote = await api(`/market/quote/${encodeURIComponent(position.symbol)}`); } catch (_) {}
    const row = document.createElement("tr");
    const values = [position.symbol, position.kind, position.qty, position.multiplier, money(position.avg_price), quote ? `${money(quote.bid)} / ${money(quote.ask)}` : "Unavailable", quote ? `FRESH · ${quote.source} · ${when(quote.as_of)}` : "STALE/ERROR · fail closed"];
    values.forEach((value) => { const cell = document.createElement("td"); cell.textContent = String(value); row.append(cell); });
    return row;
  }));
  body.replaceChildren(...rows);
  setText("position-count", rows.length);
  byId("positions-empty").classList.toggle("hidden", rows.length > 0);
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

async function renderState(state) {
  setText("mode-badge", state.mode);
  setText("account-type", state.account.account_type); setText("account-source", state.account.source);
  setText("equity", money(state.account.equity)); setText("buying-power", money(state.account.buying_power));
  setText("settled-cash", money(state.account.settled_cash)); setText("account-asof", `As of ${when(state.account.as_of)} · current`);
  renderLedger("live", state.day.live, state.as_of); renderLedger("shadow", state.day.shadow, state.as_of);
  await renderPositions(state.positions);
  renderList("orders-list", "orders-empty", "order-count", state.open_orders, (o) => ({title:`${o.side} ${o.symbol} · ${o.state}`, detail:`${o.qty} @ ${money(o.limit_price)} · ${o.source} · ${when(o.as_of)}`}));
  renderList("fills-list", "fills-empty", "fill-count", state.recent_fills, (f) => ({title:`${f.side} ${f.symbol} · ${f.qty}`, detail:`${money(f.price)} · ${f.source} · ${when(f.as_of)}`}));
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
    const requests = [api("/state"), api("/provider/status"), api("/market/hours"), loadOperations(true), mcpToolsLoaded ? Promise.resolve(null) : loadMCPTools()];
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
