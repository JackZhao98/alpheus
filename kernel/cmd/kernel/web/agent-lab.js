const byId = (id) => document.getElementById(id);

async function request(path, options = {}) {
  const response = await fetch(path, {...options, cache:"no-store"});
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw new Error(payload?.error || `HTTP_${response.status}`);
  return payload;
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
  } catch (_) {
    byId("login-error").textContent = "密码错误。";
  } finally {
    byId("login").disabled = false;
  }
});

byId("logout").addEventListener("click", async () => {
  await request("/agent/auth/logout", {method:"POST"}).catch(() => null);
  byId("openai-token").value = "";
  showAuthenticated(false);
});

const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function waitForAgentQuery(job) {
  const deadline = Date.now() + 300000;
  while (job.status === "queued" || job.status === "running") {
    if (Date.now() >= deadline) throw new Error("Agent Team 仍在运行，请稍后重试。");
    byId("status").textContent = job.status === "queued" ? "QUERY QUEUED" : "AGENTS WORKING";
    await wait(750);
    job = await request(`/agent/query-jobs/${encodeURIComponent(job.id)}`);
  }
  return job;
}

byId("query-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const symbol = byId("symbol").value.trim().toUpperCase();
  const workflow = byId("workflow").value;
  const query = byId("question").value.trim();
  const openaiToken = byId("openai-token").value.trim();
  if (!/^[A-Z0-9.-]{1,16}$/.test(symbol) || !query) {
    byId("query-error").textContent = "请输入有效股票代码和问题。";
    return;
  }
  if (!openaiToken) {
    byId("query-error").textContent = "请输入 OpenAI API Token。";
    byId("openai-token").focus();
    return;
  }
  byId("run").disabled = true;
  byId("status").textContent = "SCOUT WORKING";
  byId("query-error").textContent = "";
  try {
    let job = await request("/agent/query", {
      method:"POST", headers:{"Content-Type":"application/json"},
      body:JSON.stringify({workflow, symbol, query, openai_api_key:openaiToken})
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
    byId("query-error").textContent = error.message;
  } finally {
    byId("run").disabled = false;
  }
});

restoreSession();
