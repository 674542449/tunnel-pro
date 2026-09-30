<template>
  <div class="app">
    <!-- Custom titlebar -->
    <div class="titlebar" style="--wails-draggable:drag">
      <div class="titlebar-title">Tunnel Pro</div>
      <div class="titlebar-buttons" style="--wails-draggable:no-drag">
        <button class="tb-btn" @click="minimize">&#x2013;</button>
        <button class="tb-btn tb-close" @click="hideWindow">&#x2715;</button>
      </div>
    </div>

    <!-- Login view -->
    <div v-if="!loggedIn" class="login-view">
      <div class="login-card">
        <div class="login-logo">
          <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
            <rect width="48" height="48" rx="12" fill="#da7756"/>
            <path d="M14 24c0-5.5 4.5-10 10-10s10 4.5 10 10-4.5 10-10 10" stroke="#fff" stroke-width="3" stroke-linecap="round"/>
            <path d="M24 34c5.5 0 10-4.5 10-10" stroke="#fff" stroke-width="3" stroke-linecap="round" stroke-dasharray="4 4"/>
          </svg>
        </div>
        <h2>Tunnel Pro</h2>
        <p class="login-sub">安全高速代理</p>

        <div v-if="loginMode === 'login' || loginMode === 'register'" class="login-form">
          <input v-model="email" type="email" placeholder="邮箱" @keyup.enter="doAuth"/>
          <input v-model="pass" type="password" placeholder="密码" @keyup.enter="doAuth"/>
          <button class="btn-primary" @click="doAuth" :disabled="loading">
            {{ loading ? '...' : (loginMode === 'login' ? '登录' : '注册') }}
          </button>
          <div class="login-links">
            <a v-if="loginMode === 'login'" @click="loginMode = 'register'">创建账号</a>
            <a v-else @click="loginMode = 'login'">返回登录</a>
          </div>
        </div>

        <div v-else class="login-form">
          <button class="btn-primary" @click="loginMode = 'login'">邮箱登录</button>
          <button class="btn-outline" @click="doGuest" :disabled="loading">
            {{ loading ? '...' : '游客登录' }}
          </button>
        </div>

        <p v-if="error" class="error-msg">{{ error }}</p>
      </div>
    </div>

    <!-- Home view -->
    <div v-else class="home-view">
      <!-- Status header -->
      <div class="status-header">
        <div class="status-left">
          <div :class="['status-dot', connected ? 'on' : 'off']"></div>
          <div>
            <div class="status-label">{{ connected ? '已连接' : '未连接' }}</div>
            <div class="status-node" v-if="connected">{{ currentNode }}</div>
          </div>
        </div>
        <div class="header-right">
          <button class="btn-icon" @click="showSettings = !showSettings; showProfile = false" title="设置">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="10" cy="10" r="3"/>
              <path d="M10 1v2M10 17v2M1 10h2M17 10h2M3.5 3.5l1.4 1.4M15.1 15.1l1.4 1.4M3.5 16.5l1.4-1.4M15.1 4.9l1.4-1.4"/>
            </svg>
          </button>
          <button class="avatar-btn" @click="openProfile" :title="profile ? profile.email || '游客' : '我的'">
            <div class="avatar" :style="avatarStyle">{{ avatarLetter }}</div>
          </button>
        </div>
      </div>

      <!-- Reconnecting banner -->
      <div v-if="reconnecting" class="reconnect-banner">
        正在重连...
      </div>

      <!-- Update banner -->
      <div v-if="updateInfo.available && !reconnecting" class="update-banner" @click="openUpdate">
        发现新版本 {{ updateInfo.version }} — 点击下载
      </div>

      <!-- Settings dropdown -->
      <div v-if="showSettings" class="settings-panel">
        <div class="settings-item">
          <span>代理端口</span>
          <span class="settings-val">7890</span>
        </div>
        <div class="settings-item">
          <span>协议</span>
          <span class="settings-val">SOCKS5 + HTTP</span>
        </div>
      </div>

      <!-- Profile panel (overlay) -->
      <div v-if="showProfile" class="profile-overlay" @click.self="showProfile = false">
        <div class="profile-panel">
          <div class="profile-header">
            <div class="profile-avatar" :style="avatarStyle">{{ avatarLetter }}</div>
            <div class="profile-info">
              <div class="profile-name">{{ profile ? (profile.email || '游客用户') : '...' }}</div>
              <div class="profile-id">ID: {{ profile ? profile.id : '-' }}</div>
            </div>
            <button class="profile-close" @click="showProfile = false">&#x2715;</button>
          </div>

          <!-- Membership -->
          <div class="profile-section">
            <div class="section-title">会员信息</div>
            <div class="info-grid">
              <div class="info-item">
                <div class="info-label">状态</div>
                <div :class="['info-val', profile && profile.active ? 'val-green' : 'val-red']">
                  {{ profile && profile.active ? '有效' : '已过期' }}
                </div>
              </div>
              <div class="info-item">
                <div class="info-label">到期时间</div>
                <div class="info-val">{{ profile ? formatDate(profile.expires_at) : '-' }}</div>
              </div>
              <div class="info-item">
                <div class="info-label">剩余天数</div>
                <div class="info-val">{{ profile ? daysLeft(profile.expires_at) : '-' }}</div>
              </div>
              <div class="info-item">
                <div class="info-label">注册时间</div>
                <div class="info-val">{{ profile ? formatDate(profile.created_at) : '-' }}</div>
              </div>
            </div>
          </div>

          <!-- Traffic -->
          <div class="profile-section">
            <div class="section-title">流量统计</div>
            <div class="traffic-stats">
              <div class="traffic-item">
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--green)" stroke-width="1.5">
                  <path d="M7 11V3M3 7l4-4 4 4"/>
                </svg>
                <span>上传 {{ formatBytes(profile ? profile.upload : 0) }}</span>
              </div>
              <div class="traffic-item">
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--accent)" stroke-width="1.5">
                  <path d="M7 3v8M3 7l4 4 4-4"/>
                </svg>
                <span>下载 {{ formatBytes(profile ? profile.download : 0) }}</span>
              </div>
            </div>
            <div class="traffic-total">
              总计 {{ formatBytes(profile ? (profile.upload + profile.download) : 0) }}
            </div>
          </div>

          <!-- Orders -->
          <div class="profile-section">
            <div class="section-title" @click="showOrders = !showOrders" style="cursor:pointer">
              订单记录
              <span class="section-toggle">{{ showOrders ? '收起' : '展开' }}</span>
            </div>
            <div v-if="showOrders">
              <div v-if="orders.length === 0" class="empty-hint">暂无订单</div>
              <div v-for="o in orders" :key="o.id" class="order-item">
                <div class="order-top">
                  <span class="order-plan">{{ o.plan }}</span>
                  <span :class="['order-status', 'os-' + o.status]">{{ orderStatusText(o.status) }}</span>
                </div>
                <div class="order-bottom">
                  <span>¥{{ o.amount.toFixed(2) }}</span>
                  <span>{{ formatDate(o.created_at) }}</span>
                </div>
              </div>
            </div>
          </div>

          <!-- Change password -->
          <div v-if="profile && profile.email" class="profile-section">
            <div class="section-title" @click="showPwdForm = !showPwdForm" style="cursor:pointer">
              修改密码
              <span class="section-toggle">{{ showPwdForm ? '收起' : '展开' }}</span>
            </div>
            <div v-if="showPwdForm" class="pwd-form">
              <input v-model="oldPwd" type="password" placeholder="当前密码" />
              <input v-model="newPwd" type="password" placeholder="新密码 (至少6位)" />
              <div class="pwd-actions">
                <button class="btn-sm" @click="doChangePassword" :disabled="pwdLoading">
                  {{ pwdLoading ? '...' : '确认修改' }}
                </button>
              </div>
              <div v-if="pwdMsg" :class="['pwd-msg', pwdOk ? 'msg-ok' : 'msg-err']">{{ pwdMsg }}</div>
            </div>
          </div>

          <!-- Logout -->
          <div class="profile-section">
            <button class="btn-logout" @click="logout">退出登录</button>
          </div>
        </div>
      </div>

      <!-- Node list -->
      <div v-if="!showProfile" class="node-list">
        <div class="node-list-header">
          <span>节点列表</span>
          <button class="btn-text" @click="refreshNodes" :disabled="loading">
            {{ loading ? '加载中...' : '刷新' }}
          </button>
        </div>

        <div v-if="nodes.length === 0 && !loading" class="empty-state">
          暂无可用节点
        </div>

        <div
          v-for="node in nodes"
          :key="node.id"
          :class="['node-card', { active: status.nodeId === node.id }]"
          @click="toggleConnect(node)"
        >
          <div class="node-left">
            <img class="node-flag" :src="flagUrl(node.region)" :alt="node.region" />
            <div>
              <div class="node-name">{{ node.name }}</div>
              <div class="node-region">{{ regionName(node.region) }}</div>
            </div>
          </div>
          <div class="node-right">
            <span v-if="node.latency > 0" class="node-latency">{{ node.latency }}ms</span>
            <span v-else-if="node.latency === -1" class="node-latency timeout">超时</span>
            <button
              v-if="status.nodeId === node.id && connected"
              class="btn-disconnect"
              @click.stop="disconnect"
            >
              断开
            </button>
            <button
              v-else
              class="btn-connect"
              @click.stop="connectNode(node)"
              :disabled="connecting"
            >
              {{ connecting && connectingId === node.id ? '...' : '连接' }}
            </button>
          </div>
        </div>
      </div>

      <!-- Speed bar -->
      <div class="speed-bar">
        <div class="speed-item">
          <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--green)" stroke-width="1.5">
            <path d="M7 11V3M3 7l4-4 4 4"/>
          </svg>
          <span>{{ formatSpeed(speed.upload) }}</span>
        </div>
        <div class="speed-item">
          <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--accent)" stroke-width="1.5">
            <path d="M7 3v8M3 7l4 4 4-4"/>
          </svg>
          <span>{{ formatSpeed(speed.download) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Login, Register, GuestLogin, IsLoggedIn, Logout, GetNodes, Connect, Disconnect, GetStatus, GetSpeed, TestLatency, HideWindow, OpenURL, GetLastNodeID, GetProfile, GetOrders, ChangePassword } from '../wailsjs/go/main/App'
import { WindowMinimise, EventsOn } from '../wailsjs/runtime/runtime'

const loggedIn = ref(false)
const loginMode = ref('choose')
const email = ref('')
const pass = ref('')
const error = ref('')
const loading = ref(false)
const nodes = ref([])
const connected = ref(false)
const currentNode = ref('')
const status = ref({ connected: false, nodeName: '', nodeId: -1 })
const speed = ref({ upload: 0, download: 0 })
const connecting = ref(false)
const connectingId = ref(-1)
const showSettings = ref(false)
const reconnecting = ref(false)
const updateInfo = ref({ available: false, version: '', url: '' })

const showProfile = ref(false)
const profile = ref(null)
const orders = ref([])
const showOrders = ref(false)
const showPwdForm = ref(false)
const oldPwd = ref('')
const newPwd = ref('')
const pwdLoading = ref(false)
const pwdMsg = ref('')
const pwdOk = ref(false)

let speedInterval = null

const avatarColors = ['#da7756','#3b82f6','#22c55e','#f59e0b','#8b5cf6','#ec4899','#14b8a6','#ef4444']

const avatarLetter = computed(() => {
  if (!profile.value || !profile.value.email) return 'G'
  return profile.value.email.charAt(0).toUpperCase()
})

const avatarStyle = computed(() => {
  const letter = avatarLetter.value
  const idx = letter.charCodeAt(0) % avatarColors.length
  return { background: avatarColors[idx] }
})

const regionNameMap = {
  JP: '日本', KR: '韩国', US: '美国', DE: '德国', SG: '新加坡',
  HK: '香港', TW: '台湾', GB: '英国', FR: '法国', CA: '加拿大',
  AU: '澳大利亚', NL: '荷兰', IN: '印度', RU: '俄罗斯', BR: '巴西', TR: '土耳其',
}

function flagUrl(region) {
  const code = (region || '').toLowerCase()
  return code ? `https://flagcdn.com/w40/${code}.png` : ''
}
function regionName(region) {
  return regionNameMap[(region || '').toUpperCase()] || region || ''
}

function minimize() { WindowMinimise() }
function hideWindow() { HideWindow() }
function openUpdate() { if (updateInfo.value.url) OpenURL(updateInfo.value.url) }

function formatDate(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0')
}

function daysLeft(ts) {
  if (!ts) return '-'
  const diff = ts - Math.floor(Date.now() / 1000)
  if (diff <= 0) return '已过期'
  const days = Math.ceil(diff / 86400)
  return days + ' 天'
}

function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 B'
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB'
  if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB'
  return (bytes / 1073741824).toFixed(2) + ' GB'
}

function orderStatusText(s) {
  const m = { pending: '待支付', paid: '已支付', expired: '已过期', cancelled: '已取消' }
  return m[s] || s
}

async function openProfile() {
  showProfile.value = true
  showSettings.value = false
  showOrders.value = false
  showPwdForm.value = false
  pwdMsg.value = ''
  try {
    const p = await GetProfile()
    if (p) profile.value = p
  } catch {}
  try {
    const o = await GetOrders()
    orders.value = o || []
  } catch {}
}

async function doChangePassword() {
  pwdMsg.value = ''
  if (newPwd.value.length < 6) {
    pwdMsg.value = '新密码至少6位'
    pwdOk.value = false
    return
  }
  pwdLoading.value = true
  try {
    await ChangePassword(oldPwd.value, newPwd.value)
    pwdMsg.value = '密码修改成功'
    pwdOk.value = true
    oldPwd.value = ''
    newPwd.value = ''
  } catch (e) {
    pwdMsg.value = String(e)
    pwdOk.value = false
  }
  pwdLoading.value = false
}

async function doAuth() {
  error.value = ''
  loading.value = true
  try {
    if (loginMode.value === 'login') {
      await Login(email.value, pass.value)
    } else {
      await Register(email.value, pass.value)
    }
    loggedIn.value = true
    await refreshNodes()
  } catch (e) {
    error.value = e
  }
  loading.value = false
}

async function doGuest() {
  error.value = ''
  loading.value = true
  try {
    await GuestLogin()
    loggedIn.value = true
    await refreshNodes()
  } catch (e) {
    error.value = e
  }
  loading.value = false
}

async function refreshNodes(autoConnectId) {
  loading.value = true
  try {
    const list = await GetNodes()
    nodes.value = list || []
    const promises = nodes.value.map(n =>
      TestLatency(n.id).then(ms => { n.latency = ms })
    )
    Promise.all(promises).then(() => {
      nodes.value.sort((a, b) => {
        const la = a.latency <= 0 ? 99999 : a.latency
        const lb = b.latency <= 0 ? 99999 : b.latency
        return la - lb
      })
      if (autoConnectId && autoConnectId > 0 && !connected.value) {
        const node = nodes.value.find(n => n.id === autoConnectId)
        if (node) connectNode(node, true)
      }
    })
  } catch (e) {
    console.error(e)
  }
  loading.value = false
}

async function connectNode(node, silent) {
  connecting.value = true
  connectingId.value = node.id
  try {
    await Connect(node.id)
  } catch (e) {
    error.value = e
    if (!silent) alert('连接失败: ' + e)
  }
  connecting.value = false
  connectingId.value = -1
}

async function toggleConnect(node) {
  if (status.value.nodeId === node.id && connected.value) {
    await disconnect()
  } else {
    await connectNode(node)
  }
}

async function disconnect() {
  await Disconnect()
}

async function logout() {
  await Disconnect()
  await Logout()
  loggedIn.value = false
  loginMode.value = 'choose'
  nodes.value = []
  showSettings.value = false
  showProfile.value = false
  profile.value = null
  orders.value = []
}

function formatSpeed(bytes) {
  if (bytes < 1024) return bytes + ' B/s'
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB/s'
  return (bytes / 1048576).toFixed(1) + ' MB/s'
}

async function pollSpeed() {
  try {
    const s = await GetSpeed()
    speed.value = s
  } catch {}
}

async function pollStatus() {
  try {
    const s = await GetStatus()
    status.value = s
    connected.value = s.connected
    if (s.connected) currentNode.value = s.nodeName
  } catch {}
}

onMounted(async () => {
  const isLogged = await IsLoggedIn()
  if (isLogged) {
    loggedIn.value = true
    const lastId = await GetLastNodeID()
    await refreshNodes(lastId)
    try {
      const p = await GetProfile()
      if (p) profile.value = p
    } catch {}
  }

  EventsOn('connection-changed', (s) => {
    status.value = s
    connected.value = s.connected
    if (s.connected) currentNode.value = s.nodeName
  })

  EventsOn('reconnecting', (isReconnecting) => {
    reconnecting.value = isReconnecting
  })

  EventsOn('update-available', (info) => {
    updateInfo.value = info
  })

  speedInterval = setInterval(() => {
    pollSpeed()
    pollStatus()
  }, 1000)
})

onUnmounted(() => {
  if (speedInterval) clearInterval(speedInterval)
})
</script>

<style scoped>
.app {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.titlebar {
  height: 36px;
  background: var(--titlebar);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 8px 0 14px;
  color: #ffffff;
  font-size: 13px;
  font-weight: 600;
  flex-shrink: 0;
}
.titlebar-buttons {
  display: flex;
  gap: 2px;
}
.tb-btn {
  width: 28px;
  height: 28px;
  background: none;
  color: #aaa;
  font-size: 13px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all .15s;
}
.tb-btn:hover { background: rgba(255,255,255,.1); color: #fff; }
.tb-close:hover { background: #ef4444; color: #fff; }

.login-view {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.login-card {
  width: 100%;
  max-width: 320px;
  text-align: center;
}
.login-logo {
  margin-bottom: 16px;
}
.login-card h2 {
  font-size: 22px;
  font-weight: 700;
  margin-bottom: 4px;
}
.login-sub {
  color: var(--text-secondary);
  font-size: 13px;
  margin-bottom: 28px;
}
.login-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.login-form input {
  padding: 12px 14px;
  border: 1.5px solid var(--border);
  border-radius: 10px;
  font-size: 14px;
  background: var(--card);
  color: var(--text);
  outline: none;
  transition: border-color .2s;
}
.login-form input:focus {
  border-color: var(--accent);
}
.btn-primary {
  padding: 12px;
  background: var(--accent);
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  border-radius: 10px;
  transition: background .2s;
}
.btn-primary:hover { background: var(--accent-hover); }
.btn-primary:disabled { opacity: .6; }
.btn-outline {
  padding: 12px;
  background: transparent;
  color: var(--accent);
  font-size: 14px;
  font-weight: 600;
  border: 1.5px solid var(--accent);
  border-radius: 10px;
  transition: all .2s;
}
.btn-outline:hover { background: var(--accent); color: #fff; }
.login-links {
  margin-top: 4px;
}
.login-links a {
  color: var(--accent);
  font-size: 13px;
  cursor: pointer;
}
.login-links a:hover { text-decoration: underline; }
.error-msg {
  color: var(--red);
  font-size: 13px;
  margin-top: 10px;
}

.update-banner {
  margin: 0 18px 8px;
  padding: 10px 14px;
  background: #dbeafe;
  color: #1d4ed8;
  font-size: 12px;
  font-weight: 600;
  border-radius: 10px;
  cursor: pointer;
  text-align: center;
  transition: background .2s;
}
.update-banner:hover { background: #bfdbfe; }

.reconnect-banner {
  margin: 0 18px 8px;
  padding: 10px 14px;
  background: #fef3cd;
  color: #856404;
  font-size: 12px;
  border-radius: 8px;
  text-align: center;
  animation: pulse-bg 1.5s ease-in-out infinite;
}
@keyframes pulse-bg {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.6; }
}

.home-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.status-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 18px 12px;
}
.status-left {
  display: flex;
  align-items: center;
  gap: 10px;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.status-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}
.status-dot.on {
  background: var(--green);
  box-shadow: 0 0 8px rgba(34,197,94,.5);
  animation: pulse 2s infinite;
}
.status-dot.off {
  background: #9ca3af;
}
@keyframes pulse {
  0%, 100% { box-shadow: 0 0 8px rgba(34,197,94,.5); }
  50% { box-shadow: 0 0 16px rgba(34,197,94,.7); }
}
.status-label {
  font-size: 14px;
  font-weight: 600;
}
.status-node {
  font-size: 12px;
  color: var(--text-secondary);
}
.btn-icon {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  background: var(--card);
  color: var(--text-secondary);
  transition: all .2s;
  box-shadow: 0 1px 3px rgba(0,0,0,.06);
}
.btn-icon:hover { color: var(--accent); }

/* Avatar */
.avatar-btn {
  background: none;
  padding: 0;
  cursor: pointer;
}
.avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 15px;
  font-weight: 700;
  box-shadow: 0 1px 4px rgba(0,0,0,.12);
  transition: transform .15s, box-shadow .15s;
}
.avatar-btn:hover .avatar {
  transform: scale(1.08);
  box-shadow: 0 2px 8px rgba(0,0,0,.18);
}

/* Profile panel */
.profile-overlay {
  flex: 1;
  overflow-y: auto;
  padding: 0 14px 8px;
}
.profile-panel {
  background: var(--card);
  border-radius: 14px;
  box-shadow: 0 2px 12px rgba(0,0,0,.08);
  overflow: hidden;
}
.profile-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 18px 16px;
  background: linear-gradient(135deg, var(--accent) 0%, #c4653e 100%);
  color: #fff;
}
.profile-avatar {
  width: 46px;
  height: 46px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  font-weight: 700;
  color: #fff;
  border: 2px solid rgba(255,255,255,.4);
  flex-shrink: 0;
}
.profile-info {
  flex: 1;
  min-width: 0;
}
.profile-name {
  font-size: 15px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.profile-id {
  font-size: 11px;
  opacity: .8;
  margin-top: 2px;
}
.profile-close {
  width: 28px;
  height: 28px;
  background: rgba(255,255,255,.2);
  color: #fff;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  flex-shrink: 0;
  transition: background .15s;
}
.profile-close:hover { background: rgba(255,255,255,.35); }

.profile-section {
  padding: 14px 16px;
  border-top: 1px solid var(--border);
}
.section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 10px;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.section-toggle {
  font-size: 11px;
  color: var(--accent);
  font-weight: 500;
}

/* Info grid */
.info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.info-item {
  background: var(--bg);
  padding: 10px 12px;
  border-radius: 10px;
}
.info-label {
  font-size: 11px;
  color: var(--text-secondary);
  margin-bottom: 3px;
}
.info-val {
  font-size: 13px;
  font-weight: 600;
}
.val-green { color: var(--green); }
.val-red { color: var(--red); }

/* Traffic */
.traffic-stats {
  display: flex;
  gap: 20px;
  margin-bottom: 6px;
}
.traffic-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
}
.traffic-total {
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 4px;
}

/* Orders */
.empty-hint {
  font-size: 12px;
  color: var(--text-secondary);
  text-align: center;
  padding: 12px 0;
}
.order-item {
  padding: 10px 12px;
  background: var(--bg);
  border-radius: 10px;
  margin-bottom: 6px;
}
.order-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}
.order-plan {
  font-size: 13px;
  font-weight: 600;
}
.order-status {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 10px;
  font-weight: 500;
}
.os-paid { background: #dcfce7; color: #16a34a; }
.os-pending { background: #fef3cd; color: #ca8a04; }
.os-expired { background: var(--red-bg); color: var(--red); }
.os-cancelled { background: #f3f4f6; color: #6b7280; }
.order-bottom {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: var(--text-secondary);
}

/* Password form */
.pwd-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.pwd-form input {
  padding: 10px 12px;
  border: 1.5px solid var(--border);
  border-radius: 8px;
  font-size: 13px;
  background: var(--bg);
  color: var(--text);
  outline: none;
}
.pwd-form input:focus { border-color: var(--accent); }
.pwd-actions {
  display: flex;
  justify-content: flex-end;
}
.btn-sm {
  padding: 8px 18px;
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  border-radius: 8px;
  transition: background .2s;
}
.btn-sm:hover { background: var(--accent-hover); }
.btn-sm:disabled { opacity: .6; }
.pwd-msg {
  font-size: 12px;
  margin-top: 2px;
}
.msg-ok { color: var(--green); }
.msg-err { color: var(--red); }

.settings-panel {
  margin: 0 18px 8px;
  padding: 12px 14px;
  background: var(--card);
  border-radius: 12px;
  box-shadow: 0 2px 8px rgba(0,0,0,.06);
}
.settings-item {
  display: flex;
  justify-content: space-between;
  padding: 8px 0;
  font-size: 13px;
  border-bottom: 1px solid var(--border);
}
.settings-item:last-of-type { border-bottom: none; }
.settings-val {
  color: var(--text-secondary);
  font-weight: 500;
}
.btn-logout {
  width: 100%;
  padding: 10px;
  background: var(--red-bg);
  color: var(--red);
  font-size: 13px;
  font-weight: 600;
  border-radius: 8px;
  transition: background .2s;
}
.btn-logout:hover { background: #fecaca; }

.node-list {
  flex: 1;
  overflow-y: auto;
  padding: 0 18px 8px;
}
.node-list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
}
.btn-text {
  background: none;
  color: var(--accent);
  font-size: 12px;
  font-weight: 600;
}
.btn-text:hover { text-decoration: underline; }
.btn-text:disabled { opacity: .5; }

.empty-state {
  text-align: center;
  padding: 40px 0;
  color: var(--text-secondary);
  font-size: 14px;
}

.node-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px;
  margin-bottom: 8px;
  background: var(--card);
  border-radius: 12px;
  border: 1.5px solid transparent;
  box-shadow: 0 1px 4px rgba(0,0,0,.04);
  cursor: pointer;
  transition: all .2s;
}
.node-card:hover {
  border-color: var(--border);
  box-shadow: 0 2px 8px rgba(0,0,0,.06);
}
.node-card.active {
  border-color: var(--green);
  background: #f0fdf4;
}
.node-left {
  display: flex;
  align-items: center;
  gap: 12px;
}
.node-flag {
  width: 30px;
  height: 22px;
  border-radius: 3px;
  object-fit: cover;
  flex-shrink: 0;
}
.node-name {
  font-size: 14px;
  font-weight: 600;
}
.node-region {
  font-size: 12px;
  color: var(--text-secondary);
}
.node-right {
  display: flex;
  align-items: center;
  gap: 10px;
}
.node-latency {
  font-size: 12px;
  color: var(--green);
  font-weight: 500;
}
.node-latency.timeout {
  color: var(--red);
}
.btn-connect, .btn-disconnect {
  padding: 6px 14px;
  font-size: 12px;
  font-weight: 600;
  border-radius: 8px;
  transition: all .2s;
}
.btn-connect {
  background: var(--accent);
  color: #fff;
}
.btn-connect:hover { background: var(--accent-hover); }
.btn-connect:disabled { opacity: .5; }
.btn-disconnect {
  background: var(--red-bg);
  color: var(--red);
}
.btn-disconnect:hover { background: #fecaca; }

.speed-bar {
  display: flex;
  justify-content: center;
  gap: 32px;
  padding: 12px 18px;
  background: var(--card);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
}
.speed-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
}
</style>
