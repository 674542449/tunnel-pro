import {productVersion} from './product-version.mjs';
// Product interaction acceptance with a controlled native bridge; no window is rendered.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const require=createRequire(path.join(root,'tools/admin-ui-test/package.json'));
const {JSDOM}=require('jsdom');
const html=await fs.readFile(path.join(root,'desktop/ui/index.html'),'utf8');
const script=await fs.readFile(path.join(root,'desktop/ui/app.js'),'utf8');
const css=await fs.readFile(path.join(root,'desktop/ui/style.css'),'utf8');
const cases=[];
const idA='a'.repeat(32),idB='b'.repeat(32);
const now=Math.floor(Date.now()/1000);
const clone=value=>JSON.parse(JSON.stringify(value));
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function fixture(overrides={}){
 const dom=new JSDOM(html,{url:'https://desktop.example.test/',runScripts:'outside-only'});
 const w=dom.window,d=w.document,timers=[],calls=[],unhandled=[],readTimeouts=new Map();let timeoutID=1000000;
 const originalTimeout=w.setTimeout.bind(w),originalClearTimeout=w.clearTimeout.bind(w);
 w.setTimeout=(fn,ms,...args)=>{if(ms===20000){const id=++timeoutID;readTimeouts.set(id,fn);return id;}return originalTimeout(fn,ms,...args);};
 w.clearTimeout=id=>{if(readTimeouts.has(id))readTimeouts.delete(id);else originalClearTimeout(id);};
 w.addEventListener('unhandledrejection',e=>{unhandled.push(e.reason);e.preventDefault();});
 w.setInterval=fn=>{timers.push(fn);return timers.length;};w.clearInterval=()=>{};
 w.matchMedia=()=>({matches:false,addEventListener(){},removeEventListener(){}});
 if(w.HTMLDialogElement){w.HTMLDialogElement.prototype.showModal=function(){this.open=true;};w.HTMLDialogElement.prototype.close=function(){this.open=false;this.dispatchEvent(new w.Event('close'));};}
 const model={
  state:{version:productVersion,api_url:'https://portal.example.test',logged_in:true,email:'member@example.test',connected:false,connection_state:'disconnected',connection_stage:'disconnected',preferences:{selected_node_id:idB,system_proxy:false},health:{checked_at:0,last_success:0,consecutive_failures:0,error_kind:''},socks:'127.0.0.1:1080',http:'127.0.0.1:8088',logs:'isolated-logs',data_directory:'isolated-data',data_mode:'isolated',system_proxy:false},
  registration:true,commerce:{enabled:true,test_mode:true,invite_only:true,terms:'服务条款',privacy:'隐私政策',refund_policy:'退款政策'},
  user:{email:'member@example.test',active:true,expires_at:now+864000,upload:1024,download:1024,traffic_limit:1024**3,device_limit:3,role:'user',trial_used:false,email_verified:true},
  nodes:[{id:idA,name:'东京 · 一号',region:'日本',online:true},{id:idB,name:'洛杉矶 · 二号',region:'美国',online:true}],
  plans:[{id:'monthly',name:'月付套餐',kind:'subscription',days:30,price_cents:1999,currency:'cny',traffic_bytes:1024**3,devices:3},{id:'addon',name:'流量加购',kind:'traffic',days:30,price_cents:299,currency:'usd',traffic_bytes:512*1024**2,devices:3}],
  orders:[],release:{version:'v0.7.0',url:'https://downloads.example.test/client.zip',sha256:'a'.repeat(64),notes:'后续测试版本'},trial_hours:0,failures:{},connectGate:null,
  ...overrides
 };
 const bridge={
  Status:async()=>clone(model.state),
  Query:async(route,body)=>{calls.push(['Query',route,body]);if(model.failures[route])throw Error(model.failures[route]);if(route==='/api/public/settings')return {registration:model.registration,trial_hours:model.trial_hours};if(route==='/api/public/commerce')return clone({...model.commerce,plans:model.plans,release:model.release});if(route==='/api/me')return clone({user:model.user,commerce:model.commerce.enabled,announcement:'服务公告',trial_hours:model.trial_hours});if(route==='/api/nodes')return clone(model.nodes);if(route==='/api/plans')return clone(model.plans);if(route==='/api/orders'){if(body){const order={id:'order-'+model.orders.length,plan:clone(model.plans.find(p=>p.id===body.plan_id)),status:'pending',created_at:now,expires_at:now+1800,test:model.commerce.test_mode};model.orders.push(order);return clone(order);}return clone(model.orders);}if(route==='/api/release')return clone(model.release);if(route==='/api/trial'){model.user.active=true;return {ok:true};}throw Error('Unexpected route '+route);},
  LoginSecure:async(...values)=>{calls.push(['LoginSecure',...values]);if(model.failures.login)throw Error(model.failures.login);model.state.logged_in=true;model.state.email=values[0];},
  Logout:async()=>{calls.push(['Logout']);model.state.logged_in=false;model.state.connected=false;model.state.connection_state='disconnected';},
  SavePreferences:async(node,proxy)=>{calls.push(['SavePreferences',node,proxy]);if(model.failures.preferences)throw Error(model.failures.preferences);model.state.preferences={...model.state.preferences,selected_node_id:node,system_proxy:proxy};},
  SaveProxyMode:async(mode,exceptions)=>{calls.push(['SaveProxyMode',mode,exceptions]);if(model.failures.mode)throw Error(model.failures.mode);model.state.preferences={...model.state.preferences,proxy_mode:mode,direct_domains:exceptions.split(/[,\r\n]+/).filter(Boolean)};},
  UpdateRoutingRules:async()=>{calls.push(['UpdateRoutingRules']);if(model.failures.rules)throw Error(model.failures.rules);model.state.routing_rules={source:'updated',domains:111000,ip_prefixes:15000,updated_at:now};},
  Connect:async(node,proxy)=>{calls.push(['Connect',node,proxy]);model.state.connection_state='connecting';model.state.connection_stage='fetching_profile';if(model.connectGate)await model.connectGate;model.state.connected=true;model.state.connection_state='connected';model.state.connection_stage='connected';model.state.node_id=node;model.state.node_name=model.nodes.find(n=>n.id===node)?.name;model.state.connected_at=now;model.state.ech=true;model.state.system_proxy=proxy;},
  CancelConnect:async()=>{calls.push(['CancelConnect']);model.cancel?.();model.state.connected=false;model.state.connection_state='disconnected';model.state.connection_stage='disconnected';},
  Disconnect:async()=>{calls.push(['Disconnect']);model.state.connected=false;model.state.connection_state='disconnected';model.state.system_proxy=false;},
  Probe:async(node)=>{calls.push(['Probe',node]);if(model.failures.probe)throw Error(model.failures.probe);return 83;},
  OpenPortal:async()=>{calls.push(['OpenPortal']);if(model.failures.portal)throw Error(model.failures.portal);},
  SetTheme:async(value)=>{calls.push(['SetTheme',value]);if(model.failures.theme)throw Error('主题切换失败');},
  Minimize:async()=>{calls.push(['Minimize']);},
  Quit:async()=>{calls.push(['Quit']);if(model.failures.quit)throw Error(model.failures.quit);},
  RecoverProxy:async()=>{calls.push(['RecoverProxy']);if(model.failures.recovery)throw Error(model.failures.recovery);model.state.system_proxy=false;model.state.proxy_recovery_error='';},
  SetAPI:async(value)=>{calls.push(['SetAPI',value]);model.state.api_url=value;model.state.logged_in=false;model.state.email='';model.state.preferences.selected_node_id='';},
  ExportDiagnostics:async()=>{calls.push(['ExportDiagnostics']);return model.exportPath??'C:\\isolated\\support.json';},
  OpenDownload:async()=>{calls.push(['OpenDownload']);if(model.failures.download)throw Error(model.failures.download);}
 };
 w.go={main:{App:bridge}};
 const settle=async()=>{for(let i=0;i<12;i++)await sleep(0);};
 const tick=async()=>{for(const fn of timers)await fn();await settle();};
 const el=id=>{const value=d.getElementById(id);assert.ok(value,'Missing element '+id);return value;};
 const click=async id=>{const element=typeof id==='string'?el(id):id;element.click();await settle();};
 const input=async(id,value,event='input')=>{el(id).value=value;el(id).dispatchEvent(new w.Event(event,{bubbles:true}));await settle();};
 const visible=element=>!element.closest('[hidden]')&&(!element.closest('dialog')||element.closest('dialog').open);
 w.eval(script);await settle();
 return {dom,w,d,model,calls,bridge,el,click,input,tick,settle,visible,unhandled,expireReads:async()=>{const pending=[...readTimeouts.values()];readTimeouts.clear();pending.forEach(fn=>fn());await settle();},close:()=>dom.window.close()};
}

async function test(name,fn,overrides){const f=await fixture(overrides);try{await fn(f);assert.equal(f.unhandled.length,0);cases.push(name);}finally{f.close();}}

await test('four_distinct_pages_and_svg_controls_have_accessible_navigation',async f=>{
 for(const page of ['dashboard','nodes','subscription','settings']){await f.click('nav-'+page);assert.ok(f.el('nav-'+page).getAttribute('aria-current'));}
 assert.ok(f.d.querySelectorAll('svg').length>=8);assert.match(css,/prefers-reduced-motion/);assert.match(css,/:focus-visible/);
});
await test('closed_registration_does_not_offer_signup',async f=>{
 assert.equal(f.visible(f.el('register')),false);assert.equal(f.visible(f.el('auth')),true);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connected:false,connection_state:'disconnected',preferences:{selected_node_id:'',system_proxy:true}},registration:false});
await test('saved_node_and_proxy_preferences_are_used_by_one_click_connect',async f=>{
 await f.click('connect');assert.deepEqual(f.calls.find(c=>c[0]==='Connect'),['Connect',idB,false]);assert.match(f.el('state').textContent,/已连接/);
});
await test('node_search_filter_and_probe_use_real_selected_node',async f=>{
 await f.click('nav-nodes');await f.input('node-search','东京');assert.equal(f.d.querySelectorAll('[data-node-id]').length,1);
 await f.click(f.d.querySelector('[data-probe-id="'+idA+'"]'));assert.ok(f.calls.some(c=>c[0]==='Probe'&&c[1]===idA));assert.match(f.el('nodes').textContent,/83\s*ms/);
 await f.click(f.d.querySelector('[data-node-id="'+idA+'"]'));assert.ok(f.calls.some(c=>c[0]==='SavePreferences'&&c[1]===idA));
});
await test('no_nodes_has_actionable_empty_state_and_does_not_connect',async f=>{
 assert.ok(f.el('connect').disabled);await f.click('connect');assert.ok(!f.calls.some(c=>c[0]==='Connect'));assert.match(f.el('nodes').textContent,/暂无|没有|尚无/);
},{nodes:[]});
await test('connection_progress_can_be_cancelled_without_global_busy_deadlock',async f=>{
 f.model.connectGate=new Promise((_,reject)=>{f.model.cancel=()=>reject(Error('连接已取消'));});
 await f.click('connect');await f.tick();assert.ok(f.visible(f.el('cancel-connect')));assert.equal(f.el('cancel-connect').disabled,false);
 await f.click('cancel-connect');await f.tick();assert.equal(f.model.state.connected,false);assert.ok(f.calls.some(c=>c[0]==='CancelConnect'));assert.equal(f.el('connect').disabled,false);
});
await test('node_entry_health_failure_is_visible_while_disconnect_remains_available',async f=>{
 f.model.state.connected=true;f.model.state.connection_state='degraded';f.model.state.node_name='东京';f.model.state.health={checked_at:now,last_success:now-60,consecutive_failures:2,error_kind:'timeout'};await f.tick();
 assert.match(f.el('state').textContent,/异常|不稳|中断/);assert.equal(f.el('connect').disabled,false);await f.click('connect');assert.ok(f.calls.some(c=>c[0]==='Disconnect'));
});
await test('plan_purchase_requires_explicit_amount_confirmation_and_prevents_duplicate_orders',async f=>{
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));assert.equal(f.model.orders.length,0);assert.ok(f.el('purchase-dialog').open);assert.match(f.el('purchase-dialog').textContent,/19\.99/);
 await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);assert.ok(f.calls.some(c=>c[0]==='OpenPortal'));
 await f.click(f.d.querySelector('[data-plan-id="monthly"]'));if(f.el('purchase-dialog').open)await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);
});
await test('addon_and_test_mode_are_labelled_without_claiming_real_charge',async f=>{
 await f.click('nav-subscription');assert.match(f.el('plans').textContent,/2\.99/);assert.match(f.el('plans').textContent,/USD/);assert.match(f.el('plans').textContent,/当前.*周期/);
 await f.click(f.d.querySelector('[data-plan-id="monthly"]'));assert.match(f.el('purchase-dialog').textContent,/测试|模拟/);
});
await test('live_purchase_does_not_claim_that_no_money_is_charged',async f=>{
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));assert.doesNotMatch(f.el('purchase-dialog').textContent,/不会实际扣款|不会扣款|模拟购买/);
},{commerce:{enabled:true,test_mode:false,invite_only:false}});
await test('purchase_cancel_does_not_create_an_order',async f=>{
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));await f.click('cancel-purchase');assert.equal(f.model.orders.length,0);assert.equal(f.el('purchase-dialog').open,false);
});
await test('remote_purchase_changes_refresh_account_orders_and_nodes',async f=>{
 f.model.user.active=false;f.model.orders=[{id:'refund',plan:f.model.plans[0],status:'refunded'}];f.model.nodes=[f.model.nodes[0]];await f.click('refresh');
 assert.match(f.el('orders').textContent,/已退款/);assert.equal(f.d.querySelectorAll('[data-node-id]').length,1);assert.match(f.el('profile').textContent,/到期|未开通|不可用|额度/);
});
await test('portal_and_quit_failures_remain_visible_and_retryable',async f=>{
 f.model.failures.portal='门户打开失败';await f.click('portal');assert.match(f.el('message').textContent,/门户打开失败/);
 f.model.failures.quit='代理恢复失败';await f.click('quit');assert.match(f.el('message').textContent,/代理恢复失败/);assert.equal(f.el('quit').disabled,false);
});
await test('proxy_recovery_warning_has_working_retry',async f=>{
 f.model.state.system_proxy=true;f.model.state.proxy_recovery_error='其他程序仍在使用代理端口';await f.tick();assert.equal(f.el('proxy-warning').hidden,false);assert.ok(f.visible(f.el('recover-proxy')));
 f.model.failures.recovery='仍在使用代理端口';await f.click('recover-proxy');assert.match(f.el('message').textContent,/仍在使用/);delete f.model.failures.recovery;await f.click('recover-proxy');assert.equal(f.el('proxy-warning').hidden,true);
});
await test('manual_update_uses_validating_native_open_download_action',async f=>{
 await f.click('nav-settings');await f.click('updates');assert.match(f.el('release').textContent,/v0\.7\.0/);await f.click('download-update');assert.ok(f.calls.some(c=>c[0]==='OpenDownload'));
});
await test('support_export_cancel_does_not_report_a_saved_file',async f=>{
 await f.click('nav-settings');f.model.exportPath='';await f.click('export-diagnostics');assert.ok(f.calls.some(c=>c[0]==='ExportDiagnostics'));assert.doesNotMatch(f.el('message').textContent,/已保存|导出成功/);
});
await test('untrusted_node_and_plan_names_are_rendered_as_text',async f=>{
 assert.equal(f.d.querySelector('img[src="x"]'),null);assert.match(f.el('nodes').textContent,/<img/);assert.match(f.el('plans').textContent,/<img/);
},{nodes:[{id:idA,name:'<img src="x" onerror="alert(1)">',region:'测试',online:true}],plans:[{id:'monthly',name:'<img src="x" onerror="alert(1)">',kind:'subscription',days:30,price_cents:100,currency:'cny'}]});
await test('expired_session_clears_prior_account_and_returns_to_login',async f=>{
 f.model.state.logged_in=false;f.model.state.email='';await f.tick();assert.ok(f.visible(f.el('auth')));assert.doesNotMatch(f.el('orders').textContent,/月付套餐/);assert.doesNotMatch(f.el('profile').textContent,/member@example/);
});
await test('cancel_before_native_connect_starts_tears_down_its_late_success',async f=>{
 let release;
 f.bridge.Connect=async(node,proxy)=>{f.calls.push(['Connect',node,proxy]);await new Promise(resolve=>{release=resolve;});f.model.state.connected=true;f.model.state.connection_state='connected';f.model.state.node_id=node;};
 f.bridge.CancelConnect=async()=>{f.calls.push(['CancelConnect']);};
 await f.click('connect');await f.click('cancel-connect');assert.equal(f.el('connect').disabled,true,'Do not allow a new connection before the cancelled invocation settles');
 release();await f.settle();await f.tick();assert.equal(f.model.state.connected,false);assert.ok(f.calls.some(c=>c[0]==='Disconnect'));
});
await test('definite_rejected_purchase_can_retry_after_permissions_are_fixed',async f=>{
 const query=f.bridge.Query;let rejected=false;
 f.bridge.Query=async(route,body)=>{if(route==='/api/orders'&&body&&!rejected){rejected=true;throw Error('control request failed: HTTP 403');}return query(route,body);};
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));await f.click('confirm-purchase');assert.equal(f.model.orders.length,0);
 await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);
});
await test('lost_order_creation_response_is_reconciled_without_duplicate_post',async f=>{
 const query=f.bridge.Query;let lost=false,posts=0;
 f.bridge.Query=async(route,body)=>{const result=await query(route,body);if(route==='/api/orders'&&body){posts++;if(!lost){lost=true;throw Error('network timeout');}}return result;};
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);
 await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);assert.equal(posts,1);
});
await test('failed_order_read_never_triggers_a_blind_purchase',async f=>{
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));f.model.failures['/api/orders']='network timeout';await f.click('confirm-purchase');
 assert.equal(f.model.orders.length,0);assert.match(f.el('purchase-error').textContent,/未创建|无法核实/);
});
await test('unknown_commercial_mode_blocks_purchase_instead_of_guessing',async f=>{
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));if(f.el('purchase-dialog').open)await f.click('confirm-purchase');assert.equal(f.model.orders.length,0);assert.equal(f.el('load-alert').hidden,false);
},{failures:{'/api/public/commerce':'network timeout'}});
await test('older_release_does_not_offer_a_downgrade',async f=>{
 f.model.release.version='v0.5.1';await f.click('nav-settings');await f.click('updates');assert.match(f.el('release').textContent,/较新|无需降级/);assert.equal(f.el('download-update').hidden,true);
});
await test('invalid_release_link_has_no_download_action',async f=>{
 f.model.release.url='javascript:alert(1)';await f.click('nav-settings');await f.click('updates');assert.equal(f.el('download-update').hidden,true);assert.match(f.el('release').textContent,/不完整|无效/);
});
await test('failed_node_refresh_retains_data_but_blocks_connect_and_can_retry',async f=>{
 f.model.failures['/api/nodes']='network timeout';await f.click('refresh');assert.equal(f.el('load-alert').hidden,false);assert.equal(f.el('connect').disabled,true);
 delete f.model.failures['/api/nodes'];await f.click('retry-load');assert.equal(f.el('load-alert').hidden,true);assert.equal(f.el('connect').disabled,false);
});
await test('preference_write_failure_restores_previous_checkbox_value',async f=>{
 await f.click('nav-settings');f.model.failures.preferences='本机偏好保存失败';f.el('system-proxy').checked=true;f.el('system-proxy').dispatchEvent(new f.w.Event('change',{bubbles:true}));await f.settle();assert.equal(f.el('system-proxy').checked,false);assert.match(f.el('message').textContent,/保存失败/);
});
await test('api_switch_clears_private_content_and_refreshes_public_policy',async f=>{
 await f.click('nav-settings');await f.input('api-url','https://new-portal.example.test');await f.click('save-api');assert.equal(f.model.state.logged_in,false);assert.ok(f.visible(f.el('auth')));assert.equal(f.el('profile').textContent,'');assert.equal(f.el('nodes').textContent,'');assert.ok(f.calls.some(c=>c[0]==='SetAPI'));
});
await test('guest_password_reveal_mfa_and_login_clear_sensitive_fields',async f=>{
 await f.input('email','buyer@example.test');await f.input('password','fixture-password-123');await f.click('toggle-password');assert.equal(f.el('password').type,'text');await f.click('toggle-password');assert.equal(f.el('password').type,'password');
 await f.click('show-otp');assert.equal(f.el('otp-wrap').hidden,false);await f.input('otp','123456');await f.click('login-submit');assert.ok(f.calls.some(c=>c[0]==='LoginSecure'&&c[3]==='123456'));assert.equal(f.el('password').value,'');assert.equal(f.el('otp').value,'');assert.equal(f.model.state.logged_in,true);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connected:false,connection_state:'disconnected',preferences:{selected_node_id:'',system_proxy:true}}});
await test('public_policy_failure_keeps_signup_closed_and_has_retry',async f=>{
 assert.equal(f.visible(f.el('register')),false);assert.equal(f.el('load-alert').hidden,false);delete f.model.failures['/api/public/settings'];await f.click('retry-load');assert.equal(f.visible(f.el('register')),true);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connected:false,connection_state:'disconnected',preferences:{selected_node_id:'',system_proxy:true}},failures:{'/api/public/settings':'network timeout'}});
await test('guest_can_check_and_download_a_public_release',async f=>{
 await f.click('guest-settings');await f.click('updates');assert.match(f.el('release').textContent,/v0\.7\.0/);await f.click('download-update');assert.ok(f.calls.some(c=>c[0]==='OpenDownload'));
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connected:false,connection_state:'disconnected',preferences:{selected_node_id:'',system_proxy:true}}});
await test('reused_pending_order_confirms_its_original_amount_snapshot',async f=>{
 f.model.orders=[{id:'older-price',plan:{...f.model.plans[0],price_cents:4999},status:'pending',created_at:now-100,expires_at:now+1000,test:true}];
 await f.click('refresh');await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));assert.match(f.el('purchase-details').textContent,/49\.99/);
 await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);assert.ok(f.calls.some(c=>c[0]==='OpenPortal'));
});
await test('changed_server_order_snapshot_requires_reconfirmation_before_handoff',async f=>{
 const query=f.bridge.Query;
 f.bridge.Query=async(route,body)=>{const result=await query(route,body);if(route==='/api/orders'&&body){result.plan.price_cents=3999;f.model.orders[f.model.orders.length-1].plan.price_cents=3999;}return result;};
 await f.click('nav-subscription');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));assert.match(f.el('purchase-details').textContent,/19\.99/);await f.click('confirm-purchase');
 assert.equal(f.calls.filter(c=>c[0]==='OpenPortal').length,0);assert.ok(f.el('purchase-dialog').open);assert.match(f.el('purchase-details').textContent,/39\.99/);await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);assert.ok(f.calls.some(c=>c[0]==='OpenPortal'));
});
await test('settings_draft_survives_blur_and_status_poll',async f=>{
 await f.click('nav-settings');await f.input('api-url','https://new-portal.example.test/control/');f.el('api-url').blur();await f.tick();
 assert.equal(f.el('api-url').value,'https://new-portal.example.test/control/','Status polling must not overwrite an unsaved setting');
});
await test('saving_unchanged_service_preserves_current_session',async f=>{
 await f.click('nav-settings');await f.click('save-api');
 assert.equal(f.model.state.logged_in,true);assert.equal(f.calls.filter(c=>c[0]==='SetAPI').length,0);
});
await test('commercial_service_never_offers_unsupported_trial',async f=>{
 f.model.user.active=false;f.model.trial_hours=24;await f.click('refresh');assert.equal(f.el('trial').hidden,true);
});
await test('connection_waits_until_node_preference_write_finishes',async f=>{
 let finish;const save=f.bridge.SavePreferences;
 f.bridge.SavePreferences=async(...args)=>{await new Promise(resolve=>{finish=resolve;});return save(...args);};
 await f.click(f.d.querySelector('[data-node-id="'+idA+'"]'));assert.equal(f.el('connect').disabled,true);
 await f.click('connect');assert.equal(f.calls.filter(c=>c[0]==='Connect').length,0);
 finish();await f.settle();await f.click('connect');assert.equal(f.calls.find(c=>c[0]==='Connect')[1],idA);
});
await test('registration_is_blocked_until_invitation_policy_is_known',async f=>{
 assert.equal(f.el('register').hidden,true,'Do not offer registration without knowing mandatory invitation policy');
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connected:false,connection_state:'disconnected',preferences:{}},failures:{'/api/public/commerce':'network timeout'}});
await test('failed_service_save_preserves_draft_and_current_account',async f=>{
 f.bridge.SetAPI=async()=>{throw Error('配置文件写入失败');};
 await f.click('nav-settings');await f.input('api-url','https://new-portal.example.test/control/');await f.click('save-api');await f.tick();
 assert.equal(f.el('api-url').value,'https://new-portal.example.test/control/');assert.equal(f.model.state.logged_in,true);assert.match(f.el('message').textContent,/写入失败/);
});
await test('noncommercial_trial_and_manual_order_follow_controller_rules',async f=>{
 f.model.user.active=false;f.model.trial_hours=24;f.model.commerce.enabled=false;await f.click('refresh');
 assert.equal(f.el('trial').hidden,false);assert.equal(f.d.querySelector('[data-plan-id="monthly"]').disabled,false);
 f.model.commerce.test_mode=false;await f.click('refresh');await f.click(f.d.querySelector('[data-plan-id="monthly"]'));assert.match(f.el('purchase-mode').textContent,/人工核实/);await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);assert.match(f.el('message').textContent,/管理员核实/);
 await f.click('trial');assert.equal(f.model.user.active,true);assert.equal(f.el('trial').hidden,true);
});
await test('registration_sends_required_invitation_and_can_retry_rejection',async f=>{
 await f.click('register');assert.equal(f.el('invite').required,true);
 await f.input('email','new@example.test');await f.input('password','fixture-password-123');await f.input('invite','fixture-invitation');
 f.model.failures.login='control request failed: HTTP 400';await f.click('login-submit');assert.equal(f.model.state.logged_in,false);assert.equal(f.el('login-submit').disabled,false);
 delete f.model.failures.login;await f.click('login-submit');const call=f.calls.filter(c=>c[0]==='LoginSecure').at(-1);assert.equal(call[4],'fixture-invitation');assert.equal(call[5],true);assert.equal(f.model.state.logged_in,true);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connected:false,connection_state:'disconnected',preferences:{}}});
await test('default_node_selection_never_writes_over_user_preferences_in_background',async f=>{
 assert.equal(f.calls.filter(c=>c[0]==='SavePreferences').length,0);assert.equal(f.el('connect').disabled,false);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:true,connected:false,connection_state:'disconnected',preferences:{selected_node_id:'',system_proxy:false}}});
await test('disconnect_failure_remains_actionable_and_retry_succeeds',async f=>{
 await f.click('connect');const disconnect=f.bridge.Disconnect;f.bridge.Disconnect=async()=>{throw Error('代理恢复失败');};
 await f.click('connect');assert.match(f.el('message').textContent,/代理恢复失败/);assert.equal(f.el('connect').disabled,false);
 f.bridge.Disconnect=disconnect;await f.click('connect');assert.equal(f.model.state.connected,false);
});
await test('node_specific_access_overrides_aggregate_account_summary',async f=>{
 f.model.nodes[1].access={allowed:false,reason:'套餐不包含此线路'};await f.click('refresh');assert.equal(f.el('connect').disabled,true);assert.match(f.el('connection-hint').textContent,/套餐不包含/);
 f.model.user.active=false;f.model.nodes[0].access={allowed:true};await f.click('refresh');await f.click(f.d.querySelector('[data-node-id="'+idA+'"]'));assert.equal(f.el('connect').disabled,false);await f.click('connect');assert.equal(f.model.state.connected,true);
});
await test('changed_plan_node_scope_requires_reconfirmation',async f=>{
 await f.click(f.d.querySelector('[data-plan-id="monthly"]'));f.model.plans[0].node_ids=[idA];await f.click('confirm-purchase');assert.equal(f.model.orders.length,0);assert.match(f.el('purchase-details').textContent,/东京/);await f.click('confirm-purchase');assert.equal(f.model.orders.length,1);
});
await test('published_prerelease_and_build_metadata_are_comparable',async f=>{
 f.model.release.version='v0.7.0-rc.2+build.7';await f.click('updates');assert.equal(f.el('download-update').hidden,false);
 f.model.release.version=f.model.state.version+'-rc.9';await f.click('updates');assert.equal(f.el('download-update').hidden,true);
 f.model.release.version=f.model.state.version+'+build.7';await f.click('updates');assert.match(f.el('release').textContent,/一致/);
});
await test('controller_access_changes_reach_idle_client_without_manual_refresh',async f=>{
 const current=f.w.Date.now();f.w.Date.now=()=>current+61000;
 f.model.nodes[1].access={allowed:false,reason:'当前套餐已失效'};await f.tick();
 assert.equal(f.el('connect').disabled,true);assert.match(f.el('connection-hint').textContent,/已失效/);
});
await test('desktop_shell_has_one_native_titlebar_and_persistent_status_footer',async f=>{
 assert.equal(f.d.querySelector('.appbar'),null);assert.equal(f.d.querySelector('.auth-story'),null);
 assert.ok(f.d.querySelector('.statusbar'));assert.equal(f.el('page-title').textContent,'连接');
 await f.click('connect');assert.equal(f.el('footer-connection').textContent,'已连接');assert.match(f.el('footer-node').textContent,/洛杉矶/);
 f.el('app-menu').open=true;await f.click('hide');assert.ok(f.calls.some(c=>c[0]==='Minimize'));
});
await test('theme_updates_native_window_and_persists_only_local_appearance',async f=>{
 await f.click('nav-settings');await f.input('theme-select','dark','change');
 assert.equal(f.d.documentElement.dataset.theme,'dark');assert.equal(f.w.localStorage.getItem('tunnelx.theme'),'dark');
 assert.ok(f.calls.some(c=>c[0]==='SetTheme'&&c[1]==='dark'));
 await f.input('theme-select','system','change');assert.equal(f.d.documentElement.dataset.theme,'system');
 assert.equal(f.calls.filter(c=>c[0]==='SavePreferences').length,0);
});
await test('native_theme_failure_rolls_back_visual_selection',async f=>{
 f.model.failures.theme=true;await f.click('nav-settings');await f.input('theme-select','dark','change');
 assert.equal(f.el('theme-select').value,'system');assert.equal(f.d.documentElement.dataset.theme,'system');assert.equal(f.w.localStorage.getItem('tunnelx.theme'),null);
 assert.match(f.el('message').textContent,/主题切换失败/);
});
await test('node_sorting_uses_measured_latency_without_changing_selected_node',async f=>{
 await f.click('nav-nodes');f.bridge.Probe=async id=>id===idA?200:50;
 await f.click(f.d.querySelector('[data-probe-id="'+idA+'"]'));await f.click(f.d.querySelector('[data-probe-id="'+idB+'"]'));
 await f.input('node-sort','latency','change');assert.equal(f.d.querySelector('[data-node-id]').dataset.nodeId,idB);
 await f.input('node-sort','name','change');assert.equal(f.d.querySelector('[data-node-id]').dataset.nodeId,idA);
 assert.equal(f.calls.filter(c=>c[0]==='SavePreferences').length,0);
});
await test('keyboard_navigation_preserves_selection_until_button_is_activated',async f=>{
 f.d.dispatchEvent(new f.w.KeyboardEvent('keydown',{key:'f',ctrlKey:true,bubbles:true,cancelable:true}));assert.equal(f.d.activeElement,f.el('node-search'));
 const rows=[...f.d.querySelectorAll('[data-node-id]')];rows[0].focus();rows[0].dispatchEvent(new f.w.KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true}));
 assert.equal(f.d.activeElement.dataset.nodeId,idB);assert.equal(f.calls.filter(c=>c[0]==='SavePreferences').length,0);
 await f.input('node-sort','name','change');assert.equal(f.d.activeElement.dataset.nodeId,idB);
 f.d.dispatchEvent(new f.w.KeyboardEvent('keydown',{key:'4',ctrlKey:true,bubbles:true,cancelable:true}));assert.equal(f.el('page-settings').hidden,false);
});
await test('node_context_menu_selects_probes_and_escape_restores_focus',async f=>{
 await f.click('nav-nodes');const open=()=>f.d.querySelector('[data-node-id="'+idA+'"]').dispatchEvent(new f.w.MouseEvent('contextmenu',{bubbles:true,cancelable:true,clientX:200,clientY:200}));
 open();assert.equal(f.el('node-menu').hidden,false);await f.click('node-menu-select');assert.ok(f.calls.some(c=>c[0]==='SavePreferences'&&c[1]===idA));
 open();await f.click('node-menu-probe');assert.ok(f.calls.some(c=>c[0]==='Probe'&&c[1]===idA));
 open();f.d.dispatchEvent(new f.w.KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));assert.equal(f.el('node-menu').hidden,true);assert.equal(f.d.activeElement.dataset.nodeId,idA);
});
await test('node_context_menu_cannot_probe_denied_node',async f=>{
 await f.click('nav-nodes');f.d.querySelector('[data-node-id="'+idA+'"]').dispatchEvent(new f.w.MouseEvent('contextmenu',{bubbles:true,cancelable:true}));
 assert.equal(f.el('node-menu-probe').disabled,true);await f.click('node-menu-probe');assert.equal(f.calls.filter(c=>c[0]==='Probe').length,0);
},{nodes:[{id:idA,name:'无权限节点',region:'日本',online:true,access:{allowed:false,reason:'套餐未包含'}}]});

await test('guest_service_failure_has_direct_retry_and_recovers_registration',async f=>{
 assert.equal(f.el('auth').hidden,false);assert.equal(f.el('auth-retry').hidden,false);assert.equal(f.el('auth-retry').disabled,false);
 assert.match(f.el('auth-service').textContent,/自动重试/);assert.match(f.el('load-error').textContent,/DNS/);assert.equal(f.el('register').hidden,true);
 f.model.failures={};await f.click('auth-retry');assert.equal(f.el('auth-retry').hidden,true);assert.equal(f.el('register').hidden,false);assert.equal(f.el('load-alert').hidden,true);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connection_state:'disconnected'},failures:{'/api/public/settings':'无法解析管理服务域名，请检查 DNS 或服务地址后重新加载。','/api/public/commerce':'无法解析管理服务域名，请检查 DNS 或服务地址后重新加载。'}});

await test('missing_native_read_callback_times_out_unlocks_retry_and_ignores_late_reply',async f=>{
 const originalQuery=f.bridge.Query;const pending=[];
 f.bridge.Query=(route,body)=>route.startsWith('/api/public/')?new Promise(resolve=>pending.push(resolve)):originalQuery(route,body);
 const refresh=f.el('retry-load').onclick();await f.settle();assert.equal(f.el('auth-retry').disabled,true);assert.equal(pending.length,2);
 await f.expireReads();await refresh;assert.equal(f.el('auth-retry').hidden,false);assert.equal(f.el('auth-retry').disabled,false);assert.match(f.el('load-error').textContent,/未响应/);
 f.bridge.Query=originalQuery;await f.click('auth-retry');assert.equal(f.el('load-alert').hidden,true);assert.equal(f.el('register').hidden,false);
 pending.forEach(resolve=>resolve({registration:false,enabled:false}));await f.settle();assert.equal(f.el('register').hidden,false);assert.equal(f.el('load-alert').hidden,true);
},{state:{version:productVersion,api_url:'https://portal.example.test',logged_in:false,connection_state:'disconnected'}});

await test('exhausted_traffic_keeps_subscription_expiry_and_shows_current_period_usage',async f=>{
 assert.equal(f.el('summary-state').textContent,'流量耗尽');assert.match(f.el('summary-expiry').textContent,/到期/);assert.doesNotMatch(f.el('summary-expiry').textContent,/开通/);assert.equal(f.el('summary-remaining').textContent,'0 B');assert.equal(f.el('quota-progress').value,1073741824);assert.equal(f.el('quota-progress').max,1073741824);assert.match(f.el('profile').textContent,/订阅仍在有效期内/);
},{user:{email:'fixture@example.test',active:false,expires_at:now+86400,traffic_limit:999999999,upload:888888888,download:777777777,traffic_exhausted:true,subscription_status:'valid',traffic:{total_bytes:1073741824,used_bytes:1073741824,remaining_bytes:0,unlimited:false}}});

await test('traffic_chart_uses_native_rates_once_per_second_and_keeps_only_sixty_seconds',async f=>{
 Object.defineProperty(f.d,'visibilityState',{configurable:true,get:()=> 'visible'});
 let time=Date.now();f.w.Date.now=()=>time;
 Object.assign(f.model.state,{connected:true,connection_state:'connected',connected_at:now,node_id:idB,download_rate:2048,upload_rate:1024});
 await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'1');assert.equal(f.el('connection-visual').dataset.state,'connected');
 await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'1','Repeated renders must not fabricate samples');
 time+=1000;f.model.state.download_rate=4096;await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'2');assert.equal(f.el('traffic-empty').hidden,true);
 assert.match(f.el('traffic-down').getAttribute('d'),/L588\.0,16\.0$/);assert.match(f.el('download').textContent,/4.0 KiB\/s/);
 for(let i=0;i<65;i++){time+=1000;await f.tick();}
 assert.equal(f.el('traffic-chart').dataset.samples,'60');assert.doesNotMatch(f.el('traffic-down').getAttribute('d'),/NaN|Infinity/);
});
await test('traffic_chart_leaves_missing_measurements_as_gaps_and_clears_on_new_connection',async f=>{
 Object.defineProperty(f.d,'visibilityState',{configurable:true,get:()=> 'visible'});
 let time=Date.now();f.w.Date.now=()=>time;
 Object.assign(f.model.state,{connected:true,connection_state:'connected',connected_at:now,node_id:idB,download_rate:2048,upload_rate:1024});
 await f.tick();time+=1000;delete f.model.state.download_rate;await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'1');assert.match(f.el('traffic-empty').textContent,/有效网速/);
 time+=1000;f.model.state.download_rate=0;await f.tick();const path=f.el('traffic-down').getAttribute('d');assert.equal((path.match(/M/g)||[]).length,2);assert.ok(!path.includes('L'),'Missing sample must not be connected with a made-up line');
 time+=1000;f.model.state.node_id=idA;f.model.state.connected_at=now+3;await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'1');
 f.model.state.connected=false;f.model.state.connection_state='disconnected';await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'0');assert.equal(f.el('traffic-down').getAttribute('d'),'');
});
await test('traffic_chart_does_not_keep_sampling_when_hidden_or_after_status_failure',async f=>{
 let hidden=false;Object.defineProperty(f.d,'visibilityState',{configurable:true,get:()=>hidden?'hidden':'visible'});
 let time=Date.now();f.w.Date.now=()=>time;
 Object.assign(f.model.state,{connected:true,connection_state:'connected',connected_at:now,node_id:idB,download_rate:0,upload_rate:0});
 await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'1');
 hidden=true;f.d.dispatchEvent(new f.w.Event('visibilitychange'));time+=1000;await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'0');assert.equal(f.d.documentElement.dataset.suspended,'true');
 hidden=false;time+=1000;await f.tick();assert.equal(f.el('traffic-chart').dataset.samples,'1');
 f.bridge.Status=async()=>{throw Error('Status unavailable');};time+=1000;await f.tick();assert.equal(f.el('traffic-down').getAttribute('d'),'');assert.match(f.el('traffic-empty').textContent,/未能刷新/);
});
await test('connection_motion_follows_connecting_and_degraded_states_and_busy_controls_are_accessible',async f=>{
 f.model.state.connection_state='connecting';await f.tick();assert.equal(f.el('connection-visual').dataset.state,'connecting');
 Object.assign(f.model.state,{connected:true,connection_state:'degraded'});await f.tick();assert.equal(f.el('connection-visual').dataset.state,'degraded');assert.equal(f.el('connect').disabled,false);
 let release;const gate=new Promise(resolve=>release=resolve);f.bridge.ExportDiagnostics=async()=>{await gate;return '';};
 const pending=f.el('export-diagnostics').onclick();await f.settle();assert.equal(f.el('export-diagnostics').getAttribute('aria-busy'),'true');release();await pending;assert.equal(f.el('export-diagnostics').getAttribute('aria-busy'),'false');
 assert.match(css,/prefers-reduced-motion:reduce/);assert.match(css,/animation-play-state:paused!important/);
});


await test('three_proxy_modes_persist_and_survive_node_changes',async f=>{
 assert.equal(f.d.querySelectorAll('input[name="proxy-mode"]').length,3);
 await f.click(f.d.querySelector('input[value="bypass_cn"]'));assert.equal(f.model.state.preferences.proxy_mode,'bypass_cn');
 await f.click('nav-nodes');await f.click(f.d.querySelector('[data-node-id="'+idA+'"]'));assert.equal(f.model.state.preferences.proxy_mode,'bypass_cn');
 await f.click('nav-dashboard');await f.click(f.d.querySelector('input[value="tun"]'));assert.match(f.el('mode-help').textContent,/管理员/);assert.ok(f.el('system-proxy').disabled);
 await f.click(f.d.querySelector('input[value="global"]'));assert.equal(f.el('system-proxy').disabled,false);
});
await test('mode_failure_rolls_back_and_live_connection_locks_controls',async f=>{
 f.model.failures.mode='模式保存失败';await f.click(f.d.querySelector('input[value="tun"]'));assert.ok(f.d.querySelector('input[value="global"]').checked);assert.match(f.el('message').textContent,/模式保存失败/);
 delete f.model.failures.mode;await f.click('connect');assert.ok([...f.d.querySelectorAll('input[name="proxy-mode"]')].every(x=>x.disabled));assert.ok(f.el('save-routing').disabled);
 await f.click('connect');assert.ok([...f.d.querySelectorAll('input[name="proxy-mode"]')].every(x=>!x.disabled));
});
await test('direct_exceptions_and_rules_update_have_native_actions_and_failure_feedback',async f=>{
 await f.click('nav-settings');await f.input('direct-domains','qq.com\n192.168.0.0/16');await f.click('save-routing');assert.equal(f.calls.find(c=>c[0]==='SaveProxyMode')[2],'qq.com\n192.168.0.0/16');
 await f.click('update-rules');assert.ok(f.calls.some(c=>c[0]==='UpdateRoutingRules'));assert.match(f.el('routing-result').textContent,/下次连接/);assert.match(f.el('routing-info').textContent,/111,000/);
 f.model.failures.rules='规则源暂时不可用';await f.click('update-rules');assert.match(f.el('routing-result').textContent,/原有规则/);
});

await test('tun_ipv4_fallback_policy_is_visible_and_probe_stage_is_truthful',async f=>{
 f.model.state.preferences.proxy_mode='tun';f.model.state.tun={elevated:true,assets_ready:true,ipv6_available:false};f.model.state.connected=true;f.model.state.connection_state='connected';await f.tick();assert.match(f.el('mode-help').textContent,/IPv6.*不可用.*IPv4/);
 f.model.state.connected=false;f.model.state.connection_state='connecting';f.model.state.connection_stage='checking_tun_network';await f.tick();assert.match(f.el('duration').textContent,/IPv6/);
});
await test('layered_health_distinguishes_dns_from_gateway_and_egress',async f=>{
 Object.assign(f.model.state,{connected:true,connection_state:'degraded',health:{checked_at:now,error_kind:'dns_unavailable',consecutive_failures:1,gateway:{checked_at:now,error_kind:''},dns:{checked_at:now,error_kind:'dns_resolution'},egress:{checked_at:now,error_kind:''}}});
 await f.tick();assert.match(f.el('state').textContent,/DNS/);assert.match(f.el('network-health').textContent,/节点入口 · 正常/);assert.match(f.el('network-health').textContent,/DNS 解析 · 异常/);assert.match(f.el('connection-hint').textContent,/备用解析/);
 f.model.state.health.error_kind='egress_unavailable';f.model.state.health.dns.error_kind='';f.model.state.health.egress.error_kind='egress_https';await f.tick();assert.match(f.el('state').textContent,/出网异常/);
});
await test('automatic_network_recovery_is_visible_and_can_be_cancelled',async f=>{
 Object.assign(f.model.state,{connected:false,connection_state:'connecting',connection_stage:'reconnecting',recovering:true,recovery_attempt:3});await f.tick();assert.match(f.el('state').textContent,/恢复/);assert.match(f.el('duration').textContent,/自动恢复/);assert.match(f.el('connection-hint').textContent,/3 次/);assert.equal(f.el('cancel-connect').hidden,false);await f.click('cancel-connect');assert.ok(f.calls.some(c=>c[0]==='CancelConnect'));
});
const report={passed:true,checked_at:new Date().toISOString(),desktop_version:productVersion,visual_rendered:false,bridge:'controlled native bridge with asynchronous responses; native engine and binary acceptance are separate',cases};
await fs.writeFile(path.join(root,'docs/desktop-product-ui-'+productVersion+'.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(report));

