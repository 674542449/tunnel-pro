import {productVersion} from './product-version.mjs';
// DOM acceptance with actual embedded HTTP assets and an enforced MFA policy.
// No graphical browser, production credentials, or MFA secrets in reports.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {spawn} from 'node:child_process';
import crypto from 'node:crypto';
import vm from 'node:vm';

const baseline = process.argv.includes('--baseline');
const version = baseline ? 'v0.5.0' : (process.env.TUNNELX_CONSOLE_VERSION || productVersion);
const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const require = createRequire(path.join(root, 'tools/admin-ui-test/package.json'));
const {JSDOM, CookieJar} = require('jsdom');
const stage = await fs.mkdtemp(path.join(root, '.local/web-tests/mfa-'));
const listener = net.createServer();
await new Promise(r => listener.listen(0, '127.0.0.1', r));
const port = listener.address().port;
await new Promise(r => listener.close(r));
const base = 'http://127.0.0.1:' + port;
const settings = {listen:'127.0.0.1:'+port, public_url:base, data_file:path.join(stage,'state.json'), admin_email:'admin@example.test', admin_password:'mfa-ui-test-password', registration:true, trial_hours:0, commercial:{enabled:true, payment_mode:'test', security_key:crypto.randomBytes(32).toString('base64'), require_admin_mfa:true, beta_invite_only:true}, mail:{mode:'test'}};
await fs.writeFile(path.join(stage, 'config.json'), JSON.stringify(settings));
const child = spawn(path.join(root,'dist/control-'+version+'/windows-amd64/tunnelx-control.exe'), ['-config',path.join(stage,'config.json')], {windowsHide:true, stdio:['ignore','pipe','pipe']});
let stderr = '', dom, challenge;
child.stderr.on('data', b => stderr += b);
child.stdout.on('data', () => {});
const cases = [];
const sleep = ms => new Promise(r => setTimeout(r, ms));
function totp(secret) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of secret.replace(/=+$/, '')) bits += alphabet.indexOf(c).toString(2).padStart(5,'0');
  const bytes = [];
  for (let i=0; i+8<=bits.length; i+=8) bytes.push(parseInt(bits.slice(i,i+8),2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
  const h = crypto.createHmac('sha1',Buffer.from(bytes)).update(counter).digest();
  return String((h.readUInt32BE(h[19]&15)&0x7fffffff)%1000000).padStart(6,'0');
}
try {
  let ready = false;
  for (let i=0;i<100;i++) { if(child.exitCode!==null) throw Error(stderr); try {if((await fetch(base+'/health')).ok){ready=true;break;}}catch{} await sleep(50); }
  assert.ok(ready,'Controller startup');
  let jar = new CookieJar();
  const request = async (url, init={}) => {
    url = String(url);
    const headers = new Headers(init.headers);
    headers.set('Cookie',await jar.getCookieString(url));
    if(init.method==='POST') headers.set('Origin',base);
    const response = await fetch(url,{...init,headers});
    for(const cookie of response.headers.getSetCookie()) await jar.setCookie(cookie,url);
    if(url.includes('/security/mfa/begin')&&response.ok) challenge=await response.clone().json();
    return response;
  };
  // Establish the cookie before loading the page, matching a returning admin.
  assert.equal((await request(base+'/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:settings.admin_email,password:settings.admin_password})})).status,200);
  const preEnrollmentJar = new CookieJar();
  const separateLogin = await fetch(base+'/api/login',{method:'POST',headers:{'Content-Type':'application/json','Origin':base},body:JSON.stringify({email:settings.admin_email,password:settings.admin_password})});
  assert.equal(separateLogin.status,200);
  for(const cookie of separateLogin.headers.getSetCookie()) await preEnrollmentJar.setCookie(cookie,base);
  dom = new JSDOM(await (await fetch(base+'/')).text(),{url:base+'/', runScripts:'outside-only',cookieJar:jar});
  const w=dom.window,d=w.document;
  w.AbortController=globalThis.AbortController;
  w.Blob=globalThis.Blob;
  w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
  w.HTMLDialogElement.prototype.close=function(){this.removeAttribute('open');};
  w.fetch=request;
  const scripts=[...d.querySelectorAll('script[src]')];
  for(let i=0;i<scripts.length;i++) {
    // Expose the old split-script race by allowing the warm identity API to finish.
    if(i) await sleep(400);
    vm.runInContext(await (await fetch(scripts[i].src)).text(),dom.getInternalVMContext(),{filename:scripts[i].src});
  }
  await sleep(400);
  if(baseline) {
    assert.equal(scripts.length,2);
    assert.match(d.getElementById('message').textContent,/commerceView is not defined/);
    assert.equal(d.getElementById('content').children.length,0);
    const report={reproduced:true,version,require_admin_mfa:true,warm_session:true,second_script_delay_ms:400,error:'commerceView is not defined',blank_security_page:true,visual_rendered:false};
    await fs.writeFile(path.join(root,'.local/mfa-fix-v0.5.1/baseline.json'),JSON.stringify(report,null,2)+'\n');
    console.log(JSON.stringify(report));
  } else {
    assert.equal(scripts.length,1,'Account security must load atomically with the shell');
    assert.equal(d.getElementById('title').textContent,'账号安全');
    assert.ok(d.getElementById('content').textContent.includes('开启二次验证'));
    assert.equal(d.getElementById('mfa-gate').hidden,false);
    assert.equal(d.getElementById('message').textContent,'');
    cases.push('warm_session_security_view_with_one_served_bundle');
    const protectedResponse=await request(base+'/api/admin/summary');
    assert.equal(protectedResponse.status,403);
    const denied=await protectedResponse.json();
    assert.equal(denied.mfa_required,true);
    assert.equal(denied.mfa_enabled,false);
    assert.equal(denied.security_url,base+'/#security');
    cases.push('server_still_enforces_mfa_and_returns_actionable_security_url');
    await d.getElementById('mfa-gate-link').onclick({preventDefault(){}});
    assert.equal(w.location.hash,'#security');
    for(const b of d.querySelectorAll('#nav button')) {
      await b.onclick();
      assert.equal(d.getElementById('title').textContent,'账号安全');
      assert.equal(d.getElementById('message').textContent,'');
    }
    cases.push('all_blocked_navigation_guides_to_accessible_security_setup');
    const click=async(scope,label)=>{const b=[...scope.querySelectorAll('button')].find(b=>b.textContent===label);assert.ok(b,'Missing button '+label);await b.onclick();};
    const submit=()=>d.getElementById('edit-form').onsubmit({preventDefault(){},target:d.getElementById('edit-form')});
    const fill=values=>{for(const[k,v]of Object.entries(values))d.querySelector('#fields [name='+k+']').value=v;};
    await click(d.getElementById('content'),'开启二次验证');
    fill({password:settings.admin_password});
    await submit();
    assert.equal(d.getElementById('dialog-error').textContent,'','MFA enrollment request');
    assert.equal(d.getElementById('dialog-title').textContent,'绑定验证器');
    assert.ok(challenge.secret);
    fill({code:totp(challenge.secret)});
    await submit();
    const codes=d.querySelector('#fields textarea').value.trim().split('\n');
    assert.equal(codes.length,10);
    assert.equal(d.getElementById('dialog-title').textContent,'二次验证恢复码（仅显示一次）');
    assert.equal(d.getElementById('mfa-gate').hidden,true);
    assert.ok(!d.getElementById('content').textContent.includes('关闭二次验证'));
    d.getElementById('cancel').onclick();
    await click(d.getElementById('content'),'返回管理后台');
    assert.equal(d.getElementById('title').textContent,'总览');
    assert.equal(w.location.hash,'');
    cases.push('password_totp_enrollment_recovery_codes_and_automatic_unlock');
    for(const b of d.querySelectorAll('#nav button')) {
      await b.onclick();
      assert.equal(d.getElementById('message').textContent,'','Navigation '+b.dataset.view);
      assert.ok(d.getElementById('content').children.length,'Empty page '+b.dataset.view);
      assert.equal(b.getAttribute('aria-current'),'page');
    }
    cases.push('every_admin_page_accessible_after_mfa');
    await d.getElementById('logout').onclick();
    await d.getElementById('security-entry').onclick({preventDefault(){}});
    assert.equal(w.location.hash,'#security');
    const login=async(code='')=>{d.getElementById('email').value=settings.admin_email;d.getElementById('password').value=settings.admin_password;d.getElementById('otp').value=code;await d.getElementById('auth-form').onsubmit({preventDefault(){}});};
    await login();
    assert.equal(d.getElementById('login').hidden,false);
    assert.equal(d.getElementById('otp-wrap').hidden,false);
    await login(codes[0]);
    assert.equal(d.getElementById('login').hidden,true,d.getElementById('message').textContent);
    assert.equal(d.getElementById('title').textContent,'账号安全');
    cases.push('direct_security_entry_and_existing_mfa_recovery_login');
    await d.getElementById('logout').onclick();
    await login(codes[0]);
    assert.equal(d.getElementById('login').hidden,false,'Recovery code replay');
    cases.push('used_recovery_code_cannot_be_replayed');
    await login(codes[1]);
    assert.equal(d.getElementById('login').hidden,true);
    // Enrollment must revoke other preexisting sessions.
    jar=preEnrollmentJar;
    await assert.rejects(vm.runInContext('refreshIdentity()',dom.getInternalVMContext()));
    assert.equal((await request(base+'/api/admin/summary')).status,401);
    assert.equal(d.getElementById('login').hidden,false);
    await login(codes[2]);
    assert.equal(d.getElementById('login').hidden,true);
    assert.equal(d.getElementById('mfa-gate').hidden,true);
    cases.push('enrollment_revokes_other_sessions_and_recovery_login_remains_available');
    const report={passed:true,checked_at:new Date().toISOString(),console_version:version,require_admin_mfa:true,real_control_api:true,actual_embedded_assets:true,visual_rendered:false,cases};
    await fs.writeFile(path.join(root,'docs/admin-mfa-dom-'+version+'.json'),JSON.stringify(report,null,2)+'\n');
    console.log(JSON.stringify(report));
  }
} catch(error) {
  console.error(error);
  throw error;
} finally {
  await sleep(300);
  dom?.window.close();
  child.kill();
  if(child.exitCode===null) await new Promise(r=>child.once('exit',r));
}
