import {productVersion} from './product-version.mjs';
// First-time customer journeys against served assets and a real isolated controller.
// jsdom only; no visual browser, actual payment, production mutation, or external mail.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
import crypto from 'node:crypto';
import vm from 'node:vm';
import {spawn,spawnSync} from 'node:child_process';
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
 const owner=await browser();await owner.login(config.admin_email,config.admin_password);
 const plans=await admin('plans');
 const ownerOrder=await admin('orders',{plan_id:plans[0].id},201),ownerCheckout=await admin('orders/'+ownerOrder.id+'/checkout',{});
 await admin('payments/'+ownerCheckout.payment.id+'/test-confirm',{});await admin('admin/payments/'+ownerCheckout.payment.id+'/refund',{reason:'Owner private refund reason'});
 const customer=await rawSession(),invite=await admin('admin/beta/invites',{days:1});
 await customer('register',{email:'refund-customer@example.test',password:'refund-customer-password',invite:invite.invite});await customer('me');
 const mail=(await admin('admin/mail/test')).find(m=>m.to==='refund-customer@example.test'),verify=mail.body.match(/#verify=([A-Za-z0-9_-]+)/)[1];await customer('security/verify',{token:verify});
 const order=await customer('orders',{plan_id:plans[0].id},201),checkout=await customer('orders/'+order.id+'/checkout',{});
 await customer('payments/'+checkout.payment.id+'/test-confirm',{});await admin('admin/payments/'+checkout.payment.id+'/refund',{reason:'Customer refund <img src=x onerror=alert(1)>'});
 const buyer=await browser();await buyer.login('refund-customer@example.test','refund-customer-password');
 const ownRefunds=(await customer('billing')).refunds;
 await check('customer_refund_details_have_reason_reference_amount_and_no_other_customer_record',async()=>{
  await buyer.click('我的账单',buyer.d.getElementById('nav'));
  assert.equal(ownRefunds.length,1);assert.ok(buyer.d.getElementById('content').textContent.includes(ownRefunds[0].reason),'Saved refund reason is absent from customer billing');
  assert.ok(buyer.d.getElementById('content').textContent.includes(ownRefunds[0].reference),'Saved refund reference is absent from customer billing');
  assert.ok(!buyer.d.getElementById('content').textContent.includes('Owner private refund reason'));assert.equal(buyer.d.querySelector('#content img[src=x]'),null);
 });
 await check('admin_can_review_all_persisted_refund_details',async()=>{
  await owner.click('付款与退款',owner.d.getElementById('nav'));
  const refunds=await admin('admin/refunds');assert.equal(refunds.length,2);
  for(const value of refunds){assert.ok(owner.d.getElementById('content').textContent.includes(value.reason),'Saved refund reason is absent from management');assert.ok(owner.d.getElementById('content').textContent.includes(value.reference));}
 });
 await check('removed_finance_group_cannot_grant_refund_or_configuration_access',async()=>{
  const identity=await customer('me');await admin('admin/users/'+identity.user.id+'/role',{role:'finance'},400);
  assert.equal((await customer('me')).user.role,'user');
  await customer('admin/refunds',undefined,403);await customer('admin/payment-settings',undefined,403);
  assert.equal([...buyer.d.querySelectorAll('#nav button')].some(b=>b.textContent==='支付配置'),false);
 });
 await check('commercial_trial_control_is_honestly_disabled',async()=>{
  await owner.click('设置与公告',owner.d.getElementById('nav'));await owner.click('编辑注册与试用');
  const field=owner.d.querySelector('#fields [name=trial_hours]');assert.ok(field);assert.equal(field.disabled,true,'Commercial mode exposes an editable trial value that the API always reports as zero');
  assert.match(owner.d.getElementById('dialog-description').textContent,/商业模式|获邀/);owner.d.getElementById('cancel').onclick();
 });
 await check('persisted_audit_names_summary_and_before_after_are_visible',async()=>{
  await admin('admin/plans/'+plans[0].id,{...plans[0],price_cents:100,currency:'cny'});
  await admin('admin/plans/'+plans[0].id,{...plans[0],price_cents:200,currency:'cny'});
  const entries=await admin('admin/audit'),entry=entries.filter(e=>e.action==='plan_saved'&&e.subject===plans[0].id).at(-1);
  assert.equal(entry.actor_name,config.admin_email);assert.equal(entry.subject_name,plans[0].name);assert.ok(entry.summary);assert.ok(entry.changes.some(c=>c.before==='1.00 CNY'&&c.after==='2.00 CNY'));
  await owner.click('审计',owner.d.getElementById('nav'));
  const row=[...owner.d.querySelectorAll('#content > .card > .list-panel tbody > tr')].find(r=>r.textContent.includes('2.00 CNY'));
  assert.ok(row);assert.ok(row.textContent.includes(config.admin_email));assert.ok(row.textContent.includes(entry.summary));assert.ok(row.textContent.includes('1.00 CNY'));
  const details=row.querySelector('details.audit-changes');assert.ok(details);details.open=true;assert.equal(details.open,true);assert.equal(row.querySelector('svg[onload]'),null);
 });
 await check('legacy_four_field_audit_and_untrusted_diff_text_remain_readable',async()=>{
  const legacy=vm.runInContext("auditChanges({time:1,actor:'legacy-user',action:'node_saved',subject:'legacy-node'})",owner.dom.getInternalVMContext());
  assert.match(legacy.textContent,/历史记录未保存字段差异/);
  const oldAction=vm.runInContext("auditOperation({action:'node_saved'})",owner.dom.getInternalVMContext());assert.match(oldAction.textContent,/保存节点/);assert.match(oldAction.textContent,/node_saved/);
  const change=vm.runInContext("auditChanges({changes:[{field:'name',label:'名称',before:'',after:'<img src=x onerror=alert(1)>'}]})",owner.dom.getInternalVMContext());assert.equal(change.querySelector('img'),null);assert.match(change.textContent,/（空）/);
 });
 await check('admin_node_probe_is_separate_from_heartbeat_and_hidden_from_member_table',async()=>{
  const fixture=path.join(stage,'probe-fixture'),go=process.platform==='win32'?'C:/Program Files/Go/bin/go.exe':'go';
  const generated=spawnSync(go,['run','-tags=http2legacy','./cmd/tunnelx-admin','-out',fixture,'-domain','probe.example.com','-ip','198.51.100.77'],{cwd:root,windowsHide:true,encoding:'utf8',env:{...process.env,GOCACHE:path.join(root,'.local/go-cache')}});
  assert.equal(generated.status,0,generated.stderr);
  const client=JSON.parse(await fs.readFile(path.join(fixture,'client-strict.json'),'utf8')),ca=await fs.readFile(path.join(fixture,'origin-ca.pem'),'utf8'),now=Math.floor(Date.now()/1000);
  await admin('admin/nodes',{name:'Probe-display-fixture',region:'TEST',enabled:true,client,ca_pem:ca,test_only:true,last_seen:now,last_probe:now,probe_ok:false,probe_latency_ms:127});
  const node=(await admin('admin/nodes')).find(n=>n.name==='Probe-display-fixture'),agent=await admin('admin/nodes/'+node.id+'/agent-config');
  const heartbeat=await fetch(base+'/api/node/sync',{method:'POST',headers:{'Content-Type':'application/json','X-TunnelX-Node':node.id,Authorization:'Bearer '+agent.agent_key},body:JSON.stringify({boot_id:'a'.repeat(32),version:'v0.5.0',active:2,counters:[],capabilities:['strict-billing','leases-v1']})});assert.equal(heartbeat.status,200);
  await owner.click('节点',owner.d.getElementById('nav'));
  const row=[...owner.d.querySelectorAll('#content tbody tr')].find(r=>r.textContent.includes('Probe-display-fixture'));
  assert.ok(row);assert.match(row.textContent,/心跳在线/);assert.match(row.textContent,/外部探测失败/);assert.match(row.textContent,/127 ms/);assert.ok(row.textContent.includes(String(new Date(now*1000).getUTCFullYear())));
  const identity=(await admin('admin/users')).find(u=>u.email==='refund-customer@example.test');await admin('admin/users/'+identity.id+'/role',{role:'user'});await buyer.login('refund-customer@example.test','refund-customer-password');await buyer.click('可用节点',buyer.d.getElementById('nav'));
  assert.equal(buyer.d.querySelector('.node-probe-status'),null);assert.ok(!buyer.d.getElementById('content').textContent.includes('最近外部探测'));
 });
 await admin('admin/payment-settings',{mode:'gateways',epay:{enabled:true,url:'https://pay.example.test',merchant_id:'1',key:'fixture-key',alipay:true,wechat:true},bepusdt:{enabled:false,trade_types:['usdt.trc20','usdt.bep20','usdt.polygon']}});
 await check('test_mail_entry_follows_mail_configuration_instead_of_payment_mode',async()=>{
  assert.equal((await admin('public/commerce')).test_mode,false);assert.ok((await admin('admin/mail/test')).length>0);
  await owner.click('运维告警',owner.d.getElementById('nav'));
  assert.ok([...owner.d.querySelectorAll('#content button')].some(b=>b.textContent==='查看测试邮件'),'The test mailbox API works but the button is hidden when payment mode is gateways');
  await owner.click('查看测试邮件');assert.match(owner.d.querySelector('#fields textarea').value,/refund-customer@example.test/);owner.d.getElementById('cancel').onclick();
 });
 await check('workspace_feature_browser_runs_have_no_runtime_errors',async()=>{assert.deepEqual(pages.flatMap(p=>p.errors),[]);});
 const report={passed:failures.length===0,checked_at:new Date().toISOString(),console_version:version,served_assets:true,real_control_api:true,probe_metrics_are_fixture_data:true,legacy_audit_component_fixture:true,visual_rendered:false,real_charge:false,cases,failures};
 const reportName=process.env.TUNNELX_FEATURE_REPORT||'workspace-feature-dom-'+version+'.json';
 await fs.writeFile(path.join(root,'docs',reportName),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));if(failures.length)process.exitCode=1;
}finally{for(const p of pages){await p.drain();p.dom.window.close();}child.kill();if(child.exitCode===null)await new Promise(r=>child.once('exit',r));}
