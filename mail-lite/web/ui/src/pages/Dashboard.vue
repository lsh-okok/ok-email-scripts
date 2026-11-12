<script setup lang="ts">
import { h, onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NDataTable, NInput, NPagination, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { Activity, ChevronDown, ChevronUp, CircleCheck, FolderTree, Link, Mail, RefreshCw, Search } from 'lucide-vue-next'
import { api, copyText, date, errorText, mailboxJSONURL, query, type RequestStat } from '../api'

type DashboardStats = { accounts:number; links:number; groups:number; outlook_accounts:number; icloud_accounts:number; mx_accounts?:number; active_accounts:number; disabled_accounts:number }
const message = useMessage()
const stats = ref<DashboardStats>({accounts:0,links:0,groups:0,outlook_accounts:0,icloud_accounts:0,active_accounts:0,disabled_accounts:0})
const requests = ref<RequestStat[]>([])
const requestTotal = ref(0)
const itemCount = ref(0), page = ref(1), search = ref('')
const loading = ref(false)
const error = ref('')
const collapsed = ref(false), searchVisible = ref(true)
let timer: ReturnType<typeof setTimeout>, seq = 0
const columns: DataTableColumns<RequestStat> = [
 { title:'邮箱归属 / 具体取信地址', key:'url', width:520, render:r=>h('div',{class:'request-link'},[r.account_email?h('span',{class:'link-parent',title:r.account_email},'主邮箱：'+r.account_email):null,h('strong',{title:r.recipient},(r.account_email?'↳ 子邮箱：':'')+r.recipient),r.account_code?h('code',{class:'table-cell-ellipsis',title:r.account_code},'主邮箱标识码：'+r.account_code):null,h('a',{href:r.url,target:'_blank',rel:'noreferrer',title:r.url},r.url)]) },
 { title:'页面访问', key:'page_count', width:100, render:r=>r.page_count.toLocaleString() },
 { title:'JSON 调用', key:'json_count', width:100, render:r=>r.json_count.toLocaleString() },
 { title:'合计', key:'count', width:90, render:r=>h('strong',{class:'request-count'},r.count.toLocaleString()) },
 { title:'最近访问', key:'last_access_at', width:180, render:r=>h('span',{class:'muted'},date(r.last_access_at)) },
 { title:'JSON 接口', key:'json_url', width:120, render:r=>h(NButton,{quaternary:true,type:'primary',disabled:!mailboxJSONURL(r),'aria-label':`复制 ${r.recipient} 的 JSON 接口`,onClick:()=>copyJSON(r)},()=>'复制 JSON') }
]
async function copyJSON(stat:RequestStat){const url=mailboxJSONURL(stat);if(!url)return;try{await copyText(url);message.success('JSON 接口已复制')}catch(e){message.error(errorText(e))}}
function refreshDashboard(){ load() }
async function load(){
 const serial=++seq
 loading.value=true;error.value=''
 try{
  const [s,r]=await Promise.all([api<DashboardStats>('admin/stats'),api<{items:RequestStat[];total:number;item_count:number}>('admin/request-stats?'+query({q:search.value,page:page.value,page_size:20}))])
  if(serial!==seq)return
  stats.value=s;requests.value=r.items;requestTotal.value=r.total;itemCount.value=r.item_count
 }catch(e){if(serial===seq){error.value=errorText(e);message.error(errorText(e))}}finally{if(serial===seq)loading.value=false}
}
watch(search,()=>{clearTimeout(timer);timer=setTimeout(()=>{if(page.value===1)load();else page.value=1},300)})
watch(page,load)
onMounted(()=>{load();window.addEventListener('data-changed',load)})
onUnmounted(()=>{clearTimeout(timer);seq++;window.removeEventListener('data-changed',load)})
</script>
<template>
 <div class="dashboard-page bounded-page">
  <div class="page-title"><div><div class="page-kicker">MAIL LITE OVERVIEW</div><h1>首页</h1><p>集中查看邮箱和取信访问情况。</p></div><NButton quaternary :loading="loading" @click="refreshDashboard"><RefreshCw :size="17" class="icon-before" />刷新数据</NButton></div>
  <NAlert v-if="error" type="error" :show-icon="false" style="margin-bottom:18px">{{ error }}</NAlert>
  <div class="stats dashboard-stats">
   <RouterLink class="stat surface dashboard-stat-link" to="/accounts"><div><div class="stat-label">主邮箱总数</div><div class="stat-number">{{ stats.accounts.toLocaleString() }}</div><div class="stat-detail">Outlook {{ stats.outlook_accounts }} <span>·</span> iCloud {{ stats.icloud_accounts }} <span>·</span> MX {{ stats.mx_accounts || 0 }}</div></div><div class="stat-icon"><Mail :size="21" /></div></RouterLink>
   <RouterLink class="stat surface dashboard-stat-link" to="/accounts"><div><div class="stat-label">已启用邮箱</div><div class="stat-number">{{ stats.active_accounts.toLocaleString() }}</div><div class="stat-detail">已停用 {{ stats.disabled_accounts }} 个邮箱</div></div><div class="stat-icon success"><CircleCheck :size="21" /></div></RouterLink>
   <RouterLink class="stat surface dashboard-stat-link" to="/links"><div><div class="stat-label">访问链接</div><div class="stat-number">{{ stats.links.toLocaleString() }}</div><span class="stat-link">查看链接管理 →</span></div><div class="stat-icon"><Link :size="21" /></div></RouterLink>
   <RouterLink class="stat surface dashboard-stat-link" to="/groups"><div><div class="stat-label">邮箱分组</div><div class="stat-number">{{ stats.groups.toLocaleString() }}</div><span class="stat-link">分组与代理 →</span></div><div class="stat-icon neutral"><FolderTree :size="21" /></div></RouterLink>
   <div class="stat surface dashboard-stat-link"><div><div class="stat-label">取信访问总次数</div><div class="stat-number">{{ requestTotal.toLocaleString() }}</div><div class="stat-detail">各链接页面与 JSON 请求合计</div></div><div class="stat-icon activity"><Activity :size="21" /></div></div>
  </div>
  <section class="surface request-panel bounded-panel" :class="{'is-collapsed':collapsed}">
   <div class="section-title request-panel-title"><div class="request-panel-copy"><h2>每条取信链接的访问统计</h2><p class="muted">按具体链接分别计数，页面与 JSON 调用单独记录。当前主节点累计，重启后保留。</p></div><div class="request-panel-controls"><NTag size="small" :bordered="false">{{ itemCount.toLocaleString() }} 条链接</NTag><NTag v-if="search" size="small" :bordered="false" type="info">已筛选</NTag><NButton v-if="search" text type="primary" aria-label="清除统计筛选" @click="search=''">清除筛选</NButton><NButton quaternary :aria-expanded="searchVisible&&!collapsed" aria-controls="request-search-toolbar" :aria-label="searchVisible&&!collapsed?'隐藏统计搜索':'显示统计搜索'" @click="searchVisible=collapsed?true:!searchVisible;collapsed=false"><Search :size="16" class="icon-before" />{{ searchVisible&&!collapsed?'隐藏搜索':'显示搜索' }}</NButton><NButton quaternary :aria-expanded="!collapsed" aria-controls="request-panel-content" @click="collapsed=!collapsed"><component :is="collapsed?ChevronDown:ChevronUp" :size="16" class="icon-before" />{{ collapsed?'展开统计':'收起统计' }}</NButton></div></div>
   <div id="request-panel-content" v-show="!collapsed" class="request-panel-body">
    <div id="request-search-toolbar" v-show="searchVisible" class="toolbar"><NInput v-model:value="search" clearable class="request-search" aria-label="搜索取信链接统计" placeholder="搜索子邮箱或粘贴完整链接" /></div>
    <div class="bounded-table-host"><NDataTable flex-height table-layout="fixed" :columns="columns" :data="requests" :loading="loading" :bordered="false" :scroll-x="1110" :row-key="(r:RequestStat)=>r.link_id" /></div>
    <div class="pagination"><span class="muted">按访问合计从高到低排列 · {{ itemCount.toLocaleString() }} 条链接</span><NPagination v-model:page="page" :item-count="itemCount" :page-size="20" :page-slot="5" /></div>
   </div>
  </section>
 </div>
</template>
<style>
.dashboard-page .request-panel{flex:none;height:620px}.dashboard-page .request-panel.is-collapsed{height:auto;min-height:0}
.request-link{display:grid;gap:7px;min-width:0;padding:4px 0}
.request-link strong{font-size:13px}
.request-link a{font-size:11px;line-height:1.7}
.request-link strong,.request-link a{display:block;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.request-panel-copy{min-width:0;flex:1}
.request-panel-controls{display:flex;align-items:center;justify-content:flex-end;gap:6px;flex-wrap:wrap}
.request-panel-controls>.n-button{font-size:12px;min-height:44px}
.request-panel-body{display:flex;flex-direction:column;flex:1;min-height:0}
.request-panel-body>.toolbar,.request-panel-body>.pagination{flex-shrink:0}
.request-panel .request-search{width:440px;max-width:100%;flex:none}
.request-panel.is-collapsed{flex:none;height:auto;min-height:0}
@media(max-width:1100px){.request-panel-title{flex-wrap:wrap}.request-panel-copy{flex-basis:100%}.request-panel-controls{width:100%;justify-content:flex-start}}
</style>
