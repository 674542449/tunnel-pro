<template>
  <div :class="['app', { dark: isDark }]">
    <!-- Custom titlebar -->
    <div class="titlebar" style="--wails-draggable:drag">
      <div class="titlebar-title">Tunnel Pro</div>
      <div class="titlebar-buttons" style="--wails-draggable:no-drag">
        <button class="tb-btn" @click="minimize">&#x2013;</button>
        <button class="tb-btn tb-close" @click="hideWindow">&#x2715;</button>
      </div>
    </div>

    <!-- Toast -->
    <transition name="toast-fade">
      <div v-if="toast.show" :class="['toast', 'toast-' + toast.type]">{{ toast.msg }}</div>
    </transition>

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

        <div v-if="loginMode !== 'choose'" class="login-form">
          <div class="input-wrap">
            <input v-model="email" type="email" placeholder="邮箱" @keyup.enter="$refs.passInput?.focus()" @blur="onEmailBlur"/>
          </div>
          <div class="input-wrap">
            <input ref="passInput" v-model="pass" :type="showPass ? 'text' : 'password'" placeholder="密码" @keyup.enter="loginMode === 'register' ? $refs.pass2Input?.focus() : doAuth()"/>
            <button class="eye-btn" @click="showPass = !showPass" tabindex="-1">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                <path v-if="showPass" d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8S1 12 1 12z"/><circle v-if="showPass" cx="12" cy="12" r="3"/>
                <path v-if="!showPass" d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19M1 1l22 22"/>
              </svg>
            </button>
          </div>
          <div v-if="loginMode === 'register'" class="input-wrap">
            <input ref="pass2Input" v-model="pass2" :type="showPass ? 'text' : 'password'" placeholder="确认密码" @keyup.enter="doAuth"/>
          </div>
          <div v-if="loginMode === 'register'" class="input-wrap">
            <input v-model="inviteCode" type="text" placeholder="邀请码（选填）" @keyup.enter="doAuth"/>
          </div>
          <div v-if="emailChecking" class="email-hint">检查中...</div>
          <button class="btn-primary" @click="doAuth" :disabled="loading">
            {{ loading ? '...' : (loginMode === 'login' ? '登录' : '注册') }}
          </button>
          <div class="login-links">
            <a v-if="loginMode === 'login'" @click="loginMode = 'register'">没有账号？注册</a>
            <a v-else @click="loginMode = 'login'">已有账号？登录</a>
            <span class="link-dot">·</span>
            <a @click="loginMode = 'choose'">返回</a>
          </div>
        </div>

        <div v-else class="login-form">
          <button class="btn-primary" @click="loginMode = 'login'">邮箱登录</button>
          <button class="btn-outline" @click="loginMode = 'register'">邮箱注册</button>
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
            <div class="status-node" v-if="connected">{{ currentNode }} <span v-if="connDuration" class="conn-time">{{ connDuration }}</span></div>
          </div>
        </div>
        <div class="header-right">
          <button class="btn-icon" @click="toggleDark" :title="isDark ? '浅色模式' : '深色模式'">
            <svg v-if="!isDark" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
            <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
          </button>
          <button class="btn-icon" @click="showSettings = !showSettings; showProfile = false" title="设置">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="3"/>
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
            </svg>
          </button>
          <button class="avatar-btn" @click="openProfile">
            <div class="avatar" :style="avatarStyle">{{ avatarLetter }}</div>
            <span class="avatar-label">个人中心</span>
          </button>
        </div>
      </div>

      <!-- Reconnecting banner -->
      <div v-if="reconnecting" class="reconnect-banner">正在重连...</div>

      <!-- Update banner -->
      <div v-if="updateInfo.available && !reconnecting" class="update-banner" @click="openUpdate">
        发现新版本 {{ updateInfo.version }} — 点击下载
      </div>

      <!-- Announcements banner -->
      <div v-if="announcements.length > 0 && !reconnecting && !updateInfo.available" class="announce-banner" @click="showAnnouncementModal = true">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>
        <span>{{ announcements[0].title }}</span>
        <span v-if="announcements.length > 1" class="announce-more">+{{ announcements.length - 1 }}</span>
      </div>

      <!-- Announcement modal -->
      <div v-if="showAnnouncementModal" class="announce-overlay" @click.self="showAnnouncementModal = false">
        <div class="announce-modal">
          <div class="announce-modal-header">
            <span>公告</span>
            <button class="profile-close" @click="showAnnouncementModal = false">&#x2715;</button>
          </div>
          <div class="announce-list">
            <div v-for="a in announcements" :key="a.id" :class="['announce-item', 'al-' + a.level]">
              <div class="announce-title">
                <svg v-if="a.level === 'urgent'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="2"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
                <svg v-else-if="a.level === 'warning'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
                <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#3b82f6" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
                {{ a.title }}
              </div>
              <div class="announce-content">{{ a.content }}</div>
              <div class="announce-time">{{ formatDateTime(a.created_at) }}</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Settings dropdown -->
      <div v-if="showSettings" class="settings-panel">
        <div class="settings-item">
          <span>代理地址</span>
          <span class="settings-val copy-val" @click="copyProxy" title="点击复制">127.0.0.1:7890
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="margin-left:4px;vertical-align:middle"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </span>
        </div>
        <div class="settings-item">
          <span>协议</span>
          <span class="settings-val">SOCKS5 + HTTP</span>
        </div>
        <div class="settings-item">
          <span>代理模式</span>
        </div>
        <div class="mode-selector">
          <button :class="['mode-btn', { active: proxyMode === 'global' }]" @click="setMode('global')">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="10"/><path d="M2 12h20M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10A15.3 15.3 0 0 1 12 2z"/></svg>
            全局代理
          </button>
          <button :class="['mode-btn', { active: proxyMode === 'bypass' }]" @click="setMode('bypass')">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"/></svg>
            绕过大陆
          </button>
          <button :class="['mode-btn', { active: proxyMode === 'tun' }]" @click="setMode('tun')">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
            TUN 模式
          </button>
        </div>
        <div class="mode-hint">
          <span v-if="proxyMode === 'global'">所有流量经过代理</span>
          <span v-else-if="proxyMode === 'bypass'">中国大陆网站直连，其余走代理</span>
          <span v-else>虚拟网卡全局接管（首次需下载组件）</span>
        </div>
        <div class="settings-item">
          <span>Kill Switch</span>
          <label class="switch">
            <input type="checkbox" :checked="killSwitch" @change="toggleKillSwitch"/>
            <span class="slider"></span>
          </label>
        </div>
        <div class="mode-hint" v-if="killSwitch">代理断开时自动切断网络，防止 IP 泄露</div>
        <div class="settings-item">
          <span>连接日志</span>
          <button class="btn-text" @click="openConnLog">查看</button>
        </div>
        <div class="settings-item" style="border-bottom:none">
          <span>版本</span>
          <span class="settings-val">v{{ appVersion }}</span>
        </div>
      </div>

      <!-- Connection log modal -->
      <div v-if="showConnLog" class="announce-overlay" @click.self="showConnLog = false">
        <div class="announce-modal">
          <div class="announce-modal-header">
            <span>连接日志</span>
            <button class="profile-close" @click="showConnLog = false">&#x2715;</button>
          </div>
          <div class="connlog-list">
            <div v-if="connLog.length === 0" class="empty-hint">暂无日志</div>
            <div v-for="(log, i) in connLog" :key="i" class="connlog-item">
              <div class="connlog-left">
                <span :class="['connlog-action', log.action === 'connect' ? 'cla-on' : (log.action === 'disconnect' ? 'cla-off' : 'cla-warn')]">
                  {{ logActionText(log.action) }}
                </span>
                <span class="connlog-node">{{ log.node }}</span>
              </div>
              <div class="connlog-right">
                <span v-if="log.detail" class="connlog-detail">{{ log.detail }}</span>
                <span class="connlog-time">{{ formatDateTime(log.time) }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Profile panel -->
      <div v-if="showProfile" class="profile-overlay">
        <div class="profile-panel">
          <div class="profile-header">
            <div class="profile-avatar" :style="avatarStyle">{{ avatarLetter }}</div>
            <div class="profile-info">
              <div class="profile-name">{{ profile ? (profile.email || '游客用户') : '...' }}</div>
              <div class="profile-id">ID: {{ profile ? profile.id : '-' }}</div>
            </div>
            <button class="profile-close" @click="showProfile = false">&#x2715;</button>
          </div>

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

          <div class="profile-section">
            <div class="section-title">流量统计</div>
            <div class="traffic-stats">
              <div class="traffic-item">
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--green)" stroke-width="1.5"><path d="M7 11V3M3 7l4-4 4 4"/></svg>
                <span>上传 {{ formatBytes(profile ? profile.upload : 0) }}</span>
              </div>
              <div class="traffic-item">
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--accent)" stroke-width="1.5"><path d="M7 3v8M3 7l4 4 4-4"/></svg>
                <span>下载 {{ formatBytes(profile ? profile.download : 0) }}</span>
              </div>
            </div>
            <div class="traffic-total">总计 {{ formatBytes(profile ? (profile.upload + profile.download) : 0) }}</div>
          </div>

          <div class="profile-section">
            <div class="section-title" @click="showOrders = !showOrders" style="cursor:pointer">
              订单记录 <span class="section-toggle">{{ showOrders ? '收起' : '展开' }}</span>
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

          <!-- Bind email (guest only) -->
          <div v-if="profile && !profile.email" class="profile-section">
            <div class="section-title">绑定邮箱</div>
            <p class="bind-hint">绑定后可使用邮箱登录，保留账号数据</p>
            <div class="pwd-form">
              <input v-model="bindEmail" type="email" placeholder="邮箱" />
              <div class="input-wrap">
                <input v-model="bindPass" :type="showBindPass ? 'text' : 'password'" placeholder="设置密码 (至少6位)" />
                <button class="eye-btn" @click="showBindPass = !showBindPass" tabindex="-1">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                    <path v-if="showBindPass" d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8S1 12 1 12z"/><circle v-if="showBindPass" cx="12" cy="12" r="3"/>
                    <path v-if="!showBindPass" d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19M1 1l22 22"/>
                  </svg>
                </button>
              </div>
              <div class="pwd-actions">
                <button class="btn-sm" @click="doBindEmail" :disabled="bindLoading">{{ bindLoading ? '...' : '绑定' }}</button>
              </div>
              <div v-if="bindMsg" :class="['pwd-msg', bindOk ? 'msg-ok' : 'msg-err']">{{ bindMsg }}</div>
            </div>
          </div>

          <!-- Change password (email users only) -->
          <div v-if="profile && profile.email" class="profile-section">
            <div class="section-title" @click="showPwdForm = !showPwdForm" style="cursor:pointer">
              修改密码 <span class="section-toggle">{{ showPwdForm ? '收起' : '展开' }}</span>
            </div>
            <div v-if="showPwdForm" class="pwd-form">
              <input v-model="oldPwd" type="password" placeholder="当前密码" />
              <input v-model="newPwd" type="password" placeholder="新密码 (至少6位)" />
              <div class="pwd-actions">
                <button class="btn-sm" @click="doChangePassword" :disabled="pwdLoading">{{ pwdLoading ? '...' : '确认修改' }}</button>
              </div>
              <div v-if="pwdMsg" :class="['pwd-msg', pwdOk ? 'msg-ok' : 'msg-err']">{{ pwdMsg }}</div>
            </div>
          </div>

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

        <div v-if="nodes.length === 0 && !loading" class="empty-state">暂无可用节点</div>

        <div
          v-for="node in sortedNodes"
          :key="node.id"
          :class="['node-card', { active: status.nodeId === node.id }]"
          @click="toggleConnect(node)"
        >
          <div class="node-left">
            <button class="fav-btn" @click.stop="toggleFav(node.id)" :title="isFav(node.id) ? '取消收藏' : '收藏'">
              <svg width="16" height="16" viewBox="0 0 24 24" :fill="isFav(node.id) ? '#f59e0b' : 'none'" :stroke="isFav(node.id) ? '#f59e0b' : 'var(--text-secondary)'" stroke-width="2">
                <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>
              </svg>
            </button>
            <img class="node-flag" :src="flagUrl(node.region)" :alt="node.region" />
            <div>
              <div class="node-name">{{ node.name }}</div>
              <div class="node-region">{{ regionName(node.region) }}</div>
            </div>
          </div>
          <div class="node-right">
            <span v-if="node.latency > 0" class="node-latency">{{ node.latency }}ms</span>
            <span v-else-if="node.latency === -1" class="node-latency timeout">超时</span>
            <button v-if="status.nodeId === node.id && connected" class="btn-disconnect" @click.stop="disconnect">断开</button>
            <button v-else class="btn-connect" @click.stop="connectNode(node)" :disabled="connecting">
              {{ connecting && connectingId === node.id ? '...' : '连接' }}
            </button>
          </div>
        </div>
      </div>

      <!-- Speed bar -->
      <div class="speed-bar">
        <div class="expire-info" v-if="profile">
          <span v-if="!profile.active" class="expire-warn">会员已过期</span>
          <span v-else :class="expireDays <= 7 ? 'expire-warn' : 'expire-ok'">{{ formatDate(profile.expires_at) }} 到期 (剩余{{ expireDays }}天)</span>
        </div>
        <div class="speed-group">
          <div class="speed-item">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--green)" stroke-width="1.5"><path d="M7 11V3M3 7l4-4 4 4"/></svg>
            <span>{{ formatSpeed(speed.upload) }}</span>
          </div>
          <div class="speed-item">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="var(--accent)" stroke-width="1.5"><path d="M7 3v8M3 7l4 4 4-4"/></svg>
            <span>{{ formatSpeed(speed.download) }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Login, Register, GuestLogin, IsLoggedIn, Logout, GetNodes, Connect, Disconnect, GetStatus, GetSpeed, TestLatency, HideWindow, OpenURL, GetLastNodeID, GetProfile, GetOrders, ChangePassword, CheckEmail, BindEmail, GetSavedEmail, ToggleFavorite, GetFavorites, SetDarkMode, GetDarkMode, CopyToClipboard, GetProxyMode, SetProxyMode, GetVersion, GetAnnouncements, GetConnLog, SetKillSwitch, GetKillSwitch } from '../wailsjs/go/main/App'
import { WindowMinimise, EventsOn } from '../wailsjs/runtime/runtime'

const loggedIn = ref(false)
const loginMode = ref('choose')
const email = ref('')
const pass = ref('')
const pass2 = ref('')
const showPass = ref(false)
const error = ref('')
const loading = ref(false)
const emailChecking = ref(false)
const nodes = ref([])
const connected = ref(false)
const currentNode = ref('')
const status = ref({ connected: false, nodeName: '', nodeId: -1, connectedAt: 0 })
const speed = ref({ upload: 0, download: 0 })
const connecting = ref(false)
const connectingId = ref(-1)
const showSettings = ref(false)
const reconnecting = ref(false)
const updateInfo = ref({ available: false, version: '', url: '' })
const connDuration = ref('')
const isDark = ref(false)
const proxyMode = ref('bypass')
const appVersion = ref('')
const favorites = ref([])

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

const bindEmail = ref('')
const bindPass = ref('')
const showBindPass = ref(false)
const bindLoading = ref(false)
const bindMsg = ref('')
const bindOk = ref(false)

const announcements = ref([])
const showAnnouncementModal = ref(false)
const killSwitch = ref(false)
const showConnLog = ref(false)
const connLog = ref([])
const inviteCode = ref('')

const toast = ref({ show: false, msg: '', type: 'info' })
let toastTimer = null

let speedInterval = null

const avatarColors = ['#da7756','#3b82f6','#22c55e','#f59e0b','#8b5cf6','#ec4899','#14b8a6','#ef4444']

const avatarLetter = computed(() => {
  if (!profile.value || !profile.value.email) return 'G'
  return profile.value.email.charAt(0).toUpperCase()
})
const avatarStyle = computed(() => {
  const idx = avatarLetter.value.charCodeAt(0) % avatarColors.length
  return { background: avatarColors[idx] }
})

const expireDays = computed(() => {
  if (!profile.value || !profile.value.expires_at) return 0
  const diff = profile.value.expires_at - Math.floor(Date.now() / 1000)
  return diff <= 0 ? 0 : Math.ceil(diff / 86400)
})

const sortedNodes = computed(() => {
  const fav = new Set(favorites.value)
  return [...nodes.value].sort((a, b) => {
    const af = fav.has(a.id) ? 0 : 1
    const bf = fav.has(b.id) ? 0 : 1
    return af - bf
  })
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
function isFav(id) { return favorites.value.includes(id) }

function showToast(msg, type = 'info') {
  toast.value = { show: true, msg, type }
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value.show = false }, 2500)
}

function formatDate(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0')
}
function daysLeft(ts) {
  if (!ts) return '-'
  const diff = ts - Math.floor(Date.now() / 1000)
  if (diff <= 0) return '已过期'
  return Math.ceil(diff / 86400) + ' 天'
}
function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 B'
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB'
  if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB'
  return (bytes / 1073741824).toFixed(2) + ' GB'
}
function formatSpeed(bytes) {
  if (bytes < 1024) return bytes + ' B/s'
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB/s'
  return (bytes / 1048576).toFixed(1) + ' MB/s'
}
function orderStatusText(s) {
  return { pending: '待支付', paid: '已支付', expired: '已过期', cancelled: '已取消' }[s] || s
}

function formatDateTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  return d.getFullYear() + '-' + String(d.getMonth()+1).padStart(2,'0') + '-' + String(d.getDate()).padStart(2,'0') + ' ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0')
}

function logActionText(action) {
  return { connect: '连接', disconnect: '断开', lost: '断线', reconnect: '重连', 'reconnect-fail': '重连失败' }[action] || action
}

async function toggleKillSwitch() {
  killSwitch.value = !killSwitch.value
  await SetKillSwitch(killSwitch.value)
  showToast(killSwitch.value ? 'Kill Switch 已开启' : 'Kill Switch 已关闭', 'success')
}

async function openConnLog() {
  try {
    const logs = await GetConnLog()
    connLog.value = logs || []
  } catch {}
  showConnLog.value = true
}

async function loadAnnouncements() {
  try {
    const list = await GetAnnouncements()
    announcements.value = list || []
  } catch {}
}

function updateConnDuration() {
  const at = status.value.connectedAt
  if (!at || !connected.value) { connDuration.value = ''; return }
  const sec = Math.floor(Date.now() / 1000) - at
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  connDuration.value = String(h).padStart(2,'0') + ':' + String(m).padStart(2,'0') + ':' + String(s).padStart(2,'0')
}

async function onEmailBlur() {
  const e = email.value.trim()
  if (!e || !e.includes('@')) return
  emailChecking.value = true
  try {
    const exists = await CheckEmail(e)
    loginMode.value = exists ? 'login' : 'register'
  } catch {}
  emailChecking.value = false
}

async function doAuth() {
  error.value = ''
  if (loginMode.value === 'register' && pass.value !== pass2.value) {
    error.value = '两次密码不一致'
    return
  }
  if (loginMode.value === 'register' && pass.value.length < 6) {
    error.value = '密码至少6位'
    return
  }
  loading.value = true
  try {
    if (loginMode.value === 'login') {
      await Login(email.value, pass.value)
    } else {
      await Register(email.value, pass.value, inviteCode.value.trim())
    }
    loggedIn.value = true
    showToast(loginMode.value === 'login' ? '登录成功' : '注册成功', 'success')
    await refreshNodes()
    loadAnnouncements()
    try { const p = await GetProfile(); if (p) profile.value = p } catch {}
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
    showToast('游客登录成功', 'success')
    await refreshNodes()
    loadAnnouncements()
    try { const p = await GetProfile(); if (p) profile.value = p } catch {}
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
    refreshLatencies()
  } catch (e) {
    console.error(e)
  }
  loading.value = false
}

async function refreshLatencies() {
  nodes.value.forEach(n => {
    TestLatency(n.id).then(ms => { n.latency = ms }).catch(() => {})
  })
}

async function connectNode(node, silent) {
  connecting.value = true
  connectingId.value = node.id
  try {
    await Connect(node.id)
    if (!silent) showToast('已连接 ' + node.name, 'success')
  } catch (e) {
    error.value = e
    if (!silent) showToast('连接失败: ' + e, 'error')
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
  showToast('已断开连接', 'info')
}

async function toggleFav(nodeId) {
  try {
    const newFavs = await ToggleFavorite(nodeId)
    favorites.value = newFavs || []
  } catch {}
}

async function toggleDark() {
  isDark.value = !isDark.value
  await SetDarkMode(isDark.value)
  applyTheme()
}
function applyTheme() {
  if (isDark.value) {
    document.body.classList.add('dark')
  } else {
    document.body.classList.remove('dark')
  }
}

function copyProxy() {
  CopyToClipboard('127.0.0.1:7890')
  showToast('已复制代理地址', 'success')
}

async function setMode(mode) {
  proxyMode.value = mode
  await SetProxyMode(mode)
  const labels = { global: '全局代理', bypass: '绕过大陆', tun: 'TUN 模式' }
  showToast('已切换：' + labels[mode], 'success')
}

async function openProfile() {
  showProfile.value = true
  showSettings.value = false
  showOrders.value = false
  showPwdForm.value = false
  pwdMsg.value = ''
  bindMsg.value = ''
  try { const p = await GetProfile(); if (p) profile.value = p } catch {}
  try { const o = await GetOrders(); orders.value = o || [] } catch {}
}

async function doChangePassword() {
  pwdMsg.value = ''
  if (newPwd.value.length < 6) { pwdMsg.value = '新密码至少6位'; pwdOk.value = false; return }
  pwdLoading.value = true
  try {
    await ChangePassword(oldPwd.value, newPwd.value)
    pwdMsg.value = '密码修改成功'; pwdOk.value = true
    oldPwd.value = ''; newPwd.value = ''
  } catch (e) { pwdMsg.value = String(e); pwdOk.value = false }
  pwdLoading.value = false
}

async function doBindEmail() {
  bindMsg.value = ''
  if (!bindEmail.value.includes('@')) { bindMsg.value = '请输入有效邮箱'; bindOk.value = false; return }
  if (bindPass.value.length < 6) { bindMsg.value = '密码至少6位'; bindOk.value = false; return }
  bindLoading.value = true
  try {
    await BindEmail(bindEmail.value, bindPass.value)
    bindMsg.value = '绑定成功'; bindOk.value = true
    showToast('邮箱绑定成功', 'success')
    try { const p = await GetProfile(); if (p) profile.value = p } catch {}
  } catch (e) { bindMsg.value = String(e); bindOk.value = false }
  bindLoading.value = false
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
  showToast('已退出登录', 'info')
}

async function pollSpeed() {
  try { const s = await GetSpeed(); speed.value = s } catch {}
}
async function pollStatus() {
  try {
    const s = await GetStatus()
    status.value = s
    connected.value = s.connected
    if (s.connected) currentNode.value = s.nodeName
    updateConnDuration()
  } catch {}
}

function checkExpireReminder(p) {
  if (!p || !p.expires_at) return
  const diff = p.expires_at - Math.floor(Date.now() / 1000)
  const days = diff <= 0 ? 0 : Math.ceil(diff / 86400)
  if (days > 7) return
  const today = new Date().toISOString().slice(0, 10)
  const key = 'expire_remind_' + today
  try { if (localStorage.getItem(key)) return } catch {}
  try { localStorage.setItem(key, '1') } catch {}
  if (days === 0) {
    showToast('您的会员已过期，请续费', 'error')
  } else {
    showToast('您的会员将在 ' + days + ' 天后到期，请及时续费', 'error')
  }
}

onMounted(async () => {
  try { const dm = await GetDarkMode(); if (dm !== null) isDark.value = dm } catch {}
  applyTheme()
  try { const favs = await GetFavorites(); favorites.value = favs || [] } catch {}
  try { const pm = await GetProxyMode(); if (pm) proxyMode.value = pm } catch {}
  try { appVersion.value = await GetVersion() } catch {}

  try { killSwitch.value = await GetKillSwitch() } catch {}

  const isLogged = await IsLoggedIn()
  if (isLogged) {
    loggedIn.value = true
    await refreshNodes()
    loadAnnouncements()
    try {
      const p = await GetProfile()
      if (p) {
        profile.value = p
        checkExpireReminder(p)
      }
    } catch {}
  } else {
    try { const se = await GetSavedEmail(); if (se) email.value = se } catch {}
  }

  EventsOn('connection-changed', (s) => {
    status.value = s
    connected.value = s.connected
    if (s.connected) currentNode.value = s.nodeName
  })
  EventsOn('reconnecting', (v) => { reconnecting.value = v })
  EventsOn('update-available', (info) => { updateInfo.value = info })

  speedInterval = setInterval(() => { pollSpeed(); pollStatus() }, 1000)
})

onUnmounted(() => {
  if (speedInterval) clearInterval(speedInterval)
})
</script>

<style>
:root {
  --bg: #f9f5ef; --card: #ffffff; --text: #1a1a2e; --text-secondary: #6b7280;
  --border: #e5e1da; --accent: #da7756; --accent-hover: #c4653e;
  --green: #22c55e; --red: #ef4444; --red-bg: #fef2f2; --titlebar: #1a1a2e;
}
body.dark, body.dark #app {
  --bg: #1a1a2e; --card: #232340; --text: #e2e8f0; --text-secondary: #8b92a8;
  --border: #2d2d4a; --accent: #da7756; --accent-hover: #c4653e;
  --green: #22c55e; --red: #ef4444; --red-bg: #3b1c1c; --titlebar: #12122a;
}
*, *::before, *::after { margin: 0; padding: 0; box-sizing: border-box; }
html, body, #app { height: 100%; font-family: 'Inter', system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--text); }
button { border: none; cursor: pointer; font-family: inherit; }
input { font-family: inherit; }
a { text-decoration: none; }
::-webkit-scrollbar { width: 5px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: var(--border); border-radius: 3px; }
</style>

<style scoped>
.app { height: 100%; display: flex; flex-direction: column; background: var(--bg); color: var(--text); transition: background .3s, color .3s; }

.titlebar { height: 36px; background: var(--titlebar); display: flex; align-items: center; justify-content: space-between; padding: 0 8px 0 14px; color: #fff; font-size: 13px; font-weight: 600; flex-shrink: 0; }
.titlebar-buttons { display: flex; gap: 2px; }
.tb-btn { width: 28px; height: 28px; background: none; color: #aaa; font-size: 13px; border-radius: 6px; display: flex; align-items: center; justify-content: center; transition: all .15s; }
.tb-btn:hover { background: rgba(255,255,255,.1); color: #fff; }
.tb-close:hover { background: #ef4444; color: #fff; }

/* Toast */
.toast { position: fixed; top: 44px; left: 50%; transform: translateX(-50%); padding: 8px 20px; border-radius: 8px; font-size: 12px; font-weight: 600; z-index: 999; box-shadow: 0 4px 12px rgba(0,0,0,.15); }
.toast-success { background: #dcfce7; color: #16a34a; }
.toast-error { background: #fee2e2; color: #dc2626; }
.toast-info { background: #dbeafe; color: #2563eb; }
.app.dark .toast-success { background: #1a3a1a; color: #4ade80; }
.app.dark .toast-error { background: #3b1c1c; color: #f87171; }
.app.dark .toast-info { background: #1e2a4a; color: #60a5fa; }
.app.dark .update-banner { background: #1e2a4a; color: #60a5fa; }
.app.dark .update-banner:hover { background: #263564; }
.app.dark .btn-logout { background: #3b1c1c; }
.app.dark .btn-logout:hover { background: #501c1c; }
.app.dark .os-paid { background: #1a3a1a; color: #4ade80; }
.app.dark .os-pending { background: #3a2f10; color: #fbbf24; }
.app.dark .os-cancelled { background: #2d2d4a; color: #8b92a8; }
.toast-fade-enter-active, .toast-fade-leave-active { transition: opacity .3s, transform .3s; }
.toast-fade-enter-from, .toast-fade-leave-to { opacity: 0; transform: translateX(-50%) translateY(-8px); }

.login-view { flex: 1; display: flex; align-items: center; justify-content: center; padding: 20px; }
.login-card { width: 100%; max-width: 320px; text-align: center; }
.login-logo { margin-bottom: 16px; }
.login-card h2 { font-size: 22px; font-weight: 700; margin-bottom: 4px; }
.login-sub { color: var(--text-secondary); font-size: 13px; margin-bottom: 28px; }
.login-form { display: flex; flex-direction: column; gap: 10px; }
.login-form input, .pwd-form input { padding: 12px 14px; border: 1.5px solid var(--border); border-radius: 10px; font-size: 14px; background: var(--card); color: var(--text); outline: none; transition: border-color .2s; width: 100%; box-sizing: border-box; }
.login-form input:focus, .pwd-form input:focus { border-color: var(--accent); }
.input-wrap { position: relative; }
.input-wrap input { padding-right: 40px; }
.eye-btn { position: absolute; right: 10px; top: 50%; transform: translateY(-50%); background: none; color: var(--text-secondary); padding: 4px; cursor: pointer; }
.eye-btn:hover { color: var(--accent); }
.email-hint { font-size: 11px; color: var(--text-secondary); text-align: left; }

.btn-primary { padding: 12px; background: var(--accent); color: #fff; font-size: 14px; font-weight: 600; border-radius: 10px; transition: background .2s; }
.btn-primary:hover { background: var(--accent-hover); }
.btn-primary:disabled { opacity: .6; }
.btn-outline { padding: 12px; background: transparent; color: var(--accent); font-size: 14px; font-weight: 600; border: 1.5px solid var(--accent); border-radius: 10px; transition: all .2s; }
.btn-outline:hover { background: var(--accent); color: #fff; }
.login-links { margin-top: 8px; display: flex; align-items: center; justify-content: center; gap: 8px; }
.login-links a { color: var(--accent); font-size: 13px; cursor: pointer; }
.login-links a:hover { text-decoration: underline; }
.link-dot { color: var(--text-secondary); font-size: 13px; user-select: none; }
.error-msg { color: var(--red); font-size: 13px; margin-top: 10px; }

.update-banner { margin: 0 18px 8px; padding: 10px 14px; background: #dbeafe; color: #1d4ed8; font-size: 12px; font-weight: 600; border-radius: 10px; cursor: pointer; text-align: center; }
.update-banner:hover { background: #bfdbfe; }
.reconnect-banner { margin: 0 18px 8px; padding: 10px 14px; background: #fef3cd; color: #856404; font-size: 12px; border-radius: 8px; text-align: center; animation: pulse-bg 1.5s ease-in-out infinite; }
@keyframes pulse-bg { 0%, 100% { opacity: 1; } 50% { opacity: 0.6; } }

.home-view { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
.status-header { display: flex; align-items: center; justify-content: space-between; padding: 14px 18px 10px; }
.status-left { display: flex; align-items: center; gap: 10px; }
.header-right { display: flex; align-items: center; gap: 6px; }
.status-dot { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
.status-dot.on { background: var(--green); box-shadow: 0 0 8px rgba(34,197,94,.5); animation: pulse 2s infinite; }
.status-dot.off { background: #9ca3af; }
@keyframes pulse { 0%, 100% { box-shadow: 0 0 8px rgba(34,197,94,.5); } 50% { box-shadow: 0 0 16px rgba(34,197,94,.7); } }
.status-label { font-size: 14px; font-weight: 600; }
.status-node { font-size: 12px; color: var(--text-secondary); }
.conn-time { color: var(--green); font-weight: 600; font-size: 11px; margin-left: 6px; }

.btn-icon { width: 34px; height: 34px; display: flex; align-items: center; justify-content: center; border-radius: 10px; background: var(--card); color: var(--text-secondary); transition: all .2s; box-shadow: 0 1px 3px rgba(0,0,0,.06); }
.btn-icon:hover { color: var(--accent); }

.avatar-btn { background: none; padding: 0; cursor: pointer; display: flex; align-items: center; gap: 5px; }
.avatar-label { font-size: 11px; color: var(--text-secondary); font-weight: 600; white-space: nowrap; }
.avatar { width: 34px; height: 34px; border-radius: 50%; display: flex; align-items: center; justify-content: center; color: #fff; font-size: 14px; font-weight: 700; box-shadow: 0 1px 4px rgba(0,0,0,.12); transition: transform .15s; }
.avatar-btn:hover .avatar { transform: scale(1.08); }

.profile-overlay { flex: 1; overflow-y: auto; padding: 0 14px 8px; }
.profile-panel { background: var(--card); border-radius: 14px; box-shadow: 0 2px 12px rgba(0,0,0,.08); overflow: hidden; }
.profile-header { display: flex; align-items: center; gap: 12px; padding: 18px 16px; background: linear-gradient(135deg, var(--accent) 0%, #c4653e 100%); color: #fff; }
.profile-avatar { width: 46px; height: 46px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 20px; font-weight: 700; color: #fff; border: 2px solid rgba(255,255,255,.4); flex-shrink: 0; }
.profile-info { flex: 1; min-width: 0; }
.profile-name { font-size: 15px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.profile-id { font-size: 11px; opacity: .8; margin-top: 2px; }
.profile-close { width: 28px; height: 28px; background: rgba(255,255,255,.2); color: #fff; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; }
.profile-close:hover { background: rgba(255,255,255,.35); }

.profile-section { padding: 14px 16px; border-top: 1px solid var(--border); }
.section-title { font-size: 13px; font-weight: 600; color: var(--text-secondary); margin-bottom: 10px; display: flex; justify-content: space-between; align-items: center; }
.section-toggle { font-size: 11px; color: var(--accent); font-weight: 500; }

.info-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
.info-item { background: var(--bg); padding: 10px 12px; border-radius: 10px; }
.info-label { font-size: 11px; color: var(--text-secondary); margin-bottom: 3px; }
.info-val { font-size: 13px; font-weight: 600; }
.val-green { color: var(--green); }
.val-red { color: var(--red); }

.traffic-stats { display: flex; gap: 20px; margin-bottom: 6px; }
.traffic-item { display: flex; align-items: center; gap: 6px; font-size: 13px; font-weight: 500; }
.traffic-total { font-size: 12px; color: var(--text-secondary); margin-top: 4px; }

.empty-hint { font-size: 12px; color: var(--text-secondary); text-align: center; padding: 12px 0; }
.bind-hint { font-size: 12px; color: var(--text-secondary); margin: -4px 0 8px; }
.order-item { padding: 10px 12px; background: var(--bg); border-radius: 10px; margin-bottom: 6px; }
.order-top { display: flex; justify-content: space-between; align-items: center; margin-bottom: 4px; }
.order-plan { font-size: 13px; font-weight: 600; }
.order-status { font-size: 11px; padding: 2px 8px; border-radius: 10px; font-weight: 500; }
.os-paid { background: #dcfce7; color: #16a34a; }
.os-pending { background: #fef3cd; color: #ca8a04; }
.os-expired { background: var(--red-bg); color: var(--red); }
.os-cancelled { background: #f3f4f6; color: #6b7280; }
.order-bottom { display: flex; justify-content: space-between; font-size: 12px; color: var(--text-secondary); }

.pwd-form { display: flex; flex-direction: column; gap: 8px; }
.pwd-form input { padding: 10px 12px; border: 1.5px solid var(--border); border-radius: 8px; font-size: 13px; background: var(--bg); color: var(--text); outline: none; }
.pwd-form input:focus { border-color: var(--accent); }
.pwd-actions { display: flex; justify-content: flex-end; }
.btn-sm { padding: 8px 18px; background: var(--accent); color: #fff; font-size: 12px; font-weight: 600; border-radius: 8px; }
.btn-sm:hover { background: var(--accent-hover); }
.btn-sm:disabled { opacity: .6; }
.pwd-msg { font-size: 12px; margin-top: 2px; }
.msg-ok { color: var(--green); }
.msg-err { color: var(--red); }

.settings-panel { margin: 0 18px 8px; padding: 12px 14px; background: var(--card); border-radius: 12px; box-shadow: 0 2px 8px rgba(0,0,0,.06); }
.settings-item { display: flex; justify-content: space-between; padding: 8px 0; font-size: 13px; border-bottom: 1px solid var(--border); }
.settings-item:last-of-type { border-bottom: none; }
.settings-val { color: var(--text-secondary); font-weight: 500; }
.copy-val { cursor: pointer; display: flex; align-items: center; transition: color .2s; }
.copy-val:hover { color: var(--accent); }

.mode-selector { display: flex; gap: 6px; padding: 6px 0 4px; }
.mode-btn { flex: 1; display: flex; flex-direction: column; align-items: center; gap: 4px; padding: 10px 4px; border-radius: 10px; font-size: 11px; font-weight: 600; color: var(--text-secondary); background: var(--bg); border: 1.5px solid var(--border); transition: all .2s; cursor: pointer; }
.mode-btn:hover { border-color: var(--accent); color: var(--accent); }
.mode-btn.active { border-color: var(--accent); background: var(--accent); color: #fff; }
.mode-btn.active svg { stroke: #fff; }
.mode-hint { font-size: 11px; color: var(--text-secondary); padding: 2px 0 4px; text-align: center; }

.btn-logout { width: 100%; padding: 10px; background: var(--red-bg); color: var(--red); font-size: 13px; font-weight: 600; border-radius: 8px; transition: background .2s; }
.btn-logout:hover { background: #fecaca; }

.node-list { flex: 1; overflow-y: auto; padding: 0 18px 8px; }
.node-list-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; font-size: 13px; font-weight: 600; color: var(--text-secondary); }
.btn-text { background: none; color: var(--accent); font-size: 12px; font-weight: 600; }
.btn-text:hover { text-decoration: underline; }
.btn-text:disabled { opacity: .5; }

.empty-state { text-align: center; padding: 40px 0; color: var(--text-secondary); font-size: 14px; }

.node-card { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; margin-bottom: 8px; background: var(--card); border-radius: 12px; border: 1.5px solid transparent; box-shadow: 0 1px 4px rgba(0,0,0,.04); cursor: pointer; transition: all .2s; }
.node-card:hover { border-color: var(--border); box-shadow: 0 2px 8px rgba(0,0,0,.06); }
.node-card.active { border-color: var(--green); background: #f0fdf4; }
.app.dark .node-card.active { background: #1a2e1a; }
.node-left { display: flex; align-items: center; gap: 8px; }
.fav-btn { background: none; padding: 2px; cursor: pointer; flex-shrink: 0; display: flex; align-items: center; }
.fav-btn:hover svg { transform: scale(1.2); }
.node-flag { width: 28px; height: 20px; border-radius: 3px; object-fit: cover; flex-shrink: 0; }
.node-name { font-size: 13px; font-weight: 600; }
.node-region { font-size: 11px; color: var(--text-secondary); }
.node-right { display: flex; align-items: center; gap: 8px; }
.node-latency { font-size: 12px; color: var(--green); font-weight: 500; }
.node-latency.timeout { color: var(--red); }
.btn-connect, .btn-disconnect { padding: 6px 14px; font-size: 12px; font-weight: 600; border-radius: 8px; transition: all .2s; }
.btn-connect { background: var(--accent); color: #fff; }
.btn-connect:hover { background: var(--accent-hover); }
.btn-connect:disabled { opacity: .5; }
.btn-disconnect { background: var(--red-bg); color: var(--red); }
.btn-disconnect:hover { background: #fecaca; }

.speed-bar { display: flex; justify-content: space-between; align-items: center; padding: 10px 18px; background: var(--card); border-top: 1px solid var(--border); flex-shrink: 0; }
.expire-info { font-size: 11px; color: var(--text-secondary); }
.expire-ok { color: var(--green); font-weight: 600; }
.expire-warn { color: var(--red); font-weight: 600; }
.speed-group { display: flex; gap: 20px; }
.speed-item { display: flex; align-items: center; gap: 6px; font-size: 13px; font-weight: 500; color: var(--text-secondary); }

/* Announcement banner */
.announce-banner { margin: 0 18px 8px; padding: 10px 14px; background: #dbeafe; color: #1d4ed8; font-size: 12px; font-weight: 600; border-radius: 10px; cursor: pointer; display: flex; align-items: center; gap: 8px; }
.announce-banner:hover { background: #bfdbfe; }
.announce-more { font-size: 11px; opacity: .7; }
.app.dark .announce-banner { background: #1e2a4a; color: #60a5fa; }
.app.dark .announce-banner:hover { background: #263564; }

/* Announcement modal */
.announce-overlay { position: fixed; top: 36px; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,.4); z-index: 100; display: flex; align-items: flex-start; justify-content: center; padding: 30px 16px; }
.announce-modal { width: 100%; max-width: 380px; max-height: 80vh; background: var(--card); border-radius: 14px; box-shadow: 0 8px 32px rgba(0,0,0,.2); display: flex; flex-direction: column; overflow: hidden; }
.announce-modal-header { display: flex; justify-content: space-between; align-items: center; padding: 14px 16px; font-size: 15px; font-weight: 600; border-bottom: 1px solid var(--border); }
.announce-list, .connlog-list { overflow-y: auto; padding: 10px 14px; flex: 1; }
.announce-item { padding: 12px; background: var(--bg); border-radius: 10px; margin-bottom: 8px; }
.announce-item:last-child { margin-bottom: 0; }
.al-urgent { border-left: 3px solid #ef4444; }
.al-warning { border-left: 3px solid #f59e0b; }
.al-info { border-left: 3px solid #3b82f6; }
.announce-title { font-size: 13px; font-weight: 600; display: flex; align-items: center; gap: 6px; margin-bottom: 6px; }
.announce-content { font-size: 12px; color: var(--text-secondary); line-height: 1.5; white-space: pre-wrap; }
.announce-time { font-size: 11px; color: var(--text-secondary); margin-top: 6px; opacity: .7; }

/* Connection log */
.connlog-item { display: flex; justify-content: space-between; align-items: flex-start; padding: 10px 12px; background: var(--bg); border-radius: 8px; margin-bottom: 6px; font-size: 12px; }
.connlog-left { display: flex; align-items: center; gap: 8px; }
.connlog-action { padding: 2px 8px; border-radius: 6px; font-weight: 600; font-size: 11px; }
.cla-on { background: #dcfce7; color: #16a34a; }
.cla-off { background: #f3f4f6; color: #6b7280; }
.cla-warn { background: #fee2e2; color: #dc2626; }
.app.dark .cla-on { background: #1a3a1a; color: #4ade80; }
.app.dark .cla-off { background: #2d2d4a; color: #8b92a8; }
.app.dark .cla-warn { background: #3b1c1c; color: #f87171; }
.connlog-node { font-weight: 500; }
.connlog-right { text-align: right; }
.connlog-detail { display: block; color: var(--text-secondary); font-size: 11px; }
.connlog-time { font-size: 11px; color: var(--text-secondary); opacity: .7; }

/* Kill Switch toggle */
.switch { position: relative; display: inline-block; width: 38px; height: 20px; flex-shrink: 0; }
.switch input { opacity: 0; width: 0; height: 0; }
.slider { position: absolute; cursor: pointer; top: 0; left: 0; right: 0; bottom: 0; background: var(--border); border-radius: 20px; transition: .3s; }
.slider:before { content: ""; position: absolute; height: 16px; width: 16px; left: 2px; bottom: 2px; background: #fff; border-radius: 50%; transition: .3s; }
.switch input:checked + .slider { background: var(--accent); }
.switch input:checked + .slider:before { transform: translateX(18px); }
</style>
