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

restoreSession();
