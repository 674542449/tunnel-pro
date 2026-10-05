import {productVersion} from './product-version.mjs';
// Execute the shipped Wails IPC and production runtime through real script elements.
// Only the native Go boundary is controlled; window.go bindings and callbacks are Wails' own.
// No GUI is opened, no real credentials are used and no system proxy is modified.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const require=createRequire(path.join(root,'tools/admin-ui-test/package.json'));
const {JSDOM,ResourceLoader,VirtualConsole}=require('jsdom');
const [html,script,ipc,runtime,main]=await Promise.all([
 'desktop/ui/index.html','desktop/ui/app.js',
 'desktop/vendor/github.com/wailsapp/wails/v2/internal/frontend/runtime/ipc.js',
 'desktop/vendor/github.com/wailsapp/wails/v2/internal/frontend/runtime/runtime_prod_desktop.js','desktop/main.go'
].map(file=>fs.readFile(path.join(root,file),'utf8')));
const methods=[...main.matchAll(/func \(a \*App\) (\w+)\(/g)].map(match=>match[1]);
const bindingJSON=JSON.stringify({main:{App:Object.fromEntries(methods.map(method=>[method,{}]))}});
const runtimeAsset='window.wailsbindings='+JSON.stringify(bindingJSON)+';\n'+runtime;
const clone=value=>JSON.parse(JSON.stringify(value));
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const cases=[];

async function fixture({loggedIn=false,lateRuntime=false,failInitialStatus=false,failNodes=false}={}) {
 const nativeCalls=[],scriptErrors=[],intervals=[];
 const model={loggedIn,email:'fixture@example.test',holdNextStatus:false,heldStatus:null,failNextStatus:failInitialStatus,failNodes,clock:Date.now()};
 const node={id:'a'.repeat(32),name:'测试节点 · 东京',online:true,region:'日本'};
 const state=()=>({version:productVersion,api_url:'https://fixture.example.test',logged_in:model.loggedIn,email:model.loggedIn?model.email:'',connected:false,connection_state:'disconnected',preferences:{selected_node_id:node.id,system_proxy:false}});
 const respond=(win,request,result,error)=>{
  const payload={callbackid:request.callbackID};
  if(error)payload.error=String(error);else payload.result=result;
  // Mirrors native Callback delivery with a JSON payload; exercises global lexical scope.
  win.eval('window.wails.Callback('+JSON.stringify(JSON.stringify(payload))+');');
 };
 const nativeInvoke=(win,message)=>{
  if(!message.startsWith('C'))return;
  const request=JSON.parse(message.slice(1));nativeCalls.push(request);
  const method=request.name.split('.').at(-1),args=request.args;
  if(method==='Status'){
   const snapshot=clone(state());
   if(model.holdNextStatus){model.holdNextStatus=false;model.heldStatus=()=>respond(win,request,snapshot);return;}
   if(model.failNextStatus){model.failNextStatus=false;queueMicrotask(()=>respond(win,request,null,'本机状态暂时不可用'));return;}
   queueMicrotask(()=>respond(win,request,snapshot));return;
  }
  let result=null,error='';
  if(method==='Query'){
   const route=args[0];
   if(route==='/api/public/settings')result={registration:true,trial_hours:0};
   else if(route==='/api/public/commerce')result={enabled:true,test_mode:true,invite_only:false};
   else if(!model.loggedIn)error='control request failed: HTTP 401';
   else if(route==='/api/me')result={user:{email:model.email,active:true,expires_at:Math.floor(Date.now()/1000)+86400,traffic_limit:1073741824,device_limit:3},commerce:true};
   else if(route==='/api/nodes'){if(model.failNodes)error='control request failed: HTTP 503';else result=[node];}
   else if(route==='/api/plans'||route==='/api/orders')result=[];
   else error='Unexpected Query '+route;
  } else if(method==='LoginSecure'){model.loggedIn=true;model.email=args[0];}
  else if(method==='Logout')model.loggedIn=false;
  else if(method==='SavePreferences'||method==='SetTheme')result=null;
  else error='Unexpected native method '+method;
  queueMicrotask(()=>respond(win,request,result,error));
 };
 class Assets extends ResourceLoader {
  fetch(url){const name=new URL(url).pathname;if(name==='/wails/ipc.js')return Promise.resolve(Buffer.from(ipc));if(name==='/wails/runtime.js')return Promise.resolve(Buffer.from(runtimeAsset));if(name==='/app.js')return Promise.resolve(Buffer.from(script));if(name==='/style.css')return Promise.resolve(Buffer.from(''));return null;}
 }
 // Wails pkg/assetserver/common.go prepends runtime then IPC, yielding IPC -> runtime -> application.
 const servedHTML=html.replace('<head>','<head><script src="/wails/ipc.js"></script>'+(lateRuntime?'':'<script src="/wails/runtime.js"></script>'));
 const virtualConsole=new VirtualConsole();virtualConsole.on('jsdomError',error=>scriptErrors.push(error.message));
 const dom=new JSDOM(servedHTML,{url:'http://wails.localhost/',runScripts:'dangerously',resources:new Assets(),virtualConsole,beforeParse(win){
  win.chrome={webview:{postMessage(message){nativeInvoke(win,message);}}};
  win.Date.now=()=>model.clock;
  win.setInterval=fn=>{intervals.push(fn);return intervals.length;};win.clearInterval=()=>{};
  if(win.HTMLDialogElement){win.HTMLDialogElement.prototype.showModal=function(){this.open=true;};win.HTMLDialogElement.prototype.close=function(){this.open=false;};}
 }});
 const win=dom.window,document=win.document;
 const settle=async()=>{for(let i=0;i<12;i++)await pause(0);};
 await new Promise(resolve=>win.addEventListener('load',resolve,{once:true}));await settle();
 const el=id=>document.getElementById(id);
 const tick=async()=>{for(const interval of intervals)await interval();await settle();};
 const click=async id=>{el(id).click();await settle();};
 const login=async()=>{el('email').value=model.email;el('password').value='fixture-password-123';el('login-submit').click();await settle();};
 return {dom,win,model,nativeCalls,scriptErrors,intervals,settle,el,tick,click,login,attachRuntime:async()=>{const script=win.document.createElement('script');script.src='/wails/runtime.js';win.document.head.append(script);await settle();},close:()=>dom.window.close()};
}
async function test(name,fn,options){const f=await fixture(options);try{await fn(f);assert.deepEqual(f.scriptErrors,[]);cases.push(name);}finally{f.close();}}

await test('actual_runtime_bindings_support_guest_login_and_node_loading',async f=>{
 assert.equal(typeof f.win.go.main.App.LoginSecure,'function');
 assert.equal(f.el('register').hidden,false);await f.login();
 assert.equal(f.el('auth').hidden,true);assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1);
 assert.ok(f.nativeCalls.some(call=>call.name==='main.App.Query'&&call.args[0]==='/api/nodes'));
});
await test('login_during_older_inflight_status_still_loads_account_and_nodes',async f=>{
 f.model.holdNextStatus=true;const olderPoll=f.intervals[0]();await f.settle();assert.ok(f.model.heldStatus);
 await f.login();f.model.heldStatus();await olderPoll;await f.settle();await f.tick();
 assert.equal(f.el('auth').hidden,true);assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1,'Authenticated status must start loading private data even when the login refresh observed a stale response');
});
await test('late_runtime_initialisation_retries_complete_bootstrap_not_status_only',async f=>{
 await f.attachRuntime();await f.tick();
 assert.equal(f.el('register').hidden,false,'Public settings must load after the bridge becomes ready');
 await f.login();assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1);
},{lateRuntime:true});
await test('cached_session_is_loaded_through_actual_runtime',async f=>{
 assert.equal(f.el('auth').hidden,true);assert.match(f.el('profile').textContent,/订阅可用/);assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1);
},{loggedIn:true});
await test('initial_status_failure_recovers_public_login_and_cached_nodes',async f=>{
 assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,0);
 await f.tick();assert.equal(f.el('auth').hidden,true);
 assert.equal(f.el('register').hidden,false);assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1);
},{loggedIn:true,failInitialStatus:true});
await test('temporary_node_failure_automatically_recovers_with_bounded_retries',async f=>{
 assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,0);
 await f.tick();
 const count=()=>f.nativeCalls.filter(c=>c.name==='main.App.Query'&&c.args[0]==='/api/nodes').length;
 const afterFirstRetry=count();await f.tick();await f.tick();assert.equal(count(),afterFirstRetry,'Polling must back off failed data queries');
 f.model.failNodes=false;f.model.clock+=31000;await f.tick();
 assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1);assert.equal(f.el('load-alert').hidden,true);
 const recovered=count();f.model.clock+=31000;await f.tick();assert.equal(count(),recovered,'Successful reads must not repeat every second');
},{loggedIn:true,failNodes:true});
await test('guest_settings_always_has_a_working_return_to_login',async f=>{
 await f.click('guest-settings');assert.equal(f.el('auth').hidden,true);assert.equal(f.el('guest-back').hidden,false);
 await f.tick();await f.click('guest-back');assert.equal(f.el('auth').hidden,false);await f.login();
 assert.equal(f.el('page-dashboard').hidden,false);assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,1);
});
await test('logout_drains_old_status_without_restoring_account_view',async f=>{
 f.model.holdNextStatus=true;const oldPoll=f.intervals[0]();await f.settle();assert.ok(f.model.heldStatus);
 await f.click('logout');f.model.heldStatus();await oldPoll;await f.settle();await f.tick();
 assert.equal(f.el('auth').hidden,false);assert.equal(f.el('nodes').querySelectorAll('[data-node-id]').length,0);assert.equal(f.el('profile').textContent,'');
},{loggedIn:true});

const report={passed:true,desktop_version:productVersion,checked_at:new Date().toISOString(),gui_rendered:false,runtime:'vendored Wails production runtime + DesktopIPC via jsdom script loader',native_boundary:'isolated controlled callbacks; no native GUI or proxy changes',cases};
await fs.writeFile(path.join(root,'docs/desktop-runtime-startup-regression.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(report));
