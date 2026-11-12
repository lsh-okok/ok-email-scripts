<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NCheckbox, NDataTable, NDropdown, NEmpty, NForm, NFormItem, NInput, NInputNumber, NModal, NPagination, NSelect, NSpin, NTabPane, NTabs, NTag, useDialog, useMessage, type DataTableColumns, type DataTableRowKey } from 'naive-ui'
import { ArrowUpRight, Boxes, ClipboardList, CloudUpload, LayoutDashboard, Link2, RefreshCw, Send, Settings2, Sparkles, Users } from 'lucide-vue-next'
import { api, date, errorText, query } from '../api'
import { downloadSalesFile, salesRequestKey, salesStatus, type SalesFolder, type SalesPage } from '../sales'
import SalesInventory from '../components/SalesInventory.vue'
import SalesSettings from '../components/SalesSettings.vue'
import { resolveSalesSection, salesLocation, salesSections } from '../sales-navigation'

type Outbound = { id: number; order_no: string; mode: string; status: string; selection: string; include_master: boolean; customer: string; note: string; actor: string; created_at: string; delivered_at: string | null; items: number; downloadable: boolean }
type OutboundItem = { id: number; mailbox_id: number | null; email: string; parent_email: string; link?: string; returned_at: string | null }
type OutboundEvent = { actor: string; action: string; reason: string; count: number; created_at: string }
type OutboundDetail = Omit<Outbound, 'items'> & { items: OutboundItem[]; history: OutboundEvent[] }
type OutboundInput = { ids: number[]; master_ids: number[]; folder_id: number; quantity: number | null; mode: string; selection: string; include_master: boolean; inspection: string; valid_hours: number | null; customer: string; note: string }
type Inspection = { id: number; mode: string; scope: string; folder_id: number; status: string; total: number; processed: number; passed: number; failed: number; pending: number; actor: string; created_at: string; finished_at: string | null }
type InspectionResult = { id: number; email: string; parent_email: string; link?: string; result: string; status_code: number; message: string; checked_at: string; processed: boolean; sender_email?: string; phase: string }
type InspectionRetrySkipped = { email: string; parent_email: string; reason: string }
type InspectionRetryInfo = { source_id: number; requested: number; retried: number; skipped: InspectionRetrySkipped[] }
type InspectionDetail = { task: Inspection; results: InspectionResult[]; total: number; page: number; limit: number; stages: Record<string, number>; retry_info?: InspectionRetryInfo | null }
type ImportFile = { name: string; content: string }
type ImportPreview = { token: string; expires_at: string; report: { added_masters: number; added_children: number; duplicates: number; conflicts: number; invalid: number; restored: number; completed: number; issues: { source: string; email: string; reason: string }[] } }
type MappingPreview = { page: number; limit: number; total: number; accounts: { source_id: string; email: string; mailbox_id: number; mapped: boolean; needs_enrichment: boolean; conflict?: string }[]; links: { source_id: string; recipient: string; mailbox_id: number; mapped: boolean; needs_detection_link: boolean; link_reason?: string; conflict?: string }[]; summary: { active_accounts: number; links: number; mapped_accounts: number; mapped_links: number; conflicts: number } }

const message = useMessage(), dialog = useDialog()
const route = useRoute(), router = useRouter()
const active = computed(() => resolveSalesSection(route.query.tab)), loading = ref(false), error = ref(''), revision = ref(0)
const sectionOptions = salesSections.map(section => ({label:section.label,value:section.key}))
const lastUpdated = ref(''), recentOutbounds = ref<Outbound[]>([])
const folders = ref<SalesFolder[]>([])
const folderSummary = ref({ total_masters: 0, total_children: 0, unclassified: 0, unclassified_children: 0 })
const folderOptions = computed(() => [{ label: '未分类', value: 0 }, ...folders.value.map(v => ({ label: v.name, value: v.id }))])
const selectedChildren = ref<number[]>([]), selectedMasters = ref<number[]>([])
const outbounds = ref<Outbound[]>([]), outboundPage = ref(1), outboundTotal = ref(0), outboundSearch = ref(''), outboundFilter = ref('')
const inspections = ref<Inspection[]>([]), inspectionPage = ref(1), inspectionTotal = ref(0)
const mode = ref('reserve'), selection = ref('random'), outboundRange = ref('folder'), folderID = ref(0), quantity = ref<number | null>(1), customer = ref(''), note = ref(''), requiredInspection = ref(''), validHours = ref<number | null>(24), includeMaster = ref(false)
const creating = ref(false), actionLoading = ref<number | null>(null), downloadLoading = ref<number | null>(null)
const outboundExportChoices = ref<Record<number, boolean>>({})
const outboundAttempt = ref<{ payload: string; key: string } | null>(null)
const showGroupOutbound = ref(false), groupMasterIDs = ref<number[]>([])
const groupOutbound = reactive({ mode: 'sell', customer: '', note: '', inspection: '', validHours: 24 as number | null })
const actualRange = computed(() => mode.value === 'quick' ? 'folder' : selection.value === 'group' ? 'master' : outboundRange.value)
const scopeDescription = computed(() => actualRange.value === 'child' ? `已选 ${selectedChildren.value.length} 个子邮箱` : actualRange.value === 'master' ? `已选 ${selectedMasters.value.length} 个主邮箱` : folderOptions.value.find(v => v.value === folderID.value)?.label || '未分类')
const showOutbound = ref(false), outboundDetail = ref<OutboundDetail | null>(null), detailLoading = ref(false), selectedReturnItems = ref<number[]>([]), outboundAction = ref<'cancel' | 'return'>('return'), actionReason = ref('')
const canReturn = computed(() => !!outboundDetail.value && ['sold', 'partial_return'].includes(outboundDetail.value.status))
const importText = ref(''), importFiles = ref<ImportFile[]>([]), importPreview = ref<ImportPreview | null>(null), importBusy = ref(false), showImport = ref(false), importFolder = ref(0)
const mapping = ref<MappingPreview | null>(null), mappingLoading = ref(false), mappingCommitting = ref(false), mappingError = ref('')
const mappingBusy = computed(() => mappingLoading.value || mappingCommitting.value)
const mappingPage = ref(1), mappingFilter = ref('issues'), mappingLimit = 50
const inspectionMode = ref('link'), inspectionScope = ref('folder'), inspectionFolder = ref(0), inspectionSample = ref(1), includeSold = ref(false), inspectionBusy = ref(false)
const inspectionOptions = reactive({ timeout: 10, concurrency: 5, wait: 60, interval: 1000 })
const showInspection = ref(false), inspectionDetail = ref<InspectionDetail | null>(null), inspectionDetailLoading = ref(false), resultPage = ref(1), resultFilter = ref('')
const sessionAttemptKey = 'mail-lite-outbound-attempt'
let pollTimer: ReturnType<typeof setInterval> | undefined, polling = false
let summarySequence = 0, outboundSequence = 0, outboundDetailSequence = 0, inspectionSequence = 0, inspectionDetailSequence = 0, mappingSequence = 0
let mappingRequest: { key: string; promise: Promise<void> } | null = null

const outboundColumns: DataTableColumns<Outbound> = [
 { title: '订单号', key: 'order_no', minWidth: 185 },
 { title: '方式', key: 'mode', width: 90, render: row => row.mode === 'quick' ? '快捷出售' : row.mode === 'sell' ? '出售' : '预留' },
 { title: '客户 / 备注', key: 'customer', minWidth: 160, render: row => h('div', { class: 'mail-cell' }, [h('span', row.customer || '—'), h('small', row.note)]) },
 { title: '数量', key: 'items', width: 70 },
 { title: '状态', key: 'status', width: 110, render: row => h(NTag, { size: 'small', bordered: false }, { default: () => salesStatus(row.status) }) },
 { title: '创建时间', key: 'created_at', width: 175, render: row => date(row.created_at) },
 { title: '操作', key: 'actions', width: 250, render: row => h('div', { class: 'table-actions' }, [
  h(NButton, { size: 'small', quaternary: true, onClick: () => openOutbound(row.id) }, { default: () => '详情' }),
  row.downloadable ? row.mode === 'quick'
   ? h(NButton, { size: 'small', quaternary: true, loading: downloadLoading.value === row.id, onClick: () => downloadOutbound(row) }, { default: () => '下载' })
   : h(NDropdown, { trigger: 'click', options: [{label:'包含主邮箱资料',key:'master'},{label:'仅子邮箱与链接',key:'children'}], onSelect: (key: string) => downloadOutbound(row, key === 'master') }, { default: () => h(NButton, { size: 'small', quaternary: true, loading: downloadLoading.value === row.id }, { default: () => '下载' }) }) : null,
  row.status === 'reserved' ? h(NButton, { size: 'small', type: 'primary', secondary: true, loading: actionLoading.value === row.id, onClick: () => confirmDeliver(row.id) }, { default: () => '交付' }) : null,
  row.status === 'reserved' ? h(NButton, { size: 'small', quaternary: true, onClick: () => openOutbound(row.id, 'cancel') }, { default: () => '取消' }) : null,
  ['sold', 'partial_return'].includes(row.status) ? h(NButton, { size: 'small', quaternary: true, onClick: () => openOutbound(row.id, 'return') }, { default: () => '退库' }) : null,
 ]) },
]
const outboundItemColumns: DataTableColumns<OutboundItem> = [
 { type: 'selection', disabled: row => !!row.returned_at || !row.mailbox_id || !canReturn.value },
 { title: '子邮箱', key: 'email', minWidth: 200 }, { title: '主邮箱', key: 'parent_email', minWidth: 200 },
 { title: '接码链接', key: 'link', minWidth: 240, ellipsis: { tooltip: true }, render: row => row.link || '—' },
 { title: '退库时间', key: 'returned_at', width: 175, render: row => row.returned_at ? date(row.returned_at) : row.mailbox_id ? '—' : '库存已删除' },
]
const inspectionColumns: DataTableColumns<Inspection> = [
 { title: '任务', key: 'id', width: 70, render: row => `#${row.id}` },
 { title: '类型', key: 'mode', width: 100, render: row => row.mode === 'link' ? '链接检测' : '收信验证' },
 { title: '进度', key: 'progress', width: 110, render: row => `${row.processed}/${row.total}` },
 { title: '结果', key: 'passed', minWidth: 160, render: row => `${row.passed} 通过 · ${row.failed} 失败 · ${row.pending} 待确认` },
 { title: '状态', key: 'status', width: 100, render: row => h(NTag, { size: 'small', bordered: false }, { default: () => salesStatus(row.status) }) },
 { title: '创建时间', key: 'created_at', width: 175, render: row => date(row.created_at) },
 { title: '操作', key: 'actions', width: 245, render: row => h('div', { class: 'table-actions' }, [
  h(NButton, { size: 'small', quaternary: true, onClick: () => openInspection(row.id) }, { default: () => '详情' }),
  h(NButton, { size: 'small', quaternary: true, onClick: () => downloadInspection(row.id) }, { default: () => '报告' }),
  ['queued', 'running'].includes(row.status) ? h(NButton, { size: 'small', quaternary: true, type: 'warning', loading: actionLoading.value === -row.id, onClick: () => stopInspection(row.id) }, { default: () => '停止' }) : h(NButton, { size: 'small', quaternary: true, loading: actionLoading.value === -row.id, onClick: () => retryInspection(row.id) }, { default: () => '复检未通过项' }),
 ]) },
]
const resultColumns: DataTableColumns<InspectionResult> = [
 { title: '子邮箱', key: 'email', minWidth: 200 },
 { title: '主邮箱', key: 'parent_email', minWidth: 200, ellipsis: { tooltip: true }, render: row => row.parent_email || '—' },
 { title: '结果', key: 'result', width: 100, render: row => salesStatus(row.result) },
 { title: '说明', key: 'message', minWidth: 260 },
 { title: 'HTTP', key: 'status_code', width: 80, render: row => row.status_code || '—' },
 { title: '发件邮箱', key: 'sender_email', minWidth: 200, render: row => row.sender_email || '—' },
 { title: '检测时间', key: 'checked_at', width: 175, render: row => date(row.checked_at) },
]
const retrySkippedColumns: DataTableColumns<InspectionRetrySkipped> = [
 { title: '子邮箱', key: 'email', minWidth: 200 },
 { title: '主邮箱', key: 'parent_email', minWidth: 200, render: row => row.parent_email || '—' },
 { title: '跳过原因', key: 'reason', minWidth: 240 },
]

async function loadSummary() {
 const sequence = ++summarySequence
 const f = await api<{ items: SalesFolder[]; total_masters: number; total_children: number; unclassified: number; unclassified_children: number }>('admin/mail/folders')
 if (sequence === summarySequence) {
  folders.value = f.items
  folderSummary.value = { total_masters: f.total_masters, total_children: f.total_children, unclassified: f.unclassified, unclassified_children: f.unclassified_children }
  lastUpdated.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
 }
}
async function loadOutbounds() {
 const sequence = ++outboundSequence
 const data = await api<SalesPage<Outbound>>('admin/mail/outbounds?' + query({ q: outboundSearch.value.trim(), status: outboundFilter.value, page: outboundPage.value, limit: 25 }))
 if (sequence === outboundSequence) { outbounds.value = data.items; outboundTotal.value = data.total; if (outboundPage.value === 1 && !outboundSearch.value.trim() && !outboundFilter.value) recentOutbounds.value = data.items.slice(0, 5) }
}
async function loadInspections() {
 const sequence = ++inspectionSequence
 const data = await api<SalesPage<Inspection>>('admin/mail/inspections?' + query({ page: inspectionPage.value, limit: 25 }))
 if (sequence === inspectionSequence) { inspections.value = data.items; inspectionTotal.value = data.total }
}
async function load() {
 loading.value = true; error.value = ''
 try {
  const results = await Promise.allSettled([loadSummary(), loadOutbounds(), loadInspections()])
  const failures = results.flatMap((result, index) => result.status === 'rejected' ? [`${['文件夹', '出库记录', '检测任务'][index]}：${errorText(result.reason)}`] : [])
  error.value = failures.join('；'); revision.value++
 } finally { loading.value = false }
}
async function reloadOutbounds(reset = false) { if (reset) outboundPage.value = 1; try { await loadOutbounds() } catch (e) { message.error(errorText(e)) } }
async function reloadInspections() { try { await loadInspections() } catch (e) { message.error(errorText(e)) } }
async function loadMapping() {
 const key = query({ page: mappingPage.value, limit: mappingLimit, issues_only: mappingFilter.value === 'issues' ? 1 : undefined })
 if (mappingRequest?.key === key) return mappingRequest.promise
 const sequence = ++mappingSequence
 mappingLoading.value = true; mappingError.value = ''
 const promise = (async () => {
  try {
   const result = await api<MappingPreview>(`admin/mail/mapping/preview?${key}`)
   if (sequence !== mappingSequence) return
   mapping.value = result; mappingPage.value = result.page
  } catch (e) {
   if (sequence !== mappingSequence) return
   mappingError.value = errorText(e); mapping.value = null
  } finally {
   if (sequence === mappingSequence) { mappingLoading.value = false; mappingRequest = null }
  }
 })()
 mappingRequest = { key, promise }
 return promise
}
function filterMapping(value: string) { mappingFilter.value = value; mappingPage.value = 1; loadMapping() }
function pageMapping(value: number) { mappingPage.value = value; loadMapping() }
async function commitMapping() {
 if (mappingBusy.value || !mapping.value || mappingError.value) return
 mappingCommitting.value = true
 try {
  const result = await api<{ created_accounts: number; created_links: number; needs_detection_links: number; conflicts: unknown[] }>('admin/mail/mapping', 'POST', { include_links: true })
  message.success(`新增 ${result.created_accounts} 个主邮箱、${result.created_links} 个子邮箱；${result.needs_detection_links} 个来源链接待处理`)
  if (result.conflicts.length) message.warning(`已跳过 ${result.conflicts.length} 个冲突，请查看预览`)
  mappingPage.value = 1; await loadMapping(); await load()
 } catch (e) { message.error(errorText(e)) } finally { mappingCommitting.value = false }
}
async function chooseImportFiles(event: Event) {
 const input = event.target as HTMLInputElement, files = Array.from(input.files || [])
 importPreview.value = null
 if (files.length > 500 || files.reduce((n, file) => n + file.size, 0) > 20 * 1024 * 1024) { message.warning('一次最多 500 个文件，总大小不超过 20 MB'); input.value = ''; return }
 importBusy.value = true
 try { importFiles.value = await Promise.all(files.map(async file => ({ name: file.name, content: await file.text() }))) } catch (e) { message.error(errorText(e)) } finally { importBusy.value = false; input.value = '' }
}
async function previewImport() {
 const files = [...importFiles.value, ...(importText.value.trim() ? [{ name: 'pasted.txt', content: importText.value }] : [])]
 if (!files.length) return message.warning('请粘贴 TXT 内容或选择文件')
 importBusy.value = true; importPreview.value = null
 try { importPreview.value = await api<ImportPreview>('admin/mail/import/preview', 'POST', { folder_id: importFolder.value, files }); message.success('预览已生成，请核对导入报告') } catch (e) { message.error(errorText(e)) } finally { importBusy.value = false }
}
async function commitImport() {
 if (!importPreview.value) return
 importBusy.value = true
 try { await api('admin/mail/import/commit', 'POST', { token: importPreview.value.token }); message.success('库存导入完成'); importPreview.value = null; importText.value = ''; importFiles.value = []; showImport.value = false; await load() } catch (e) { message.error(errorText(e)) } finally { importBusy.value = false }
}
function clearOutboundAttempt() { outboundAttempt.value = null; try { sessionStorage.removeItem(sessionAttemptKey) } catch { /* Storage can be disabled. */ } }
async function createOutbound() {
 if (creating.value) return
 if (actualRange.value === 'child' && !selectedChildren.value.length) return message.warning('请先在库存中选择子邮箱')
 if (actualRange.value === 'master' && !selectedMasters.value.length) return message.warning('请先在库存中选择主邮箱')
 if (selection.value !== 'group' || mode.value === 'quick') if (!Number.isInteger(quantity.value) || !quantity.value || quantity.value < 1) return message.warning('请输入有效抽取数量')
 if (requiredInspection.value && (!Number.isInteger(validHours.value) || !validHours.value)) return message.warning('请输入检测有效期')
 const body = { ids: actualRange.value === 'child' ? [...selectedChildren.value].sort((a,b) => a-b) : [], master_ids: actualRange.value === 'master' ? [...selectedMasters.value].sort((a,b) => a-b) : [], folder_id: actualRange.value === 'folder' ? folderID.value : 0, quantity: mode.value !== 'quick' && selection.value === 'group' ? 1 : quantity.value, mode: mode.value, selection: mode.value === 'quick' ? 'random' : selection.value, include_master: mode.value !== 'quick' && (selection.value === 'group' || includeMaster.value), inspection: requiredInspection.value, valid_hours: requiredInspection.value ? validHours.value : 0, customer: customer.value.trim(), note: note.value.trim() }
 await submitOutbound(body)
}
function openGroupOutbound(ids: number[]) {
 if (creating.value || !ids.length) return
 groupMasterIDs.value = [...new Set(ids)].sort((a,b) => a-b)
 showGroupOutbound.value = true
}
async function createGroupOutbound() {
 if (creating.value || !groupMasterIDs.value.length) return
 if (groupOutbound.inspection && (!Number.isInteger(groupOutbound.validHours) || !groupOutbound.validHours || groupOutbound.validHours < 1 || groupOutbound.validHours > 720)) return message.warning('请输入 1–720 小时的检测有效期')
 await submitOutbound({ ids: [], master_ids: [...groupMasterIDs.value], folder_id: 0, quantity: 1, mode: groupOutbound.mode, selection: 'group', include_master: true, inspection: groupOutbound.inspection, valid_hours: groupOutbound.inspection ? groupOutbound.validHours : 0, customer: groupOutbound.customer.trim(), note: groupOutbound.note.trim() }, true)
}
async function submitOutbound(body: OutboundInput, fromInventory = false) {
 if (creating.value) return
 const payload = JSON.stringify(body)
 creating.value = true
 try {
  if (!outboundAttempt.value || outboundAttempt.value.payload !== payload) outboundAttempt.value = { payload, key: salesRequestKey() }
  try { sessionStorage.setItem(sessionAttemptKey, JSON.stringify(outboundAttempt.value)) } catch { /* Retain the key in memory. */ }
  const result = await api<{ id: number; reused?: boolean }>(body.mode === 'quick' ? 'admin/mail/quick-outbound' : 'admin/mail/outbounds', 'POST', { ...body, request_key: outboundAttempt.value.key })
  clearOutboundAttempt(); message.success(result.reused ? '已找回此前创建的出库单' : '出库单已创建')
  if (fromInventory) {
   showGroupOutbound.value = false
   selectedMasters.value = selectedMasters.value.filter(id => !body.master_ids.includes(id))
   groupOutbound.customer = ''; groupOutbound.note = ''
  } else { customer.value = ''; note.value = ''; selectedChildren.value = []; selectedMasters.value = [] }
  await load(); await openOutbound(result.id)
 } catch (e) { message.error(errorText(e) + '；相同内容重试会复用本次请求') } finally { creating.value = false }
}
async function openOutbound(id: number, action: 'cancel' | 'return' = 'return') {
 const sequence = ++outboundDetailSequence
 showOutbound.value = true; detailLoading.value = true; outboundDetail.value = null; selectedReturnItems.value = []; outboundAction.value = action; actionReason.value = ''
 try { const data = await api<OutboundDetail>(`admin/mail/outbounds/${id}`); if(sequence === outboundDetailSequence && showOutbound.value) outboundDetail.value = data }
 catch (e) { if(sequence === outboundDetailSequence && showOutbound.value) message.error(errorText(e)) }
 finally { if(sequence === outboundDetailSequence) detailLoading.value = false }
}
function outboundIncludesMaster(order: Outbound | OutboundDetail) {
 return order.mode !== 'quick' && (outboundExportChoices.value[order.id] ?? (order.selection === 'group' || order.include_master))
}
async function downloadOutbound(order: Outbound | OutboundDetail, include = outboundIncludesMaster(order)) {
 if (downloadLoading.value !== null) return
 downloadLoading.value = order.id
 outboundExportChoices.value[order.id] = include
 try { downloadSalesFile(await api<{ filename: string; content: string }>(`admin/mail/outbounds/${order.id}/export?include_master=${include ? 1 : 0}`)) } catch (e) { message.error(errorText(e)) } finally { downloadLoading.value = null }
}
function confirmDeliver(id: number) {
 dialog.warning({ title: '交付预留订单', content: '交付后，订单内所有预留子邮箱将标记为已售。', positiveText: '确认交付', negativeText: '取消', onPositiveClick: () => actionOutbound(id, 'deliver') })
}
async function actionOutbound(id: number, action: 'deliver' | 'cancel' | 'return') {
 if (action !== 'deliver' && !actionReason.value.trim()) { message.warning('请填写操作原因'); return false }
 if (action === 'return' && !selectedReturnItems.value.length) { message.warning('请选择要退库的子邮箱'); return false }
 actionLoading.value = id
 try {
  await api(`admin/mail/outbounds/${id}/action`, 'POST', { action, reason: action === 'deliver' ? '' : actionReason.value.trim(), item_ids: action === 'return' ? selectedReturnItems.value : [] })
  message.success(action === 'cancel' ? '预留已取消' : action === 'return' ? '所选邮箱已退库，原出库单下载已停用' : '订单已交付')
  if (action === 'return' && outboundDetail.value?.selection === 'group') message.info('如需重新销售整组，请在库存中恢复主邮箱在库状态')
  await load(); if (showOutbound.value && outboundDetail.value?.id === id) await openOutbound(id)
  return true
 } catch (e) { message.error(errorText(e)); return false } finally { actionLoading.value = null }
}
function requestDetailAction() {
 if (!outboundDetail.value) return
 if (!actionReason.value.trim()) return message.warning('请填写操作原因')
 if (outboundAction.value === 'return' && !selectedReturnItems.value.length) return message.warning('请选择要退库的子邮箱')
 const id = outboundDetail.value.id, action = outboundAction.value
 dialog.warning({ title: action === 'return' ? '确认退库' : '取消预留', content: action === 'return' ? `退库 ${selectedReturnItems.value.length} 个子邮箱后，原出库单将停用下载。` : '取消后，订单内所有预留子邮箱将恢复在库。', positiveText: '确认', negativeText: '返回', onPositiveClick: () => actionOutbound(id, action) })
}
async function createInspection() {
 if (inspectionBusy.value) return
 if (inspectionScope.value === 'selected' && !selectedChildren.value.length) return message.warning('请先选择子邮箱')
 if (inspectionScope.value === 'master' && !selectedMasters.value.length) return message.warning('请先选择主邮箱')
 if (inspectionMode.value === 'inbox') {
  dialog.warning({ title: '确认发送测试邮件', content: '收信验证将使用已配置的发件邮箱，向当前范围中的子邮箱发送测试邮件，再通过接码链接查找本次邮件标识。', positiveText: '确认并发送', negativeText: '取消', onPositiveClick: () => submitInspection(true) })
 } else await submitInspection(false)
}
async function submitInspection(confirmSend: boolean) {
 inspectionBusy.value = true
 try {
  const result = await api<{ id: number }>('admin/mail/inspections', 'POST', { mode: inspectionMode.value, scope: inspectionScope.value, ids: inspectionScope.value === 'selected' ? selectedChildren.value : [], master_ids: inspectionScope.value === 'master' ? selectedMasters.value : [], folder_id: inspectionFolder.value, sample: inspectionSample.value, include_sold: includeSold.value, confirm_send: confirmSend, options: inspectionOptions })
  message.success('检测任务已排队'); inspectionPage.value = 1; await loadInspections(); await openInspection(result.id); return true
 } catch (e) { message.error(errorText(e)); return false } finally { inspectionBusy.value = false }
}
async function openInspection(id: number) {
 showInspection.value = true; inspectionDetail.value = null; resultPage.value = 1; resultFilter.value = ''; await loadInspectionDetail(id)
}
async function loadInspectionDetail(id = inspectionDetail.value?.task.id, silent = false) {
 if (!id) return
 const sequence = ++inspectionDetailSequence
 if (!silent) inspectionDetailLoading.value = true
 try { const data = await api<InspectionDetail>(`admin/mail/inspections/${id}?` + query({ page: resultPage.value, limit: 25, result: resultFilter.value })); if (sequence === inspectionDetailSequence) inspectionDetail.value = data } catch (e) { if (!silent) message.error(errorText(e)) } finally { if (sequence === inspectionDetailSequence) inspectionDetailLoading.value = false }
}
function filterResults() { resultPage.value = 1; loadInspectionDetail() }
function stopInspection(id: number) {
 dialog.warning({ title: '停止检测任务', content: '已完成的结果会保留，尚未完成的项目可稍后复检。', positiveText: '停止任务', negativeText: '继续检测', onPositiveClick: async () => {
  actionLoading.value = -id
  try { await api(`admin/mail/inspections/${id}/stop`, 'POST', {}); message.success('任务已停止'); await loadInspections(); if (inspectionDetail.value?.task.id === id) await loadInspectionDetail(id); return true } catch (e) { message.error(errorText(e)); return false } finally { actionLoading.value = null }
 } })
}
async function retryInspection(id: number) {
 actionLoading.value = -id
 try {
  const result = await api<{ id: number; retry_info: InspectionRetryInfo }>(`admin/mail/inspections/${id}/retry`, 'POST', {})
  const skipped = result.retry_info.skipped.length
  message.success(`已创建 ${result.retry_info.retried} 项复检${skipped ? `，跳过 ${skipped} 项，原因见任务详情` : ''}；收信验证不重复发信`)
  await loadInspections(); await openInspection(result.id)
 } catch (e) { message.error(errorText(e)) } finally { actionLoading.value = null }
}
async function downloadInspection(id: number) {
 try { downloadSalesFile(await api<{ filename: string; content: string }>(`admin/mail/inspections/${id}/export`)) } catch (e) { message.error(errorText(e)) }
}
function tabChanged(value: string) { router.push(salesLocation(resolveSalesSection(value))) }
watch(active, value => { if (value === 'mapping' && !mapping.value) loadMapping() }, { immediate: true })
async function poll() {
 if (polling || document.hidden || !inspections.value.some(v => ['queued', 'running'].includes(v.status)) && !showInspection.value) return
 polling = true
 try { await loadInspections(); if (showInspection.value && inspectionDetail.value) await loadInspectionDetail(inspectionDetail.value.task.id, true); await loadSummary() } catch { /* A manual refresh surfaces persistent errors. */ } finally { polling = false }
}
watch([importText, importFiles, importFolder], () => { importPreview.value = null })
watch(showImport, value => { if (!value) { importText.value = ''; importFiles.value = []; importPreview.value = null } })
watch(showOutbound, value => { if (!value) { outboundDetailSequence++; detailLoading.value = false; outboundDetail.value = null; selectedReturnItems.value = []; actionReason.value = '' } }, {flush:'sync'})
watch(showInspection, value => { if (!value) { inspectionDetailSequence++; inspectionDetail.value = null } })
onMounted(() => {
 try { const saved = JSON.parse(sessionStorage.getItem(sessionAttemptKey) || 'null'); if (saved && typeof saved.payload === 'string' && typeof saved.key === 'string') outboundAttempt.value = saved } catch { /* Start without an existing request. */ }
 load(); pollTimer = setInterval(poll, 5000)
})
onUnmounted(() => { outboundDetailSequence++; mappingSequence++; if (pollTimer) clearInterval(pollTimer) })
</script>

<template>
 <div class="sales-page">
  <div class="page-title sales-title"><div><h1>销售工作台</h1><p>从库存到交付，在这里完成每一笔销售。</p></div><div class="sales-title-actions"><NButton quaternary :loading="loading" @click="load"><RefreshCw :size="16" class="icon-before" /><span class="refresh-label">刷新数据</span></NButton><NButton secondary @click="showImport=true"><CloudUpload :size="16" class="icon-before" />导入库存</NButton><NButton type="primary" @click="tabChanged('outbound')"><Send :size="16" class="icon-before" />新建出库</NButton></div></div>
  <NAlert v-if="error" type="error" :show-icon="false" class="sales-error">部分数据加载失败：{{ error }} <NButton text type="error" :loading="loading" @click="load">重新加载</NButton></NAlert>
  <section class="surface sales-panel">
   <div class="sales-mobile-navigation"><label for="sales-section">当前功能</label><NSelect :value="active" :options="sectionOptions" :input-props="{id:'sales-section'}" aria-label="销售工作台功能" @update:value="tabChanged" /></div>
   <NTabs class="sales-tabs" type="line" :animated="false" :value="active" @update:value="tabChanged">
    <NTabPane name="overview" display-directive="show:lazy"><template #tab><span class="sales-tab"><LayoutDashboard :size="16" />工作流概览</span></template>
     <div class="overview-heading"><div><h2>销售概览</h2><p class="muted">处理库存同步、出库与交付。</p></div><RouterLink class="overview-home-link" to="/home">查看首页销售统计 <ArrowUpRight :size="14" /></RouterLink><span class="updated-at" aria-live="polite">{{ loading ? '正在更新…' : lastUpdated ? `更新于 ${lastUpdated}` : '等待加载' }}</span></div>
     <div class="overview-grid">
      <section class="overview-card quick-workflows"><div class="section-head"><h3>常用操作</h3><span class="muted">库存 → 检测 → 交付</span></div><div class="workflow-grid">
       <button class="workflow-card" @click="tabChanged('mapping')"><span class="workflow-icon"><Link2 :size="19" /></span><span><strong>同步销售库存</strong><small>映射启用账号，自动关联取信链接</small></span><ArrowUpRight :size="16" class="workflow-arrow" /></button>
       <button class="workflow-card" @click="tabChanged('inventory')"><span class="workflow-icon green"><Boxes :size="19" /></span><span><strong>管理库存</strong><small>筛选邮箱、补齐链接与批量导出</small></span><ArrowUpRight :size="16" class="workflow-arrow" /></button>
       <button class="workflow-card" @click="tabChanged('outbound')"><span class="workflow-icon orange"><Send :size="19" /></span><span><strong>创建出库单</strong><small>随机或整组出库，预留并交付</small></span><ArrowUpRight :size="16" class="workflow-arrow" /></button>
       <button class="workflow-card" @click="tabChanged('inspection')"><span class="workflow-icon purple"><Sparkles :size="19" /></span><span><strong>检测邮箱</strong><small>检查取信链接或验证收信</small></span><ArrowUpRight :size="16" class="workflow-arrow" /></button>
      </div></section>
      <section class="overview-card recent-orders"><div class="section-head"><h3>近期出库</h3><NButton text size="small" type="primary" @click="tabChanged('outbound')">全部记录 <ArrowUpRight :size="14" class="ml" /></NButton></div><div v-if="recentOutbounds.length" class="recent-list"><button v-for="order in recentOutbounds" :key="order.id" class="recent-order" @click="openOutbound(order.id)"><span class="recent-order-icon"><ClipboardList :size="18" /></span><span class="recent-order-info"><strong>{{ order.customer || order.order_no }}</strong><small>{{ order.customer ? `${order.order_no} · ` : '' }}{{ date(order.created_at) }}</small></span><span class="recent-order-result"><NTag size="small" :bordered="false" :type="order.status==='reserved'?'warning':order.status==='sold'?'success':'default'">{{ salesStatus(order.status) }}</NTag><small>{{ order.items }} 个邮箱</small></span></button></div><NEmpty v-else :description="loading ? '正在加载出库记录…' : '暂无出库记录'" class="overview-empty"><template #extra><NButton size="small" secondary type="primary" @click="tabChanged('outbound')">创建第一笔出库</NButton></template></NEmpty></section>
     </div>
    </NTabPane>
    <NTabPane name="inventory" display-directive="show:lazy" class="sales-inventory-pane"><template #tab><span class="sales-tab"><Boxes :size="16" />库存管理</span></template><SalesInventory v-model:selected-masters="selectedMasters" v-model:selected-children="selectedChildren" :folders="folders" :folder-summary="folderSummary" :revision="revision" @refresh="load" @submit-outbound="openGroupOutbound" /></NTabPane>
    <NTabPane name="mapping" display-directive="show:lazy"><template #tab><span class="sales-tab"><Link2 :size="16" />账号映射</span></template>
     <div class="section-head"><div><h2>当前账号映射</h2><p class="muted">映射全部启用的主邮箱及其子邮箱；筛选和翻页仅影响预览，重复提交会跳过已映射项。</p></div><div class="row"><NButton quaternary :loading="mappingLoading" :disabled="mappingBusy" @click="loadMapping">刷新预览</NButton><NButton type="primary" :loading="mappingCommitting" :disabled="!mapping || mappingBusy || !!mappingError" @click="commitMapping">提交全部映射</NButton></div></div>
     <NAlert v-if="mappingError" type="error" class="mapping-error" title="映射预览加载失败"><div class="row"><span>{{ mappingError }}</span><NButton size="small" :loading="mappingLoading" :disabled="mappingBusy" @click="loadMapping">重试</NButton></div></NAlert>
     <NSpin :show="mappingLoading && !mapping"><div v-if="mapping" class="mapping-grid"><div class="mapping-card"><Users :size="20" /><strong>{{ mapping.summary.active_accounts }}</strong><span>启用的主账号</span><small>已映射 {{ mapping.summary.mapped_accounts }}</small></div><div class="mapping-card"><Link2 :size="20" /><strong>{{ mapping.summary.links }}</strong><span>子邮箱链接</span><small>已映射 {{ mapping.summary.mapped_links }}</small></div><div class="mapping-card warning"><ClipboardList :size="20" /><strong>{{ mapping.summary.conflicts }}</strong><span>需要处理的冲突</span><small>提交会跳过冲突项</small></div></div><NEmpty v-else-if="!mappingError" :description="mappingLoading ? '正在加载映射预览…' : '点击刷新预览'" /></NSpin>
     <NAlert v-if="mapping" type="info" :show-icon="false" style="margin-bottom:16px">新映射的子邮箱自动关联原取信链接，无需手动填写。旧版本已映射的空链接可在库存管理中一键补齐；来源失效或映射冲突会显示原因。<NButton text type="primary" @click="tabChanged('inventory')">查看库存及取信链接</NButton></NAlert>
     <template v-if="mapping">
      <h3 class="mapping-heading">主邮箱</h3><div class="mapping-rows"><div v-for="item in mapping.accounts" :key="item.source_id" class="mapping-row"><span>{{ item.email }}</span><NTag size="small" :type="item.conflict?'error':item.mapped?'success':'default'" :bordered="false">{{ item.conflict || (item.mapped ? item.needs_enrichment ? '资料待补全' : '已映射' : '待映射') }}</NTag></div></div>
      <div class="mapping-toolbar"><h3 class="mapping-heading">子邮箱</h3><NSelect aria-label="子邮箱预览范围" :value="mappingFilter" :disabled="mappingBusy" :options="[{label:'需要处理的子邮箱',value:'issues'},{label:'全部子邮箱',value:'all'}]" @update:value="filterMapping" /><span class="muted">共 {{ mapping.total }} 条 · 每页 {{ mappingLimit }} 条</span></div>
      <NSpin :show="mappingLoading"><div v-if="mapping.links.length" class="mapping-rows"><div v-for="item in mapping.links" :key="item.source_id" class="mapping-row"><span>{{ item.recipient }}</span><NTag size="small" :type="item.conflict?'error':item.needs_detection_link?'warning':item.mapped?'success':'default'" :bordered="false">{{ item.conflict || item.link_reason || (item.needs_detection_link ? item.mapped ? '空链接可一键补齐' : '映射后自动关联' : item.mapped ? '已映射' : '待映射') }}</NTag></div></div><NEmpty v-else :description="mappingFilter==='issues' ? '暂无需要处理的子邮箱' : '暂无子邮箱'" class="mapping-empty" /></NSpin>
      <NPagination v-if="mapping.total > mappingLimit" :page="mappingPage" :item-count="mapping.total" :page-size="mappingLimit" :disabled="mappingBusy" simple @update:page="pageMapping" />
     </template>
    </NTabPane>
    <NTabPane name="outbound" display-directive="show:lazy"><template #tab><span class="sales-tab"><Send :size="16" />出库记录</span></template>
     <div class="outbound-form surface"><div class="section-head"><div><h2>创建出库单</h2><p class="muted">随机模式可从文件夹或选中邮箱抽取；整组模式交付所选主邮箱的全部子邮箱。</p></div></div>
      <NForm label-placement="top" :disabled="creating" class="form-grid">
       <NFormItem label="方式"><NSelect v-model:value="mode" :options="[{label:'预留',value:'reserve'},{label:'出售',value:'sell'},{label:'快捷出售',value:'quick'}]" /></NFormItem>
       <NFormItem label="抽取"><NSelect v-model:value="selection" :disabled="mode==='quick'" :options="[{label:'随机',value:'random'},{label:'整组',value:'group'}]" /></NFormItem>
       <NFormItem label="范围"><NSelect :value="actualRange" :disabled="mode==='quick' || selection==='group'" :options="[{label:'指定文件夹',value:'folder'},{label:`选中子邮箱 (${selectedChildren.length})`,value:'child'},{label:`选中主邮箱 (${selectedMasters.length})`,value:'master'}]" @update:value="value=>outboundRange=value" /></NFormItem>
       <NFormItem v-if="actualRange==='folder'" label="来源文件夹"><NSelect v-model:value="folderID" :options="folderOptions" /></NFormItem>
       <NFormItem v-if="mode==='quick' || selection==='random'" label="子邮箱数量"><NInputNumber v-model:value="quantity" :min="1" :max="10000" :precision="0" /></NFormItem>
       <NFormItem label="检测要求"><NSelect v-model:value="requiredInspection" :options="[{label:'不限制检测结果',value:''},{label:'链接检测通过',value:'link'},{label:'收信验证通过',value:'inbox'}]" /></NFormItem>
       <NFormItem v-if="requiredInspection" label="检测有效期（小时）"><NInputNumber v-model:value="validHours" :min="1" :max="720" :precision="0" /></NFormItem>
       <NFormItem label="客户"><NInput v-model:value="customer" maxlength="120" placeholder="可选" /></NFormItem><NFormItem label="备注"><NInput v-model:value="note" maxlength="1000" placeholder="可选" /></NFormItem>
       <NFormItem v-if="mode!=='quick' && selection!=='group'" label="交付内容"><NCheckbox v-model:checked="includeMaster">包含主邮箱资料</NCheckbox></NFormItem>
      </NForm><div class="row"><span class="muted">当前范围：{{ scopeDescription }}</span><NButton v-if="actualRange!=='folder'" text type="primary" @click="tabChanged('inventory')">选择库存</NButton><NButton type="primary" :loading="creating" @click="createOutbound"><Send :size="15" class="icon-before" />创建出库单</NButton></div>
     </div>
     <div class="list-toolbar"><NInput v-model:value="outboundSearch" clearable placeholder="订单号、客户或备注" @keyup.enter="reloadOutbounds(true)" /><NSelect v-model:value="outboundFilter" :options="[{label:'全部状态',value:''},...['reserved','sold','cancelled','partial_return','returned'].map(value=>({label:salesStatus(value),value}))]" @update:value="reloadOutbounds(true)" /><NButton @click="reloadOutbounds(true)">查询</NButton></div>
     <NDataTable :columns="outboundColumns" :data="outbounds" :loading="loading" :bordered="false" :row-key="(row:Outbound)=>row.id" :scroll-x="1170" /><NPagination v-model:page="outboundPage" :item-count="outboundTotal" :page-size="25" simple @update:page="reloadOutbounds()" />
    </NTabPane>
    <NTabPane name="import" display-directive="show:lazy"><template #tab><span class="sales-tab"><CloudUpload :size="16" />导入库存</span></template><div class="import-placeholder"><CloudUpload :size="28" /><h2>导入销售库存</h2><p class="muted">支持 TXT 分组以及 CSV、JSON 文件，先生成预览，再确认导入。</p><NButton type="primary" @click="showImport=true">打开导入窗口</NButton></div></NTabPane>
    <NTabPane name="inspection" display-directive="show:lazy"><template #tab><span class="sales-tab"><Sparkles :size="16" />检测任务</span></template>
     <div class="section-head"><div><h2>后台检测任务</h2><p class="muted">链接检测确认页面可访问；收信验证会发送测试邮件，需先配置发件邮箱。</p></div><NButton text type="primary" @click="tabChanged('settings')">配置发件邮箱与定时检测</NButton></div>
     <NForm label-placement="top" class="inspection-form form-grid" :disabled="inspectionBusy">
      <NFormItem label="检测类型"><NSelect v-model:value="inspectionMode" :options="[{label:'链接检测',value:'link'},{label:'收信验证（发送邮件）',value:'inbox'}]" /></NFormItem>
      <NFormItem label="检测范围"><NSelect v-model:value="inspectionScope" :options="[{label:'指定文件夹',value:'folder'},{label:`选中子邮箱 (${selectedChildren.length})`,value:'selected'},{label:`选中主邮箱 (${selectedMasters.length})`,value:'master'},{label:'全部库存',value:'all'}]" /></NFormItem>
      <NFormItem v-if="inspectionScope==='folder'" label="文件夹"><NSelect v-model:value="inspectionFolder" :options="folderOptions" /></NFormItem><NFormItem label="每组抽样"><NSelect v-model:value="inspectionSample" :options="[{label:'全部检测',value:0},{label:'每组 1 个',value:1},{label:'每组 3 个',value:3},{label:'每组 5 个',value:5}]" /></NFormItem>
      <NFormItem label="请求超时（秒）"><NInputNumber v-model:value="inspectionOptions.timeout" :min="1" :max="60" :precision="0" /></NFormItem><NFormItem label="并发数"><NInputNumber v-model:value="inspectionOptions.concurrency" :min="1" :max="10" :precision="0" /></NFormItem>
      <NFormItem v-if="inspectionMode==='inbox'" label="收信等待（秒）"><NInputNumber v-model:value="inspectionOptions.wait" :min="5" :max="300" :precision="0" /></NFormItem><NFormItem v-if="inspectionMode==='inbox'" label="发信间隔（毫秒）"><NInputNumber v-model:value="inspectionOptions.interval" :min="200" :max="10000" :precision="0" /></NFormItem>
     </NForm><div class="row inspection-actions"><NCheckbox v-model:checked="includeSold">包含预留和已售邮箱</NCheckbox><NButton v-if="inspectionScope==='selected' || inspectionScope==='master'" text @click="tabChanged('inventory')">选择库存</NButton><NButton type="primary" :loading="inspectionBusy" @click="createInspection"><Sparkles :size="15" class="icon-before" />{{ inspectionMode==='inbox'?'确认发信并提交':'提交链接检测' }}</NButton><NButton quaternary @click="reloadInspections">刷新任务</NButton></div>
     <NDataTable :columns="inspectionColumns" :data="inspections" :loading="loading" :bordered="false" :row-key="(row:Inspection)=>row.id" :scroll-x="1070" /><NPagination v-model:page="inspectionPage" :item-count="inspectionTotal" :page-size="25" simple @update:page="reloadInspections" />
    </NTabPane>
    <NTabPane name="settings" display-directive="show:lazy"><template #tab><span class="sales-tab"><Settings2 :size="16" />检测配置</span></template><SalesSettings /></NTabPane>
   </NTabs>
  </section>

  <NModal v-model:show="showGroupOutbound" preset="card" title="提交整组出库" class="sales-modal" style="width:min(560px,calc(100vw - 32px))" :mask-closable="!creating" :close-on-esc="!creating" :closable="!creating">
   <NAlert type="info" :show-icon="false">已选 {{ groupMasterIDs.length }} 个主邮箱，将其全部子邮箱整组出库，并包含主邮箱资料。</NAlert>
   <p class="muted">所选组内如有不可售或不符合检测要求的子邮箱，本次整组出库将无法提交。</p>
   <NForm label-placement="top" :disabled="creating">
    <NFormItem label="出库方式"><NSelect v-model:value="groupOutbound.mode" :options="[{label:'出售',value:'sell'},{label:'预留',value:'reserve'}]" /></NFormItem>
    <NFormItem label="客户"><NInput v-model:value="groupOutbound.customer" maxlength="120" placeholder="可选" /></NFormItem>
    <NFormItem label="备注"><NInput v-model:value="groupOutbound.note" maxlength="1000" placeholder="可选" /></NFormItem>
    <NFormItem label="检测要求"><NSelect v-model:value="groupOutbound.inspection" :options="[{label:'不限制检测结果',value:''},{label:'链接检测通过',value:'link'},{label:'收信验证通过',value:'inbox'}]" /></NFormItem>
    <NFormItem v-if="groupOutbound.inspection" label="检测有效期（小时）"><NInputNumber v-model:value="groupOutbound.validHours" :min="1" :max="720" :precision="0" /></NFormItem>
   </NForm>
   <p class="muted">{{ groupOutbound.mode==='sell' ? '确认后生成出库单，所选主邮箱及其全部子邮箱标记为已售。' : '确认后预留整组子邮箱，可在出库单中确认交付或取消预留。' }}</p>
   <template #footer><div class="modal-actions"><NButton :disabled="creating" @click="showGroupOutbound=false">取消</NButton><NButton type="primary" :loading="creating" @click="createGroupOutbound">{{ groupOutbound.mode==='sell' ? '确认出库' : '确认预留' }}</NButton></div></template>
  </NModal>
  <NModal v-model:show="showImport" preset="card" title="导入销售库存" class="sales-modal" style="width:min(760px,calc(100vw - 32px))" :mask-closable="!importBusy">
   <NForm label-placement="top" :disabled="importBusy"><NFormItem label="目标文件夹"><NSelect v-model:value="importFolder" :options="folderOptions" /></NFormItem><NFormItem label="TXT 内容"><NInput v-model:value="importText" type="textarea" :rows="7" placeholder="main@example.com----password...&#10;child@example.com----https://example.com/inbox" /></NFormItem></NForm>
   <label class="file-picker"><CloudUpload :size="18" />选择 TXT / CSV / JSON 文件<input type="file" accept=".txt,.csv,.json" multiple :disabled="importBusy" @change="chooseImportFiles" /></label><div v-if="importFiles.length" class="file-summary">已选 {{ importFiles.length }} 个文件：{{ importFiles.map(v=>v.name).join('、') }} <NButton text size="tiny" :disabled="importBusy" @click="importFiles=[]">清空</NButton></div>
   <div v-if="importPreview"><div class="import-report"><NTag :bordered="false">新增主邮箱 {{ importPreview.report.added_masters }}</NTag><NTag :bordered="false">新增子邮箱 {{ importPreview.report.added_children }}</NTag><NTag :bordered="false">补全 {{ importPreview.report.completed }} · 恢复归属 {{ importPreview.report.restored }}</NTag><NTag type="warning" :bordered="false">重复 {{ importPreview.report.duplicates }} · 冲突 {{ importPreview.report.conflicts }}</NTag><NTag type="error" :bordered="false">无效 {{ importPreview.report.invalid }}</NTag></div><p class="muted">预览有效至 {{ date(importPreview.expires_at) }}</p><div class="issue-list"><p v-for="(issue,index) in importPreview.report.issues" :key="index">{{ issue.source }} · {{ issue.email }}：{{ issue.reason }}</p></div></div>
   <template #footer><div class="modal-actions"><NButton :disabled="importBusy" @click="showImport=false">关闭</NButton><NButton secondary type="primary" :loading="importBusy" @click="previewImport">生成预览</NButton><NButton type="primary" :disabled="!importPreview" :loading="importBusy" @click="commitImport">确认导入</NButton></div></template>
  </NModal>
  <NModal v-model:show="showOutbound" preset="card" title="出库单详情" class="sales-modal" style="width:min(1120px,calc(100vw - 32px))">
   <NSpin :show="detailLoading"><template v-if="outboundDetail"><div class="section-head"><div><h2>{{ outboundDetail.order_no }}</h2><p class="muted">{{ salesStatus(outboundDetail.status) }} · {{ outboundDetail.customer || '未填写客户' }} · {{ date(outboundDetail.created_at) }}</p><p v-if="outboundDetail.note" class="muted">{{ outboundDetail.note }}</p></div><div class="row"><NCheckbox v-if="outboundDetail.downloadable && outboundDetail.mode!=='quick'" :checked="outboundIncludesMaster(outboundDetail)" :disabled="downloadLoading!==null" @update:checked="value=>outboundExportChoices[outboundDetail!.id]=value">包含主邮箱资料</NCheckbox><NButton v-if="outboundDetail.downloadable" :loading="downloadLoading===outboundDetail.id" @click="downloadOutbound(outboundDetail)">下载交付文件</NButton><NButton v-if="outboundDetail.status==='reserved'" type="primary" :loading="actionLoading===outboundDetail.id" @click="confirmDeliver(outboundDetail.id)">确认交付</NButton></div></div>
    <NDataTable :columns="outboundItemColumns" :data="outboundDetail.items" :row-key="(row:OutboundItem)=>row.id" :checked-row-keys="selectedReturnItems" :scroll-x="1000" :pagination="{pageSize:20}" @update:checked-row-keys="(keys:DataTableRowKey[])=>selectedReturnItems=keys.map(Number)" />
    <div v-if="outboundDetail.status==='reserved' || canReturn" class="action-panel"><h3>{{ canReturn?'退库':'取消预留' }}</h3><p v-if="canReturn" class="muted">选择退库的子邮箱并填写原因。退库后会停用原交付文件下载；整组退库后，如需重新销售，还需在库存中恢复主邮箱在库状态。</p><NForm label-placement="top"><NFormItem label="操作原因"><NInput v-model:value="actionReason" maxlength="500" placeholder="必填" /></NFormItem></NForm><div class="row"><NButton v-if="canReturn" size="small" @click="selectedReturnItems=outboundDetail.items.filter(v=>v.mailbox_id && !v.returned_at).map(v=>v.id)">选择全部未退库项</NButton><NButton type="warning" :loading="actionLoading===outboundDetail.id" @click="outboundAction=canReturn?'return':'cancel'; requestDetailAction()">{{ canReturn?`退库选中 ${selectedReturnItems.length} 个`:'取消此预留单' }}</NButton></div></div>
    <div class="history"><h3>操作记录</h3><div v-for="(event,index) in outboundDetail.history" :key="index" class="history-row"><span>{{ date(event.created_at) }}</span><strong>{{ event.action }}</strong><span>{{ event.actor }} · {{ event.count }} 个</span><span>{{ event.reason || '—' }}</span></div></div>
   </template><NEmpty v-else-if="!detailLoading" description="出库单详情加载失败" /></NSpin>
  </NModal>
  <NModal v-model:show="showInspection" preset="card" title="检测任务详情" class="sales-modal" style="width:min(1120px,calc(100vw - 32px))">
   <NSpin :show="inspectionDetailLoading"><template v-if="inspectionDetail"><div class="section-head"><div><h2>任务 #{{ inspectionDetail.task.id }} · {{ salesStatus(inspectionDetail.task.status) }}</h2><p class="muted">已处理 {{ inspectionDetail.task.processed }}/{{ inspectionDetail.task.total }} · 通过 {{ inspectionDetail.task.passed }} · 失败 {{ inspectionDetail.task.failed }} · 待确认 {{ inspectionDetail.task.pending }}</p></div><div class="row"><NButton @click="loadInspectionDetail()">刷新</NButton><NButton @click="downloadInspection(inspectionDetail.task.id)">下载报告</NButton><NButton v-if="['queued','running'].includes(inspectionDetail.task.status)" type="warning" :loading="actionLoading===-inspectionDetail.task.id" @click="stopInspection(inspectionDetail.task.id)">停止</NButton><NButton v-else :loading="actionLoading===-inspectionDetail.task.id" @click="retryInspection(inspectionDetail.task.id)">复检未通过项</NButton></div></div>
    <div v-if="inspectionDetail.retry_info" class="retry-summary">
     <NAlert :type="inspectionDetail.retry_info.skipped.length ? 'warning' : 'info'" :show-icon="false">来源任务 #{{ inspectionDetail.retry_info.source_id }}：原未通过 {{ inspectionDetail.retry_info.requested }} 项，本次复检 {{ inspectionDetail.retry_info.retried }} 项，跳过 {{ inspectionDetail.retry_info.skipped.length }} 项。</NAlert>
     <details v-if="inspectionDetail.retry_info.skipped.length" class="retry-skipped"><summary>查看跳过项目及原因（{{ inspectionDetail.retry_info.skipped.length }}）</summary><NDataTable :columns="retrySkippedColumns" :data="inspectionDetail.retry_info.skipped" :row-key="(row:InspectionRetrySkipped)=>row.email" :scroll-x="640" :pagination="{pageSize:5}" /></details>
    </div>
    <p v-if="inspectionDetail.task.mode==='inbox'" class="muted">复检沿用原测试邮件标识，不会重新发送测试邮件。</p><NSelect v-model:value="resultFilter" class="result-filter" :options="[{label:'全部结果',value:''},...['passed','failed','pending'].map(value=>({label:salesStatus(value),value}))]" @update:value="filterResults" /><NDataTable :columns="resultColumns" :data="inspectionDetail.results" :row-key="(row:InspectionResult)=>row.id" :scroll-x="1315" /><NPagination v-model:page="resultPage" :item-count="inspectionDetail.total" :page-size="25" simple @update:page="loadInspectionDetail()" />
   </template><NEmpty v-else-if="!inspectionDetailLoading" description="检测详情加载失败" /></NSpin>
  </NModal>
 </div>
</template>

<style scoped>
.retry-summary{margin:0 0 16px}.retry-skipped{margin-top:10px}.retry-skipped summary{cursor:pointer;color:var(--muted);font-size:12px;padding:8px 0}.retry-skipped :deep(.n-data-table){margin-top:8px}
.sales-page{width:100%;min-width:0;min-height:0;flex:1;display:flex;flex-direction:column}
.sales-title{margin-bottom:22px}.sales-title h1{font-size:25px;margin-bottom:6px}.sales-title-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.sales-error{margin-bottom:18px}
.sales-title,.sales-error{flex-shrink:0}
.sales-panel{min-width:0;min-height:0;flex:1;display:flex;flex-direction:column;overflow:hidden}
.sales-tabs{flex:1;min-width:0;min-height:0;overflow:hidden}
.sales-panel :deep(.n-tabs-nav){display:none}.sales-mobile-navigation{display:none}
.sales-panel :deep(.n-tab-pane){min-width:0;min-height:0;flex:1;overflow:auto;overscroll-behavior:contain;scrollbar-width:thin;padding:22px}
.sales-panel :deep(.sales-inventory-pane){display:flex;flex-direction:column;overflow:hidden;padding:0}
.sales-tab{display:flex;align-items:center;gap:7px;font-size:13px;font-weight:500;white-space:nowrap}.sales-panel :deep(.n-tabs-nav-scroll-content){border:0}
.overview-heading{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:20px}.overview-heading>div{flex:1}.overview-home-link{display:inline-flex;gap:5px;align-items:center;font-size:12px;min-height:44px}.overview-heading h2{font-size:17px;margin-bottom:6px}.overview-heading p{font-size:12px;margin:0}.updated-at{color:var(--muted);font-size:11px;white-space:nowrap}
.overview-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1.1fr);gap:18px}.overview-card{border:1px solid var(--border);border-radius:12px;padding:20px;min-width:0}.overview-card h3{font-size:14px;margin:0;font-weight:600}.overview-card .section-head{align-items:center;margin-bottom:16px;gap:8px}.overview-card .section-head>.muted{font-size:11px}.overview-card .section-head .n-button{font-size:12px}
.workflow-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}.workflow-card{display:flex;align-items:center;gap:11px;border:1px solid var(--border);background:var(--hover);border-radius:10px;padding:14px 12px;min-height:92px;text-align:left;color:var(--text);cursor:pointer;transition:border-color .15s,background .15s}.workflow-card:hover{border-color:#3478f680;background:var(--active)}.workflow-card>span:nth-child(2){min-width:0;display:grid;gap:5px}.workflow-card strong{font-size:13px;font-weight:550}.workflow-card small{font-size:11px;color:var(--muted);line-height:1.6}.workflow-icon{width:35px;height:35px;border-radius:9px;display:grid;place-items:center;background:#3478f615;color:var(--blue);flex-shrink:0}.workflow-icon.green{background:#2db58a15;color:#239f77}.workflow-icon.orange{background:#e9903215;color:#c07820}.workflow-icon.purple{background:#8a55d915;color:#9569db}.workflow-arrow{flex-shrink:0;color:var(--muted);margin-left:auto}
.recent-list{display:grid}.recent-order{display:flex;align-items:center;gap:12px;border:0;border-top:1px solid var(--border);background:transparent;min-width:0;padding:13px 0;color:var(--text);text-align:left;cursor:pointer;width:100%}.recent-order:hover{background:var(--hover)}.recent-order-icon{width:34px;height:38px;background:var(--hover);border-radius:8px;display:grid;place-items:center;flex-shrink:0;color:var(--muted)}.recent-order-info{display:grid;gap:5px;flex:1;min-width:0}.recent-order-info strong{font-size:12px;font-weight:550;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.recent-order-info small{font-size:11px;color:var(--muted);overflow-wrap:anywhere}.recent-order-result{display:grid;gap:4px;justify-items:end;flex-shrink:0}.recent-order-result small{font-size:11px;color:var(--muted)}.overview-empty{padding:28px 0}.section-head{display:flex;justify-content:space-between;align-items:flex-start;gap:16px;margin-bottom:16px}.section-head h2{margin-bottom:5px}.section-head p{margin:0;font-size:12px;overflow-wrap:anywhere}.row,:deep(.table-actions),.modal-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}:deep(.mail-cell){display:grid;gap:4px;min-width:0}:deep(.mail-cell small){color:var(--muted);overflow-wrap:anywhere}.mapping-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:14px;margin-bottom:20px}.mapping-card{border:1px solid var(--border);border-radius:12px;padding:18px;display:grid;gap:7px;color:var(--blue)}.mapping-card strong{font-size:26px;color:var(--text)}.mapping-card span,.mapping-card small{font-size:12px;color:var(--muted)}.mapping-card.warning{color:#c07820}.mapping-rows{border-top:1px solid var(--border);max-height:400px;overflow:auto}.mapping-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 4px;border-bottom:1px solid var(--border);font-size:12px;overflow-wrap:anywhere}.outbound-form{padding:18px;margin-bottom:20px;background:var(--hover)}.outbound-form .section-head{margin-bottom:12px}.form-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0 16px}.form-grid :deep(.n-input-number){width:100%}.list-toolbar{display:flex;gap:10px;margin:18px 0}.list-toolbar :deep(.n-input){max-width:300px}.list-toolbar :deep(.n-select){width:180px}.inspection-actions{margin-bottom:24px}.import-placeholder{border:1px dashed var(--border);border-radius:12px;display:grid;place-items:center;text-align:center;padding:60px 20px;gap:10px;color:var(--blue)}.import-placeholder h2{color:var(--text);margin:0}.import-placeholder p{max-width:480px;margin:0}.import-report{display:flex;gap:8px;flex-wrap:wrap;padding:12px 0}.modal-actions{justify-content:flex-end}.n-pagination{margin-top:18px;justify-content:flex-end}.action-panel{background:var(--hover);padding:18px;border-radius:12px;margin-top:20px}.action-panel h3,.history h3{font-size:14px}.action-panel p,.muted{font-size:12px}.history{margin-top:24px}.history-row{display:flex;gap:12px;flex-wrap:wrap;padding:10px 0;border-bottom:1px solid var(--border);font-size:12px}.history-row span{color:var(--muted)}.file-summary{font-size:12px;overflow-wrap:anywhere;margin:12px 0}.issue-list{max-height:180px;overflow:auto;font-size:12px;color:var(--muted)}.issue-list p{margin-bottom:6px}.result-filter{max-width:200px;margin-bottom:16px}.sales-modal{max-height:90dvh;overflow:auto}

.mapping-heading{font-size:13px;margin:18px 0 12px}.mapping-toolbar{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin:18px 0 12px}.mapping-toolbar .mapping-heading{margin:0}.mapping-toolbar :deep(.n-select){width:210px}.mapping-error{margin-bottom:18px}.mapping-empty{padding:24px 0}

@media(max-width:1200px){.overview-grid{grid-template-columns:minmax(0,1.4fr) minmax(0,1fr)}.workflow-grid{grid-template-columns:1fr}.workflow-card{min-height:72px}.form-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.sales-title{align-items:flex-start;flex-wrap:wrap}}
@media(max-width:767px){.sales-title{gap:16px}.sales-title h1{font-size:23px}.sales-title-actions{width:100%;gap:7px}.sales-title-actions>.n-button{font-size:12px;padding:0 12px;flex:1}.sales-title-actions>.n-button:first-child{flex:0;padding:0 12px;min-width:44px}.sales-title-actions>.n-button:first-child .icon-before{margin:0}.refresh-label{display:none}.sales-panel{padding:0 14px 16px;border-radius:12px}.sales-mobile-navigation{display:grid;gap:8px;margin:0 -14px;padding:14px;border-bottom:1px solid var(--border);background:var(--hover)}.sales-mobile-navigation>label{font-size:11px;color:var(--muted)}.sales-panel :deep(.n-tabs-tab){min-height:54px}.sales-panel :deep(.n-tabs-tab-pad){width:22px}.sales-tab{font-size:12px}.sales-tab>svg{display:none}.sales-panel :deep(.n-tab-pane){padding-top:18px}.overview-heading{align-items:flex-start;flex-direction:column;gap:8px}.overview-grid{display:flex;flex-direction:column;gap:14px}.overview-card{padding:16px}.quick-workflows{order:0}.recent-orders{order:2}.workflow-grid{grid-template-columns:1fr}.workflow-card{min-height:74px;padding:12px}.workflow-card small{font-size:11px}.section-head{flex-direction:column}.overview-card .section-head{flex-direction:row}.form-grid{grid-template-columns:1fr}.list-toolbar{flex-wrap:wrap}.list-toolbar :deep(.n-input){max-width:none;width:100%}.mapping-grid{grid-template-columns:1fr;gap:10px}.mapping-card{padding:14px}.mapping-row{align-items:flex-start;flex-wrap:wrap}.outbound-form{padding:14px}.recent-order-icon{display:none}}
@media(min-width:768px){.import-placeholder{min-height:100%;align-content:center}.sales-tabs :deep(.mapping-rows){max-height:none}}
@media(max-width:767px){
 .sales-page{display:block;flex:none;padding-bottom:12px}
 .sales-panel{display:block;padding:0}
 .sales-tabs{overflow:visible}
 .sales-panel :deep(.n-tab-pane){flex:none;overflow:visible;padding:16px 14px}
 .sales-panel :deep(.sales-inventory-pane){display:block;overflow:visible;padding:0}
 .sales-mobile-navigation{margin:0;padding:14px;border-radius:12px 12px 0 0}
 .import-placeholder{min-height:360px;align-content:center}
}
</style>
