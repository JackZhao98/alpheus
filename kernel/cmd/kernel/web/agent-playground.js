const byId=(id)=>document.getElementById(id);
const state={replay:null,timer:null,playing:false,detectors:[],session:null,events:[]};

async function request(path,options={}){
  const response=await fetch(path,{...options,cache:"no-store"});
  const payload=await response.json().catch(()=>null);
  if(!response.ok){const error=new Error(payload?.error||`HTTP ${response.status}`);error.code=payload?.error_code||`http_${response.status}`;error.status=response.status;throw error}
  return payload;
}
function text(id,value){byId(id).textContent=value}
function money(value){const number=Number(value);return Number.isFinite(number)?new Intl.NumberFormat("en-US",{style:"currency",currency:"USD"}).format(number):"—"}
function when(value){const date=new Date(value);return Number.isNaN(date.getTime())?"—":date.toLocaleString("zh-CN",{month:"numeric",day:"numeric",hour:"2-digit",minute:"2-digit",second:"2-digit"})}
function number(value){const parsed=Number(value);return Number.isFinite(parsed)?new Intl.NumberFormat("en-US",{maximumFractionDigits:2}).format(parsed):"—"}
function localValue(date){const shifted=new Date(date.getTime()-date.getTimezoneOffset()*60000);return shifted.toISOString().slice(0,19)}
function initializeRange(){const end=new Date();const start=new Date(end.getTime()-24*60*60*1000);byId("replay-start").value=localValue(start);byId("replay-end").value=localValue(end)}
function showError(error){const known={strategy_playground_paper_only:"回放只允许在 Paper Strategy Playground 中运行。",moody_blues_replay_unavailable:"Moody Blues 历史数据暂时不可用。",agent_intraday_session_unavailable:"实验无法永久保存。",moody_blues_generation_conflict:"另一个播放器已经推进了这个实验，请刷新。"};text("playground-error",known[error?.code]||error?.message||"请求失败")}
function clearError(){text("playground-error","")}

async function loadDetectors(){
  const payload=await request("/agent/console/triggers");
  state.detectors=(payload.items||[]).filter(item=>item.data_source==="moody_blues_replay");
  text("detector-count",`${state.detectors.length} INSTALLED`);
  const list=byId("detector-list");list.replaceChildren();
  if(!state.detectors.length){const empty=document.createElement("div");empty.className="empty-module";empty.innerHTML="<strong>没有 Replay Detector</strong><p>可以先在 Console 注册数学检测器；未选择时仍会评估所有已启用的兼容检测器。</p>";list.append(empty);return}
  for(const detector of state.detectors){
    const label=document.createElement("label");label.className="detector-option";
    const input=document.createElement("input");input.type="checkbox";input.value=detector.trigger_id;input.checked=detector.enabled;
    const copy=document.createElement("div");const title=document.createElement("strong");title.textContent=detector.title||detector.strategy_id;const detail=document.createElement("small");detail.textContent=`${detector.metric} ${detector.comparator} ${detector.threshold} · GEN ${detector.generation}`;copy.append(title,detail);
    const status=document.createElement("em");status.textContent=detector.enabled?"ENABLED":"PAUSED";if(!detector.enabled){input.disabled=true}
    label.append(input,copy,status);list.append(label);
  }
}
function selectedDetectorIDs(){return [...document.querySelectorAll("#detector-list input:checked")].map(input=>input.value)}
function sessionNode(id,stateName,title,detail){const node=byId(id);node.className=`session-node ${stateName||""}`;node.querySelector("strong").textContent=title;node.querySelector("small").textContent=detail}
function renderSessionFlow(payload){
  const evaluations=payload?.trigger_evaluations||[];const wake=evaluations.find(item=>item?.wake?.run_id);
  sessionNode("session-data",payload?.observation?"active":"",payload?.observation?`FRAME ${payload.generation}`:"等待帧",payload?.observation?when(payload.observation.source_timestamp):"Moody Blues");
  sessionNode("session-trigger",wake?"active":evaluations.length?"waiting":"",wake?"SIGNAL → WAKE":evaluations.length?"已评估，未命中":"等待帧",wake?String(wake.wake.run_id).slice(0,8):`${evaluations.length} evaluations`);
  sessionNode("session-cortex",wake?"waiting":"",wake?"RUNNING":"休眠",wake?`RUN ${String(wake.wake.run_id).slice(0,8)}`:"Multi-Agent");
}
function renderReplay(payload){
  state.replay={replay_id:payload.replay_id,generation:payload.generation,state:payload.state};
  byId("replay-next").disabled=payload.state!=="active"||state.playing;byId("replay-play").disabled=payload.state!=="active";
  text("replay-state",`${String(payload.state).toUpperCase()} · GEN ${payload.generation}`);
  const observation=payload.observation;if(observation){const metrics=observation.metrics||{};text("replay-clock",when(observation.source_timestamp||observation.available_at));text("replay-spot",number(metrics.spot));text("replay-zero",number(metrics.zero_gamma));text("replay-call",number(metrics.major_pos_oi));text("replay-put",number(metrics.major_neg_oi))}
  const evaluations=payload.trigger_evaluations||[];const wake=evaluations.find(item=>item?.wake?.run_id);text("replay-trigger-state",wake?`WAKE ${String(wake.wake.run_id).slice(0,8)}`:evaluations.length?`${evaluations.length} EVALUATED`:"NO SIGNAL");
  if(payload.state==="complete")pause();renderSessionFlow(payload);
}
async function loadSessions(){
  const payload=await request("/agent/console/sessions?environment=paper");state.session=payload.items?.[0]||null;state.events=payload.events||[];
  if(!state.session)return;
  text("session-title",`${state.session.symbol} · ${String(state.session.category).replace("gex_","").toUpperCase()} · ${String(state.session.state).toUpperCase()}`);
  text("account-id",`ACCOUNT ${String(state.session.paper_account_id||"").replace("playground-","").slice(0,8)}`);
  text("starting-cash",money(state.session.initial_cash));text("portfolio-value",money(state.session.initial_cash));text("event-count",`${state.events.length} EVENTS`);
  const list=byId("event-list");list.replaceChildren();
  for(const event of state.events.slice(0,30)){const row=document.createElement("div");row.className="evidence-row";const kind=document.createElement("span");kind.textContent=String(event.kind).toUpperCase();const detail=document.createElement("strong");detail.textContent=event.run_id?`RUN ${String(event.run_id).slice(0,8)}`:`GEN ${event.replay_generation}`;const at=document.createElement("time");at.textContent=when(event.occurred_at);row.append(kind,detail,at);list.append(row)}
}
async function createReplay(){
  clearError();pause();const start=new Date(byId("replay-start").value);const end=new Date(byId("replay-end").value);const cash=Number(byId("initial-cash").value);
  if(Number.isNaN(start.getTime())||Number.isNaN(end.getTime())||end<start||end>new Date()){showError(new Error("请选择一个已经发生的有效时间段。"));return}
  if(!Number.isFinite(cash)||cash<1000||cash>10000000){showError(new Error("初始资金必须在 $1,000 到 $10,000,000 之间。"));return}
  const button=byId("replay-create");button.disabled=true;
  try{const requestID=globalThis.crypto?.randomUUID?`playground-${crypto.randomUUID()}`:`playground-${Date.now()}`;const payload=await request("/agent/console/data-streams/gexbot/replays",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({request_id:requestID,environment:"paper",symbol:"SPX",category:byId("replay-category").value,start_available_at:start.toISOString(),end_available_at:end.toISOString(),as_of:new Date().toISOString(),initial_cash:cash,detector_ids:selectedDetectorIDs()})});renderReplay(payload);await loadSessions()}catch(error){showError(error)}finally{button.disabled=false}
}
async function advance(){
  if(!state.replay?.replay_id||state.replay.state!=="active")return false;clearError();byId("replay-next").disabled=true;
  try{const payload=await request(`/agent/console/data-streams/gexbot/replays/${encodeURIComponent(state.replay.replay_id)}/next`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({generation:state.replay.generation})});renderReplay(payload);await loadSessions();return true}catch(error){showError(error);pause();return false}finally{byId("replay-next").disabled=state.replay?.state!=="active"||state.playing}
}
function pause(){if(state.timer)clearTimeout(state.timer);state.timer=null;state.playing=false;const button=byId("replay-play");button.textContent="自动播放";button.disabled=state.replay?.state!=="active";byId("replay-next").disabled=state.replay?.state!=="active"}
async function autoplay(){if(!state.playing)return;const moved=await advance();if(!moved||!state.playing||state.replay?.state!=="active"){pause();return}state.timer=setTimeout(autoplay,Number(byId("replay-speed").value)||3000)}
function togglePlay(){if(state.playing){pause();return}if(state.replay?.state!=="active")return;state.playing=true;byId("replay-next").disabled=true;byId("replay-play").textContent="暂停";autoplay()}
async function restore(){
  try{await request("/agent/auth/session")}catch(error){if(error.status===401){byId("login-screen").hidden=false;return}showError(error);return}
  byId("login-screen").hidden=true;const results=await Promise.allSettled([loadDetectors(),loadSessions(),request("/agent/cortex-operations")]);const health=results[2];byId("system-dot").className=`system-dot ${health.status==="fulfilled"&&health.value?.status==="healthy"?"healthy":"degraded"}`;const failed=results.find(result=>result.status==="rejected");if(failed)showError(failed.reason)
}
byId("replay-create").addEventListener("click",createReplay);byId("replay-next").addEventListener("click",advance);byId("replay-play").addEventListener("click",togglePlay);
byId("login-form").addEventListener("submit",async event=>{event.preventDefault();text("login-error","");try{await request("/agent/auth/login",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({password:byId("login-password").value})});byId("login-password").value="";await restore()}catch(error){text("login-error",error.message||"登录失败")}});
initializeRange();restore();
