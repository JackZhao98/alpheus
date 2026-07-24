const byId = (id) => document.getElementById(id);

const state = {
  rooms: [],
  room: null,
  messages: [],
  mode: "research",
  runID: null,
  trace: [],
  pollToken: 0,
  sending: false,
};

async function request(path, options = {}) {
  const response = await fetch(path, {...options, cache:"no-store"});
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(payload?.error || `HTTP ${response.status}`);
    error.code = payload?.error_code || `http_${response.status}`;
    error.status = response.status;
    throw error;
  }
  return payload;
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function formatWhen(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const today = new Date();
  if (date.toDateString() === today.toDateString()) {
    return date.toLocaleTimeString("zh-CN",{hour:"2-digit",minute:"2-digit"});
  }
  return date.toLocaleDateString("zh-CN",{month:"numeric",day:"numeric"});
}

function formatError(error) {
  const known = {
    agent_room_paused:"这个 Room 已暂停，请先恢复。",
    agent_room_changed:"Room 状态已经变化，请刷新后重试。",
    agent_room_mode_unavailable:"这个模式会在下一阶段启用。",
    cortex_unavailable:"Cortex 暂时无法连接。",
  };
  return known[error?.code] || error?.message || "请求失败";
}

function roomIsRunning(room) {
  return ["queued","running","waiting","canceling"].includes(room?.last_run_state);
}

function renderRooms() {
  const query = byId("room-filter").value.trim().toLowerCase();
  const visible = state.rooms.filter((room) => room.title.toLowerCase().includes(query));
  byId("room-count").textContent = String(state.rooms.length);
  const nodes = visible.map((room) => {
    const button = element("button","room-item");
    button.type = "button";
    if (state.room?.conversation_id === room.conversation_id) button.classList.add("selected");
    if (roomIsRunning(room)) button.classList.add("running");
    if (room.state === "paused") button.classList.add("paused");
    button.append(element("i"));
    const copy = element("div");
    copy.append(element("strong","",room.title));
    copy.append(element("span","",`${room.mode === "research" ? "Research" : room.mode} · ${formatWhen(room.last_activity_at)}`));
    button.append(copy);
    button.addEventListener("click",() => selectRoom(room.conversation_id));
    return button;
  });
  if (!nodes.length) {
    nodes.push(element("p","room-list-empty",query ? "没有匹配的会话。" : "还没有正式会话。发送第一条消息后，它会持久保存在这里。"));
  }
  byId("room-list").replaceChildren(...nodes);
}

function setURLRoom(id) {
  const url = new URL(window.location.href);
  if (id) url.searchParams.set("room",id);
  else url.searchParams.delete("room");
  history.replaceState(null,"",url);
}

function updateRoomHeader() {
  const room = state.room;
  byId("room-title").textContent = room?.title || "新的 Agent Room";
  byId("room-subtitle").textContent = room
    ? "这个 Room 的历史和回答来自持久数据库。"
    : "Cortex 会根据问题自行选择 Agent、数据源和工具。";
  byId("mode-badge").textContent = (room?.mode || state.mode).replaceAll("_"," ").toUpperCase();
  byId("pause-room").hidden = !room;
  byId("pause-room").textContent = room?.state === "paused" ? "恢复" : "暂停";
  byId("context-mode").textContent = room?.mode === "research" || !room ? "Research" : room.mode;
  byId("context-state").textContent = room ? (room.state === "paused" ? "已暂停" : "持续对话") : "新会话";
  const exchanges = room?.message_count || state.messages.length;
  byId("message-count").textContent = `${exchanges} ${exchanges === 1 ? "EXCHANGE" : "EXCHANGES"}`;
  const paused = room?.state === "paused";
  const busy = paused || state.sending || Boolean(state.runID);
  byId("message-input").disabled = busy;
  byId("symbol-input").disabled = busy;
  byId("send-message").disabled = busy;
  byId("message-input").placeholder = paused
    ? "恢复 Room 后可以继续对话"
    : (state.runID ? "等待当前回答完成…" : "问 Cortex 一个问题…");
  byId("composer-note").textContent = paused
    ? "Room 已暂停 · 恢复后可继续"
    : "只读研究模式 · Agent 不能创建或修改订单";
}

function messageNode(role,text,createdAt,pending = false) {
  const article = element("article",`message ${role}${pending ? " pending" : ""}`);
  article.append(element("span","message-avatar",role === "user" ? "YOU" : "A"));
  const copy = element("div","message-copy");
  const label = element("div","message-label");
  label.append(element("strong","",role === "user" ? "YOU" : "CORTEX"));
  if (createdAt) {
    const time = element("time","",formatWhen(createdAt));
    time.dateTime = createdAt;
    label.append(time);
  }
  copy.append(label);
  copy.append(element("p","",text || (pending ? "正在形成回答…" : "没有返回文本。")));
  article.append(copy);
  return article;
}

function renderMessages(pendingUserText = "") {
  const nodes = [];
  for (const entry of state.messages) {
    if (entry.user_text) nodes.push(messageNode("user",entry.user_text,entry.created_at));
    if (entry.assistant_text) nodes.push(messageNode("assistant",entry.assistant_text,entry.created_at));
  }
  if (pendingUserText) nodes.push(messageNode("user",pendingUserText,new Date().toISOString(),true));
  byId("message-list").replaceChildren(...nodes);
  byId("new-room-view").hidden = Boolean(state.room) || nodes.length > 0;
  byId("conversation-view").hidden = !state.room && nodes.length === 0;
  if (nodes.length) requestAnimationFrame(() => nodes[nodes.length-1].scrollIntoView({block:"end",behavior:"smooth"}));
  updateRoomHeader();
}

const stageLabels = {
  user_request_admitted:"请求已进入 Cortex",
  intent_interpreter_completed:"意图解析完成",
  scout_task_admitted:"Scout 任务已建立",
  scout_research_completed:"开放研究完成",
  desk_continuation_ready:"研究结果已交回 Decision Desk",
  decision_desk_completed:"Decision Desk 已形成回答",
  tool_call_authorized:"工具调用已授权",
  tool_receipt_succeeded:"工具返回已验证",
  tool_branch_failed:"工具分支失败",
  cortex_attempt_failed:"Cortex 执行失败",
  task_graph_branch_failed:"并行 Agent 分支失败",
  task_graph_round_started:"并行 Agent 轮次启动",
  task_graph_join_completed:"并行结果已汇合",
};

const failureLabels = {
  kernel_tool_action_invalid:"工具动作不符合已冻结契约",
  kernel_tool_identity_invalid:"工具身份与已授权工具不一致",
  kernel_tool_arguments_json_invalid:"工具参数不是有效 JSON",
  kernel_tool_arguments_invalid:"工具参数未通过校验",
  kernel_tool_argument_unknown:"工具包含未注册参数",
  kernel_tool_argument_required:"工具缺少必填参数",
  kernel_tool_argument_shape_invalid:"工具参数结构不正确",
  kernel_tool_asset_type_invalid:"资产类型无效；股票与 ETF 应使用 instrument",
  kernel_tool_interval_invalid:"历史行情间隔无效；应使用 hour 或 day 等正式值",
  kernel_tool_start_time_invalid:"历史行情开始时间不是 RFC3339 UTC",
  kernel_tool_end_time_invalid:"历史行情结束时间无效或早于开始时间",
  kernel_tool_bounds_invalid:"行情时段参数不受支持",
  kernel_tool_adjustment_type_invalid:"复权参数不受支持",
  kernel_tool_adjustment_interval_invalid:"all 复权只能用于日内间隔",
  kernel_tool_symbols_invalid:"股票代码列表无效",
  kernel_tool_query_invalid:"搜索关键词无效",
  kernel_tool_limit_invalid:"搜索数量必须是 1–20 的整数",
  task_graph_tool_failed:"已授权工具执行失败或数据提供方拒绝请求",
  task_graph_admission_failed:"并行 Agent 任务未能建立",
  task_graph_join_failed:"并行 Agent 结果未达到汇合条件",
  runtime_deadline_expired:"运行超过了冻结的截止时间",
};

function humanStage(event) {
  if (event.error_code) {
    return failureLabels[event.error_code] || `Cortex 失败：${event.error_code}`;
  }
  if (stageLabels[event.stage]) return stageLabels[event.stage];
  if (event.stage?.startsWith("handoff_to_")) {
    return `交接给 ${event.stage.slice(11).replaceAll("_"," ")}`;
  }
  if (event.stage?.endsWith("_completed")) {
    return `${event.stage.slice(0,-10).replaceAll("_"," ")} 完成`;
  }
  return (event.stage || "Cortex 状态更新").replaceAll("_"," ");
}

function eventKind(event) {
  if (event.error_code || event.stage?.endsWith("_failed")) return "failure";
  if (event.tool_id || event.stage?.startsWith("tool_")) return "tool";
  if (event.stage?.startsWith("handoff_") || event.target_role) return "agent";
  if (event.stage?.endsWith("_completed")) return "done";
  return "";
}

function renderActivity(runState = "") {
  const terminal = ["succeeded","failed","canceled"].includes(runState);
  byId("activity-state").textContent = runState ? runState.toUpperCase() : "IDLE";
  const events = state.trace.map((event) => {
    const row = element("div",`activity-event ${eventKind(event)}`);
    row.append(element("i"));
    const copy = element("div");
    copy.append(element("strong","",humanStage(event)));
    const facts = [];
    if (event.target_role) facts.push(event.target_role.replaceAll("_"," "));
    if (event.role_id) facts.push(event.role_id.replaceAll("_"," "));
    if (event.tool_id) facts.push(event.tool_id);
    if (event.error_code) facts.push(event.error_code);
    if (event.retryable === false) facts.push("已停止自动重试");
    if (event.state) facts.push(event.state);
    if (!facts.length && (event.at || event.created_at)) facts.push(formatWhen(event.at || event.created_at));
    copy.append(element("span","",facts.join(" · ") || "已持久化"));
    row.append(copy);
    return row;
  });
  if (!events.length) {
    const empty = element("div","activity-empty");
    empty.append(element("i"));
    empty.append(element("p","",state.runID ? "Cortex 已接受请求，等待第一条活动记录。" : "发送问题后，这里会显示 Cortex 实际完成的意图解析、Agent 交接和工具调用。"));
    events.push(empty);
  }
  byId("activity-timeline").replaceChildren(...events);
  const failed = runState === "failed";
  const banner = byId("run-banner");
  banner.classList.toggle("failed",failed);
  banner.hidden = (!state.runID && !failed) || (terminal && !failed);
  byId("cancel-run").hidden = terminal;
  if (failed) {
    const failure = [...state.trace].reverse().find((event) => event.error_code);
    byId("run-status").textContent = "本次运行未完成";
    byId("run-stage").textContent = failure ?
      humanStage(failure) : "Cortex 已安全停止，但没有可公开的详细原因。";
  } else if (state.runID && !terminal) {
    const last = state.trace[state.trace.length-1];
    byId("run-stage").textContent = last ? humanStage(last) : "等待执行记录…";
    byId("run-status").textContent = runState === "canceling" ? "正在停止运行" : "Cortex 正在工作";
  }
}

async function loadRooms() {
  const payload = await request("/agent/rooms");
  state.rooms = payload.rooms || [];
  renderRooms();
}

async function selectRoom(id) {
  state.pollToken += 1;
  state.runID = null;
  state.trace = [];
  renderActivity();
  closeMobilePanels();
  try {
    const payload = await request(`/agent/rooms/${encodeURIComponent(id)}`);
    state.room = payload.room;
    state.messages = payload.messages || [];
    setURLRoom(id);
    renderRooms();
    renderMessages();
    if (state.room.last_run_id) {
      const lastRun = await request(`/agent/cortex-runs/${encodeURIComponent(state.room.last_run_id)}`);
      state.trace = lastRun.trace || [];
      if (roomIsRunning(state.room)) state.runID = state.room.last_run_id;
      renderActivity(lastRun.status);
      if (state.runID) pollRun(state.runID);
    }
  } catch (error) {
    showPageError(error);
  }
}

function newRoom() {
  state.pollToken += 1;
  state.room = null;
  state.messages = [];
  state.runID = null;
  state.trace = [];
  setURLRoom("");
  renderRooms();
  renderMessages();
  renderActivity();
  closeMobilePanels();
  byId("message-input").focus();
}

function showPageError(error) {
  byId("page-error").textContent = formatError(error);
}

function clearPageError() {
  byId("page-error").textContent = "";
}

async function submitMessage(event) {
  event.preventDefault();
  if (state.sending || state.room?.state === "paused") return;
  const query = byId("message-input").value.trim();
  const symbol = byId("symbol-input").value.trim().toUpperCase();
  if (!query) return;
  state.sending = true;
  clearPageError();
  updateRoomHeader();
  renderMessages(query);
  try {
    const body = {
      mode: state.room?.mode || state.mode,
      symbol,
      query,
    };
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
    state.trace = [];
    state.messages.push({
      user_text:query,
      created_at:new Date().toISOString(),
      run_id:accepted.id,
    });
    byId("message-input").value = "";
    autoSizeComposer();
    setURLRoom(state.room.conversation_id);
    await loadRooms();
    renderMessages();
    renderActivity("running");
    pollRun(accepted.id);
  } catch (error) {
    renderMessages();
    showPageError(error);
  } finally {
    state.sending = false;
    updateRoomHeader();
  }
}

async function pollRun(runID) {
  const token = ++state.pollToken;
  for (let attempt = 0; attempt < 540; attempt += 1) {
    if (token !== state.pollToken || state.runID !== runID) return;
    try {
      const run = await request(`/agent/cortex-runs/${encodeURIComponent(runID)}`);
      state.trace = run.trace || [];
      renderActivity(run.status);
      if (run.status === "succeeded" || run.status === "failed" || run.status === "canceled") {
        state.runID = null;
        if (state.room) await selectRoom(state.room.conversation_id);
        await loadRooms();
        return;
      }
    } catch (error) {
      showPageError(error);
    }
    await new Promise((resolve) => setTimeout(resolve,1000));
  }
  showPageError({message:"运行时间较长，可稍后从会话列表继续查看。"});
}

async function cancelRun() {
  if (!state.runID) return;
  byId("cancel-run").disabled = true;
  const id = crypto.randomUUID();
  try {
    await request(`/agent/cortex-runs/${encodeURIComponent(state.runID)}/cancel`,{
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({request_id:id,idempotency_key:`agent-room-cancel-${id}`}),
    });
  } catch (error) {
    showPageError(error);
  } finally {
    byId("cancel-run").disabled = false;
  }
}

async function togglePause() {
  if (!state.room) return;
  const nextState = state.room.state === "paused" ? "active" : "paused";
  try {
    const result = await request(`/agent/rooms/${encodeURIComponent(state.room.conversation_id)}`,{
      method:"PATCH",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({
        expected_generation:state.room.generation,
        mode:state.room.mode,
        title:state.room.title,
        state:nextState,
      }),
    });
    state.room = result.room;
    await loadRooms();
    renderMessages();
  } catch (error) {
    showPageError(error);
    if (error.status === 409) await selectRoom(state.room.conversation_id);
  }
}

async function refreshSystemHealth() {
  const indicator = byId("system-indicator");
  try {
    const health = await request("/agent/cortex-operations");
    indicator.className = `system-indicator ${health.status}`;
    indicator.lastChild.textContent = health.status === "healthy" ? "系统正常" : "部分降级";
  } catch {
    indicator.className = "system-indicator degraded";
    indicator.lastChild.textContent = "系统不可用";
  }
}

function autoSizeComposer() {
  const input = byId("message-input");
  input.style.height = "auto";
  input.style.height = `${Math.min(input.scrollHeight,180)}px`;
}

function openMobilePanel(panel) {
  byId(panel).classList.add("open");
  byId("mobile-scrim").hidden = false;
}

function closeMobilePanels() {
  byId("room-rail").classList.remove("open");
  byId("activity-rail").classList.remove("open");
  byId("mobile-scrim").hidden = true;
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
    showPageError(error);
    return;
  }
  byId("login-screen").hidden = true;
  await Promise.all([loadRooms(),refreshSystemHealth()]);
  const requested = new URL(window.location.href).searchParams.get("room");
  const initial = state.rooms.find((room) => room.conversation_id === requested);
  if (initial) await selectRoom(initial.conversation_id);
  else newRoom();
}

byId("composer").addEventListener("submit",submitMessage);
byId("message-input").addEventListener("input",autoSizeComposer);
byId("message-input").addEventListener("keydown",(event) => {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    byId("composer").requestSubmit();
  }
});
byId("symbol-input").addEventListener("input",(event) => {
  event.target.value = event.target.value.toUpperCase().replace(/[^A-Z0-9.^_-]/g,"");
});
byId("new-room").addEventListener("click",newRoom);
byId("room-filter").addEventListener("input",renderRooms);
byId("pause-room").addEventListener("click",togglePause);
byId("cancel-run").addEventListener("click",cancelRun);
byId("open-rooms").addEventListener("click",() => openMobilePanel("room-rail"));
byId("open-activity").addEventListener("click",() => openMobilePanel("activity-rail"));
byId("close-activity").addEventListener("click",closeMobilePanels);
byId("mobile-scrim").addEventListener("click",closeMobilePanels);
byId("starter-list").addEventListener("click",(event) => {
  if (event.target.tagName !== "BUTTON") return;
  byId("message-input").value = event.target.textContent;
  autoSizeComposer();
  byId("message-input").focus();
});
byId("login-form").addEventListener("submit",async (event) => {
  event.preventDefault();
  byId("login-error").textContent = "";
  try {
    await request("/agent/auth/login",{
      method:"POST",
      headers:{"Content-Type":"application/json"},
      body:JSON.stringify({password:byId("login-password").value}),
    });
    byId("login-password").value = "";
    await restore();
  } catch (error) {
    byId("login-error").textContent = formatError(error);
  }
});

restore();
