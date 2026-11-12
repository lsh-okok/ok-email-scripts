import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import { navigationError, navigationRetryPath } from './navigation'
const Accounts = () => import('./pages/Accounts.vue')
const Dashboard = () => import('./pages/Dashboard.vue')
const GroupsPage = () => import('./pages/GroupsPage.vue')
const Links = () => import('./pages/Links.vue')
const Nodes = () => import('./pages/Nodes.vue')
const Cards = () => import('./pages/Cards.vue')
const Verification = () => import('./pages/Verification.vue')
const SettingsPage = () => import('./pages/Settings.vue')
import './style.css'

const router = createRouter({ history: createWebHistory('/admin/'), routes: [{ path: '/', redirect: '/home' }, { path: '/home', component: Dashboard, meta: { title: '首页' } }, { path: '/accounts', component: Accounts, meta: { title: '邮箱管理' } }, { path: '/groups', component: GroupsPage, meta: { title: '分组管理' } }, { path: '/links', component: Links, meta: { title: '链接管理' } }, { path: '/cards', component: Cards, meta: { title: '卡密管理' } }, { path: '/verification', component: Verification, meta: { title: '邮箱验证' } }, { path: '/sales', redirect: '/cards' }, { path: '/nodes', component: Nodes, meta: { title: '节点管理' } }, { path: '/settings', component: SettingsPage, meta: { title: '系统设置' } }, { path: '/:pathMatch(.*)*', redirect: '/home' }] })
router.onError((_error, to) => {
 navigationRetryPath.value = '/admin' + to.fullPath
 navigationError.value = '页面资源加载失败，请重新加载页面后重试。'
})
router.afterEach((_to, _from, failure) => { if (!failure) navigationError.value = '' })
createApp(App).use(router).mount('#app')
