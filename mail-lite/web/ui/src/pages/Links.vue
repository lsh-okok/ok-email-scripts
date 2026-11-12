<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NDatePicker, NEmpty, NForm, NFormItem, NInput, NModal, NPagination, NProgress, NRadioButton, NRadioGroup, NSelect, NTag, useDialog, useMessage, type DataTableColumns, type DataTableRowKey } from 'naive-ui'
import { Copy, Download, ExternalLink, Plus, RefreshCw, Search, SlidersHorizontal } from 'lucide-vue-next'
import { api, copyText, date, errorText, mailboxJSONURL, query, session, type Account, type Link, type Page, type Platform } from '../api'
import ImportForm from '../components/ImportForm.vue'
import Platforms from '../components/Platforms.vue'

const msg = useMessage(), dialog = useDialog()
const mobileViewport = matchMedia('(max-width: 767px)'), mobile = ref(mobileViewport.matches)
function updateViewport() { mobile.value = mobileViewport.matches }
const items = ref<Link[]>([]), platforms = ref<Platform[]>([]), accounts = ref<Account[]>([])
const total = ref(0), page = ref(1), size = ref(20), search = ref(''), account = ref(''), platform = ref('')
const loading = ref(false), error = ref(''), selected = ref<DataTableRowKey[]>([]), filters = ref(false), importing = ref(false), rules = ref(false)
const exporting = ref(false), downloadBytes = ref(0), saving = ref(false), deleting = ref(false), selecting = ref(false), selectedAll = ref(false)
const editIds = ref<string[]>([]), editLink = ref<Link|null>(null), editAction = ref<'platform'|'expiry'>('expiry'), editScope = ref('')
const editPlatform = ref<string|null>(''), expiryMode = ref<1|3|7|15|30|'permanent'|'date'>('permanent'), editExpires = ref<number|null>(null), editError = ref('')
const deleteIds = ref<string[]>([]), deleteError = ref(''), deleteScope = ref(''), loadedFilter = ref('')
const busy = computed(() => saving.value || deleting.value || selecting.value)
const accountOpts = computed(() => accounts.value.map(a => ({ label:a.email, value:a.id })))
const platformOpts = computed(() => platforms.value.map(p => ({ label:p.name, value:p.id })))
const editTitle = computed(() => `${editIds.value.length > 1 ? '批量' : ''}${editAction.value === 'expiry' ? '修改有效期' : '设置平台规则'}`)
let timer:ReturnType<typeof setTimeout>, accountTimer:ReturnType<typeof setTimeout>, seq = 0, accountSeq = 0, selectionSeq = 0
const params = () => query({ q:search.value, account_id:account.value, platform_id:platform.value })
const accountEmail = (link:Link) => link.account_email || accounts.value.find(a => a.id === link.account_id)?.email || '—'
const platformName = (link:Link) => link.platform_name || platforms.value.find(p => p.id === link.platform_id)?.name || '全部平台'
const listReady = computed(() => !loading.value && !error.value && loadedFilter.value === params())
const pageChecked = computed(() => items.value.filter(l => selected.value.includes(l.id)).length)
const selectedAccount = computed(() => accounts.value.find(a => a.id === account.value)?.email || items.value.find(l => l.account_id === account.value)?.account_email || '所选主邮箱')
const selectionScope = computed(() => account.value ? `${selectedAccount.value} 的${search.value.trim() || platform.value ? '筛选结果' : '子邮箱链接'}` : '所选链接')
const selectAllLabel = computed(() => `全选该主邮箱${search.value.trim() || platform.value ? '筛选结果' : '全部链接'}（${total.value.toLocaleString()} 条）`)

async function load() {
 const serial = ++seq, filter = params(), requestedPage = page.value, requestedSize = size.value
 loading.value = true; error.value = ''
 try {
  const data = await api<Page<Link>>(`admin/links?${filter}&page=${requestedPage}&page_size=${requestedSize}`)
  if (serial !== seq || filter !== params() || requestedPage !== page.value || requestedSize !== size.value) return
  const lastPage = Math.max(1, Math.ceil(data.total / size.value))
  if (page.value > lastPage) { page.value = lastPage; return }
  items.value = data.items; total.value = data.total; loadedFilter.value = filter
 } catch(e) { if(serial === seq) error.value = errorText(e) }
 finally { if(serial === seq) loading.value = false }
}
async function loadAccounts(q = '') {
 const serial = ++accountSeq
 try {
  const data = await api<Page<Account>>('admin/accounts?' + query({q, page_size:100}))
  if(serial !== accountSeq) return
  const active = accounts.value.find(a => a.id === account.value)
  accounts.value = active && !data.items.some(a => a.id === active.id) ? [active,...data.items] : data.items
 }
 catch(e) { msg.error(errorText(e)) }
}
function searchAccounts(q:string) { clearTimeout(accountTimer); accountTimer = setTimeout(() => loadAccounts(q), 250) }
async function loadRules() { try { platforms.value = (await api<{items:Platform[]}>('admin/platforms')).items } catch(e) { msg.error(errorText(e)) } }
function resetPageAndLoad() { if(page.value === 1) load(); else page.value = 1 }
function clearSelection() { selectionSeq++; selected.value = []; selectedAll.value = false; selecting.value = false }
watch(search, () => { clearSelection(); clearTimeout(timer); timer = setTimeout(resetPageAndLoad, 300) }, {flush:'sync'})
watch([account, platform], () => { clearSelection(); resetPageAndLoad() }, {flush:'sync'})
watch(size, resetPageAndLoad)
watch(page, load)
watch(editAction, () => { editError.value = '' })
function setSelected(id:string, value:boolean) { if(!busy.value && listReady.value) { selectedAll.value = false; selected.value = value ? [...new Set([...selected.value,id])] : selected.value.filter(k => k !== id) } }
function selectPage(keys:DataTableRowKey[]) {
 if(busy.value || !listReady.value) return
 const pageIds = new Set(items.value.map(l => l.id))
 selected.value = [...new Set([...selected.value.filter(id => !pageIds.has(String(id))), ...keys.filter(id => pageIds.has(String(id)))])]
 selectedAll.value = false
}
async function selectAccountLinks() {
 if(busy.value || !listReady.value || !account.value || !total.value) return
 const serial = ++selectionSeq, filter = params()
 selecting.value = true
 try {
  const data = await api<{ids:string[];total:number}>('admin/links/selection?' + filter)
  if(serial !== selectionSeq || filter !== params()) return
  selected.value = data.ids; selectedAll.value = !!data.ids.length
 } catch(e) { if(serial === selectionSeq) msg.error(errorText(e)) }
 finally { if(serial === selectionSeq) selecting.value = false }
}
async function copy(url:string) {
 if(!url) { msg.warning('接口尚未返回 JSON 地址，请刷新后重试'); return }
 try { await copyText(url); msg.success('链接已复制') }
 catch { dialog.info({ title:'复制访问链接', content:url, positiveText:'关闭' }) }
}
function openEditor(action:'platform'|'expiry', ids:string[], link:Link|null = null) {
 if(busy.value || !listReady.value || !ids.length) return
 editIds.value = ids.slice(); editLink.value = link; editAction.value = action
 editScope.value = link ? '仅修改此链接：' + link.recipient : `${selectionScope.value}，本次修改 ${ids.length} 条。`
 editPlatform.value = link?.platform_id || ''; editExpires.value = link?.expires_at ? Date.parse(link.expires_at) : null
 expiryMode.value = link?.expires_at ? 'date' : 'permanent'; editError.value = ''
}
function closeEditor() { if(!saving.value) { editIds.value = []; editLink.value = null; editError.value = '' } }
async function saveEdit() {
 if(busy.value || !editIds.value.length) return
 let value = editPlatform.value || ''
 if(editAction.value === 'expiry') {
  if(expiryMode.value === 'date' && (editExpires.value === null || !Number.isFinite(editExpires.value))) { editError.value = '请选择有效的到期日期和时间'; return }
  value = expiryMode.value === 'permanent' ? '' : new Date(expiryMode.value === 'date' ? editExpires.value! : Date.now() + expiryMode.value * 24 * 60 * 60 * 1000).toISOString()
 }
 saving.value = true; editError.value = ''
 try {
  await api('admin/links/batch', 'POST', { ids:editIds.value.slice(), action:editAction.value, value })
  msg.success(`已更新 ${editIds.value.length} 条链接的${editAction.value === 'expiry' ? '有效期' : '平台规则'}`)
  editIds.value = []; editLink.value = null
  window.dispatchEvent(new Event('data-changed'))
 } catch(e) { editError.value = errorText(e) }
 finally { saving.value = false }
}
function remove(ids:string[]) { if(!busy.value && listReady.value && ids.length) { deleteIds.value = ids.slice(); deleteScope.value = `${selectionScope.value}，本次删除 ${ids.length} 条。`; deleteError.value = '' } }
function closeDelete() { if(!deleting.value) { deleteIds.value = []; deleteError.value = '' } }
async function confirmRemove() {
 if(busy.value || !deleteIds.value.length) return
 deleting.value = true; deleteError.value = ''
 try {
  await api('admin/links/batch', 'POST', { ids:deleteIds.value.slice(), action:'delete', value:'' })
  msg.success(`已删除 ${deleteIds.value.length} 条链接`); deleteIds.value = []
  window.dispatchEvent(new Event('data-changed'))
 } catch(e) { deleteError.value = errorText(e) }
 finally { deleting.value = false }
}
async function exportLinks() {
 if(exporting.value) return
 const exportTotal = total.value
 exporting.value = true; downloadBytes.value = 0
 try {
  const response = await fetch('/api/admin/links/export?' + params(), { headers:{'X-CSRF-Token':session.csrf} })
  if(!response.ok) throw new Error((await response.json()).error || '导出失败')
  const reader = response.body!.getReader(), chunks:Uint8Array<ArrayBuffer>[] = []
  while(true) { const {done,value} = await reader.read(); if(done) break; chunks.push(new Uint8Array(value)); downloadBytes.value += value.length }
  const url = URL.createObjectURL(new Blob(chunks,{type:'text/plain;charset=utf-8'})), a = document.createElement('a')
  a.href = url; a.download = 'mail-lite-links.txt'; a.click(); setTimeout(() => URL.revokeObjectURL(url),1000)
  msg.success(`已导出当前筛选的 ${exportTotal.toLocaleString()} 条链接`)
 } catch(e) { msg.error(errorText(e)) }
 finally { exporting.value = false }
}
const columns:DataTableColumns<Link> = [
 { type:'selection', width:44, disabled:() => busy.value || !listReady.value },
 { title:'子邮箱 / 访问地址', key:'recipient', width:310, render:l => h('div',{class:'link-lineage'},[
  h('div',{class:'email-cell',title:l.recipient},l.recipient),
  h('a',{href:l.url,target:'_blank',rel:'noreferrer',class:'link-url',title:l.url},l.url)
 ]) },
 { title:'主邮箱', key:'account_email', width:280, render:l => h('span',{class:'table-cell-ellipsis',title:accountEmail(l)},accountEmail(l)) },
 { title:'平台', key:'platform_id', width:125, render:l => h(NTag,{bordered:false,size:'small',type:l.platform_id?'info':'default'},() => platformName(l)) },
 { title:'访问次数', key:'count', width:145, render:l => h('div',[h('strong',{class:'request-count'},l.count.toLocaleString()),h('div',{class:'subline'},'页面 ' + l.page_count + ' · JSON ' + l.json_count)]) },
 { title:'有效期', key:'expires_at', width:170, render:l => h('span',{class:'muted',style:'font-size:12px'},l.expires_at ? date(l.expires_at) : '长期有效') },
 { title:'操作', key:'actions', width:240, render:l => h('div',{class:'link-row-actions'},[
  h(NButton,{quaternary:true,type:'primary','aria-label':`复制 ${l.recipient} 的访问链接`,onClick:() => copy(l.url)},() => '复制'),
  h(NButton,{quaternary:true,disabled:!mailboxJSONURL(l),'aria-label':`复制 ${l.recipient} 的 JSON 接口`,onClick:() => copy(mailboxJSONURL(l))},() => 'JSON'),
  h(NButton,{quaternary:true,disabled:busy.value||!listReady.value,'aria-label':`编辑 ${l.recipient} 的链接`,onClick:() => openEditor('expiry',[l.id],l)},() => '编辑'),
  h(NButton,{quaternary:true,type:'error',disabled:busy.value||!listReady.value,'aria-label':`删除 ${l.recipient} 的链接`,onClick:() => remove([l.id])},() => '删除')
 ]) }
]
function reload() { clearSelection(); load(); loadAccounts(); loadRules() }
onMounted(() => { reload(); window.addEventListener('data-changed',reload); mobileViewport.addEventListener('change',updateViewport) })
onUnmounted(() => { clearTimeout(timer); clearTimeout(accountTimer); seq++; accountSeq++; selectionSeq++; window.removeEventListener('data-changed',reload); mobileViewport.removeEventListener('change',updateViewport) })
</script>
<template>
 <div class="links-page bounded-page">
  <div class="page-title"><div><h1>链接管理</h1><p>查看主邮箱与子邮箱归属，管理访问地址、平台规则和有效期。</p></div><div class="actions"><NButton :loading="exporting" :disabled="exporting" @click="exportLinks"><Download :size="16" class="icon-before" />导出筛选结果</NButton><NButton type="primary" @click="importing=true"><Plus :size="18" class="icon-before" />生成链接</NButton></div></div>
  <NAlert type="info" :show-icon="false" style="margin-bottom:24px"><div>{{ account ? '将为下方选中的主邮箱生成链接。' : '先选择主邮箱再导入子邮箱，或用 TXT 文件首行指定主邮箱。' }}</div><div class="muted" style="margin-top:6px;font-size:12px">链接首段 p0 表示不限制平台；替换为平台段可切换筛选，访问次数仍归到同一条链接。</div><NButton text type="primary" style="margin-left:0;margin-top:6px" @click="rules=true">管理平台规则</NButton></NAlert>
  <section class="surface bounded-panel links-panel" aria-label="访问链接列表">
   <div class="toolbar" :class="{'filters-open':filters}">
    <NInput v-model:value="search" :disabled="busy" clearable class="search" aria-label="搜索子邮箱" placeholder="搜索子邮箱或完整链接"><template #prefix><Search :size="16" /></template></NInput>
    <NButton class="filter-trigger" :aria-label="filters?'收起筛选':'展开筛选'" :aria-expanded="filters" @click="filters=!filters"><SlidersHorizontal :size="18" /></NButton>
    <NSelect v-model:value="account" :disabled="busy" class="filter" filterable remote clearable :options="[{label:'全部主邮箱',value:''},...accountOpts]" placeholder="全部主邮箱（可搜索）" aria-label="主邮箱" @search="searchAccounts" />
    <NSelect v-model:value="platform" :disabled="busy" class="filter" clearable :options="[{label:'全部平台',value:''},...platformOpts]" placeholder="全部平台" aria-label="平台" />
    <span class="spacer" /><NButton quaternary aria-label="刷新链接列表" :loading="loading" :disabled="busy" @click="load"><RefreshCw :size="17" /></NButton>
   </div>
   <div v-if="account || selected.length" class="selection-bar"><strong v-if="selected.length">已选 {{ selected.length.toLocaleString() }} 条 · {{ selectedAll?'已跨页全选':'可跨页选择' }}</strong><NButton v-if="account&&!selectedAll" secondary type="primary" :disabled="busy||!listReady||!total" :loading="selecting" @click="selectAccountLinks">{{ selectAllLabel }}</NButton><template v-if="selected.length"><NButton secondary type="primary" :disabled="busy||!listReady" @click="openEditor('platform',selected.map(String))">设置平台</NButton><NButton secondary type="primary" :disabled="busy||!listReady" @click="openEditor('expiry',selected.map(String))">修改有效期</NButton><NButton type="error" secondary :disabled="busy||!listReady" @click="remove(selected.map(String))">删除所选</NButton><NButton quaternary :disabled="busy" @click="clearSelection">取消</NButton></template></div>
   <div v-if="exporting" class="download-status"><NProgress type="line" :percentage="100" processing :show-indicator="false" /><small class="muted">正在导出筛选范围，已接收 {{ (downloadBytes/1024/1024).toFixed(2) }} MiB</small></div>
   <NAlert v-if="error" type="error">{{ error }}</NAlert>
   <div v-if="!mobile" class="content-table bounded-table-host"><NDataTable flex-height virtual-scroll :min-row-height="82" table-layout="fixed" :data="items" :columns="columns" :loading="loading" :row-key="(l:Link)=>l.id" :checked-row-keys="selected" :scroll-x="1314" :bordered="false" @update:checked-row-keys="selectPage" /></div>
   <div v-else class="mobile-cards">
    <NCheckbox style="margin-top:16px;min-height:44px" :disabled="busy||!listReady" :checked="!!items.length&&pageChecked===items.length" :indeterminate="!!pageChecked&&pageChecked!==items.length" @update:checked="v=>selectPage(v?items.map(l=>l.id):[])">选择当前页</NCheckbox>
    <article v-for="l in items" :key="l.id" class="mobile-card">
     <div class="mobile-card-head"><NCheckbox :disabled="busy||!listReady" :checked="selected.includes(l.id)" :aria-label="`选择 ${l.recipient}`" @update:checked="v=>setSelected(l.id,v)" /><div class="link-lineage"><div class="email-cell">{{ l.recipient }}</div></div></div>
     <div class="card-meta">主邮箱：{{ accountEmail(l) }}</div>
     <a class="link-url mobile-link-url" :href="l.url" :title="l.url" target="_blank" rel="noreferrer">{{ l.url }}</a>
     <div class="card-meta"><NTag size="small" :bordered="false">{{ platformName(l) }}</NTag><span>{{ l.expires_at?date(l.expires_at):'长期有效' }}</span></div>
     <div class="card-meta"><strong>访问 {{ l.count.toLocaleString() }} 次</strong><span>页面 {{ l.page_count }} · JSON {{ l.json_count }}</span></div>
     <div class="card-actions"><NButton secondary type="primary" @click="copy(l.url)"><Copy :size="15" class="icon-before" />复制</NButton><NButton tag="a" :href="l.url" target="_blank" rel="noreferrer" :aria-label="`打开 ${l.recipient} 的取信链接`"><ExternalLink :size="16" /></NButton><NButton quaternary :disabled="!mailboxJSONURL(l)" @click="copy(mailboxJSONURL(l))">JSON</NButton><NButton quaternary :disabled="busy||!listReady" @click="openEditor('expiry',[l.id],l)">编辑</NButton><NButton quaternary type="error" :disabled="busy||!listReady" @click="remove([l.id])">删除</NButton></div>
    </article>
    <NEmpty v-if="!items.length&&!loading" description="还没有匹配的链接" style="padding:40px 0" />
   </div>
   <div class="pagination"><span class="muted" style="font-size:12px">共 {{ total.toLocaleString() }} 条链接 · 导出覆盖当前全部筛选结果</span><NPagination v-model:page="page" v-model:page-size="size" :disabled="busy" :item-count="total" :page-slot="5" show-size-picker :page-sizes="[20,100,250,500,750]" /></div>
  </section>
 </div>
 <ImportForm v-model:show="importing" kind="links" :account-id="account||''" :account-label="accounts.find(a=>a.id===account)?.email" :platforms="platforms" @submitted="load" />
 <Platforms v-model:show="rules" :items="platforms" @changed="loadRules" />
 <NModal :show="!!editIds.length" :mask-closable="!saving" :close-on-esc="!saving" @update:show="v=>!v&&closeEditor()">
  <NCard class="form-modal link-edit-modal" :title="editTitle" :closable="!saving" @close="closeEditor">
   <NForm label-placement="top" @submit.prevent="saveEdit">
    <p class="muted">{{ editScope }}</p>
    <NRadioGroup v-if="editLink" v-model:value="editAction" :disabled="saving" aria-label="选择要编辑的链接设置"><NRadioButton value="expiry">有效期</NRadioButton><NRadioButton value="platform">平台规则</NRadioButton></NRadioGroup>
    <NAlert v-if="editError" type="error" :show-icon="false" style="margin-top:16px">{{ editError }}</NAlert>
    <NFormItem v-if="editAction==='platform'" label="平台规则"><NSelect v-model:value="editPlatform" :disabled="saving" :options="platformOpts" clearable placeholder="全部平台（不限制）" /></NFormItem>
    <template v-else>
     <NFormItem label="有效期类型"><NSelect v-model:value="expiryMode" to="body" :disabled="saving" :options="[{label:'1天',value:1},{label:'3天',value:3},{label:'7天',value:7},{label:'15天',value:15},{label:'30天',value:30},{label:'长期有效',value:'permanent'},{label:'指定到期时间',value:'date'}]" /></NFormItem>
     <NFormItem v-if="expiryMode==='date'" label="到期时间（本地时区）"><NDatePicker v-model:value="editExpires" :disabled="saving" type="datetime" format="yyyy-MM-dd HH:mm:ss" clearable placeholder="选择日期和时间" /></NFormItem>
     <p class="muted">按天设置从保存时起计算。到期后链接和 JSON 接口停止取信，可重新设置恢复。</p>
    </template>
    <div class="form-actions"><NButton :disabled="saving" @click="closeEditor">取消</NButton><NButton attr-type="submit" type="primary" :loading="saving" :disabled="saving">保存{{ editIds.length>1?'所选':'' }}</NButton></div>
   </NForm>
  </NCard>
 </NModal>
 <NModal :show="!!deleteIds.length" :mask-closable="!deleting" :close-on-esc="!deleting" @update:show="v=>!v&&closeDelete()"><NCard class="form-modal" :title="`删除 ${deleteIds.length} 条访问链接`" :closable="!deleting" @close="closeDelete"><p>{{ deleteScope }}</p><p>仅删除打开窗口时选中的链接，对应 JSON 接口同时失效，不删除邮箱中的邮件。</p><NAlert v-if="deleteError" type="error" :show-icon="false">{{ deleteError }}</NAlert><div class="form-actions"><NButton :disabled="deleting" @click="closeDelete">取消</NButton><NButton type="error" :loading="deleting" :disabled="deleting" @click="confirmRemove">删除链接</NButton></div></NCard></NModal>
</template>
