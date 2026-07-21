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

function renderTrace(job) {
  const trace = Array.isArray(job?.trace) ? job.trace : [];
  byId("trace-status").textContent = job?.id
    ? `${String(job.status || "unknown").toUpperCase()} · ATTEMPT ${job.attempt || 0}`
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
      error_code: event.error_code || undefined,
    })),
  };
  byId("trace").textContent = JSON.stringify(summary, null, 2);
}

function showAuthenticated(authenticated) {
  byId("login-panel").classList.toggle("hidden", authenticated);
  byId("query-panel").classList.toggle("hidden", !authenticated);
  byId("logout").classList.toggle("hidden", !authenticated);
  if (!authenticated) byId("password").focus();
}

async function restoreSession() {
  try {
    await request("/agent/auth/session");
    showAuthenticated(true);
    await refreshCredentialStatus();
  } catch (_) {
    showAuthenticated(false);
  }
}

byId("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  byId("login").disabled = true;
  byId("login-error").textContent = "";
  try {
    await request("/agent/auth/login", {
      method:"POST", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({password:byId("password").value})
    });
    byId("password").value = "";
    showAuthenticated(true);
    await refreshCredentialStatus();
  } catch (_) {
    byId("login-error").textContent = "密码错误。";
  } finally {
    byId("login").disabled = false;
  }
});

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

byId("logout").addEventListener("click", async () => {
  await request("/agent/auth/logout", {method:"POST"}).catch(() => null);
  byId("openai-token").value = "";
  byId("brave-token").value = "";
  byId("gexbot-token").value = "";
  byId("robinhood-research-token").value = "";
  showAuthenticated(false);
});

const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function waitForAgentQuery(job) {
  const deadline = Date.now() + 420000;
  while (job.status === "queued" || job.status === "running") {
    if (Date.now() >= deadline) throw new Error("Agent Team 仍在运行，请稍后重试。");
    byId("status").textContent = job.status === "queued" ? "QUERY QUEUED" : "AGENTS WORKING";
    renderTrace(job);
    await wait(750);
    job = await request(`/agent/query-jobs/${encodeURIComponent(job.id)}`);
  }
  renderTrace(job);
  return job;
}

byId("query-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const symbol = byId("symbol").value.trim().toUpperCase();
  const workflow = byId("workflow").value;
  const query = byId("question").value.trim();
  if (!/^[A-Z0-9.-]{1,16}$/.test(symbol) || !query) {
    byId("query-error").textContent = "请输入有效股票代码和问题。";
    return;
  }
  byId("run").disabled = true;
  byId("status").textContent = "SCOUT WORKING";
  byId("query-error").textContent = "";
  byId("result").textContent = "Awaiting dispatcher…";
  renderTrace(null);
  try {
    let job = await request("/agent/query", {
      method:"POST", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({workflow, symbol, query})
    });
    job = await waitForAgentQuery(job);
    if (job.status !== "succeeded") throw new Error(job.error_code || "agent_query_failed");
    const result = job.result;
    byId("result").textContent = JSON.stringify(result, null, 2);
    byId("status").textContent = result.cognition === "stub"
      ? "STUB PASS · MODEL NOT CONNECTED"
      : `COMPLETE · ${String(result.model || "OPENAI").toUpperCase()} · NO OPERATION`;
  } catch (error) {
    if (error.message === "unauthorized") showAuthenticated(false);
    byId("result").textContent = "No result returned.";
    byId("status").textContent = "FAILED CLOSED";
    byId("query-error").textContent = formatError(error);
  } finally {
    byId("run").disabled = false;
  }
});

restoreSession();
