'use strict';

// The native bridge owns credentials, proxy restoration and transport state.
// This view keeps independent operation locks so a slow request cannot block cancel/status.
const $ = id => document.getElementById(id);
const app = () => {
  const bridge = window.go?.main?.App;
  if (!bridge || typeof bridge.Status !== 'function') throw Error('客户端正在初始化，请稍候；若仍未恢复，请退出并重新打开客户端。');
  return bridge;
};
// A missing native callback must not leave bootstrap locked forever. This is
// only for reads: a timed-out purchase/login must never be blindly replayed.
function nativeRead(invoke) {
  return new Promise((resolve,reject) => {
    const timer = setTimeout(() => reject(Error('客户端读取服务信息未响应，请点击重新加载；若持续失败，请退出并重新打开客户端。')),20000);
    Promise.resolve().then(invoke).then(resolve,reject).finally(() => clearTimeout(timer));
  });
}
const readQuery = path => nativeRead(() => app().Query(path,null));
const locks = new Set(), loading = new Set(), failures = {}, probes = new Map();
let status = {}, profile = null, nodeList = [], planList = [], orderList = [];
let publicSettings = null, commerce = null, selected = '', view = 'dashboard', guestSettings = false;
let authMode = 'login', epoch = 0, prefsReady = false, statusFlight = null, dataFlight = null, publicFlight = null;
let localConnecting = false, connectGeneration = 0, connectionAttempt = null, purchasePlan = null, purchaseBusy = false, purchaseTestMode = false;
const uncertainOrders = new Set();
let releaseGeneration = 0, lastFocusRefresh = 0, recoveryNotice = '';
let publicLoaded = false, workspaceLoaded = false, nextDataRetry = 0, retryDelay = 3000;
let apiDraftDirty = false;
let routingDraftDirty = false;
let nextWorkspaceRefresh = 0;
let trafficSamples = [], trafficKey = '', trafficSecond = -1;
const stageNames = {reconnecting:'网络已变化，正在自动恢复连接',restoring_proxy:'正在恢复上次的代理设置',fetching_profile:'正在获取线路信息',verifying_tls:'正在验证安全连接',authenticating:'正在验证账号',checking_tun_network:'正在检查节点 IPv6 出口',starting_tun:'正在配置虚拟网卡与路由',starting_proxy:'正在启动本机连接',connected:'已连接',degraded:'网络检查异常',cancelling:'正在取消连接',disconnected:'未连接'};
const pageInfo = {dashboard:['','连接','选择节点并管理当前连接。'],nodes:['','线路','选择线路 · 右键操作 · 按方向键移动'],subscription:['','账户与订阅','管理套餐、用量和订单。'],settings:['','设置','外观、连接偏好与诊断。']};

function element(tag, value, className) {
  const node = document.createElement(tag);
  if (value !== undefined) node.textContent = String(value);
  if (className) node.className = className;
  return node;
}
function icon(name) {
  const node = document.createElementNS('http://www.w3.org/2000/svg','svg');
  node.setAttribute('class','icon'); node.setAttribute('viewBox','0 0 24 24'); node.setAttribute('aria-hidden','true'); node.setAttribute('focusable','false');
  const use = document.createElementNS('http://www.w3.org/2000/svg','use'); use.setAttribute('href','#i-'+name); node.append(use); return node;
}
// Country/region flags are bundled SVGs, independent of OS emoji fonts or CDNs.
const flagCodes = new Set('AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'.split(' '));
const flagNames = new Map(), flagAliases = new Map([['英国','GB'],['韩国','KR'],['香港','HK'],['澳门','MO'],['台湾','TW']]);
for (const locale of ['en','zh-CN']) {
  try {
    const display = new Intl.DisplayNames([locale],{type:'region'});
    for (const code of flagCodes) {
      const name = display.of(code); flagAliases.set(name.toLowerCase(),code);
      if (locale === 'zh-CN') flagNames.set(code,name);
    }
  } catch (_) { /* Code labels remain available on older runtimes. */ }
}
function flagCodeIn(value) {
  const text = String(value || '').trim();
  if (/[<>]|:\/\/|\.\.[\\/]/.test(text)) return '';
  const exact = text.toUpperCase() === 'UK' ? 'GB' : text.toUpperCase();
  if (flagCodes.has(exact)) return exact;
  const alias = flagAliases.get(text.toLowerCase()); if (alias) return alias;
  // Token boundaries prevent US matching RUSSIA, AU matching AUTO, etc.
  const matches = [...text.matchAll(/(?:^|[\s\[\](){}|/_,，·—\-\u3400-\u9fff])([a-z]{2})(?=$|[\s\[\](){}|/_,，·—\-0-9\u3400-\u9fff])/gi)];
  const codes = [...new Set(matches.map(m => m[1].toUpperCase() === 'UK' ? 'GB' : m[1].toUpperCase()).filter(code => flagCodes.has(code)))];
  return codes.length === 1 ? codes[0] : '';
}
function nodeFlagCode(node) { return node ? flagCodeIn(node.region) || flagCodeIn(node.name) : ''; }
function renderNodeSymbol(symbol,node) {
  const code = nodeFlagCode(node);
  if (symbol.dataset.flagCode === code && symbol.hasChildNodes()) return;
  symbol.dataset.flagCode=code;
  symbol.replaceChildren(); symbol.classList.toggle('has-flag',!!code);
  symbol.title = code ? (flagNames.get(code) || code)+' · '+code : '未识别国家或地区';
  if (!code) { symbol.append(icon('globe')); return; }
  const image = document.createElement('img');
  image.className='node-flag'; image.width=28; image.height=21;
  image.alt=(flagNames.get(code) || code)+'旗帜'; image.dataset.countryCode=code;
  image.addEventListener('error',() => { if (image.parentNode !== symbol) return; symbol.classList.remove('has-flag'); symbol.replaceChildren(icon('globe')); },{once:true});
  image.src='flags/'+code.toLowerCase()+'.svg'; symbol.append(image);
}
function bytes(value) {
  let n = Math.max(0, Number(value) || 0), i = 0; const units = ['B','KiB','MiB','GiB','TiB'];
  while (n >= 1024 && i < units.length-1) { n /= 1024; i++; }
  return n.toFixed(i ? 1 : 0)+' '+units[i];
}
const date = n => Number(n) > 0 ? new Date(Number(n)*1000).toLocaleString('zh-CN',{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}) : '—';
const amount = p => (Number(p.price_cents || 0)/100).toFixed(2)+' '+String(p.currency || 'CNY').toUpperCase();
const period = p => p.kind === 'traffic' ? '当前有效周期' : Number(p.days || 0)+' 天';
const isConnected = () => !!status.connected || ['connected','degraded'].includes(status.connection_state);
const isConnecting = () => localConnecting || status.connection_state === 'connecting';
const nodeAllowed = node => typeof node?.access?.allowed === 'boolean' ? node.access.allowed : !!profile?.user?.active && !profile?.user?.verification_required;
const planScope = plan => plan.node_ids?.length ? '指定 '+plan.node_ids.length+' 条线路：'+plan.node_ids.map(id => nodeList.find(n => n.id === id)?.name || id).join('、') : '全部符合套餐类型的线路';
function message(value, kind = 'error') {
  $('message').textContent = value ? String(value) : ''; $('message').hidden = !value; $('message').className = 'notice '+kind;
}
function friendly(error, context) {
  const raw = String(error?.message || error || '');
  const code = raw.match(/HTTP\s+(\d{3})/i)?.[1];
  if (context === 'auth' && ['400','401','403'].includes(code)) return '登录未完成，请检查邮箱、密码和验证码；已开启二次验证的账号还需要填写验证码或恢复码。';
  if (code === '401') return '登录已过期，请重新登录后继续。';
  if (code === '403') return '当前账号暂时没有此操作权限，请到网站检查邮箱验证和账户状态。';
  if (code === '429') return '操作过于频繁，请稍后重试。';
  if (code === '400') return '提交的信息未被接受，请检查填写内容后重试。';
  if (['500','502','503','504'].includes(code)) return '服务暂时不可用，请稍后重试。你的账号仍会保留在本机。';
  if (/timeout|deadline|fetch|network|dial |connection refused|no such host|unreachable/i.test(raw)) return '暂时无法连接服务，请检查网络或服务地址后重试。';
  if (/[\u3400-\u9fff]/.test(raw)) return raw;
  return '操作未完成，请重试；若仍失败，可在设置中导出诊断交给客服。';
}
function setLocks() {
  document.querySelectorAll('[data-lock]').forEach(b => { const busy = locks.has(b.dataset.lock); b.disabled = busy; b.setAttribute('aria-busy',String(busy)); });
  $('quit').disabled = locks.has('quit'); $('hide').disabled = locks.has('hide');
  $('save-api').disabled = ['save-api','auth','logout'].some(key => locks.has(key)) || isConnecting() || isConnected();
  $('api-url').disabled = locks.has('save-api');
  $('register').disabled = locks.has('auth'); $('back-to-login').disabled = locks.has('auth');
  $('system-proxy').disabled = locks.has('preferences') || locks.has('mode') || status.preferences?.proxy_mode === 'tun';
  renderProxyMode();
  $('auth-retry').disabled = !!publicFlight || locks.has('refresh');
  renderConnection();
}
async function operation(key, fn) {
  if (locks.has(key)) return;
  locks.add(key); message(''); setLocks();
  try { await fn(); }
  catch (error) { message(friendly(error,key)); await checkUnauthorized(error,key); }
  finally { locks.delete(key); setLocks(); if (key === 'preferences') renderNodes(); await refreshStatus().catch(() => {}); }
}
async function checkUnauthorized(error, context) {
  if (context === 'auth' || !/HTTP\s+401/i.test(String(error?.message || error))) return;
  // The engine clears expired credentials. Read the authoritative state immediately.
  await refreshStatus().catch(() => {});
}
function empty(target, title, description, retry, isLoading = false) {
  const box = element('div',undefined,'empty-state'+(isLoading ? ' loading-state' : ''));
  if (isLoading) { const skeleton = element('div',undefined,'loading-skeleton'); skeleton.setAttribute('aria-hidden','true'); skeleton.append(element('span'),element('span'),element('span')); box.append(skeleton); }
  if (!isLoading) box.append(icon('globe'));
  box.append(element('strong',title),element('p',description));
  if (retry) { const b = element('button','重新加载','subtle'); b.onclick = () => operation('refresh',refreshAll); box.append(b); }
  $(target).replaceChildren(box);
}
function showView(next, focus = false) {
  if (!pageInfo[next]) return;
  if (!status.logged_in && next !== 'settings') return;
  if (view !== next) closeNodeMenu();
  view = next;
  document.querySelectorAll('[data-view]').forEach(b => {
    b.classList.toggle('selected',b.dataset.view === next);
    if (b.dataset.view === next) b.setAttribute('aria-current','page'); else b.removeAttribute('aria-current');
    b.disabled = !status.logged_in && b.dataset.view !== 'settings';
  });
  for (const name of Object.keys(pageInfo)) $('page-'+name).hidden = name !== next;
  const [kicker,title,description] = pageInfo[next];
  $('page-kicker').textContent = kicker; $('page-title').textContent = title; $('page-description').textContent = description;
  if (focus) { $('main-content').scrollTop = 0; $('main-content').focus({preventScroll:true}); }
}
function renderShell() {
  const workspace = status.logged_in || guestSettings;
  $('auth').hidden = workspace; $('account').hidden = !workspace; $('sidebar').hidden = !workspace;
  $('refresh').hidden = !status.logged_in; $('guest-back').hidden = status.logged_in || !guestSettings;
  $('header-account').hidden = !status.logged_in; $('header-account').textContent = status.logged_in ? (status.email || '') : '';
  $('version').textContent = status.version || 'v0.6.16'; $('settings-version').textContent = status.version || 'v0.6.16';
  if (!apiDraftDirty) $('api-url').value = status.api_url || '';
  $('startup-warning').textContent = status.startup_warning || ''; $('startup-warning').hidden = !status.startup_warning;
  showView(status.logged_in ? view : 'settings');
}
function clearPrivate() {
  closeNodeMenu();
  resetTraffic();
  workspaceLoaded = false; nextDataRetry = 0;
  epoch++; dataFlight = null; nodeList = []; planList = []; orderList = []; profile = null; selected = ''; probes.clear(); prefsReady = false;
  for (const key of ['me','nodes','plans','orders']) { delete failures[key]; loading.delete(key); }
  for (const id of ['nodes','plans','orders','profile','profile-email','announcement']) $(id).replaceChildren();
  for (const id of ['profile-remaining','profile-expiry','profile-devices','summary-remaining','summary-expiry']) $(id).textContent = '—';
  $('summary-state').textContent = '登录后查看订阅'; $('sidebar-plan-text').textContent = '登录后查看订阅';
  $('announcement-card').hidden = true; $('trial').hidden = true; $('account-alert').hidden = true;
  $('nav-node-count').textContent = ''; $('quota-progress').hidden = true;
  $('password').value = ''; $('otp').value = ''; $('invite').value = ''; $('node-search').value = ''; $('node-online').value = 'all';
  $('node-region').replaceChildren(new Option('全部地区','all'));
  closePurchase(true); uncertainOrders.clear(); connectGeneration++; connectionAttempt = null; localConnecting = false;
  renderLoadAlert(); renderConnection();
}
function renderAuth() {
  const registerAllowed = publicSettings?.registration === true && !!commerce;
  if (!registerAllowed && authMode === 'register') authMode = 'login';
  const signup = authMode === 'register';
  $('auth-title').textContent = signup ? '创建账号' : '登录 tunnelX';
  $('auth-description').textContent = signup ? '创建账号后，选择适合你的套餐。' : '登录你的账号，继续上次的连接。';
  $('login-submit-text').textContent = signup ? '创建账号' : '登录';
  $('password').autocomplete = signup ? 'new-password' : 'current-password';
  $('password').minLength = signup ? 12 : 1;
  $('password').placeholder = signup ? '至少 12 位，建议使用独立密码' : '请输入密码';
  $('register').hidden = signup || !registerAllowed; $('back-to-login').hidden = !signup;
  $('auth-switch-label').textContent = signup ? '已有账号？' : (registerAllowed ? '还没有账号？' : (publicSettings && commerce ? '当前暂不开放注册' : '服务信息尚未加载'));
  $('forgot').hidden = signup; $('show-otp').hidden = signup || !$('otp-wrap').hidden;
  if (signup) $('otp-wrap').hidden = true;
  $('invite-wrap').hidden = !signup || commerce?.invite_only !== true;
  $('invite').required = signup && commerce?.invite_only === true;
  const serviceFailed = failures.publicSettings || failures.commerce;
  $('auth-service').textContent = publicLoaded ? '账号由当前服务商管理' : (serviceFailed ? '服务信息读取失败，将自动重试。' : '正在读取服务信息…');
  $('auth-retry').hidden = publicLoaded;
  $('auth-retry').disabled = !!publicFlight || locks.has('refresh');
  $('auth-retry').textContent = publicFlight ? '正在加载…' : '重新加载';
}
function renderLoadAlert() {
  const names = {publicSettings:'注册设置',commerce:'服务信息',me:'账户',nodes:'线路',plans:'套餐',orders:'订单',status:'本机状态'};
  const keys = Object.keys(failures);
  $('load-alert').hidden = !keys.length;
  $('load-error').textContent = keys.length ? keys.map(k => names[k] || k).join('、')+'未能刷新。'+friendly(failures[keys[0]]) : '';
}
function applyPreferences() {
  if (prefsReady || locks.has('preferences')) return;
  selected = status.preferences?.selected_node_id || '';
  $('system-proxy').checked = status.preferences?.system_proxy !== false;
  if (!routingDraftDirty) $('direct-domains').value = (status.preferences?.direct_domains || []).join('\n');
  prefsReady = true;
}
async function refreshStatus() {
  if (statusFlight) return statusFlight;
  statusFlight = (async () => {
    const next = await nativeRead(() => app().Status());
    const sessionEnded = status.logged_in && !next.logged_in;
    const serverChanged = status.api_url && next.api_url !== status.api_url;
    if (sessionEnded || serverChanged) { clearPrivate(); guestSettings = false; if (sessionEnded) message('登录已结束，请重新登录后继续。','warning'); }
    if (serverChanged) { apiDraftDirty = false; publicLoaded = false; publicSettings = null; commerce = null; publicFlight = null; releaseGeneration++; $('release').replaceChildren(); $('download-update').hidden = true; }
    if (!status.logged_in && next.logged_in) { workspaceLoaded = false; nextDataRetry = 0; view = 'dashboard'; }
    status = next; sampleTraffic(); syncNativeTheme().catch(() => {}); delete failures.status; applyPreferences(); renderShell(); renderConnection(); renderDiagnostics(); renderLoadAlert(); setLocks();
    if (sessionEnded) renderAuth();
  })().catch(error => { failures.status = error; resetTraffic('状态未能刷新，等待下一次采样'); renderLoadAlert(); throw error; }).finally(() => { statusFlight = null; });
  return statusFlight;
}
async function loadPublic() {
  if (publicFlight) return publicFlight;
  const currentEpoch = epoch, api = status.api_url;
  const flight = (async () => {
    const entries = [['publicSettings','/api/public/settings'],['commerce','/api/public/commerce']];
    await Promise.all(entries.map(async ([key,route]) => {
      try {
        const value = await readQuery(route);
        if (currentEpoch !== epoch || api !== status.api_url) return;
        if (key === 'publicSettings') publicSettings = value; else commerce = value;
        delete failures[key];
      } catch (error) {
        if (currentEpoch !== epoch || api !== status.api_url) return;
        if (key === 'publicSettings') publicSettings = null; else commerce = null;
        failures[key] = error;
      }
    }));
    if (currentEpoch !== epoch || api !== status.api_url) return;
    publicLoaded = !failures.publicSettings && !failures.commerce;
    renderAuth(); renderPlans(); renderProfile(); renderLoadAlert();
  })();
  publicFlight = flight; renderAuth(); try { await flight; } finally { if (publicFlight === flight) publicFlight = null; renderAuth(); }
}
async function loadWorkspace(background = false) {
  if (!status.logged_in) return;
  if (dataFlight) return dataFlight;
  const currentEpoch = epoch, api = status.api_url;
  const entries = [['me','/api/me'],['nodes','/api/nodes'],['plans','/api/plans'],['orders','/api/orders']];
  if (!background) { entries.forEach(([key]) => loading.add(key)); renderLists(); renderProfile(); }
  const flight = Promise.all(entries.map(async ([key,route]) => {
    try {
      const value = await readQuery(route);
      if (epoch !== currentEpoch || api !== status.api_url || !status.logged_in) return;
      if (key === 'me') profile = value;
      else if (key === 'nodes') nodeList = Array.isArray(value) ? value : [];
      else if (key === 'plans') planList = Array.isArray(value) ? value : [];
      else orderList = Array.isArray(value) ? value : [];
      delete failures[key];
    } catch (error) {
      if (epoch !== currentEpoch || api !== status.api_url) return;
      failures[key] = error; await checkUnauthorized(error,key);
    } finally {
      if (epoch === currentEpoch) loading.delete(key);
    }
  })).then(() => {
    if (epoch !== currentEpoch || !status.logged_in) return;
    workspaceLoaded = !entries.some(([key]) => failures[key]);
    nextWorkspaceRefresh = Date.now() + 60000;
    if (!failures.nodes && !nodeList.some(n => n.id === selected)) {
      selected = nodeList.find(n => n.online && nodeAllowed(n))?.id || nodeList.find(nodeAllowed)?.id || nodeList[0]?.id || '';
      // Persist explicit choices only: an automatic background write could
      // otherwise overwrite a user's next node or system proxy preference.
    }
    updateRegionOptions(); renderLists(); renderProfile(); renderConnection(); renderLoadAlert();
  });
  dataFlight = flight; try { await flight; } finally { if (dataFlight === flight) dataFlight = null; }
}
async function refreshAll() { await refreshStatus(); await Promise.all([loadPublic(),loadWorkspace()]); }
// Retry data as well as native status after startup/network failures. Never
// retry account mutations; back off failed reads and keep status responsive.
async function poll() {
  try {
    await refreshStatus();
    if (locks.has('auth') || Date.now() < nextDataRetry) return;
    const periodic = status.logged_in && workspaceLoaded && Date.now() >= nextWorkspaceRefresh;
    if (publicLoaded && (!status.logged_in || workspaceLoaded) && !periodic) return;
    nextDataRetry = Date.now() + retryDelay;
    await Promise.all([publicLoaded && !periodic ? null : loadPublic(), status.logged_in && (!workspaceLoaded || periodic) ? loadWorkspace(periodic) : null]);
    const incomplete = !publicLoaded || status.logged_in && !workspaceLoaded;
    retryDelay = incomplete ? Math.min(retryDelay * 2, 30000) : 3000;
    nextDataRetry = Date.now() + retryDelay;
  } catch (_) { /* refreshStatus presents the error; the next poll can recover. */ }
}
function renderLists() { renderNodes(); renderPlans(); renderOrders(); }
function renderProfile() {
  if (!status.logged_in) return;
  const user = profile?.user;
  if (!user) {
    $('profile-email').textContent = status.email || '我的账户';
    $('profile').textContent = failures.me ? '账户未能读取，请重新加载。' : '正在读取账户与权益…';
    $('summary-state').textContent = failures.me ? '账户读取失败' : '正在读取账户';
    return;
  }
  const limit = Number(user.traffic?.total_bytes ?? user.traffic_limit ?? 0), used = Number(user.traffic?.used_bytes ?? (Number(user.upload || 0)+Number(user.download || 0)));
  const remaining = user.traffic ? (user.traffic.unlimited ? '不限流量' : bytes(user.traffic.remaining_bytes)) : limit > 0 ? bytes(Math.max(0,limit-used)) : (user.active ? '不限流量' : '未开通');
  let availability = user.disabled ? '账号已停用，请联系服务商' : user.active ? '订阅可用' : '未开通、已到期或流量额度已用完';
  if (!user.disabled && user.traffic_exhausted) availability = '流量耗尽，订阅仍在有效期内';
  else if (!user.disabled && user.subscription_status === 'expired') availability = '订阅已到期';
  else if (!user.disabled && user.subscription_status === 'scheduled') availability = '订阅待生效';
  if (user.verification_required) availability = '请先到网站完成邮箱验证';
  $('profile-email').textContent = user.email || status.email || '我的账户';
  $('profile').textContent = availability+' · 当前周期已用 '+bytes(used);
  $('profile-remaining').textContent = remaining; $('profile-expiry').textContent = date(user.expires_at);
  $('profile-devices').textContent = Number(user.device_limit) > 0 ? user.device_limit+' 台' : '未设置上限';
  $('summary-state').textContent = user.traffic_exhausted ? '流量耗尽' : user.active ? '订阅可用' : '暂时无法连接';
  $('summary-expiry').textContent = Number(user.expires_at) > 0 ? date(user.expires_at)+' 到期' : '选购套餐后即可开通';
  $('summary-remaining').textContent = remaining; $('sidebar-plan-text').textContent = user.traffic_exhausted ? '流量耗尽 · 订阅有效' : user.active ? '订阅可用 · '+remaining : '尚无可用订阅';
  $('quota-progress').hidden = user.traffic?.unlimited === true || limit <= 0; $('quota-progress').max = Math.max(limit,1); $('quota-progress').value = Math.min(used,limit);
  $('account-alert').hidden = user.active && !user.verification_required;
  $('account-alert-text').textContent = user.verification_required ? '完成邮箱验证后再连接。可前往网站处理，其他页面仍可浏览。' : availability+'，可以先浏览线路和套餐。';
  $('account-action').textContent = user.verification_required || user.disabled ? '前往网站' : '查看套餐';
  const trialHours = Number(profile.trial_hours || 0);
  $('trial').hidden = profile.commerce !== false || trialHours <= 0 || !!user.trial_used || !!user.active || user.role === 'admin';
  $('trial').textContent = '激活 '+trialHours+' 小时试用';
  $('announcement').textContent = profile.announcement || ''; $('announcement-card').hidden = !profile.announcement;
  $('test-mode-note').hidden = commerce?.test_mode !== true;
}
function renderConnection() {
  const connected = isConnected(), connecting = isConnecting(), degraded = status.connection_state === 'degraded';
  $('connection-visual').dataset.state = connecting ? 'connecting' : degraded ? 'degraded' : connected ? 'connected' : 'idle';
  const node = nodeList.find(n => n.id === selected);
  const available = nodeAllowed(node);
  $('connect').disabled = !status.logged_in || connecting || ['disconnect','preferences','mode','save-api','logout'].some(key => locks.has(key)) || (!connected && (!node?.online || !available || !!failures.nodes || !!failures.me || loading.has('me') || loading.has('nodes')));
  $('connect').classList.toggle('connected',connected); $('connect').classList.toggle('connecting',connecting); $('connect').classList.toggle('degraded',degraded);
  $('connect-label').textContent = connected ? '断开' : connecting ? '连接中' : '连接';
  $('connect').setAttribute('aria-label',connected ? '断开当前连接' : connecting ? '连接正在进行' : '连接所选线路');
  $('cancel-connect').hidden = !connecting; $('cancel-connect').disabled = locks.has('cancel');
  $('state').textContent = degraded ? (status.health?.error_kind==='dns_unavailable' ? 'DNS 解析异常' : status.health?.error_kind==='egress_unavailable' ? '节点出网异常' : '线路入口异常') : connected ? '已连接' : connecting ? (status.recovering ? '正在恢复连接' : '正在连接') : '未连接';
  $('state').className = 'pill '+(degraded ? 'warm' : connected ? 'good' : connecting ? 'warm' : 'neutral');
  $('footer-connection').textContent = $('state').textContent;
  $('footer-connection').dataset.state = degraded ? 'degraded' : connected ? 'connected' : 'disconnected';
  $('footer-node').textContent = connected ? (status.node_name || node?.name || '当前线路') : node?.name || (status.logged_in ? '选择线路后连接' : '登录后连接');
  $('node-name').textContent = connected ? (status.node_name || node?.name || '当前线路') : (node?.name || (loading.has('nodes') ? '正在读取线路…' : '选择一条线路'));
  const duration = Math.max(0,Math.floor(Date.now()/1000-Number(status.connected_at || 0)));
  $('duration').textContent = connecting ? (connectionAttempt?.cancelRequested ? '正在取消连接，请稍候…' : stageNames[status.connection_stage] || '正在建立连接…') : connected ? '已连接 '+(duration < 60 ? duration+' 秒' : Math.floor(duration/60)+' 分钟') : '准备好后，点击连接。';
  $('connection-hint').textContent = degraded ? (status.health?.error_kind==='dns_unavailable' ? '节点入口可达，但域名解析失败；正在持续检查备用解析通道。' : status.health?.error_kind==='egress_unavailable' ? '节点入口可达，但 HTTPS 出网检查失败；请查看分层检查结果。' : '最近一次节点入口检查失败，可尝试断开重连或更换线路。') : connected ? '连接已建立。访问速度取决于当前网络与目标站点。' : connecting ? (status.recovering ? '正在恢复网络连接（第 '+(status.recovery_attempt || 1)+' 次尝试），可随时取消。' : '首次连接可能需要一点时间，可随时取消。') : failures.nodes ? '线路未能刷新，请点击上方重新加载。' : !profile ? '正在确认账户与线路信息。' : !available ? (node?.access?.reason || '请先在订阅页查看权益或完成邮箱验证。') : !node ? '暂无可用线路，请稍后刷新或联系服务商。' : !node.online ? '所选线路当前离线，请更换一条在线线路。' : '已准备好。所选代理模式会在连接时生效。';
  $('download').textContent = connected && Number.isFinite(status.download_rate) ? bytes(status.download_rate)+'/s' : '—';
  $('upload').textContent = connected && Number.isFinite(status.upload_rate) ? bytes(status.upload_rate)+'/s' : '—';
  $('selected-node-name').textContent = node?.name || '还未选择'; $('selected-node-meta').textContent = node ? (node.region || '未标注地区')+' · '+(node.test_only ? '测试线路' : '服务线路') : '在「线路」中选择适合你的节点。';
  renderNodeSymbol($('selected-node-symbol'),node);
  $('selected-node-health').textContent = failures.nodes ? '线路状态未能刷新' : node ? (node.online ? '最近上报在线' : '最近上报离线') : loading.has('nodes') ? '正在读取' : '暂无线路';
  $('selected-node-health').className = 'pill '+(node?.online && !failures.nodes ? 'good' : 'neutral');
}

// One point per second, from native counters only. Gaps and missing readings
// are not interpolated; each connection starts with an empty history.
function resetTraffic(caption = '连接后显示实时趋势') {
  trafficSamples = []; trafficKey = ''; trafficSecond = -1;
  for (const key of ['down','up']) $('traffic-'+key).setAttribute('d','');
  $('traffic-chart').dataset.samples = '0';
  $('traffic-empty').hidden = false; $('traffic-empty').textContent = caption;
  $('traffic-caption').textContent = '最近 60 秒'; $('traffic-scale').textContent = '等待采样';
}
function sampleTraffic(now = Date.now()) {
  if (!status.logged_in || !isConnected()) { resetTraffic(); return; }
  if (document.visibilityState === 'hidden') return;
  const key = epoch+':'+status.node_id+':'+status.connected_at;
  if (key !== trafficKey) { resetTraffic('正在采集网速…'); trafficKey = key; }
  const second = Math.floor(now/1000);
  if (second === trafficSecond) return;
  if (second < trafficSecond) { resetTraffic('正在重新采样…'); trafficKey = key; }
  trafficSecond = second;
  const down = status.download_rate, up = status.upload_rate;
  const valid = Number.isFinite(down) && down >= 0 && Number.isFinite(up) && up >= 0;
  trafficSamples = trafficSamples.filter(s => s.time > second-60);
  if (valid) trafficSamples.push({time:second,down,up});
  const scale = Math.max(1024,...trafficSamples.flatMap(s => [s.down,s.up]));
  for (const name of ['down','up']) {
    let previous = -Infinity;
    const path = trafficSamples.map(s => {
      const command = s.time === previous+1 ? 'L' : 'M'; previous = s.time;
      return command+(12+(59-second+s.time)*576/59).toFixed(1)+','+(76-s[name]/scale*60).toFixed(1);
    }).join(' ');
    $('traffic-'+name).setAttribute('d',path);
  }
  $('traffic-chart').dataset.samples = String(trafficSamples.length);
  $('traffic-empty').hidden = valid && trafficSamples.length > 1;
  $('traffic-empty').textContent = valid ? '正在采集网速…' : '等待有效网速数据';
  $('traffic-caption').textContent = '最近 60 秒 · '+trafficSamples.length+' 次采样';
  $('traffic-scale').textContent = trafficSamples.length ? '刻度 '+bytes(scale)+'/s' : '等待采样';
}
function renderDiagnostics() {
  $('protocol').textContent = 'HTTP/2'+(isConnected() ? (status.ech ? ' · ECH 已接受' : ' · 加密状态请查看诊断') : ' · 连接后显示加密状态');
  $('addresses').textContent = 'SOCKS5 '+(status.socks || '—')+' · HTTP '+(status.http || '—');
  const health = status.health;
  $('health-details').textContent = health?.checked_at ? date(health.checked_at)+' · '+(health.consecutive_failures ? '连续失败 '+health.consecutive_failures+' 次；最近成功 '+date(health.last_success) : '最近检查成功')+(health.error_kind ? ' · '+health.error_kind : '') : '尚未完成节点入口检查';
  const layers = [['gateway','节点入口'],['dns','DNS 解析'],['egress','HTTPS 出网']];
  const layerText = layer => !isConnected() ? '未连接' : !layer?.checked_at ? '待检查' : layer.error_kind ? '异常' : '正常';
  $('network-health').replaceChildren(...layers.map(([key,label]) => {
    const layer = health?.[key]; return element('span',label+' · '+layerText(layer),'pill '+(!isConnected() || !layer?.checked_at ? 'neutral' : layer.error_kind ? 'warm' : 'good'));
  }));
  if (health?.gateway?.checked_at) $('health-details').textContent = layers.map(([key,label]) => label+'：'+layerText(health[key])+' · '+date(health[key]?.checked_at)).join('\n');
  const log = status.logging;
  $('diagnostics').textContent = (log ? '已记录 '+(log.written_events || 0)+' 条 · 丢失 '+(log.dropped_events || 0)+' 条 · 写入错误 '+(log.io_errors || 0)+'\n' : '')+(status.logs || '日志目录尚未就绪');
  const modes = {'appdata':'当前 Windows 用户数据','portable':'便携目录数据','local-appdata':'当前 Windows 用户数据','legacy-portable':'原便携目录数据','isolated':'独立数据目录'};
  $('data-directory').textContent = (modes[status.data_mode] || '本机数据目录')+'\n'+(status.data_directory || '—');
  $('proxy-warning').textContent = status.proxy_recovery_error || ''; $('proxy-warning').hidden = !status.proxy_recovery_error;
  $('recover-proxy').hidden = !status.proxy_recovery_error && (!status.system_proxy || isConnected());
  if (status.proxy_recovery_error && status.proxy_recovery_error !== recoveryNotice) {
    recoveryNotice = status.proxy_recovery_error; if (!status.logged_in) guestSettings = true; renderShell(); showView('settings');
  }
  if (!status.proxy_recovery_error) recoveryNotice = '';
}
function updateRegionOptions() {
  const previous = $('node-region').value;
  const regions = [...new Set(nodeList.map(n => n.region || '未标注地区'))].sort((a,b) => a.localeCompare(b,'zh-CN'));
  $('node-region').replaceChildren(new Option('全部地区','all'),...regions.map(r => new Option(r,r)));
  $('node-region').value = regions.includes(previous) ? previous : 'all';
}
function renderNodes() {
  const focused = document.activeElement;
  const focusedID = focused?.dataset.nodeId || focused?.dataset.probeId;
  const focusType = focused?.dataset.probeId ? 'probeId' : 'nodeId';
  $('nodes').setAttribute('aria-busy',String(loading.has('nodes')));
  $('nav-node-count').textContent = nodeList.length ? String(nodeList.length) : '';
  if (!nodeList.length) {
    $('node-count').textContent = loading.has('nodes') ? '正在加载线路' : failures.nodes ? '线路读取失败' : '0 条线路';
    if (loading.has('nodes')) return empty('nodes','正在读取线路','稍等一下，正在向服务商获取最新列表。',false,true);
    return empty('nodes',failures.nodes ? '线路暂时无法读取' : '暂无可用线路',failures.nodes ? '请检查网络后重新加载。已有订阅不会因此消失。' : '新账户可能需要先开通套餐。已有订阅时，请联系服务商检查线路分配。',!!failures.nodes);
  }
  const search = $('node-search').value.trim().toLowerCase(), region = $('node-region').value, filter = $('node-online').value;
  const shown = nodeList.filter(n => (!search || (n.name+' '+(n.region || '')).toLowerCase().includes(search)) && (region === 'all' || (n.region || '未标注地区') === region) && (filter === 'all' || (filter === 'online') === !!n.online));
  const sorting = $('node-sort').value;
  if (sorting !== 'default') shown.sort((a,b) => {
    if (sorting === 'online' && !!a.online !== !!b.online) return a.online ? -1 : 1;
    if (sorting === 'latency') {
      const left = probes.get(a.id)?.ms ?? Infinity, right = probes.get(b.id)?.ms ?? Infinity;
      if (left !== right) return left < right ? -1 : 1;
    }
    return String(a.name).localeCompare(String(b.name),'zh-CN');
  });
  $('node-count').textContent = '显示 '+shown.length+' / '+nodeList.length+' 条线路'+(failures.nodes ? ' · 上次读取结果' : '');
  if (!shown.length) return empty('nodes','没有匹配的线路','试着更换关键词、地区或在线筛选。');
  $('nodes').replaceChildren(...shown.map(n => {
    const row = element('div',undefined,'node-row'+(n.id === selected ? ' selected' : ''));
    const choose = element('button',undefined,'select'); choose.dataset.nodeId = n.id; choose.type = 'button'; choose.setAttribute('aria-pressed',String(n.id === selected)); choose.setAttribute('aria-label','选择线路 '+n.name);
    row.addEventListener('contextmenu',event => { event.preventDefault(); openNodeMenu(n.id,event.clientX,event.clientY); });
    choose.addEventListener('keydown',event => {
      if (event.key === 'ContextMenu' || event.shiftKey && event.key === 'F10') {
        event.preventDefault(); const bounds = choose.getBoundingClientRect(); openNodeMenu(n.id,bounds.left+24,bounds.bottom);
      }
    });
    choose.disabled = locks.has('preferences') || isConnecting();
    const symbol = element('span',undefined,'line-symbol'); renderNodeSymbol(symbol,n);
    const label = element('span'); label.append(element('span',n.name,'node-title'),element('span',(n.region || '未标注地区')+(n.test_only ? ' · 测试线路' : ''),'node-meta')); choose.append(symbol,label);
    if (n.access?.allowed === false) label.append(element('span',n.access.reason || '当前套餐不包含此线路','node-meta'));
    choose.onclick = () => operation('preferences',async () => { await savePreferences(n.id,$('system-proxy').checked); renderNodes(); renderConnection(); if (isConnected()) message('已保存线路，下次连接时使用。','success'); });
    const online = element('span',n.online ? '在线' : '离线','pill '+(n.online ? 'good' : 'neutral')); online.title = n.last_seen ? '最近上报：'+date(n.last_seen) : '来自服务端最近上报状态';
    const block = element('div',undefined,'latency-block'), result = probes.get(n.id);
    const probe = element('button',result?.busy ? '检查中…' : result?.ms !== undefined ? result.ms+' ms' : '测延迟','probe'); probe.dataset.probeId = n.id; probe.type = 'button'; probe.disabled = !!result?.busy || n.access?.allowed === false; probe.setAttribute('aria-busy',String(!!result?.busy)); probe.setAttribute('aria-label','检测 '+n.name+' 的延迟');
    probe.onclick = () => probeNode(n.id); block.append(probe);
    if (result?.error) block.append(element('span',result.error,'latency-error'));
    else if (result?.checked) block.append(element('span',new Date(result.checked).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'})+' 实测','latency-label'));
    row.append(choose,online,block); return row;
  }));
  if (focusedID) [...$('nodes').querySelectorAll('button')].find(b => b.dataset[focusType] === focusedID && !b.disabled)?.focus({preventScroll:true});
}
async function savePreferences(nodeID, systemProxy) {
  const currentEpoch = epoch; await app().SavePreferences(nodeID,systemProxy);
  if (currentEpoch !== epoch) return;
  selected = nodeID; status.preferences = {...status.preferences,selected_node_id:nodeID,system_proxy:systemProxy}; $('system-proxy').checked = systemProxy; prefsReady = true;
}
async function probeNode(nodeID) {
  if (probes.get(nodeID)?.busy) return;
  const currentEpoch = epoch; probes.set(nodeID,{busy:true}); renderNodes();
  try { const ms = await app().Probe(nodeID); if (epoch === currentEpoch) probes.set(nodeID,{ms:Number(ms),checked:Date.now()}); }
  catch (error) { if (epoch === currentEpoch) probes.set(nodeID,{error:friendly(error)}); await checkUnauthorized(error,'probe'); }
  finally { if (epoch === currentEpoch) renderNodes(); }
}
function renderPlans() {
  if (!status.logged_in) return;
  $('plans').setAttribute('aria-busy',String(loading.has('plans')));
  if (!planList.length) {
    if (loading.has('plans')) return empty('plans','正在读取套餐','价格和权益以当前服务商提供的信息为准。',false,true);
    return empty('plans',failures.plans ? '套餐暂时无法读取' : '暂无在售套餐',failures.plans ? '请稍后重新加载。' : '服务商暂未提供可购买的套餐，可通过网站联系支持。',!!failures.plans);
  }
  $('plans').replaceChildren(...planList.map(p => {
    const card = element('article',undefined,'card plan-card');
    card.append(element('h3',p.name),element('p',p.kind === 'traffic' ? '流量加购 · 当前有效周期' : '订阅套餐 · '+period(p),'plan-type'));
    const price = element('p',(Number(p.price_cents || 0)/100).toFixed(2),'plan-price'); price.append(element('small',String(p.currency || 'CNY').toUpperCase())); card.append(price);
    const specs = element('div',undefined,'plan-specs');
    for (const [name,value] of [['wallet',Number(p.traffic_bytes) > 0 ? bytes(p.traffic_bytes)+' 流量' : '不限流量'],['clock',period(p)],['shield',p.kind === 'traffic' ? '保留当前到期时间与设备上限' : (Number(p.devices) > 0 ? p.devices+' 台设备' : '设备上限以网站为准')]]) { const line = element('span'); line.append(icon(name),document.createTextNode(value)); specs.append(line); }
    specs.append(element('span',planScope(p))); card.append(specs);
    const button = element('button',pendingOrder(p.id) ? '继续付款' : '选择套餐','subtle full-width'); button.type = 'button'; button.dataset.planId = p.id;
    button.disabled = !!failures.plans || purchaseBusy || !commerce;
    button.onclick = () => openPurchase(p); card.append(button); return card;
  }));
}
const orderStates = {paid:'已开通',paid_review:'已付款待处理',pending:'待付款',refunded:'已退款',cancelled:'已取消',expired:'已过期'};
function renderOrders() {
  if (!status.logged_in) return;
  $('orders').setAttribute('aria-busy',String(loading.has('orders')));
  if (!orderList.length) {
    if (loading.has('orders')) return empty('orders','正在读取订单','正在同步账户最近的购买记录。',false,true);
    return empty('orders',failures.orders ? '订单暂时无法读取' : '还没有订单',failures.orders ? '请重新加载，或前往网站查看订单。' : '选择套餐后，订单会显示在这里。',!!failures.orders);
  }
  $('orders').replaceChildren(...[...orderList].sort((a,b) => Number(b.created_at || 0)-Number(a.created_at || 0)).slice(0,6).map(o => {
    const row = element('div',undefined,'order-row'), summary = element('div'), side = element('div',undefined,'order-side');
    summary.append(element('p',o.plan?.name || '套餐订单','order-name'),element('p',amount(o.plan || {})+' · '+date(o.created_at)+(o.test ? ' · 测试订单' : ''),'order-meta'));
    const orderStatus = o.status === 'pending' && Number(o.expires_at) > 0 && Number(o.expires_at)*1000 <= Date.now() ? 'expired' : o.status;
    side.append(element('span',orderStates[orderStatus] || '处理中','pill '+(orderStatus === 'paid' ? 'good' : orderStatus === 'pending' ? 'warm' : 'neutral')));
    if (orderStatus === 'pending') { const button = element('button','前往网站付款','text-button'); button.onclick = () => openPortal(); side.append(button); }
    row.append(summary,side); return row;
  }));
}
function pendingOrder(planID) { return orderList.find(o => o.plan?.id === planID && o.status === 'pending' && (!Number(o.expires_at) || Number(o.expires_at)*1000 > Date.now())); }
function closePurchase(force = false) {
  if (purchaseBusy && !force) return;
  if ($('purchase-dialog').open) $('purchase-dialog').close();
  purchasePlan = null; $('purchase-error').textContent = ''; $('purchase-review').hidden = true;
}
function purchaseSignature(plan, testMode) {
  return JSON.stringify([Number(plan.price_cents || 0),String(plan.currency || 'CNY').toUpperCase(),plan.kind || 'subscription',Number(plan.days || 0),Number(plan.traffic_bytes || 0),Number(plan.devices || 0),!!testMode,[...(plan.node_ids || [])].sort()]);
}
function renderPurchaseReview(plan, existing) {
  purchasePlan = {...plan}; purchaseTestMode = existing ? existing.test === true : commerce?.test_mode === true;
  $('purchase-title').textContent = plan.name;
  $('purchase-description').textContent = existing ? '将继续这笔待付款订单，金额与权益以该订单创建时的内容为准。请在网站使用同一账号登录。' : '确认金额与权益后创建订单。请在打开的网站中使用同一账号登录并完成付款。';
  const entries = [['可用线路',planScope(plan)],['订单金额',amount(plan)],['生效周期',period(plan)],['流量额度',Number(plan.traffic_bytes) > 0 ? bytes(plan.traffic_bytes) : '不限流量'],['设备数量',plan.kind === 'traffic' ? '保持当前上限' : Number(plan.devices) > 0 ? plan.devices+' 台' : '以网站为准']];
  $('purchase-details').replaceChildren(...entries.flatMap(([key,value]) => [element('dt',key),element('dd',value)]));
  $('purchase-mode').textContent = commerce?.enabled === false ? '此服务使用人工核实订单。创建后请到网站查看，联系管理员核实并开通；客户端不会自动扣款。' : purchaseTestMode ? '这是一笔测试订单，模拟购买不会实际扣款，权益仅适用于测试节点。' : '';
  $('purchase-mode').hidden = !purchaseTestMode && commerce?.enabled !== false;
  $('confirm-purchase').textContent = existing ? '使用待付款订单前往网站' : '创建订单并前往网站';
}
function openPurchase(plan) {
  if (!status.logged_in || purchaseBusy) return;
  const existing = pendingOrder(plan.id);
  renderPurchaseReview(existing?.plan || plan,existing); $('purchase-error').textContent = '';
  $('confirm-purchase').disabled = !commerce; $('purchase-review').hidden = true;
  if (!commerce) $('purchase-error').textContent = '服务信息未能读取，暂时无法确认购买模式。请关闭此窗口，重新加载后再选择套餐。';
  if (!$('purchase-dialog').open) $('purchase-dialog').showModal();
}
async function confirmPurchase() {
  if (purchaseBusy || !purchasePlan || !status.logged_in || !commerce) return;
  const plan = purchasePlan, currentEpoch = epoch;
  purchaseBusy = true; $('confirm-purchase').disabled = true; $('purchase-close').disabled = true; $('cancel-purchase').disabled = true; $('purchase-error').textContent = ''; renderPlans();
  let checkedOrders = false, created = false;
  try {
    // A previous POST may have succeeded even if its response was lost. Read before every create.
    const latest = await readQuery('/api/orders');
    if (currentEpoch !== epoch || !status.logged_in) return;
    if (!Array.isArray(latest)) throw Error('订单列表未能核实，请重试。');
    orderList = latest; checkedOrders = true; delete failures.orders; renderOrders();
    const existing = pendingOrder(plan.id);
    if (existing && purchaseSignature(existing.plan,existing.test) !== purchaseSignature(plan,purchaseTestMode)) {
      renderPurchaseReview(existing.plan,existing);
      $('purchase-error').textContent = '刚刚发现一笔内容不同的待付款订单，已更新金额、周期和购买模式。请核对后再次确认。';
      return;
    }
    if (!existing) {
      if (uncertainOrders.has(plan.id)) { $('purchase-review').hidden = false; throw Error('上次创建结果尚未确认。请先前往网站核实订单，避免重复购买。'); }
      const currentPlans = await readQuery('/api/plans');
      if (currentEpoch !== epoch || !status.logged_in) return;
      const currentPlan = Array.isArray(currentPlans) && currentPlans.find(p => p.id === plan.id);
      if (!currentPlan) throw Error('这个套餐已停止销售，请关闭窗口并刷新套餐列表。');
      if (purchaseSignature(currentPlan,commerce.test_mode) !== purchaseSignature(plan,purchaseTestMode)) {
        renderPurchaseReview(currentPlan,null);
        $('purchase-error').textContent = '套餐内容已变化，已更新金额、周期和购买模式。请核对后再次确认。';
        return;
      }
      uncertainOrders.add(plan.id);
      let result;
      try { result = await app().Query('/api/orders',{plan_id:plan.id}); }
      catch (error) {
        // Explicit client rejection means no order was accepted; network/5xx failures remain uncertain.
        if (currentEpoch === epoch && /HTTP\s+(400|401|403|404|409|422|429)\b/i.test(String(error?.message || error))) uncertainOrders.delete(plan.id);
        throw error;
      }
      if (currentEpoch !== epoch || !status.logged_in) return;
      if (!result?.id) throw Error('服务未返回订单号，请前往网站核实订单。');
      orderList = [result,...orderList.filter(o => o.id !== result.id)]; created = true;
      uncertainOrders.delete(plan.id);
      if (!result.plan) throw Error('订单已创建，但订单内容未能读取。请前往网站核实后付款。');
      if (purchaseSignature(result.plan,result.test) !== purchaseSignature(plan,purchaseTestMode)) {
        renderPurchaseReview(result.plan,result); renderOrders();
        $('purchase-error').textContent = '订单已创建，但实际内容与刚才确认的内容不同。请核对更新后的金额、周期和购买模式，再次确认后前往网站。';
        return;
      }
    }
    uncertainOrders.delete(plan.id); renderOrders();
    // Keep the confirmed order in memory before opening the browser, including when opening fails.
    closePurchase(true);
    await app().OpenPortal();
    if (currentEpoch === epoch) message((created ? '订单已创建。' : '已复用现有待付款订单。')+(commerce?.enabled === false ? '请到网站查看订单，联系管理员核实并开通。' : '请在网站使用同一账号完成付款，返回客户端后刷新权益。'),'success');
  } catch (error) {
    if (currentEpoch !== epoch) return;
    const detail = !checkedOrders ? '无法核实已有订单，本次未创建新订单。'+friendly(error) : friendly(error);
    $('purchase-error').textContent = detail;
    if (!$('purchase-dialog').open) message('订单已保留。'+detail+' 可点击「购买、账单与账号安全」继续付款。');
    else if (checkedOrders && uncertainOrders.has(plan.id)) $('purchase-review').hidden = false;
    await checkUnauthorized(error,'purchase');
  } finally {
    purchaseBusy = false; $('confirm-purchase').disabled = !commerce; $('purchase-close').disabled = false; $('cancel-purchase').disabled = false; renderPlans(); renderLoadAlert();
  }
}
async function openPortal() { return operation('portal',() => app().OpenPortal()); }
async function reconcileCancelled(attempt) {
  // Both CancelConnect and the original Connect may settle first. Serialize cleanup,
  // then inspect state again on each settlement so a late successful connect is closed.
  const cleanup = (attempt.cleanup || Promise.resolve()).catch(() => {}).then(async () => {
    if (connectionAttempt !== attempt) return;
    await refreshStatus();
    if (isConnected()) { await app().Disconnect(); await refreshStatus(); }
  });
  attempt.cleanup = cleanup;
  return cleanup;
}
async function connect() {
  if (localConnecting) return;
  if (isConnected()) return operation('disconnect',async () => { await app().Disconnect(); message('已断开连接，原有代理设置已恢复。','success'); });
  if (isConnecting() || $('connect').disabled) return;
  const generation = ++connectGeneration, attempt = {generation,cancelRequested:false,settled:false,cleanup:null};
  connectionAttempt = attempt; localConnecting = true; message(''); renderConnection(); renderNodes();
  try {
    await app().Connect(selected,$('system-proxy').checked);
    if (generation === connectGeneration) {
      if (attempt.cancelRequested) { await reconcileCancelled(attempt); message('已取消连接，原有代理设置已恢复。','success'); }
      else message('连接已建立。','success');
    }
  } catch (error) {
    if (generation === connectGeneration) {
      if (attempt.cancelRequested) {
        try { await reconcileCancelled(attempt); message('已取消连接。','success'); }
        catch (cleanupError) { message('取消后的代理清理未完成。'+friendly(cleanupError)); }
      } else { message(friendly(error)); await checkUnauthorized(error,'connect'); }
    }
  } finally {
    attempt.settled = true;
    if (generation === connectGeneration) { localConnecting = false; if (connectionAttempt === attempt) connectionAttempt = null; await refreshStatus().catch(() => {}); renderNodes(); }
  }
}
async function cancelConnect() {
  if (!isConnecting() || locks.has('cancel')) return;
  const attempt = connectionAttempt;
  if (attempt) attempt.cancelRequested = true;
  locks.add('cancel'); renderConnection();
  try {
    await app().CancelConnect();
    if (attempt) await reconcileCancelled(attempt);
    else { await refreshStatus(); if (isConnected()) await app().Disconnect(); }
    if (!attempt || attempt.settled) { localConnecting = false; message('已取消连接。','success'); }
    else message('正在取消连接，等待本机连接任务结束。','warning');
  } catch (error) { message(friendly(error)); }
  finally { locks.delete('cancel'); await refreshStatus().catch(() => {}); renderConnection(); renderNodes(); }
}
function versionParts(value) {
  const match = String(value || '').trim().match(/^v?(\d+)\.(\d+)\.(\d+)(?:-([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*))?(?:\+[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)?$/);
  return match ? {core:match.slice(1,4).map(BigInt),pre:match[4]?.split('.') || []} : null;
}
function compareVersion(left,right) {
  const a = versionParts(left), b = versionParts(right); if (!a || !b) return null;
  for (let i = 0; i < 3; i++) if (a.core[i] !== b.core[i]) return a.core[i] > b.core[i] ? 1 : -1;
  if (!a.pre.length || !b.pre.length) return a.pre.length === b.pre.length ? 0 : a.pre.length ? -1 : 1;
  for (let i = 0; i < Math.max(a.pre.length,b.pre.length); i++) {
    if (a.pre[i] === b.pre[i]) continue;
    if (a.pre[i] === undefined || b.pre[i] === undefined) return a.pre[i] === undefined ? -1 : 1;
    const na = /^\d+$/.test(a.pre[i]), nb = /^\d+$/.test(b.pre[i]);
    if (na !== nb) return na ? -1 : 1;
    const left = na ? BigInt(a.pre[i]) : a.pre[i], right = nb ? BigInt(b.pre[i]) : b.pre[i];
    if (left !== right) return left > right ? 1 : -1;
  }
  return 0;
}
async function checkUpdates() {
  const generation = ++releaseGeneration, currentEpoch = epoch, api = status.api_url;
  $('download-update').hidden = true; $('release').textContent = '正在读取发布信息…';
  try {
    const publicInfo = await readQuery('/api/public/commerce');
    if (generation !== releaseGeneration || currentEpoch !== epoch || api !== status.api_url) return;
    const release = publicInfo?.release;
    if (!release?.version) { $('release').textContent = '服务商尚未发布 Windows 客户端。'; return; }
    const comparison = compareVersion(release.version,status.version || 'v0.6.16');
    const heading = comparison === 1 ? '发现更新：'+release.version : comparison === 0 ? '当前与服务商发布版本一致：'+release.version : comparison === -1 ? '服务商发布版本为 '+release.version+'。当前版本较新，无需降级。' : '服务商发布版本：'+release.version+'。版本格式无法比较，请向服务商确认。';
    $('release').replaceChildren(element('p',heading),element('p',release.notes || ''));
    let valid = false; try { valid = new URL(release.url).protocol === 'https:' && /^[a-f0-9]{64}$/i.test(release.sha256 || ''); } catch (_) { /* metadata is incomplete */ }
    $('download-update').hidden = comparison !== 1 || !valid;
    if (comparison === 1 && !valid) $('release').append(element('p','下载信息尚不完整，请联系服务商补充。'));
  } catch (error) {
    if (generation === releaseGeneration && currentEpoch === epoch) { $('release').textContent = '发布信息未能读取，可以重新检查。'; throw error; }
  }
}

// Desktop appearance is a local preference, separate from accounts and connection settings.
let theme = 'system', nativeTheme = '', menuNode = '';
try { const saved = localStorage.getItem('tunnelx.theme'); if (['system','light','dark'].includes(saved)) theme = saved; } catch (_) {}
function applyTheme(value) {
  theme = value; document.documentElement.dataset.theme = value; $('theme-select').value = value;
}
async function syncNativeTheme() {
  const bridge = window.go?.main?.App;
  if (typeof bridge?.SetTheme !== 'function' || nativeTheme === theme) return;
  const next = theme; await bridge.SetTheme(next); nativeTheme = next;
}
function closeNodeMenu(restore = false) {
  const id = menuNode; $('node-menu').hidden = true; menuNode = '';
  if (restore) [...$('nodes').querySelectorAll('[data-node-id]')].find(b => b.dataset.nodeId === id)?.focus({preventScroll:true});
}
function openNodeMenu(id,x,y) {
  const node = nodeList.find(n => n.id === id); if (!node || !status.logged_in) return;
  menuNode = id;
  $('node-menu-select').disabled = locks.has('preferences') || isConnecting();
  $('node-menu-probe').disabled = node.access?.allowed === false || !!probes.get(id)?.busy;
  const menu = $('node-menu'); menu.hidden = false;
  menu.style.left = Math.max(4,Math.min(x,window.innerWidth-180))+'px';
  menu.style.top = Math.max(4,Math.min(y,window.innerHeight-90))+'px';
  [...menu.querySelectorAll('button')].find(b => !b.disabled)?.focus();
}
applyTheme(theme);
$('theme-select').onchange = () => operation('theme',async () => {
  const previous = theme; applyTheme($('theme-select').value);
  try { await syncNativeTheme(); }
  catch (error) { applyTheme(previous); throw error; }
  try { localStorage.setItem('tunnelx.theme',theme); }
  catch (_) { message('主题已应用，但本机偏好未能保存。','warning'); }
});
$('node-sort').onchange = renderNodes;
$('node-menu-select').onclick = () => {
  const id = menuNode; closeNodeMenu();
  const button = [...$('nodes').querySelectorAll('[data-node-id]')].find(b => b.dataset.nodeId === id);
  button?.focus(); button?.click();
};
$('node-menu-probe').onclick = () => {
  const id = menuNode; closeNodeMenu(true);
  const node = nodeList.find(n => n.id === id); if (node && node.access?.allowed !== false) probeNode(id);
};
document.addEventListener('pointerdown',event => {
  if (!$('node-menu').contains(event.target)) closeNodeMenu();
  if (!$('app-menu').contains(event.target)) $('app-menu').open = false;
});
$('main-content').addEventListener('scroll',() => closeNodeMenu());
window.addEventListener('resize',() => closeNodeMenu());
document.addEventListener('keydown',event => {
  if (event.defaultPrevented || event.isComposing) return;
  if (!$('node-menu').hidden) {
    if (event.key === 'Escape') { event.preventDefault(); closeNodeMenu(true); return; }
    if (['ArrowDown','ArrowUp','Home','End'].includes(event.key)) {
      event.preventDefault(); const items=[...$('node-menu').querySelectorAll('button')].filter(b => !b.disabled);
      const index=items.indexOf(document.activeElement), delta=event.key === 'ArrowUp' ? -1 : 1;
      items[event.key === 'Home' ? 0 : event.key === 'End' ? items.length-1 : (index+delta+items.length)%items.length]?.focus(); return;
    }
    if (event.key === 'Tab') closeNodeMenu();
  }
  if ($('purchase-dialog').open) return;
  if (event.key === 'Escape' && $('app-menu').open) { event.preventDefault(); $('app-menu').open=false; $('app-menu').querySelector('summary').focus(); return; }
  if (event.key === 'F5') { event.preventDefault(); if (status.logged_in) operation('refresh',refreshAll); return; }
  if (event.ctrlKey && !event.altKey && event.key.toLowerCase() === 'f' && status.logged_in) {
    event.preventDefault(); showView('nodes'); $('node-search').focus(); $('node-search').select(); return;
  }
  if (event.ctrlKey && !event.altKey && ['1','2','3','4'].includes(event.key) && status.logged_in) {
    event.preventDefault(); showView(['dashboard','nodes','subscription','settings'][Number(event.key)-1],true); return;
  }
  if (view === 'nodes' && event.target.matches('[data-node-id]') && ['ArrowDown','ArrowUp','Home','End'].includes(event.key)) {
    event.preventDefault(); const rows=[...$('nodes').querySelectorAll('[data-node-id]')].filter(b => !b.disabled);
    const index=rows.indexOf(event.target), delta=event.key === 'ArrowUp' ? -1 : 1;
    rows[event.key === 'Home' ? 0 : event.key === 'End' ? rows.length-1 : Math.max(0,Math.min(rows.length-1,index+delta))]?.focus();
  }
});

document.querySelectorAll('svg.icon').forEach(svg => { svg.setAttribute('viewBox','0 0 24 24'); svg.setAttribute('aria-hidden','true'); svg.setAttribute('focusable','false'); });
document.querySelectorAll('[data-view]').forEach(button => { button.onclick = () => showView(button.dataset.view,true); });
$('guest-settings').onclick = () => { guestSettings = true; renderShell(); showView('settings',true); };
$('guest-back').onclick = () => { guestSettings = false; renderShell(); $('email').focus(); };
$('register').onclick = () => { if (publicSettings?.registration !== true || !commerce || locks.has('auth')) return; authMode = 'register'; renderAuth(); $('email').focus(); };
$('back-to-login').onclick = () => { authMode = 'login'; renderAuth(); $('email').focus(); };
$('show-otp').onclick = () => { $('otp-wrap').hidden = false; $('show-otp').hidden = true; $('otp').focus(); };
$('toggle-password').onclick = () => { const reveal = $('password').type === 'password'; $('password').type = reveal ? 'text' : 'password'; $('toggle-password').setAttribute('aria-pressed',String(reveal)); $('toggle-password').setAttribute('aria-label',reveal ? '隐藏密码' : '显示密码'); };
$('login').onsubmit = event => {
  event.preventDefault();
  if (authMode === 'register' && (publicSettings?.registration !== true || !commerce)) return message('注册设置尚未就绪，请重新加载服务信息。');
  operation('auth',async () => {
    try { await app().LoginSecure($('email').value.trim(),$('password').value,$('otp').value.trim(),$('invite').value.trim(),authMode === 'register'); }
    catch (error) { if (authMode === 'login' && /HTTP\s+(400|401|403)|验证码|恢复码|MFA/i.test(String(error?.message || error))) { $('otp-wrap').hidden = false; $('show-otp').hidden = true; } throw error; }
    // A status poll dispatched before login can still contain logged_in=false.
    // Drain it before issuing the authoritative post-login status request.
    if (statusFlight) await statusFlight.catch(() => {});
    clearPrivate();
    $('password').value = ''; $('otp').value = ''; $('invite').value = ''; authMode = 'login'; guestSettings = false; view = 'dashboard';
    await refreshAll(); message('欢迎回来。','success');
  });
};
$('forgot').onclick = () => operation('forgot',async () => { await app().OpenPortal(); message('已打开账号网站，请点击「找回密码」，使用注册邮箱重置密码。','success'); });
$('connect').onclick = connect; $('cancel-connect').onclick = cancelConnect;
$('refresh').onclick = () => operation('refresh',refreshAll); $('retry-load').onclick = () => operation('refresh',refreshAll); $('auth-retry').onclick = () => operation('refresh',refreshAll);
$('change-node').onclick = () => showView('nodes',true); $('manage-subscription').onclick = () => showView('subscription',true);
$('account-action').onclick = () => profile?.user?.verification_required || profile?.user?.disabled ? openPortal() : showView('subscription',true);
$('node-search').oninput = renderNodes; $('node-region').onchange = renderNodes; $('node-online').onchange = renderNodes;
$('system-proxy').onchange = () => { const desired = $('system-proxy').checked, previous = status.preferences?.system_proxy !== false; operation('preferences',async () => { try { await savePreferences(selected,desired); $('proxy-preference-note').textContent = '偏好已保存，下次连接生效。'; } catch (error) { $('system-proxy').checked = previous; throw error; } }); };
$('logout').onclick = () => operation('logout',async () => { await app().Logout(); if (statusFlight) await statusFlight.catch(() => {}); clearPrivate(); status.logged_in = false; status.connected = false; status.connection_state = 'disconnected'; guestSettings = false; renderShell(); renderAuth(); message('已退出账号。','success'); });
$('trial').onclick = () => operation('trial',async () => { await app().Query('/api/trial',{}); await loadWorkspace(); message('试用已开通，请选择一条在线线路连接。','success'); });
for (const id of ['portal','orders-portal','help-portal','purchase-review']) $(id).onclick = openPortal;
$('purchase-form').onsubmit = event => { event.preventDefault(); confirmPurchase(); };
$('purchase-close').onclick = () => closePurchase(); $('cancel-purchase').onclick = () => closePurchase();
$('purchase-dialog').addEventListener('cancel',event => { if (purchaseBusy) event.preventDefault(); else closePurchase(); });
$('api-url').oninput = () => { apiDraftDirty = true; };
$('save-api').onclick = () => operation('save-api',async () => {
  const value = $('api-url').value.trim().replace(/\/+$/,''); let parsed;
  try { parsed = new URL(value); } catch (_) { throw Error('请输入完整的 HTTPS 服务地址。'); }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password) throw Error('服务地址必须使用 HTTPS，且不能包含账号密码。');
  if (parsed.search || parsed.hash || value.includes('?') || value.includes('#')) throw Error('服务地址不能包含查询参数或页面锚点。');
  if (value === status.api_url) { apiDraftDirty = false; renderShell(); message('服务地址未变化，当前登录已保留。','success'); return; }
  await app().SetAPI(value); if (statusFlight) await statusFlight.catch(() => {}); apiDraftDirty = false; clearPrivate(); publicLoaded = false; publicSettings = null; commerce = null; publicFlight = null; releaseGeneration++; $('release').replaceChildren(); $('download-update').hidden = true;
  for (const key of ['publicSettings','commerce']) delete failures[key];
  guestSettings = false; await refreshStatus(); await loadPublic(); message('服务地址已保存，请使用该服务商的账号重新登录。','success');
});
$('hide').onclick = () => operation('hide',() => app().Minimize()); $('quit').onclick = () => operation('quit',() => app().Quit());
$('recover-proxy').onclick = () => operation('recover',async () => { await app().RecoverProxy(); message('已恢复 Windows 原有系统代理设置。','success'); });
$('updates').onclick = () => operation('updates',checkUpdates);
$('download-update').onclick = () => operation('download',async () => { await app().OpenDownload(); message('已在浏览器打开发布包下载。下载完成后请退出旧版本，再解压运行。','success'); });
$('export-diagnostics').onclick = () => operation('export',async () => { $('export-result').textContent = ''; const path = await app().ExportDiagnostics(); if (path) { $('export-result').textContent = '诊断文件已保存到：'+path; message('诊断文件已保存，可提供给客服排查。','success'); } else $('export-result').textContent = '已取消导出。'; });

async function refreshAfterFocus() {
  if (document.visibilityState === 'hidden' || Date.now()-lastFocusRefresh < 3000) return;
  lastFocusRefresh = Date.now(); await refreshAll().catch(error => message(friendly(error)));
}
window.addEventListener('focus',refreshAfterFocus);
document.addEventListener('visibilitychange',() => { document.documentElement.dataset.suspended = String(document.visibilityState === 'hidden'); if (document.visibilityState !== 'hidden') refreshAfterFocus(); else resetTraffic('返回窗口后重新采样'); });
async function init() { try { await refreshAll(); } catch (_) { renderAuth(); } } // load-alert tracks failure and clears on recovery.
document.documentElement.dataset.suspended = String(document.visibilityState === 'hidden');
renderAuth(); renderConnection(); init();
setInterval(poll,1000);

function renderProxyMode() {
  const mode = status.preferences?.proxy_mode || 'global';
  const active = isConnected() || isConnecting(), busy = locks.has('mode') || locks.has('preferences');
  document.querySelectorAll('input[name="proxy-mode"]').forEach(input => { input.checked = input.value === mode; input.disabled = active || busy; });
  $('direct-domains').disabled = active || busy;
  $('save-routing').disabled = active || busy;
  $('mode-status').textContent = active ? '断开后可切换' : '下次连接生效';
  const explanations = {bypass_cn:'国内域名、大陆 IP 和局域网直连，其他流量经节点转发。规则覆盖会随 GitHub 更新，可在设置中添加直连例外。',global:'遵循 Windows 系统代理的软件通过节点连接。不支持系统代理的应用可选择 TUN 模式。',tun:'通过虚拟网卡接管 TCP / UDP；节点和管理服务保留直连出口，局域网沿用原有路由。需要管理员权限。'};
  $('mode-help').textContent = explanations[mode] + (mode === 'tun' && isConnected() && status.tun?.ipv6_available === false ? ' 节点 IPv6 出口不可用，已自动使用 IPv4。' : '') + (mode === 'tun' && !status.tun?.elevated ? ' 请退出客户端，再右键选择“以管理员身份运行”。' : '') + (mode === 'tun' && status.tun?.error ? ' '+status.tun.error : '') + (mode !== 'tun' && status.preferences?.system_proxy === false ? ' 当前系统代理已关闭；请在设置中开启，或手动为应用配置本机代理。' : '');
  const rules = status.routing_rules;
  $('routing-info').textContent = rules ? `${Number(rules.domains || 0).toLocaleString()} 条域名 · ${Number(rules.ip_prefixes || 0).toLocaleString()} 段 IP · ${rules.source === 'updated' ? '更新于 '+date(rules.updated_at) : '随版本内置'}${rules.warning ? ' · '+rules.warning : ''}` : '使用客户端内置的大陆域名与 IP 规则。';
}
async function saveProxyMode(mode) {
  const exceptions = $('direct-domains').value;
  await app().SaveProxyMode(mode,exceptions);
  status.preferences = {...status.preferences,proxy_mode:mode,direct_domains:exceptions.split(/[,\r\n]+/).map(s=>s.trim()).filter(Boolean)};
  routingDraftDirty = false;
  message('代理模式与直连例外已保存，下次连接生效。','success');
}
document.querySelectorAll('input[name="proxy-mode"]').forEach(input => input.addEventListener('change',() => { if (input.checked) operation('mode',() => saveProxyMode(input.value)); }));
$('direct-domains').addEventListener('input',() => { routingDraftDirty = true; });
$('save-routing').onclick = () => operation('mode',() => saveProxyMode(status.preferences?.proxy_mode || 'global'));
$('update-rules').onclick = () => operation('rules',async () => {
  $('routing-result').textContent = '正在下载并校验 GitHub 规则…';
  try { await app().UpdateRoutingRules(); $('routing-result').textContent = '规则已更新，下次连接时使用新规则。'; }
  catch (error) { $('routing-result').textContent = '更新未完成，原有规则继续有效。'; throw error; }
});
