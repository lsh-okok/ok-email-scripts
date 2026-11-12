<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NDataTable, NEmpty, NForm, NFormItem, NInputNumber, NModal, NPagination, NSelect, NSpin, NTabPane, NTabs, NTag, useDialog, useMessage, type DataTableColumns } from 'naive-ui'
import { RefreshCw, Sparkles, Settings2 } from 'lucide-vue-next'
import { api, date, errorText, query } from '../api'
import { downloadVerificationFile, verificationStatus, type VerificationPage } from '../verification'
import VerificationSettings from '../components/VerificationSettings.vue'
type Inspection = { id: number; mode: string; scope: string; folder_id: number; status: string; total: number; processed: number; passed: number; failed: number; pending: number; actor: string; created_at: string; finished_at: string | null }
type InspectionResult = { id: number; email: string; parent_email: string; link?: string; result: string; status_code: number; message: string; checked_at: string; processed: boolean; sender_email?: string; phase: string }
type InspectionRetrySkipped = { email: string; parent_email: string; reason: string }
type InspectionRetryInfo = { source_id: number; requested: number; retried: number; skipped: InspectionRetrySkipped[] }
type InspectionDetail = { task: Inspection; results: InspectionResult[]; total: number; page: number; limit: number; stages: Record<string, number>; retry_info?: InspectionRetryInfo | null }

type Mapping = { accounts: { source_id: string; email: string; mailbox_id: number; mapped: boolean; conflict?: string }[]; summary: { active_accounts: number; links: number; mapped_accounts: number; mapped_links: number; conflicts: number } }
type MappingResult = { created_accounts: number; created_links: number; needs_detection_links: number; conflicts: { email: string; reason: string }[] }
const message = useMessage(), dialog = useDialog(), route = useRoute(), router = useRouter()
const active = computed(() => route.query.tab === 'settings' ? 'settings' : 'inspection')
const loading = ref(false), error = ref(''), syncing = ref(false), mapping = ref<Mapping | null>(null), syncConflicts = ref<MappingResult['conflicts']>([])
const inspections = ref<Inspection[]>([]), inspectionPage = ref(1), inspectionTotal = ref(0)
const inspectionMode = ref('link'), inspectionScope = ref('all'), inspectionSample = ref(1), selectedMasters = ref<number[]>([]), inspectionBusy = ref(false), actionLoading = ref<number | null>(null)
const masterOptions = computed(() => (mapping.value?.accounts || []).filter(v => v.mapped && !v.conflict).map(v => ({ label: v.email, value: v.mailbox_id })))
const inspectionOptions = reactive({ timeout: 10, concurrency: 5, wait: 60, interval: 1000 })
const showInspection = ref(false), inspectionDetail = ref<InspectionDetail | null>(null), inspectionDetailLoading = ref(false), resultPage = ref(1), resultFilter = ref('')
let inspectionSequence = 0, inspectionDetailSequence = 0, mappingSequence = 0, loadSequence = 0, pollTimer: ReturnType<typeof setInterval> | undefined, polling = false
let syncRequest: Promise<void> | undefined, disposed = false
const inspectionColumns: DataTableColumns<Inspection> = [
 { title: '任务', key: 'id', width: 70, render: row => `#${row.id}` },
 { title: '类型', key: 'mode', width: 100, render: row => row.mode === 'link' ? '链接检测' : '收信验证' },
 { title: '进度', key: 'progress', width: 110, render: row => `${row.processed}/${row.total}` },
 { title: '结果', key: 'passed', minWidth: 160, render: row => `${row.passed} 通过 · ${row.failed} 失败 · ${row.pending} 待确认` },
 { title: '状态', key: 'status', width: 100, render: row => h(NTag, { size: 'small', bordered: false }, { default: () => verificationStatus(row.status) }) },
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
 { title: '结果', key: 'result', width: 100, render: row => verificationStatus(row.result) },
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


async function loadMapping() {
 const serial = ++mappingSequence
 const data = await api<Mapping>('admin/mail/mapping/preview?page=1&limit=1')
 if (serial === mappingSequence) { mapping.value = data; const available = new Set(masterOptions.value.map(v => v.value)); selectedMasters.value = selectedMasters.value.filter(id => available.has(id)) }
}
async function loadInspections() {
 const serial = ++inspectionSequence
 const data = await api<VerificationPage<Inspection>>('admin/mail/inspections?' + query({ page: inspectionPage.value, limit: 25 }))
 if (serial === inspectionSequence) { inspections.value = data.items; inspectionTotal.value = data.total }
}
async function load() {
 const serial = ++loadSequence
 loading.value = true; error.value = ''
 const results = await Promise.allSettled([loadMapping(), loadInspections()])
 if (serial !== loadSequence) return
 error.value = results.flatMap((r, i) => r.status === 'rejected' ? [`${['检测范围','检测任务'][i]}：${errorText(r.reason)}`] : []).join('；')
 loading.value = false
}
async function synchronize(notify = false): Promise<void> {
 if (syncRequest) return syncRequest
 syncing.value = true
 syncRequest = (async () => {
  const result = await api<MappingResult>('admin/mail/mapping', 'POST', { include_links: true })
  syncConflicts.value = result.conflicts
  await loadMapping()
  if (notify) message.success(`检测范围已同步，新增 ${result.created_accounts} 个主邮箱、${result.created_links} 个子邮箱`)
 })()
 try { await syncRequest } finally { syncing.value = false; syncRequest = undefined }
}
async function syncClicked() { try { await synchronize(true) } catch (e) { message.error(errorText(e)) } }
async function reloadInspections() { try { await loadInspections() } catch (e) { message.error(errorText(e)) } }
function tabChanged(value: string) { router.replace({ path: '/verification', query: value === 'settings' ? { tab: 'settings' } : {} }) }
async function createInspection() {
 if (inspectionBusy.value || syncing.value) return
 if (inspectionScope.value === 'master' && !selectedMasters.value.length) return message.warning('请选择需要验证的主邮箱')
 if (inspectionMode.value === 'inbox') {
  dialog.warning({ title: '确认发送测试邮件', content: '收信验证将使用已配置的发件邮箱，向所选范围中的子邮箱发送测试邮件，再通过取信链接查找本次邮件标识。', positiveText: '确认并发送', negativeText: '取消', onPositiveClick: () => submitInspection(true) })
 } else await submitInspection(false)
}
async function submitInspection(confirmSend: boolean) {
 inspectionBusy.value = true
 try {
  await synchronize()
  if (inspectionScope.value === 'master' && !selectedMasters.value.length) throw new Error('所选主邮箱已停用或删除，请重新选择')
  const result = await api<{ id: number }>('admin/mail/inspections', 'POST', { mode: inspectionMode.value, scope: inspectionScope.value, master_ids: inspectionScope.value === 'master' ? selectedMasters.value : [], sample: inspectionSample.value, include_sold: false, confirm_send: confirmSend, options: inspectionOptions })
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
 try { downloadVerificationFile(await api<{ filename: string; content: string }>(`admin/mail/inspections/${id}/export`)) } catch (e) { message.error(errorText(e)) }
}

async function poll() {
 if (polling || document.hidden || !inspections.value.some(v => ['queued', 'running'].includes(v.status)) && !showInspection.value) return
 polling = true
 try { await loadInspections(); if (showInspection.value && inspectionDetail.value) await loadInspectionDetail(inspectionDetail.value.task.id, true) } catch { /* Refresh displays persistent errors. */ } finally { polling = false }
}
watch(showInspection, value => { if (!value) { inspectionDetailSequence++; inspectionDetail.value = null; inspectionDetailLoading.value = false } })
onMounted(async () => { try { await synchronize() } catch (e) { if (!disposed) message.error(errorText(e)) }; if (disposed) return; await load(); if (!disposed) pollTimer = setInterval(poll, 5000) })
onUnmounted(() => { disposed = true; inspectionSequence++; inspectionDetailSequence++; mappingSequence++; loadSequence++; if (pollTimer) clearInterval(pollTimer) })
</script>
<template>
 <div class="verification-page">
  <div class="page-title"><div><h1>邮箱验证</h1><p>检查子邮箱取信链接，并通过测试邮件验证实际收信。</p></div><NButton quaternary :loading="loading" @click="load"><RefreshCw :size="16" class="icon-before" />刷新数据</NButton></div>
  <NAlert v-if="error" type="error" :show-icon="false" class="verification-alert">{{ error }} <NButton text type="error" :loading="loading" @click="load">重新加载</NButton></NAlert>
  <section class="surface verification-scope">
   <div><h2>检测范围</h2><p class="muted">使用邮箱管理中的启用主邮箱及其子邮箱链接；新增后可同步，提交检测时也会自动同步。</p><div v-if="mapping" class="scope-counts"><span>主邮箱 <strong>{{ mapping.summary.active_accounts }}</strong></span><span>子邮箱链接 <strong>{{ mapping.summary.links }}</strong></span><span>已同步 <strong>{{ mapping.summary.mapped_links }}</strong></span><NTag v-if="mapping.summary.conflicts" type="warning" :bordered="false">{{ mapping.summary.conflicts }} 项冲突</NTag></div></div>
   <NButton secondary :loading="syncing" :disabled="inspectionBusy" @click="syncClicked">同步邮箱与链接</NButton>
  </section>
  <NAlert v-if="syncConflicts.length" type="warning" class="verification-alert" :show-icon="false"><details><summary>{{ syncConflicts.length }} 个邮箱重复或归属冲突，已跳过；展开查看</summary><p v-for="(item,index) in syncConflicts" :key="index">{{ item.email }}：{{ item.reason.replaceAll('销售库存','检测范围').replaceAll('库存','检测范围') }}</p></details></NAlert>
  <section class="surface verification-panel">
   <NTabs type="line" :animated="false" :value="active" @update:value="tabChanged">
    <NTabPane name="inspection" display-directive="show:lazy"><template #tab><span class="verification-tab"><Sparkles :size="16" />检测任务</span></template>
     <div class="section-head"><div><h2>验证邮箱</h2><p class="muted">链接检测会确认取信是否正常；收信验证会发送测试邮件，需先配置发件邮箱。</p></div><NButton text type="primary" @click="tabChanged('settings')">发件邮箱与定时检测</NButton></div>
     <NForm label-placement="top" class="form-grid" :disabled="inspectionBusy || syncing">
      <NFormItem label="检测类型"><NSelect v-model:value="inspectionMode" :options="[{label:'链接检测',value:'link'},{label:'收信验证（发送邮件）',value:'inbox'}]" /></NFormItem>
      <NFormItem label="检测范围"><NSelect v-model:value="inspectionScope" :options="[{label:'全部启用邮箱',value:'all'},{label:'指定主邮箱',value:'master'}]" /></NFormItem>
      <NFormItem v-if="inspectionScope==='master'" label="主邮箱" class="master-select"><NSelect v-model:value="selectedMasters" multiple filterable clearable :options="masterOptions" placeholder="搜索并选择主邮箱" /></NFormItem>
      <NFormItem label="每个主邮箱抽样"><NSelect v-model:value="inspectionSample" :options="[{label:'全部子邮箱',value:0},{label:'每组 1 个',value:1},{label:'每组 3 个',value:3},{label:'每组 5 个',value:5}]" /></NFormItem>
      <NFormItem label="请求超时（秒）"><NInputNumber v-model:value="inspectionOptions.timeout" :min="1" :max="60" :precision="0" /></NFormItem><NFormItem label="并发数"><NInputNumber v-model:value="inspectionOptions.concurrency" :min="1" :max="10" :precision="0" /></NFormItem>
      <NFormItem v-if="inspectionMode==='inbox'" label="收信等待（秒）"><NInputNumber v-model:value="inspectionOptions.wait" :min="5" :max="300" :precision="0" /></NFormItem><NFormItem v-if="inspectionMode==='inbox'" label="发信间隔（毫秒）"><NInputNumber v-model:value="inspectionOptions.interval" :min="200" :max="10000" :precision="0" /></NFormItem>
     </NForm>
     <div class="row inspection-actions"><NButton type="primary" :loading="inspectionBusy" :disabled="syncing" @click="createInspection"><Sparkles :size="15" class="icon-before" />{{ inspectionMode==='inbox'?'确认发信并提交':'提交链接检测' }}</NButton><NButton quaternary @click="reloadInspections">刷新任务</NButton><span class="muted">停用、过期或已删除来源的链接会自动排除。</span></div>
     <NDataTable :columns="inspectionColumns" :data="inspections" :loading="loading" :bordered="false" :row-key="(row:Inspection)=>row.id" :scroll-x="1070" /><NPagination v-model:page="inspectionPage" :item-count="inspectionTotal" :page-size="25" simple @update:page="reloadInspections" />
    </NTabPane>
    <NTabPane name="settings" display-directive="show:lazy"><template #tab><span class="verification-tab"><Settings2 :size="16" />检测配置</span></template><VerificationSettings :synchronize="synchronize" /></NTabPane>
   </NTabs>
  </section>
  <NModal v-model:show="showInspection" preset="card" title="检测任务详情" class="verification-modal" style="width:min(1120px,calc(100vw - 32px))">
   <NSpin :show="inspectionDetailLoading"><template v-if="inspectionDetail"><div class="section-head"><div><h2>任务 #{{ inspectionDetail.task.id }} · {{ verificationStatus(inspectionDetail.task.status) }}</h2><p class="muted">已处理 {{ inspectionDetail.task.processed }}/{{ inspectionDetail.task.total }} · 通过 {{ inspectionDetail.task.passed }} · 失败 {{ inspectionDetail.task.failed }} · 待确认 {{ inspectionDetail.task.pending }}</p></div><div class="row"><NButton @click="loadInspectionDetail()">刷新</NButton><NButton @click="downloadInspection(inspectionDetail.task.id)">下载报告</NButton><NButton v-if="['queued','running'].includes(inspectionDetail.task.status)" type="warning" :loading="actionLoading===-inspectionDetail.task.id" @click="stopInspection(inspectionDetail.task.id)">停止</NButton><NButton v-else :loading="actionLoading===-inspectionDetail.task.id" @click="retryInspection(inspectionDetail.task.id)">复检未通过项</NButton></div></div>
    <div v-if="inspectionDetail.retry_info" class="retry-summary">
     <NAlert :type="inspectionDetail.retry_info.skipped.length ? 'warning' : 'info'" :show-icon="false">来源任务 #{{ inspectionDetail.retry_info.source_id }}：原未通过 {{ inspectionDetail.retry_info.requested }} 项，本次复检 {{ inspectionDetail.retry_info.retried }} 项，跳过 {{ inspectionDetail.retry_info.skipped.length }} 项。</NAlert>
     <details v-if="inspectionDetail.retry_info.skipped.length" class="retry-skipped"><summary>查看跳过项目及原因（{{ inspectionDetail.retry_info.skipped.length }}）</summary><NDataTable :columns="retrySkippedColumns" :data="inspectionDetail.retry_info.skipped" :row-key="(row:InspectionRetrySkipped)=>row.email" :scroll-x="640" :pagination="{pageSize:5}" /></details>
    </div>
    <p v-if="inspectionDetail.task.mode==='inbox'" class="muted">复检沿用原测试邮件标识，不会重新发送测试邮件。</p><NSelect v-model:value="resultFilter" class="result-filter" :options="[{label:'全部结果',value:''},...['passed','failed','pending'].map(value=>({label:verificationStatus(value),value}))]" @update:value="filterResults" /><NDataTable :columns="resultColumns" :data="inspectionDetail.results" :row-key="(row:InspectionResult)=>row.id" :scroll-x="1315" /><NPagination v-model:page="resultPage" :item-count="inspectionDetail.total" :page-size="25" simple @update:page="loadInspectionDetail()" />
   </template><NEmpty v-else-if="!inspectionDetailLoading" description="检测详情加载失败" /></NSpin>
  </NModal>
 </div>
</template>
<style scoped>
.verification-page{min-width:0}.verification-page h1{font-size:25px}.verification-alert{margin-bottom:16px}.verification-scope{display:flex;justify-content:space-between;align-items:center;gap:20px;padding:20px;margin-bottom:18px}.verification-scope h2{margin:0;font-size:16px}.verification-scope p{font-size:12px;line-height:1.7;margin:8px 0 12px}.scope-counts{display:flex;gap:20px;flex-wrap:wrap;font-size:12px;color:var(--muted)}.scope-counts strong{font-size:17px;margin-left:5px;color:var(--text)}.verification-panel{padding:4px 22px 22px}.verification-tab{display:flex;gap:7px;align-items:center}.section-head{display:flex;justify-content:space-between;gap:16px;align-items:flex-start;margin:14px 0 16px}.section-head h2{font-size:18px;margin:0 0 8px}.section-head p{font-size:12px;line-height:1.7;margin:0}.form-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0 16px}.form-grid :deep(.n-input-number){width:100%}.master-select{grid-column:span 2}.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.inspection-actions{margin-bottom:22px}.inspection-actions>.muted{font-size:12px}.retry-summary{margin:0 0 16px}.retry-skipped{margin-top:10px}.retry-skipped summary{cursor:pointer;color:var(--muted);font-size:12px;padding:8px 0}.retry-skipped :deep(.n-data-table){margin-top:8px}.result-filter{width:170px;margin-bottom:14px}.verification-panel :deep(.n-pagination){margin-top:16px}@media(max-width:1000px){.form-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:600px){.verification-scope,.section-head{flex-direction:column;align-items:stretch}.verification-scope{padding:16px}.verification-panel{padding:4px 14px 16px}.form-grid{grid-template-columns:1fr}.master-select{grid-column:auto}.scope-counts{gap:12px}.page-title{gap:8px}}
</style>
