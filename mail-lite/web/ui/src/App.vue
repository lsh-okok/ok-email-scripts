<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { NConfigProvider, NMessageProvider, NDialogProvider, NButton, NInput, NSelect, NSpin, NAlert, zhCN, dateZhCN, darkTheme } from 'naive-ui'
import { Mail, Link, Network, LogOut, ArrowRight, SunMoon, Home, KeyRound, ShieldCheck, Settings } from 'lucide-vue-next'
import { api, loadSession, session, errorText } from './api'
import { navigationError, navigationRetryPath } from './navigation'
import SidebarGroups from './components/SidebarGroups.vue'
import BackgroundTasks from './components/BackgroundTasks.vue'
import './list-layout.css'
const route = useRoute()
const boundedPage = computed(() => route.path === '/links')
const theme = ref(localStorage.getItem('mail-lite-theme') || 'system')
const systemDark = ref(matchMedia('(prefers-color-scheme: dark)').matches)
const dark = computed(() => theme.value === 'dark' || theme.value === 'system' && systemDark.value)
const media = matchMedia('(prefers-color-scheme: dark)')
const onSystem = () => { systemDark.value = media.matches; applyTheme() }
function applyTheme() { document.documentElement.dataset.theme = dark.value ? 'dark' : 'light'; localStorage.setItem('mail-lite-theme', theme.value) }
function selectTheme(v: string) { theme.value = v; applyTheme() }
const password = ref(''), loading = ref(false), error = ref('')
const nav = [{ to: '/home', label: '首页', icon: Home }, { to: '/accounts', label: '邮箱管理', icon: Mail }, { to: '/links', label: '链接管理', icon: Link }, { to: '/cards', label: '卡密管理', icon: KeyRound }, { to: '/verification', label: '邮箱验证', icon: ShieldCheck }, { to: '/nodes', label: '节点管理', icon: Network }, { to: '/settings', label: '系统设置', icon: Settings }]
const mobileNav = [{ to: '/home', label: '首页', icon: Home }, { to: '/accounts', label: '邮箱', icon: Mail }, { to: '/links', label: '链接', icon: Link }, { to: '/cards', label: '卡密', icon: KeyRound }, { to: '/verification', label: '验证', icon: ShieldCheck }, { to: '/settings', label: '设置', icon: Settings }]
const overrides = { common: { primaryColor: '#3478f6', primaryColorHover: '#548ff9', primaryColorPressed: '#2160d7', borderRadius: '10px', fontFamily: 'Inter, "PingFang SC", "Microsoft YaHei", sans-serif', heightMedium: '44px', heightSmall: '44px', fontSize: '14px' }, Button: { fontWeight: '500' }, Card: { borderRadius: '14px' } }
async function login() { if (!password.value) return; loading.value = true; error.value = ''; try { const s = await api<{ csrf: string; authenticated: boolean }>('login', 'POST', { password: password.value }); Object.assign(session, s); password.value = '' } catch (e) { error.value = errorText(e) } finally { loading.value = false } }
async function logout() { try { await api('logout', 'POST'); session.authenticated = false } catch(e) { error.value = errorText(e) } }
onMounted(async () => { applyTheme(); media.addEventListener('change', onSystem); try { await loadSession() } catch(e) { error.value = errorText(e); session.loaded = true } })
onUnmounted(() => media.removeEventListener('change', onSystem))
</script>
<template>
 <NConfigProvider :theme="dark ? darkTheme : null" :theme-overrides="overrides" :locale="zhCN" :date-locale="dateZhCN">
  <NMessageProvider placement="top"><NDialogProvider>
   <div v-if="!session.loaded" class="loading-screen"><NSpin size="large" /></div>
   <div v-else-if="!session.authenticated" class="login-screen">
    <div class="login-brand"><span class="brand-icon"><Mail :size="23" /></span><strong>Mail Lite</strong></div>
    <form class="login-card surface" @submit.prevent="login"><span class="eyebrow">MAIL, SIMPLIFIED</span><h1>欢迎回来</h1><p class="muted">登录管理邮箱、访问链接和节点。</p><label for="admin-password">管理员密码</label><NInput :input-props="{id:'admin-password',autocomplete:'current-password'}" v-model:value="password" type="password" show-password-on="click" placeholder="输入管理员密码" autocomplete="current-password" :disabled="loading" /><NAlert v-if="error" type="error" :show-icon="false">{{ error }}</NAlert><NButton type="primary" block attr-type="submit" :loading="loading">登录管理台 <ArrowRight :size="16" class="ml" /></NButton><small class="muted">轻量运行 · 安全访问</small></form>
    <NSelect class="login-theme" aria-label="外观主题" :value="theme" :options="[{label:'跟随系统',value:'system'},{label:'浅色',value:'light'},{label:'深色',value:'dark'}]" @update:value="selectTheme" />
   </div>
   <div v-else class="shell">
    <BackgroundTasks />
    <aside class="sidebar"><RouterLink to="/home" class="brand"><span class="brand-icon"><Mail :size="23" /></span><span>Mail Lite<small>邮箱工作台</small></span></RouterLink><span class="nav-label">工作空间</span><nav><template v-for="item in nav" :key="item.to"><SidebarGroups v-if="item.to === '/accounts'" /><RouterLink v-else :to="item.to"><component :is="item.icon" :size="19" />{{ item.label }}</RouterLink></template></nav></aside>
    <div class="workspace" :class="{'inbox-workspace':route.path==='/accounts','bounded-workspace':boundedPage}"><header class="topbar"><span class="breadcrumb">工作空间 <span>/</span> <strong>{{ route.meta.title }}</strong></span><div class="top-actions"><SunMoon :size="18" class="theme-icon" /><NSelect aria-label="外观主题" class="theme-select" :value="theme" :options="[{label:'跟随系统',value:'system'},{label:'浅色',value:'light'},{label:'深色',value:'dark'}]" @update:value="selectTheme" /><NButton quaternary aria-label="退出登录" @click="logout"><LogOut :size="18" /></NButton></div></header>
     <NAlert v-if="navigationError" type="error" role="alert" style="margin:16px 24px 0">{{ navigationError }} <NButton tag="a" :href="navigationRetryPath" size="small" secondary type="error">重新加载页面</NButton></NAlert>
     <main :class="{'inbox-main':route.path==='/accounts','bounded-main':boundedPage}"><RouterView v-slot="{ Component }"><KeepAlive include="Accounts"><component :is="Component" /></KeepAlive></RouterView></main>
    </div><nav class="bottom-nav" :style="{ gridTemplateColumns: `repeat(${mobileNav.length},1fr)` }"><RouterLink v-for="item in mobileNav" :key="item.to" :to="item.to"><component :is="item.icon" :size="21" /><span>{{ item.label }}</span></RouterLink></nav>
   </div>
  </NDialogProvider></NMessageProvider>
 </NConfigProvider>
</template>
