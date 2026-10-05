import {productVersion} from './product-version.mjs';
// First-time customer journeys against served assets and a real isolated controller.
// jsdom only; no visual browser, actual payment, production mutation, or external mail.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
import crypto from 'node:crypto';
import vm from 'node:vm';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';

const root=path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const require=createRequire(path.join(root,'tools/admin-ui-test/package.json'));
const {JSDOM,CookieJar,VirtualConsole}=require('jsdom');
const version=process.env.TUNNELX_CONSOLE_VERSION||productVersion;
const stage=await fs.mkdtemp(path.join(root,'.local/web-tests/buyer-'));
const listener=net.createServer();
await new Promise(r=>listener.listen(0,'127.0.0.1',r));
const port=listener.address().port;
await new Promise(r=>listener.close(r));
const base='http://127.0.0.1:'+port;
const config={listen:'127.0.0.1:'+port,public_url:base,data_file:path.join(stage,'state.json'),admin_email:'admin@example.test',admin_password:'buyer-admin-password',registration:true,trial_hours:0,commercial:{enabled:true,payment_mode:'test',security_key:crypto.randomBytes(32).toString('base64'),require_admin_mfa:false,beta_invite_only:true},mail:{mode:'test'}};
await fs.writeFile(path.join(stage,'config.json'),JSON.stringify(config));
const executable=process.env.TUNNELX_CONTROL_BINARY||path.join(root,'dist/control-'+version+'/windows-amd64/tunnelx-control.exe');
const child=spawn(executable,['-config',path.join(stage,'config.json')],{windowsHide:true,stdio:['ignore','pipe','pipe']});
let stderr='';child.stderr.on('data',b=>stderr+=b);child.stdout.on('data',()=>{});
const pages=[],cases=[],failures=[];
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function rawSession(){
 const jar=new CookieJar();let csrf='';
 return async(endpoint,body,expect=200)=>{
  const url=base+'/api/'+endpoint,headers={'Cookie':await jar.getCookieString(url)};
  if(body!==undefined)Object.assign(headers,{'Origin':base,'Content-Type':'application/json','X-CSRF-Token':csrf});
  const response=await fetch(url,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body)});
  for(const value of response.headers.getSetCookie())await jar.setCookie(value,url);
  const data=await response.json();assert.equal(response.status,expect,endpoint+': '+JSON.stringify(data));
  if(endpoint==='me')csrf=data.csrf;
  return data;
 };
}
async function browser(fragment='',jar=new CookieJar(),delayIdentity=0){
 const pending=new Set(),errors=[],requests=[],intervals=[];
 const virtualConsole=new VirtualConsole();virtualConsole.on('jsdomError',e=>errors.push(String(e)));virtualConsole.on('error',e=>errors.push(String(e)));
 const html=await(await fetch(base+'/')).text();
 const dom=new JSDOM(html,{url:base+'/'+fragment,runScripts:'outside-only',cookieJar:jar,virtualConsole});
 const w=dom.window,d=w.document;
 w.setInterval=(callback,ms)=>{intervals.push({callback,ms});return intervals.length;};
 w.AbortController=globalThis.AbortController;w.Blob=globalThis.Blob;
 w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
 w.HTMLDialogElement.prototype.close=function(){this.removeAttribute('open');};
 w.fetch=(url,init={})=>{
  const operation=(async()=>{
   const target=String(url),headers=new Headers(init.headers);headers.set('Cookie',await jar.getCookieString(target));
   if(init.method==='POST')headers.set('Origin',base);
   requests.push({path:new URL(target).pathname,method:init.method||'GET',body:init.body?JSON.parse(init.body):undefined});
   const response=await fetch(target,{...init,headers});
   for(const cookie of response.headers.getSetCookie())await jar.setCookie(cookie,target);
   if(new URL(target).pathname==='/api/me'&&delayIdentity)await sleep(delayIdentity);
   return response;
  })();pending.add(operation);operation.then(()=>pending.delete(operation),()=>pending.delete(operation));return operation;
 };
 for(const script of d.querySelectorAll('script[src]'))vm.runInContext(await(await fetch(script.src)).text(),dom.getInternalVMContext(),{filename:script.src});
 vm.runInContext('globalThis.journey={api,refreshIdentity,render,getUser:()=>me,getView:()=>view};',dom.getInternalVMContext());
 const drain=async()=>{for(let count=0;count<100;count++){if(pending.size)await Promise.allSettled([...pending]);await sleep(10);if(!pending.size)return;}throw Error('browser requests did not settle');};
 const click=async(label,scope=d)=>{const b=[...scope.querySelectorAll('button')].find(b=>b.textContent===label);assert.ok(b,'Missing button '+label);assert.equal(b.disabled,false,'Disabled button '+label);await b.onclick();await drain();};
 const fill=values=>{for(const [name,value]of Object.entries(values)){const node=d.querySelector('#fields [name="'+name+'"]');assert.ok(node,'Missing field '+name);node.value=value;}};
 const submit=async()=>{const form=d.getElementById('edit-form');await form.onsubmit({preventDefault(){},target:form});await drain();};
 const login=async(email,password)=>{d.getElementById('email').value=email;d.getElementById('password').value=password;await d.getElementById('auth-form').onsubmit({preventDefault(){}});await drain();};
 const page={dom,w,d,jar,drain,click,fill,submit,login,errors,requests,intervals};pages.push(page);await drain();return page;
}
async function check(name,fn){try{await fn();cases.push(name);}catch(error){failures.push({name,error:error.message});console.error(name+': '+error.message);}}
try{
 for(let i=0;i<100;i++){if(child.exitCode!==null)throw Error(stderr);try{if((await fetch(base+'/health')).ok)break;}catch{}await sleep(50);}
 const admin=await rawSession();await admin('login',{email:config.admin_email,password:config.admin_password});await admin('me');
 const invite=await admin('admin/beta/invites',{days:1});
 const token=new URL(invite.url).hash.split('=')[1];
 const buyer=await browser('#invite='+token);
 await check('guest_catalog_invite_and_no_private_navigation',async()=>{
  assert.equal(buyer.d.getElementById('public-shop').hidden,false);assert.match(buyer.d.getElementById('public-shop').textContent,/不会实际扣款/);
  assert.equal(buyer.d.getElementById('invite').value,token);assert.equal(buyer.w.location.hash,'');assert.equal(buyer.d.querySelectorAll('#nav button').length,0);
 });
 buyer.d.getElementById('email').value='first-buyer@example.test';buyer.d.getElementById('password').value='first-buyer-password';
 await buyer.click('注册账号');
 await check('registration_and_verification_gate',async()=>{
  assert.equal(buyer.d.getElementById('login').hidden,true,buyer.d.getElementById('message').textContent);
  await buyer.click('套餐',buyer.d.getElementById('nav'));await buyer.click('创建订单');
  assert.match(buyer.d.getElementById('message').textContent,/验证邮箱/);assert.deepEqual(await buyer.w.journey.api('orders'),[]);
  await buyer.click('账号安全',buyer.d.getElementById('nav'));assert.match(buyer.d.getElementById('content').textContent,/邮箱待验证/);
 });
 const mail=await admin('admin/mail/test');
 const verify=mail.find(m=>m.to==='first-buyer@example.test').body.match(/#verify=([A-Za-z0-9_-]+)/)[1];
 const verified=await browser('#verify='+verify,buyer.jar,120);
 await check('verification_link_opens_in_signed_in_browser',async()=>{
  assert.equal((await verified.w.journey.api('security/status')).email_verified,true);assert.equal(verified.w.location.hash,'');
 });
 await verified.click('套餐',verified.d.getElementById('nav'));await verified.click('创建订单');
 let order=(await verified.w.journey.api('orders'))[0];
 await check('cancel_pending_order_from_customer_screen',async()=>{
  await verified.click('取消订单');assert.equal((await verified.w.journey.api('orders')).find(o=>o.id===order.id).status,'cancelled');assert.equal(verified.d.querySelectorAll('#content .primary').length,0);
 });
 await verified.click('套餐',verified.d.getElementById('nav'));await verified.click('创建订单');
 order=(await verified.w.journey.api('orders')).at(-1);
 await check('customer_order_payment_confirmation_and_billing',async()=>{
  await verified.click('测试付款');assert.equal(verified.d.getElementById('dialog').open,true);assert.match(verified.d.getElementById('dialog-description').textContent,/不会实际扣款/);await verified.submit();
  assert.equal((await verified.w.journey.api('orders')).find(o=>o.id===order.id).status,'paid');
  await verified.click('我的账单',verified.d.getElementById('nav'));assert.match(verified.d.getElementById('content').textContent,/测试节点/);assert.match(verified.d.getElementById('content').textContent,/未扣款/);
 });
 await check('order_background_poll_preserves_active_search_input',async()=>{
  await verified.click('我的订单',verified.d.getElementById('nav'));
  const input=verified.d.querySelector('#content input[type=search]');input.value=order.plan.name;input.focus();input.oninput();
  verified.intervals.find(timer=>timer.ms===15000).callback();await verified.drain();
  assert.equal(verified.d.activeElement,input,'Background order refresh removed the focused search input');
  assert.equal(input.value,order.plan.name);input.blur();
 });
 await check('member_usage_tiles_match_account_and_background_refresh_does_not_replay_entry_motion',async()=>{
  await verified.click('我的账号',verified.d.getElementById('nav'));
  assert.equal(verified.d.body.dataset.role,'user');
  const account=(await verified.w.journey.api('me')).user;
  const fmt=n=>{const units=['B','KiB','MiB','GiB','TiB'];let i=0;while(n>=1024&&i<4){n/=1024;i++;}return n.toFixed(i?1:0)+' '+units[i];};
  const values=[...verified.d.querySelectorAll('.member-metric strong')].map(n=>n.textContent);
  assert.deepEqual(values,[account.traffic.unlimited?'不限流量':fmt(account.traffic.total_bytes),fmt(account.traffic.used_bytes),account.traffic.unlimited?'不限流量':fmt(account.traffic.remaining_bytes)]);
  assert.ok(verified.d.querySelector('#content .member-enter'));
  verified.intervals.find(timer=>timer.ms===30000).callback();await verified.drain();
  assert.equal(verified.d.querySelector('#content .member-enter'),null);
 });
 await check('renewal_and_traffic_addon_keep_distinct_billing_periods',async()=>{
  const initial=(await verified.w.journey.api('billing')).entitlements.find(e=>e.order_id===order.id);
  await admin('admin/plans',{name:'当前周期 5 GiB 加购',kind:'traffic',days:30,price_cents:300,currency:'cny',traffic_bytes:5*1073741824,devices:3,enabled:true});
  await verified.click('套餐',verified.d.getElementById('nav'));
  const addonRow=[...verified.d.querySelectorAll('#content tbody tr')].find(row=>row.textContent.includes('当前周期 5 GiB 加购'));
  await verified.click('创建订单',addonRow);await verified.click('测试付款');await verified.submit();
  const addon=(await verified.w.journey.api('billing')).entitlements.find(e=>e.kind==='traffic');assert.equal(addon.ends_at,initial.ends_at);assert.equal(addon.bytes,5*1073741824);
  await verified.click('套餐',verified.d.getElementById('nav'));
  const renewalRow=[...verified.d.querySelectorAll('#content tbody tr')].find(row=>row.textContent.includes(order.plan.name));
  await verified.click('创建订单',renewalRow);await verified.click('测试付款');await verified.submit();
  const renewal=(await verified.w.journey.api('billing')).entitlements.find(e=>e.kind!=='traffic'&&e.order_id!==order.id);
  assert.equal(renewal.starts_at,initial.ends_at);assert.equal(renewal.ends_at-renewal.starts_at,order.plan.days*86400);
  await verified.click('我的账单',verified.d.getElementById('nav'));assert.match(verified.d.getElementById('content').textContent,/待生效/);
 });
 await check('buyer_ticket_reply_close_reopen_and_text_escaping',async()=>{
  await verified.click('售后工单',verified.d.getElementById('nav'));await verified.click('提交工单');verified.fill({subject:'下载体验问题',body:'<img src=x onerror=window.injected=true> 卡顿详情'});await verified.submit();
  let t=(await verified.w.journey.api('tickets'))[0];await admin('admin/tickets/'+t.id+'/reply',{body:'请提供发生时间。'});
  await verified.w.journey.render();await verified.click('查看与回复');assert.match(verified.d.querySelector('.ticket-history').textContent,/请提供发生时间/);assert.equal(verified.d.querySelector('.ticket-history img'),null);
  verified.fill({body:'北京时间 18:30。'});await verified.submit();await verified.click('关闭工单');assert.equal((await verified.w.journey.api('tickets'))[0].status,'closed');
  await verified.click('查看与回复');verified.fill({body:'问题再次出现。'});await verified.submit();assert.equal((await verified.w.journey.api('tickets'))[0].status,'open');
 });
 await check('refund_visible_to_buyer_and_entitlement_revoked',async()=>{
  const bill=await verified.w.journey.api('billing');const payment=bill.payments.find(p=>p.order_id===order.id);await admin('admin/payments/'+payment.id+'/refund',{reason:'用户旅程模拟退款'});
  await verified.click('我的账单',verified.d.getElementById('nav'));assert.match(verified.d.getElementById('content').textContent,/已退款撤销/);assert.match(verified.d.getElementById('content').textContent,/refunded/);
 });
 await check('logout_removes_private_data_and_restores_catalog',async()=>{
  await verified.click('退出登录');assert.equal(verified.d.getElementById('content').textContent,'');assert.equal(verified.d.getElementById('public-shop').hidden,false);assert.equal(verified.d.querySelectorAll('#nav button').length,0);
 });
 await check('another_customer_cannot_see_or_change_buyer_records',async()=>{
  const other=await rawSession(),secondInvite=await admin('admin/beta/invites',{days:1});
  await other('register',{email:'second-buyer@example.test',password:'second-buyer-password',invite:secondInvite.invite});await other('me');
  assert.deepEqual(await other('orders'),[]);assert.deepEqual((await other('billing')).payments,[]);assert.deepEqual(await other('tickets'),[]);
  await other('admin/users',undefined,403);
  const firstTicket=(await admin('admin/tickets'))[0];await other('tickets/'+firstTicket.id+'/reply',{body:'不能替其他人回复。'},409);await other('orders/'+order.id+'/cancel',{},409);
 });
 await verified.click('找回密码');verified.fill({email:'first-buyer@example.test'});await verified.submit();
 const resetMail=(await admin('admin/mail/test')).filter(m=>m.to==='first-buyer@example.test'&&m.subject.includes('重置')).at(-1);
 const reset=resetMail.body.match(/#reset=([A-Za-z0-9_-]+)/)[1];
 const resetting=await browser('#reset='+reset,new CookieJar(),120);
 await check('guest_reset_link_survives_initial_identity_401',async()=>{
  assert.equal(resetting.d.getElementById('dialog').open,true,'Initial identity check closed the password reset form');assert.equal(resetting.d.getElementById('dialog-title').textContent,'设置新密码');assert.equal(resetting.w.location.hash,'');
  resetting.fill({password:'first-buyer-new-password'});await resetting.submit();assert.match(resetting.d.getElementById('message').textContent,/密码已重置/);
  await resetting.login('first-buyer@example.test','first-buyer-new-password');assert.equal(resetting.d.getElementById('login').hidden,true,resetting.d.getElementById('message').textContent);
 });
 await check('security_link_also_works_when_browser_reuses_existing_page',async()=>{
  await admin('security/forgot',{email:'first-buyer@example.test'},202);
  const latest=(await admin('admin/mail/test')).filter(m=>m.to==='first-buyer@example.test'&&m.subject.includes('重置')).at(-1);
  const nextToken=latest.body.match(/#reset=([A-Za-z0-9_-]+)/)[1],existing=await browser();
  existing.w.location.hash='#reset='+nextToken;await sleep(20);await existing.drain();
  assert.equal(existing.d.getElementById('dialog').open,true,'Changing the fragment to a security link did not open the reset form');
  existing.fill({password:'first-buyer-final-password'});await existing.submit();assert.match(existing.d.getElementById('message').textContent,/密码已重置/);
 });
 await check('all_customer_browser_runs_have_no_runtime_errors',async()=>{assert.deepEqual(pages.flatMap(p=>p.errors),[]);});
 const report={passed:failures.length===0,checked_at:new Date().toISOString(),console_version:version,served_assets:true,real_control_api:true,visual_rendered:false,real_charge:false,cases,failures};
 await fs.writeFile(path.join(root,'docs/buyer-journey-'+version+'.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));if(failures.length)process.exitCode=1;
}finally{for(const p of pages){await p.drain();p.dom.window.close();}child.kill();if(child.exitCode===null)await new Promise(r=>child.once('exit',r));}
