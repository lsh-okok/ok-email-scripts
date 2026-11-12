<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NEmpty, NInput, NModal, NPagination, NSelect, NSpin, NTag, useMessage, type DataTableColumns, type DataTableRowKey } from 'naive-ui'
import { Copy, Download, ExternalLink, KeyRound, Plus, RefreshCw, Search } from 'lucide-vue-next'
import { api, copyText, date, errorText, providerName, query, type Settings } from '../api'

type CardStatus = 'unissued' | 'unused' | 'redeemed' | 'disabled'
interface CardRow {
 account_id: string
 email: string
 provider: string
 account_status: string
 link_count: number
 code: string
 status: CardStatus
 created_at: string
 redeemed_at: string
}
interface CardPage {
 items: CardRow[]
 total: number
 page: number
 page_size: number
 summary: Record<CardStatus, number>
}

const msg = useMessage()
const items = ref<CardRow[]>([]), total = ref(0), page = ref(1), size = ref(50)
const search = ref(''), status = ref(''), selected = ref<string[]>([])
const summary = ref<Record<CardStatus, number>>({ unissued: 0, unused: 0, redeemed: 0, disabled: 0 })
const loading = ref(false), generating = ref(false), changing = ref(''), error = ref('')
const publicBase = ref(''), addressReady = ref(false), addressError = ref('')
const generated = ref<CardRow[]>([]), showGenerated = ref(false)
const viewport = matchMedia('(max-width: 767px)'), mobile = ref(viewport.matches)
let sequence = 0, debounce: ReturnType<typeof setTimeout> | undefined
const labels: Record<CardStatus, string> = { unissued: '未生成', unused: '未兑换', redeemed: '已兑换', disabled: '已停用' }
const tagTypes = { unissued: 'default', unused: 'info', redeemed: 'success', disabled: 'warning' } as const
const statusOptions = [{ label: '全部状态', value: '' }, ...Object.entries(labels).map(([value, label]) => ({ label, value }))]
const busy = computed(() => generating.value || !!changing.value)
const ready = computed(() => !loading.value && !error.value)
const selectedRows = computed(() => items.value.filter(row => selected.value.includes(row.account_id)))
const selectedCodes = computed(() => selectedRows.value.filter(row => row.code))
const generationRows = computed(() => selectedRows.value.filter(canGenerate))
const selectableRows = computed(() => items.value.filter(canSelect))
const pageChecked = computed(() => !!selectableRows.value.length && selectableRows.value.every(row => selected.value.includes(row.account_id)))
const pageIndeterminate = computed(() => !!selected.value.length && !pageChecked.value)
const redemptionURL = computed(() => (publicBase.value || location.origin).replace(/\/+$/, '') + '/redeem')
const generatedCodes = computed(() => generated.value.map(row => row.code).filter(Boolean).join('\n'))

function canGenerate(row: CardRow) { return !row.code && row.account_status === 'active' && row.provider !== 'mx' }
function canSelect(row: CardRow) { return !!row.code || canGenerate(row) }
function selectionChanged(keys: DataTableRowKey[]) { if (!busy.value && ready.value) selected.value = keys.map(String) }
function toggleRow(row: CardRow, checked: boolean) {
 if (busy.value || !ready.value || !canSelect(row)) return
 selected.value = checked ? [...new Set([...selected.value, row.account_id])] : selected.value.filter(id => id !== row.account_id)
}
function togglePage(checked: boolean) { if (!busy.value && ready.value) selected.value = checked ? selectableRows.value.map(row => row.account_id) : [] }
function updateViewport() { mobile.value = viewport.matches }

async function load() {
 const request = ++sequence
 loading.value = true; error.value = ''; selected.value = []
 try {
  const data = await api<CardPage>('admin/cards?' + query({ q: search.value.trim(), status: status.value, page: page.value, page_size: size.value }))
  if (request !== sequence) return
  const lastPage = Math.max(1, Math.ceil(data.total / size.value))
  if (page.value > lastPage) { page.value = lastPage; return }
  items.value = data.items; total.value = data.total; summary.value = data.summary
 } catch (e) { if (request === sequence) error.value = errorText(e) }
 finally { if (request === sequence) loading.value = false }
}
async function loadAddress() {
 addressError.value = ''
 try { publicBase.value = (await api<Settings>('admin/settings')).public_base_url.trim(); addressReady.value = true }
 catch (e) { addressReady.value = false; addressError.value = errorText(e) }
}
function reloadFirstPage() { if (page.value === 1) load(); else page.value = 1 }
function filterStatus(value: string) { if (!busy.value) status.value = status.value === value ? '' : value }
watch(search, () => {
 sequence++; selected.value = []; loading.value = true
 clearTimeout(debounce); debounce = setTimeout(reloadFirstPage, 300)
}, { flush: 'sync' })
watch(status, () => { clearTimeout(debounce); selected.value = []; reloadFirstPage() }, { flush: 'sync' })
watch(size, () => { selected.value = []; reloadFirstPage() }, { flush: 'sync' })
watch(page, () => { selected.value = []; load() }, { flush: 'sync' })

async function copy(value: string, label = '卡密') {
 if (!value) return
 try { await copyText(value); msg.success(label + '已复制') }
 catch (e) { msg.error(errorText(e)) }
}
function download(rows: CardRow[]) {
 const codes = rows.map(row => row.code).filter(Boolean)
 if (!codes.length) return
 const url = URL.createObjectURL(new Blob([codes.join('\n') + '\n'], { type: 'text/plain;charset=utf-8' }))
 const anchor = document.createElement('a')
 anchor.href = url; anchor.download = `卡密-${new Date().toISOString().slice(0, 10)}.txt`; anchor.click()
 setTimeout(() => URL.revokeObjectURL(url), 1000)
}
async function generate(rows: CardRow[]) {
 const snapshot = rows.filter(canGenerate).map(row => row.account_id)
 if (busy.value || !ready.value || !snapshot.length) return
 generating.value = true
 try {
  const result = await api<{ items: CardRow[] }>('admin/cards', 'POST', { account_ids: snapshot })
  generated.value = result.items; showGenerated.value = true
  msg.success(`已生成 ${result.items.length} 个卡密`)
  await load()
 } catch (e) { msg.error(errorText(e)) }
 finally { generating.value = false }
}
async function toggleCard(row: CardRow) {
 if (busy.value || !ready.value || !row.code) return
 const enabled = row.status === 'disabled'
 changing.value = row.account_id
 try {
  await api('admin/cards/' + encodeURIComponent(row.account_id), 'PUT', { enabled })
  msg.success(enabled ? '卡密已恢复' : '卡密已停用')
  await load()
 } catch (e) { msg.error(errorText(e)) }
 finally { changing.value = '' }
}
const columns: DataTableColumns<CardRow> = [
 { type: 'selection', disabled: row => !canSelect(row) || busy.value || !ready.value },
 { title: '主邮箱', key: 'email', minWidth: 230, render: row => h('div', { class: 'card-identity' }, [h('strong', row.email), h('small', `${providerName(row.provider)}${row.account_status !== 'active' ? ' · 邮箱已停用' : ''}`)]) },
 { title: '子邮箱链接', key: 'link_count', width: 105, render: row => row.link_count.toLocaleString() },
 { title: '卡密', key: 'code', minWidth: 290, render: row => row.code ? h('code', { class: 'card-code' }, row.code) : h('span', { class: 'muted' }, row.provider === 'mx' ? 'MX 域名不支持生成' : '尚未生成') },
 { title: '状态', key: 'status', width: 95, render: row => h(NTag, { type: tagTypes[row.status], bordered: false, size: 'small' }, { default: () => labels[row.status] }) },
 { title: '兑换时间', key: 'redeemed_at', width: 178, render: row => date(row.redeemed_at) },
 { title: '操作', key: 'actions', width: 160, render: row => h('div', { class: 'card-row-actions' }, row.code ? [
  h(NButton, { size: 'small', quaternary: true, onClick: () => copy(row.code) }, { default: () => '复制' }),
  h(NButton, { size: 'small', quaternary: true, type: row.status === 'disabled' ? 'primary' : 'warning', loading: changing.value === row.account_id, disabled: busy.value || !ready.value, onClick: () => toggleCard(row) }, { default: () => row.status === 'disabled' ? '恢复' : '停用' }),
 ] : [h(NButton, { size: 'small', secondary: true, type: 'primary', disabled: !canGenerate(row) || busy.value || !ready.value, loading: generating.value && selected.value.includes(row.account_id), onClick: () => generate([row]) }, { default: () => '生成卡密' })]) },
]

onMounted(() => { load(); loadAddress(); viewport.addEventListener('change', updateViewport) })
onUnmounted(() => { sequence++; clearTimeout(debounce); viewport.removeEventListener('change', updateViewport) })
</script>

<template>
 <div class="cards-page">
  <div class="page-title">
   <div><div class="page-kicker">MAILBOX REDEMPTION</div><h1>卡密管理</h1><p>一个卡密对应一个主邮箱，兑换后获取该邮箱下的子邮箱和取信链接。</p></div>
   <div class="actions"><NButton :disabled="!addressReady" @click="copy(redemptionURL, '兑换页地址')"><Copy :size="16" class="icon-before" />复制兑换页地址</NButton><NButton v-if="addressReady" tag="a" :href="redemptionURL" target="_blank" rel="noopener noreferrer"><ExternalLink :size="16" class="icon-before" />打开兑换页</NButton></div>
  </div>
  <div class="card-stats">
   <button v-for="(label, key) in labels" :key="key" type="button" class="surface card-stat" :class="{ selected: status === key }" :disabled="busy" @click="filterStatus(key)"><span>{{ label }}</span><strong>{{ summary[key].toLocaleString() }}</strong><KeyRound :size="20" /></button>
  </div>
  <NAlert v-if="addressError" type="warning" :show-icon="false" class="cards-notice">无法读取兑换页地址：{{ addressError }} <NButton size="small" quaternary @click="loadAddress">重试</NButton></NAlert>
  <div class="surface cards-panel">
   <div class="toolbar cards-toolbar">
    <NInput v-model:value="search" class="search" clearable placeholder="搜索主邮箱或卡密" :disabled="busy" :input-props="{ 'aria-label': '搜索主邮箱或卡密' }"><template #prefix><Search :size="17" /></template></NInput>
    <NSelect v-model:value="status" class="filter" :disabled="busy" :options="statusOptions" aria-label="卡密状态" />
    <span class="spacer" /><NButton quaternary :loading="loading" :disabled="busy" @click="load"><RefreshCw :size="16" class="icon-before" />刷新</NButton>
   </div>
   <div class="cards-guide"><KeyRound :size="16" /><span>每个主邮箱只生成一个固定卡密。重复兑换可再次取回链接，停用卡密后暂停兑换。</span></div>
   <NAlert v-if="error" type="error" :show-icon="false" class="cards-notice">{{ error }} <NButton size="small" secondary @click="load">重新加载</NButton></NAlert>
   <div v-if="selected.length" class="selection-bar">
    <strong>已选 {{ selected.length }} 个主邮箱</strong>
    <NButton type="primary" secondary :disabled="!generationRows.length || busy || !ready" :loading="generating" @click="generate(generationRows)"><Plus :size="16" class="icon-before" />生成所选卡密（{{ generationRows.length }}）</NButton>
    <NButton :disabled="!selectedCodes.length || busy || !ready" @click="copy(selectedCodes.map(row => row.code).join('\n'))"><Copy :size="16" class="icon-before" />复制所选卡密</NButton>
    <NButton :disabled="!selectedCodes.length || busy || !ready" @click="download(selectedCodes)"><Download :size="16" class="icon-before" />导出 TXT</NButton>
    <NButton quaternary :disabled="busy" @click="selected = []">取消选择</NButton>
   </div>
   <div v-if="!mobile" class="content-table"><NDataTable :columns="columns" :data="items" :row-key="row => row.account_id" :checked-row-keys="selected" :loading="loading" :scroll-x="1160" :bordered="false" @update:checked-row-keys="selectionChanged" /></div>
   <NSpin v-else :show="loading">
    <div class="cards-mobile">
     <div v-if="items.length" class="cards-select-page"><NCheckbox :checked="pageChecked" :indeterminate="pageIndeterminate" :disabled="busy || !ready || !selectableRows.length" @update:checked="togglePage">选择当前页</NCheckbox></div>
     <article v-for="row in items" :key="row.account_id" class="card-mobile-row">
      <div class="card-mobile-heading"><NCheckbox :checked="selected.includes(row.account_id)" :disabled="busy || !ready || !canSelect(row)" :aria-label="'选择 ' + row.email" @update:checked="checked => toggleRow(row, checked)" /><div class="card-identity"><strong>{{ row.email }}</strong><small>{{ providerName(row.provider) }} · {{ row.link_count }} 条子邮箱链接{{ row.account_status !== 'active' ? ' · 邮箱已停用' : '' }}</small></div><NTag size="small" :bordered="false" :type="tagTypes[row.status]">{{ labels[row.status] }}</NTag></div>
      <code v-if="row.code" class="card-code card-mobile-code">{{ row.code }}</code><p v-else class="card-mobile-empty muted">{{ row.provider === 'mx' ? 'MX 域名不支持生成卡密' : '尚未生成卡密' }}</p>
      <div v-if="row.redeemed_at" class="card-mobile-date muted">兑换时间 {{ date(row.redeemed_at) }}</div>
      <div class="card-mobile-actions"><template v-if="row.code"><NButton secondary @click="copy(row.code)">复制卡密</NButton><NButton :type="row.status === 'disabled' ? 'primary' : 'warning'" secondary :loading="changing === row.account_id" :disabled="busy || !ready" @click="toggleCard(row)">{{ row.status === 'disabled' ? '恢复卡密' : '停用卡密' }}</NButton></template><NButton v-else type="primary" secondary :disabled="!canGenerate(row) || busy || !ready" @click="generate([row])">生成卡密</NButton></div>
     </article>
     <NEmpty v-if="!items.length && !loading" class="cards-empty" description="暂无符合条件的主邮箱" />
    </div>
   </NSpin>
   <div class="pagination"><span class="muted">共 {{ total.toLocaleString() }} 个主邮箱 · 每行一卡密</span><NPagination v-model:page="page" v-model:page-size="size" :disabled="busy || loading" :item-count="total" :page-slot="mobile ? 3 : 5" show-size-picker :page-sizes="[20, 50, 100]" /></div>
  </div>
  <NModal v-model:show="showGenerated" :mask-closable="false">
   <NCard title="卡密已生成" class="form-modal card-result-modal" :bordered="false" role="dialog" aria-modal="true" closable @close="showGenerated = false">
    <p class="muted">共 {{ generated.length }} 个卡密，每行一个，可直接复制到兑换页面批量兑换。</p>
    <NInput :value="generatedCodes" type="textarea" readonly :autosize="{ minRows: 5, maxRows: 12 }" :input-props="{ 'aria-label': '生成的卡密', spellcheck: false }" />
    <details class="card-result-mapping"><summary>查看卡密与主邮箱对应关系</summary><div v-for="row in generated" :key="row.account_id"><strong>{{ row.email }}</strong><code>{{ row.code }}</code></div></details>
    <div class="form-actions"><NButton @click="download(generated)"><Download :size="16" class="icon-before" />下载 TXT</NButton><NButton type="primary" @click="copy(generatedCodes)"><Copy :size="16" class="icon-before" />复制全部卡密</NButton></div>
   </NCard>
  </NModal>
 </div>
</template>

<style scoped>
.cards-panel :deep(.card-identity){display:grid;gap:6px;min-width:0}
.cards-panel :deep(.card-identity>strong){font-size:13px;font-weight:550;overflow-wrap:anywhere}
.cards-panel :deep(.card-identity>small){font-size:11px;color:var(--muted)}
.cards-panel :deep(.card-code){font-size:12px;line-height:1.7;user-select:all;overflow-wrap:anywhere;word-break:break-all}
.cards-panel :deep(.card-row-actions){display:flex;align-items:center;gap:0}
.cards-page{min-width:0}.cards-page .page-title{align-items:flex-start}.card-stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:18px;margin-bottom:24px}.card-stat{position:relative;display:grid;gap:10px;text-align:left;padding:21px 24px;color:var(--text);cursor:pointer;min-width:0}.card-stat:hover,.card-stat.selected{border-color:var(--blue)}.card-stat.selected{background:var(--active)}.card-stat>span{font-size:12px;color:var(--muted)}.card-stat>strong{font-size:30px;font-weight:600;font-variant-numeric:tabular-nums}.card-stat>svg{position:absolute;right:23px;top:25px;color:var(--blue)}.card-stat:disabled{cursor:default;opacity:.7}.cards-panel{overflow:hidden;min-width:0}.cards-toolbar{flex-wrap:wrap}.cards-toolbar .search{flex:1;max-width:360px;min-width:180px}.cards-guide{display:flex;align-items:flex-start;gap:9px;padding:14px 22px;color:var(--muted);font-size:12px;line-height:1.7;border-bottom:1px solid var(--border)}.cards-guide>svg{flex:none;margin-top:2px}.cards-notice{margin:14px 20px}.card-identity{display:grid;gap:6px;min-width:0}.card-identity>strong{font-size:13px;font-weight:550;overflow-wrap:anywhere}.card-identity>small{font-size:11px;color:var(--muted)}.card-code{font-size:12px;line-height:1.7;user-select:all;overflow-wrap:anywhere;word-break:break-all}.card-row-actions{display:flex;align-items:center;gap:0}.cards-panel .pagination>span{font-size:12px}.cards-mobile{min-height:180px}.cards-select-page{padding:9px 16px;border-bottom:1px solid var(--border)}.cards-select-page .n-checkbox{min-height:44px;align-items:center}.card-mobile-row{padding:16px;border-bottom:1px solid var(--border)}.card-mobile-row:last-child{border-bottom:0}.card-mobile-heading{display:flex;align-items:center;gap:8px}.card-mobile-heading>.n-checkbox{min-height:44px;min-width:36px;align-items:center;justify-content:center}.card-mobile-heading>.card-identity{flex:1}.card-mobile-heading>.n-tag{flex:none}.card-mobile-code{display:block;padding:11px 13px;margin:14px 0;background:var(--hover);border:1px solid var(--border);border-radius:9px}.card-mobile-empty{font-size:12px;margin:14px 0}.card-mobile-date{font-size:11px;margin:10px 0}.card-mobile-actions{display:flex;gap:8px}.card-mobile-actions>.n-button{flex:1}.cards-empty{padding:44px 16px}.card-result-mapping{margin-top:20px;font-size:12px}.card-result-mapping>summary{cursor:pointer;color:var(--blue);min-height:44px;line-height:44px}.card-result-mapping>div{display:grid;gap:5px;padding:12px 0;border-top:1px solid var(--border);min-width:0}.card-result-mapping strong{font-weight:500;overflow-wrap:anywhere}.card-result-mapping code{user-select:all}.card-result-modal :deep(textarea){font-family:ui-monospace,Consolas,monospace;font-size:12px}
@media(max-width:1100px){.cards-page .page-title{flex-direction:column;gap:16px}.card-stats{gap:12px}.card-stat{padding:18px}.card-stat>svg{right:18px;top:21px}}
@media(max-width:767px){.card-stats{grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-bottom:18px}.card-stat{padding:15px 17px;gap:7px}.card-stat>strong{font-size:27px}.card-stat>svg{right:17px;top:18px;width:17px}.cards-page .page-title .actions{gap:7px;width:100%}.cards-page .page-title .actions>.n-button{flex:1;padding:0 9px;font-size:12px}.cards-toolbar{padding:14px;gap:9px}.cards-toolbar .search{min-width:0;max-width:none;flex-basis:calc(100% - 95px)}.cards-toolbar .filter{width:calc(100% - 95px);order:2}.cards-toolbar>.n-button{order:3;margin-left:auto}.cards-toolbar .spacer{display:none}.cards-guide{padding:12px 16px}.cards-panel .selection-bar{padding:12px 14px;gap:8px}.cards-panel .selection-bar>strong{width:100%}.cards-panel .selection-bar>.n-button{font-size:12px;padding:0 10px;min-height:44px}.cards-panel .pagination{flex-direction:column;padding:16px;gap:12px}.cards-panel :deep(.n-pagination){max-width:100%;flex-wrap:wrap;justify-content:center;gap:6px}.cards-notice{margin:12px 14px}.card-result-modal .form-actions>.n-button{padding:0 12px;font-size:12px}}
</style>
