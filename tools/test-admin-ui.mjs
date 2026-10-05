import {productVersion} from './product-version.mjs';
// Exercise DOM forms against the real loopback control API; no browser or visual renderer.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {spawn,spawnSync} from 'node:child_process';
const root=path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const consoleVersion=process.env.TUNNELX_CONSOLE_VERSION||productVersion;
const require=createRequire(path.join(root,'tools/admin-ui-test/package.json'));
const {JSDOM,CookieJar}=require('jsdom');
await fs.mkdir(path.join(root,'.local/web-tests'),{recursive:true});
const stage=await fs.mkdtemp(path.join(root,'.local/web-tests/run-'));
const fixture=path.join(stage,'fixture');
const generated=spawnSync('go',['run','-tags=http2legacy','./cmd/tunnelx-admin','-out',fixture,'-domain','public.example.test','-ip','127.0.0.1'],{cwd:root,windowsHide:true,encoding:'utf8'});
assert.equal(generated.status,0,'Isolated test certificates could not be generated: '+generated.stderr);
const listener=net.createServer();await new Promise(r=>listener.listen(0,'127.0.0.1',r));const port=listener.address().port;await new Promise(r=>listener.close(r));
const base='http://127.0.0.1:'+port;
const settings={listen:'127.0.0.1:'+port,public_url:base,data_file:path.join(stage,'store.json'),admin_email:'admin@example.test',admin_password:'admin-password-for-dom-tests',registration:true,trial_hours:24};
await fs.writeFile(path.join(stage,'config.json'),JSON.stringify(settings));
const testBinary=path.join(root,'dist/control-'+consoleVersion,process.platform==='win32'?'windows-amd64':'linux-'+(process.arch==='arm64'?'arm64':'amd64'),process.platform==='win32'?'tunnelx-control.exe':'tunnelx-control');
const child=spawn(testBinary,['-config',path.join(stage,'config.json')],{windowsHide:true,stdio:['ignore','pipe','pipe']});
let stderr='';child.stderr.on('data',b=>stderr+=b);child.stdout.on('data',()=>{});
let dom;const cases=[];const record=name=>cases.push(name);
async function ready(){for(let i=0;i<100;i++){if(child.exitCode!==null)throw Error('Control test server exited: '+stderr);try{const r=await fetch(base+'/health');if(r.ok)return;}catch{}await new Promise(r=>setTimeout(r,50));}throw Error('Control test server not ready');}
try{
 await ready();const jar=new CookieJar(),html=await(await fetch(base+'/')).text();
 dom=new JSDOM(html,{url:base+'/',runScripts:'outside-only',cookieJar:jar});const w=dom.window,d=w.document,downloads=[];
 w.AbortController=globalThis.AbortController;w.Blob=globalThis.Blob;
 w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
 w.HTMLDialogElement.prototype.close=function(){this.removeAttribute('open');};
 w.URL.createObjectURL=blob=>{downloads.push(blob);return 'blob:acceptance';};w.URL.revokeObjectURL=()=>{};w.HTMLAnchorElement.prototype.click=function(){};
 async function nativeFetch(url,init={}){
  const target=new URL(String(url),base),headers=new Headers(init.headers);
  headers.set('Cookie',await jar.getCookieString(target.href));if(init.method==='POST')headers.set('Origin',base);
  const r=await fetch(target,{...init,headers});for(const cookie of r.headers.getSetCookie())await jar.setCookie(cookie,target.href);return r;
 }
 w.fetch=nativeFetch;w.eval((await fs.readFile(path.join(root,'internal/control/web/app.js'),'utf8'))+'\n'+(await fs.readFile(path.join(root,'internal/control/web/commerce.js'),'utf8'))+'\nconst pendingAPI=new Set(),originalAPI=api;api=(...args)=>{const p=originalAPI(...args);pendingAPI.add(p);p.then(()=>pendingAPI.delete(p),()=>pendingAPI.delete(p));return p;};globalThis.testAPI={drain:async()=>{while(pendingAPI.size)await Promise.allSettled([...pendingAPI]);},api,setView:key=>{view=key;return render();}};');
 const field=name=>d.querySelector('#fields [name="'+name+'"]');
 const fill=values=>{for(const [name,value]of Object.entries(values)){assert.ok(field(name),'Field missing: '+name);field(name).value=String(value);}};
 const submit=()=>d.getElementById('edit-form').onsubmit({preventDefault(){},target:d.getElementById('edit-form')});
 const buttons=(scope,label)=>[...scope.querySelectorAll('button')].filter(b=>b.textContent===label);
 async function click(scope,label){const b=buttons(scope,label)[0];assert.ok(b,'Button missing: '+label);assert.equal(b.disabled,false,'Button remained disabled: '+label);await b.onclick();}
 async function page(key){const b=d.querySelector('[data-view="'+key+'"]');assert.ok(b);assert.equal(b.disabled,false);await b.onclick();}
 function row(name){const r=[...d.querySelectorAll('#content tr')].find(r=>r.firstChild?.textContent===name);assert.ok(r,'Row missing: '+name);return r;}
 async function login(password=settings.admin_password){d.getElementById('email').value=settings.admin_email;d.getElementById('password').value=password;await d.getElementById('auth-form').onsubmit({preventDefault(){}});assert.equal(d.getElementById('login').hidden,true,d.getElementById('message').textContent);}
 await new Promise(r=>setTimeout(r,180));assert.equal(d.body.dataset.mode,'guest');assert.equal(d.getElementById('message').textContent,'');record('first_visit_has_no_false_expired_login_banner');
 const spriteResponse=await nativeFetch(base+'/icons.svg');assert.equal(spriteResponse.status,200);assert.match(spriteResponse.headers.get('content-type'),/image\/svg\+xml/);const sprite=new w.DOMParser().parseFromString(await spriteResponse.text(),'image/svg+xml');assert.equal(sprite.querySelector('parsererror'),null);const symbols=new Set([...sprite.querySelectorAll('symbol[id]')].map(s=>s.id));
 function validIcons(){for(const use of d.querySelectorAll('svg use')){const ref=use.getAttribute('href');assert.ok(ref?.startsWith('./icons.svg#'));assert.ok(symbols.has(ref.split('#')[1]),'SVG symbol missing: '+ref);assert.equal(use.namespaceURI,'http://www.w3.org/2000/svg');assert.equal(use.closest('svg').getAttribute('aria-hidden'),'true');}}
 validIcons();for(const name of ['network.svg','mark.svg']){const response=await nativeFetch(base+'/'+name);assert.equal(response.status,200);assert.match(response.headers.get('content-type'),/image\/svg\+xml/);const svg=new w.DOMParser().parseFromString(await response.text(),'image/svg+xml');assert.equal(svg.querySelector('parsererror'),null);assert.equal(svg.querySelector('script,foreignObject'),null);}record('local_svg_assets_and_valid_icon_references');
 await login();record('cookie_login_and_csrf');assert.equal(d.body.dataset.mode,'member');assert.equal(d.querySelectorAll('#nav button svg').length,9);assert.equal(d.querySelector('#nav [data-view=overview]').getAttribute('aria-current'),'page');assert.deepEqual([...d.querySelectorAll('.metric b')].map(b=>Number(b.textContent)),[1,0,0,0]);validIcons();record('overview_real_metrics_and_accessible_navigation');
 for(const key of ['orders','nodes','plans','audit','settings']){await page(key);validIcons();assert.equal(d.getElementById('message').textContent,'');}record('initial_empty_collection_pages');
 const cfg=JSON.parse(await fs.readFile(path.join(fixture,'client-strict.json'),'utf8')),ca=await fs.readFile(path.join(fixture,'origin-ca.pem'),'utf8');
 await page('nodes');await click(d.getElementById('content'),'手动添加');
 assert.equal(d.querySelectorAll('#fields textarea').length,2);assert.equal(d.querySelector('#fields select').type,'select-one');
 fill({name:'DOM-node',region:'KR',server_name:cfg.server_name,server_ip:cfg.server_ip,port:cfg.port,ech_config:'YWJj',ca_pem:ca});
 await submit();assert.equal(d.getElementById('dialog').open,true);assert.match(d.getElementById('dialog-error').textContent,/ECH/);record('invalid_node_error_visible_inside_modal');
 fill({ech_config:cfg.ech_config});const first=submit();await submit();await first;
 assert.equal(d.getElementById('dialog').open,false);
 let nodes=await w.testAPI.api('admin/nodes');assert.equal(nodes.length,1);const node=nodes[0];
 const keyBefore=(await w.testAPI.api('admin/nodes/'+node.id+'/agent-config')).agent_key;
 await click(row('DOM-node'),'编辑');fill({name:'DOM-node-edited'});await submit();
 assert.equal((await w.testAPI.api('admin/nodes/'+node.id+'/agent-config')).agent_key,keyBefore);record('node_add_edit_textarea_select_and_duplicate_submit');
 await click(row('DOM-node-edited'),'下载配置 ZIP');assert.equal(downloads.length,1);assert.equal(Buffer.from(await downloads[0].arrayBuffer()).subarray(0,2).toString(),'PK');record('single_profile_zip_download');
 await click(row('DOM-node-edited'),'停用');await click(row('DOM-node-edited'),'编辑');fill({region:'JP'});await submit();
 assert.equal((await w.testAPI.api('admin/nodes/'+node.id)).enabled,false);record('disabled_node_still_editable');
 await click(row('DOM-node-edited'),'删除');fill({name:'DOM-node-edited'});await submit();assert.equal((await w.testAPI.api('admin/nodes')).length,0);record('disabled_offline_node_delete');

 await click(d.getElementById('content'),'添加节点');
 assert.equal(d.querySelectorAll('#fields textarea').length,0);assert.equal(d.querySelectorAll('#fields input').length,6);
 fill({name:'Install-node',ip:'146.56.99.255',port:18449,domain:'node.example.com'});await submit();
 assert.equal(d.getElementById('dialog').open,true);assert.equal(d.getElementById('save').hidden,true);assert.ok(d.querySelector('.install-command').readOnly);assert.match(d.querySelector('.install-command').value,/bash -c/);assert.match(d.getElementById('dialog-description').textContent,/有效期/);
 await click(d.getElementById('fields'),'复制命令');assert.match(d.getElementById('dialog-error').textContent,/Ctrl\+C|已复制/);d.getElementById('cancel').onclick();
 const installRow=row('Install-node');assert.match(installRow.textContent,/待安装/);assert.equal(buttons(installRow,'启用').length,0);assert.equal(buttons(installRow,'下载配置 ZIP').length,0);assert.equal(buttons(installRow,'编辑').length,0);
 const pending=(await w.testAPI.api('admin/nodes')).find(n=>n.name==='Install-node');const oldAgent=(await w.testAPI.api('admin/nodes/'+pending.id+'/agent-config')).agent_key;
 await click(installRow,'生成安装命令');assert.equal(d.getElementById('dialog').open,true);d.getElementById('cancel').onclick();assert.notEqual((await w.testAPI.api('admin/nodes/'+pending.id+'/agent-config')).agent_key,oldAgent);
 assert.equal((await w.testAPI.api('nodes')).length,0);record('simple_node_setup_command_copy_and_pending_isolation');
 await click(row('Install-node'),'删除');fill({name:'Install-node'});await submit();assert.equal((await w.testAPI.api('admin/nodes')).length,0);record('pending_node_command_regeneration_and_delete');
 await click(d.getElementById('content'),'添加节点');
 field('certificate_mode').value='private';field('certificate_mode').dispatchEvent(new w.Event('change'));assert.equal(field('domain').required,false);assert.match(field('domain').parentElement.textContent,/无需注册/);
 fill({name:'Private-node',ip:'203.0.113.20',port:18450,domain:'Gateway.Custom.Invalid.',inner_name:'Origin.Custom.Invalid.'});await submit();
 assert.match(d.getElementById('fields').textContent,/无需域名解析/);d.getElementById('cancel').onclick();
 const privateNode=(await w.testAPI.api('admin/nodes')).find(n=>n.name==='Private-node');assert.equal(privateNode.certificate_mode,'private');assert.equal(privateNode.server_name,'origin.custom.invalid');assert.equal(privateNode.public_domain,'gateway.custom.invalid');
 await click(row('Private-node'),'生成安装命令');assert.match(d.getElementById('fields').textContent,/无需域名解析/);d.getElementById('cancel').onclick();
 await click(row('Private-node'),'删除');fill({name:'Private-node'});await submit();record('private_certificate_setup_custom_names_and_regenerated_command');
 await page('plans');await click(d.getElementById('content'),'新增套餐');
 fill({name:'DOM-plan',price:'9.99',gib:'1.5',devices:2});assert.equal(field('price').validity.stepMismatch,false);assert.equal(d.getElementById('edit-form').checkValidity(),true);await submit();
 let plans=await w.testAPI.api('admin/plans');let plan=plans.find(p=>p.name==='DOM-plan');assert.equal(plan.price_cents,999);assert.equal(plan.traffic_bytes,1610612736);record('decimal_price_and_fractional_quota_form');
 const snapshotOrder=await w.testAPI.api('orders',{plan_id:plan.id});
 await click(row('DOM-plan'),'编辑');fill({price:'12.34'});await submit();assert.equal((await w.testAPI.api('orders')).find(o=>o.id===snapshotOrder.id).plan.price_cents,999);
 await click(row('DOM-plan'),'停用');assert.equal((await w.testAPI.api('admin/plans')).find(p=>p.id===plan.id).enabled,false);
 await click(row('DOM-plan'),'删除');fill({name:'DOM-plan'});await submit();record('plan_edit_disable_delete_preserves_order_snapshot');
 await page('orders');await click(row(snapshotOrder.id),'取消订单');assert.equal((await w.testAPI.api('orders')).find(o=>o.id===snapshotOrder.id).status,'cancelled');record('order_user_identity_and_cancellation');
 await page('settings');await click(d.getElementById('content'),'编辑公告与更新');
 fill({announcement:'<script>window.untrusted=true</script>',url:'https://example.com/client.zip',sha256:'g'.repeat(64)});await submit();
 assert.equal(d.getElementById('dialog').open,true);assert.match(d.getElementById('dialog-error').textContent,/十六进制/);
 fill({sha256:'a'.repeat(64)});await submit();assert.equal(w.untrusted,undefined);assert.ok(d.getElementById('content').textContent.includes('<script>'));record('announcement_xss_and_update_checksum_validation');
 await click(d.getElementById('content'),'编辑注册与试用');fill({registration:'false',trial_hours:2});await submit();assert.equal((await w.testAPI.api('public/settings')).registration,false);record('runtime_registration_trial_settings');
 // Force overlapping asynchronous renders and verify only the latest page commits.
 let release;const gate=new Promise(r=>release=r);w.fetch=async(u,o)=>{const response=await nativeFetch(u,o);if(String(u).endsWith('/admin/users'))await gate;return response;};
 const old=w.testAPI.setView('users');await new Promise(r=>setTimeout(r,25));await w.testAPI.setView('plans');release();await old;w.fetch=nativeFetch;assert.equal(d.getElementById('title').textContent,'套餐');record('stale_async_render_discarded');
 for(let i=0;i<24;i++)await w.testAPI.api('admin/plans',{name:'Page-plan-'+i,days:30,price_cents:100,traffic_bytes:1073741824,devices:1,enabled:true});
 await page('plans');assert.equal(d.querySelectorAll('#content tr').length,21);await click(d.getElementById('content'),'下一页');assert.equal(d.querySelectorAll('#content tr').length,6);
 const search=d.querySelector('#content input[type=search]');search.value='Page-plan-23';search.oninput();assert.equal(d.querySelectorAll('#content tr').length,2);record('search_and_pagination');
 await w.testAPI.api('admin/settings',{access:{registration:true,trial_hours:2}});await d.getElementById('logout').onclick();assert.equal(d.body.dataset.mode,'guest');
 d.getElementById('email').value='member@example.test';d.getElementById('password').value='member-password-for-dom-tests';await d.getElementById('register').onclick();assert.equal(d.body.dataset.mode,'member');assert.equal(d.querySelectorAll('#nav button').length,6);assert.equal(d.querySelector('#nav [data-view=users]'),null);assert.equal(d.querySelector('.metrics'),null);validIcons();
 await click(d.getElementById('content'),'激活 2 小时试用（1 GiB）');const usage=d.querySelector('progress');assert.ok(usage);assert.equal(usage.max,1073741824);assert.equal(usage.value,0);assert.equal(d.querySelector('.account-heading .pill').textContent,'可使用');await page('nodes');assert.equal(d.querySelector('#content .empty strong').textContent,'暂无记录');validIcons();
 await d.getElementById('logout').onclick();await login();await w.testAPI.api('admin/settings',{access:{registration:false,trial_hours:2}});record('member_dashboard_trial_and_role_scoped_navigation');
 await page('users');const own=row(settings.admin_email);assert.equal(buttons(own,'封禁').length,0);await click(own,'重置密码');fill({password:'new-password-for-dom-tests'});await submit();
 assert.equal(d.getElementById('login').hidden,false);await login('new-password-for-dom-tests');record('self_password_reset_requires_relogin');
 await jar.removeAllCookies();await page('users');assert.equal(d.getElementById('login').hidden,false);assert.equal(d.querySelectorAll('#nav button').length,0);record('expired_session_returns_to_login');
 w.fetch=async()=>new Response('<html>bad gateway</html>',{status:502});await assert.rejects(()=>w.testAPI.api('public/settings'),/无效响应/);record('non_json_server_error_is_actionable');
 const report={passed:true,checked_at:new Date().toISOString(),console_version:consoleVersion,visual_rendered:false,real_control_api:true,cases};
 await fs.writeFile(path.join(root,'docs/admin-dom-acceptance-'+consoleVersion+'.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));
}finally{await dom?.window.testAPI?.drain();dom?.window.close();child.kill();await new Promise(r=>child.once('exit',r));}
