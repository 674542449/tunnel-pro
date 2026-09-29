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
        <p class="login-sub">Secure &amp; Fast Proxy</p>

        <div v-if="loginMode === 'login' || loginMode === 'register'" class="login-form">
          <input v-model="email" type="email" placeholder="Email" @keyup.enter="doAuth"/>
          <input v-model="pass" type="password" placeholder="Password" @keyup.enter="doAuth"/>
          <button class="btn-primary" @click="doAuth" :disabled="loading">
            {{ loading ? '...' : (loginMode === 'login' ? 'Login' : 'Register') }}
          </button>
          <div class="login-links">
            <a v-if="loginMode === 'login'" @click="loginMode = 'register'">Create account</a>
            <a v-else @click="loginMode = 'login'">Back to login</a>
          </div>
        </div>

        <div v-else class="login-form">
          <button class="btn-primary" @click="loginMode = 'login'">Email Login</button>
          <button class="btn-outline" @click="doGuest" :disabled="loading">
            {{ loading ? '...' : 'Guest Login' }}
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
            <div class="status-label">{{ connected ? 'Connected' : 'Disconnected' }}</div>
            <div class="status-node" v-if="connected">{{ currentNode }}</div>
          </div>
        </div>
        <button class="btn-icon" @click="showSettings = !showSettings" title="Settings">
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5">
            <circle cx="10" cy="10" r="3"/>
            <path d="M10 1v2M10 17v2M1 10h2M17 10h2M3.5 3.5l1.4 1.4M15.1 15.1l1.4 1.4M3.5 16.5l1.4-1.4M15.1 4.9l1.4-1.4"/>
          </svg>
        </button>
      </div>

      <!-- Update banner -->
      <div v-if="updateInfo.available" class="update-banner" @click="openUpdate">
        New version {{ updateInfo.version }} available — click to download
      </div>

      <!-- Settings dropdown -->
      <div v-if="showSettings" class="settings-panel">
        <div class="settings-item">
          <span>Proxy Port</span>
          <span class="settings-val">7890</span>
        </div>
        <div class="settings-item">
          <span>Mode</span>
          <span class="settings-val">SOCKS5 + HTTP</span>
        </div>
        <button class="btn-logout" @click="logout">Logout</button>
      </div>

      <!-- Node list -->
      <div class="node-list">
        <div class="node-list-header">
          <span>Nodes</span>
          <button class="btn-text" @click="refreshNodes" :disabled="loading">
            {{ loading ? 'Loading...' : 'Refresh' }}
          </button>
        </div>

        <div v-if="nodes.length === 0 && !loading" class="empty-state">
          No available nodes
        </div>

        <div
          v-for="node in nodes"
          :key="node.id"
          :class="['node-card', { active: status.nodeId === node.id }]"
          @click="toggleConnect(node)"
        >
          <div class="node-left">
            <span class="node-flag">{{ node.flag }}</span>
            <div>
              <div class="node-name">{{ node.name }}</div>
              <div class="node-region">{{ node.region }}</div>
            </div>
          </div>
          <div class="node-right">
            <span v-if="node.latency > 0" class="node-latency">{{ node.latency }}ms</span>
            <span v-else-if="node.latency === -1" class="node-latency timeout">Timeout</span>
            <button
              v-if="status.nodeId === node.id && connected"
              class="btn-disconnect"
              @click.stop="disconnect"
            >
              Disconnect
            </button>
            <button
              v-else
              class="btn-connect"
              @click.stop="connectNode(node)"
              :disabled="connecting"
            >
              {{ connecting && connectingId === node.id ? '...' : 'Connect' }}
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
import { ref, onMounted, onUnmounted } from 'vue'
import { Login, Register, GuestLogin, IsLoggedIn, Logout, GetNodes, Connect, Disconnect, GetStatus, GetSpeed, ResetSpeed, TestLatency, HideWindow, OpenURL } from '../wailsjs/go/main/App'
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
const updateInfo = ref({ available: false, version: '', url: '' })

let speedInterval = null

function minimize() { WindowMinimise() }
function hideWindow() { HideWindow() }
function openUpdate() { if (updateInfo.value.url) OpenURL(updateInfo.value.url) }

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

async function refreshNodes() {
  loading.value = true
  try {
    const list = await GetNodes()
    nodes.value = list || []
    for (const n of nodes.value) {
      TestLatency(n.id).then(ms => { n.latency = ms })
    }
  } catch (e) {
    console.error(e)
  }
  loading.value = false
}

async function connectNode(node) {
  connecting.value = true
  connectingId.value = node.id
  try {
    await Connect(node.id)
  } catch (e) {
    error.value = e
    alert('Connection failed: ' + e)
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
    await ResetSpeed()
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
    await refreshNodes()
  }

  EventsOn('connection-changed', (s) => {
    status.value = s
    connected.value = s.connected
    if (s.connected) currentNode.value = s.nodeName
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
  margin-top: 8px;
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
  font-size: 26px;
  line-height: 1;
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
