<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { Copy, ExternalLink, Folder, FolderOpen, Layers3, Plus } from 'lucide-vue-next'
import { NAlert, NButton, NCheckbox, NDataTable, NForm, NFormItem, NInput, NModal, NPagination, NSelect, NTag, useDialog, useMessage, type DataTableColumns, type DataTableRowKey } from 'naive-ui'
import { api, date, errorText, query } from '../api'
import { downloadSalesFile, salesStatus, type SalesChild, type SalesFolder, type SalesMaster, type SalesPage } from '../sales'

const props = defineProps<{ folders: SalesFolder[]; folderSummary: { total_masters: number; total_children: number; unclassified: number; unclassified_children: number }; selectedMasters: number[]; selectedChildren: number[]; revision: number }>()
const emit = defineEmits<{ 'update:selectedMasters': [number[]]; 'update:selectedChildren': [number[]]; 'submit-outbound': [number[]]; refresh: [] }>()
const message = useMessage(), dialog = useDialog()
const loading = ref(false), saving = ref(false), exporting = ref(false)
const masters = ref<SalesMaster[]>([]), children = ref<SalesChild[]>([])
const masterPage = ref(1), childPage = ref(1), masterTotal = ref(0), childTotal = ref(0)
const search = ref(''), folder = ref<number | null>(null), childFlag = ref(''), masterFlag = ref('')
const inventoryKind = ref<'master' | 'child'>('master')
const pageSize = 25
const folderOptions = computed(() => [{ label: '未分类', value: 0 }, ...props.folders.map(v => ({ label: v.name, value: v.id }))])
const currentGroup = computed(() => folder.value === null ? '全部库存' : folder.value === 0 ? '未分类' : props.folders.find(v => v.id === folder.value)?.name || '分组')
const groupItems = computed(() => [
 { id: null, name: '全部库存', masters: props.folderSummary.total_masters, children: props.folderSummary.total_children },
 { id: 0, name: '未分类', masters: props.folderSummary.unclassified, children: props.folderSummary.unclassified_children },
 ...props.folders,
])
function selectFolder(id: number | null) { if (folder.value === id) return; folder.value = id; filterChanged() }
function textCell(text: string, tag = 'span') { return h(tag, { title: text }, text || '—') }
function linkDetails(row: SalesChild) { return row.link_mode === 'source' ? `${row.link_platform || '来源平台'} · ${row.link_expires_at ? date(row.link_expires_at) : '长期有效'}` : '使用自定义链接' }
const showMaster = ref(false), masterID = ref(0), masterRaw = ref(''), masterNote = ref(''), masterFolder = ref(0)
const masterStatus = ref('available'), originalMasterStatus = ref('available'), masterReason = ref('')
const showChild = ref(false), childID = ref(0), childEmail = ref(''), childParent = ref<number | null>(null), childLink = ref(''), childNote = ref(''), childState = ref('available')
const childLinkMode = ref<'source' | 'custom'>('custom'), originalLinkMode = ref<'source' | 'custom'>('custom'), childDetails = ref<SalesChild | null>(null)
const originalLink = ref(''), originalFlags = ref({ abnormal: false, disabled: false, after_sale: false })
const childFlags = ref({ abnormal: false, disabled: false, after_sale: false }), flagReason = ref('')
const parentOptions = ref<{ label: string; value: number }[]>([]), parentLoading = ref(false)
const showFolder = ref(false), folderName = ref(''), folderEditID = ref(0)
const exportFormat = ref('txt'), batchBusy = ref(false), showMove = ref(false), moveFolder = ref(0), moveIDs = ref<number[]>([])
const exportMaster = ref(true)
const showDelete = ref(false), deleteIDs = ref<number[]>([]), deleteConfirmed = ref(false)
const deletePreview = ref<{ masters: number; children: number; reserved: number; sold: number; version: string } | null>(null)
type LinkSyncPreview = { ready: number; skipped: number; failed: number; version: string; items: { id: number; email: string; status: string; reason: string; link?: string }[] }
const showLinkSync = ref(false), linkSyncBusy = ref(false), linkSyncPreview = ref<LinkSyncPreview | null>(null), linkSyncIDs = ref<number[]>([])
async function previewLinkSync(refresh = false) {
 if (!refresh) linkSyncIDs.value = [...props.selectedChildren]
 linkSyncBusy.value = true
 try { linkSyncPreview.value = await api<LinkSyncPreview>('admin/mail/children/link-sync/preview', 'POST', { ids: linkSyncIDs.value }); showLinkSync.value = true } catch (e) { message.error(errorText(e)) } finally { linkSyncBusy.value = false }
}
async function commitLinkSync() {
 if (!linkSyncPreview.value) return
 linkSyncBusy.value = true
 try { const result = await api<{ updated: number; skipped: number; failed: number }>('admin/mail/children/link-sync', 'POST', { ids: linkSyncIDs.value, version: linkSyncPreview.value.version }); showLinkSync.value = false; message.success(`已关联 ${result.updated} 个链接，跳过 ${result.skipped} 个，待处理 ${result.failed} 个`); emit('refresh'); await load() } catch (e) { message.error(errorText(e)) } finally { linkSyncBusy.value = false }
}
async function copyLink(row: SalesChild) { try { await navigator.clipboard.writeText(row.link || ''); message.success('链接已复制') } catch { message.error('复制失败，请从编辑窗口复制链接') } }
let loadSequence = 0, parentSequence = 0

const masterColumns: DataTableColumns<SalesMaster> = [
 { type: 'selection', width: 44 },
 { title: '主邮箱 / 所属分组', key: 'email', width: 320, render: row => h('div', { class: 'mail-cell identity-cell' }, [textCell(row.email, 'strong'), textCell(row.folder || '未分类', 'small')]) },
 { title: '子邮箱总数', key: 'children', width: 110, render: row => h('span', { class: 'stock-number' }, row.children.toLocaleString()) },
 { title: '可售', key: 'sellable', width: 100, render: row => h(NTag, { size: 'small', type: row.sellable ? 'success' : 'default', bordered: false }, { default: () => row.sellable.toLocaleString() }) },
 { title: '预留 / 已售', key: 'sold', width: 120, render: row => h('span', { class: 'stock-number' }, `${row.reserved} / ${row.sold}`) },
 { title: '资料 / 状态', key: 'incomplete', width: 110, render: row => h(NTag, { size: 'small', type: row.incomplete ? 'warning' : 'default', bordered: false }, { default: () => row.incomplete ? '待补全' : salesStatus(row.status) }) },
 { title: '备注', key: 'note', minWidth: 180, ellipsis: { tooltip: true } },
 { title: '操作', key: 'action', width: 96, fixed: 'right', render: row => h(NButton, { size: 'small', quaternary: true, onClick: () => editMaster(row) }, { default: () => '编辑' }) },
]
const childColumns: DataTableColumns<SalesChild> = [
 { type: 'selection', width: 44 },
 { title: '子邮箱 / 所属主邮箱', key: 'email', width: 320, render: row => h('div', { class: 'mail-cell identity-cell' }, [textCell(row.email, 'strong'), textCell(row.parent_email || '孤立子邮箱', 'small')]) },
 { title: '取信链接', key: 'link', width: 310, render: row => h('div', { class: 'mail-cell link-cell' }, [
  h('div', { class: 'link-cell-head' }, [h(NTag, { size: 'small', type: row.link_available ? 'success' : 'warning', bordered: false }, { default: () => row.link_mode === 'source' ? '系统关联' : row.link ? '自定义' : '待补全' }),
   row.link && row.link_available ? h('div', { class: 'link-actions' }, [h(NButton, { size: 'tiny', quaternary: true, title: '复制链接', 'aria-label': '复制链接', onClick: () => copyLink(row) }, { default: () => h(Copy, { size: 14 }) }), h(NButton, { size: 'tiny', quaternary: true, tag: 'a', href: row.link, title: '打开链接', 'aria-label': '打开链接', target: '_blank', rel: 'noopener noreferrer' }, { default: () => h(ExternalLink, { size: 14 }) })]) : null]),
  textCell(linkDetails(row), 'small'),
  h('small', { class: { 'link-warning': !!row.link_reason }, title: row.link_reason || (row.link_local_only ? '仅本机可访问' : '链接可用') }, row.link_reason || (row.link_local_only ? '仅本机可访问' : '链接可用')),
 ]) },
 { title: '检测', key: 'link_check', width: 132, render: row => h('div', { class: 'mail-cell' }, [textCell(`链接 ${salesStatus(row.link_check)}`), textCell(`收信 ${salesStatus(row.inbox_check)}`, 'small')]) },
 { title: '状态', key: 'status', width: 100, render: row => h(NTag, { size: 'small', type: row.sellable ? 'success' : row.abnormal || row.disabled || row.after_sale ? 'warning' : 'default', bordered: false }, { default: () => row.abnormal ? '异常' : row.disabled ? '禁用' : row.after_sale ? '售后' : salesStatus(row.status) }) },
 { title: '备注', key: 'note', minWidth: 160, ellipsis: { tooltip: true } },
 { title: '操作', key: 'action', width: 96, fixed: 'right', render: row => h(NButton, { size: 'small', quaternary: true, onClick: () => editChild(row) }, { default: () => row.link ? '编辑' : '补全链接' }) },
]

async function load() {
 const sequence = ++loadSequence
 loading.value = true
 try {
  const common = { q: search.value.trim(), folder: folder.value }
  const [m, c] = await Promise.all([
   api<SalesPage<SalesMaster>>('admin/mail/masters?' + query({ ...common, inventory: masterFlag.value, page: masterPage.value, limit: pageSize })),
   api<SalesPage<SalesChild>>('admin/mail/children?' + query({ ...common, flag: childFlag.value, page: childPage.value, limit: pageSize })),
  ])
  if (sequence !== loadSequence) return
  masters.value = m.items; children.value = c.items; masterTotal.value = m.total; childTotal.value = c.total
 } catch (e) { if (sequence === loadSequence) message.error(errorText(e)) } finally { if (sequence === loadSequence) loading.value = false }
}
function filterChanged() { masterPage.value = 1; childPage.value = 1; load() }
function selectMasters(keys: DataTableRowKey[]) { emit('update:selectedMasters', keys.map(Number)) }
function selectChildren(keys: DataTableRowKey[]) { emit('update:selectedChildren', keys.map(Number)) }
async function editMaster(row?: SalesMaster) {
 masterID.value = row?.id || 0; masterRaw.value = ''; masterNote.value = row?.note || ''; masterFolder.value = row?.folder_id || 0
 masterStatus.value = row?.status || 'available'; originalMasterStatus.value = masterStatus.value; masterReason.value = ''
 if (row) {
  try { const data = await api<{ raw: string }>(`admin/mail/masters/${row.id}`); masterRaw.value = data.raw } catch (e) { message.error(errorText(e)); return }
 }
 showMaster.value = true
}
async function saveMaster() {
 if (!masterRaw.value.trim()) return message.warning('请填写主邮箱资料')
 if (masterStatus.value !== originalMasterStatus.value && !masterReason.value.trim()) return message.warning('请填写状态修改原因')
 saving.value = true
 try {
  await api(masterID.value ? `admin/mail/masters/${masterID.value}` : 'admin/mail/masters', masterID.value ? 'PUT' : 'POST', { raw: masterRaw.value.trim(), note: masterNote.value.trim(), folder_id: masterFolder.value })
  if (masterID.value && masterStatus.value !== originalMasterStatus.value) await api('admin/mail/status', 'POST', { ids: [masterID.value], status: masterStatus.value, reason: masterReason.value.trim() })
  showMaster.value = false; masterRaw.value = ''; message.success('主邮箱已保存'); emit('refresh'); await load()
 } catch (e) { message.error(errorText(e)) } finally { saving.value = false }
}
async function searchParents(text = '') {
 const sequence = ++parentSequence; parentLoading.value = true
 try { const data = await api<SalesPage<SalesMaster>>('admin/mail/masters?' + query({ q: text, limit: 50 })); if (sequence === parentSequence) parentOptions.value = data.items.map(v => ({ label: v.email, value: v.id })) } catch (e) { if (sequence === parentSequence) message.error(errorText(e)) } finally { if (sequence === parentSequence) parentLoading.value = false }
}
function editChild(row?: SalesChild) {
 childDetails.value = row || null; childLinkMode.value = row?.link_mode || 'custom'; originalLinkMode.value = childLinkMode.value
 childID.value = row?.id || 0; childEmail.value = row?.email || ''; childParent.value = row?.parent_id || null; childLink.value = row?.link || ''; originalLink.value = childLink.value; childNote.value = row?.note || ''; childState.value = row?.status || 'available'
 const flags = { abnormal: row?.abnormal || false, disabled: row?.disabled || false, after_sale: row?.after_sale || false }
 originalFlags.value = { ...flags }; childFlags.value = { ...flags }; flagReason.value = ''; showChild.value = true
 if (!row) searchParents()
}
async function saveChild() {
 const flags = (Object.keys(childFlags.value) as (keyof typeof childFlags.value)[]).filter(flag => childFlags.value[flag] !== originalFlags.value[flag])
 if (flags.length && !flagReason.value.trim()) return message.warning('请填写变更标记的原因')
 if (!childID.value && (!childEmail.value.trim() || !childParent.value || !childLink.value.trim())) return message.warning('请填写子邮箱、主邮箱和接码链接')
 saving.value = true
 try {
  const data = childID.value ? { note: childNote.value.trim(), ...(childLinkMode.value !== originalLinkMode.value ? { link_mode: childLinkMode.value } : {}), ...(childLinkMode.value === 'custom' && (childLink.value !== originalLink.value || childLinkMode.value !== originalLinkMode.value) ? { link: childLink.value.trim() } : {}) } : { email: childEmail.value.trim(), parent_id: childParent.value, link: childLink.value.trim(), note: childNote.value.trim() }
  await api(childID.value ? `admin/mail/children/${childID.value}` : 'admin/mail/children', childID.value ? 'PUT' : 'POST', data)
  for (const flag of flags) {
   await api('admin/mail/children/flags', 'POST', { ids: [childID.value], flag, value: childFlags.value[flag], reason: flagReason.value.trim() })
   originalFlags.value[flag] = childFlags.value[flag]
  }
  showChild.value = false; message.success('子邮箱已保存'); emit('refresh'); await load()
 } catch (e) { message.error(errorText(e)) } finally { saving.value = false }
}
function editFolder(rename = false) {
 folderEditID.value = rename ? folder.value || 0 : 0
 folderName.value = rename ? props.folders.find(v => v.id === folderEditID.value)?.name || '' : ''
 showFolder.value = true
}
async function saveFolder() {
 if (!folderName.value.trim()) return message.warning('请填写分组名称')
 saving.value = true
 try { await api(folderEditID.value ? `admin/mail/folders/${folderEditID.value}` : 'admin/mail/folders', folderEditID.value ? 'PUT' : 'POST', { name: folderName.value.trim() }); showFolder.value = false; folderName.value = ''; message.success(folderEditID.value ? '分组已重命名' : '分组已创建'); emit('refresh') } catch (e) { message.error(errorText(e)) } finally { saving.value = false }
}
function deleteFolder() {
 if (!folder.value) return
 const id = folder.value, name = props.folders.find(v => v.id === id)?.name || ''
 dialog.warning({ title: '删除分组', content: `删除“${name}”后，分组内的主邮箱将移到未分类。`, positiveText: '删除并移到未分类', negativeText: '取消', onPositiveClick: async () => {
  try { await api(`admin/mail/folders/${id}`, 'DELETE'); folder.value = null; emit('refresh'); await load(); message.success('分组已删除') } catch (e) { message.error(errorText(e)); return false }
 } })
}
async function exportSelected(kind: 'master' | 'child') {
 const ids = kind === 'master' ? props.selectedMasters : props.selectedChildren
 if (!ids.length) return message.warning('请先选择邮箱')
 if (ids.length > 500) return message.warning('一次最多导出 500 条所选记录')
 exporting.value = true
 try { downloadSalesFile(await api<{ filename: string; content: string }>('admin/mail/export', 'POST', { kind, ids, format: exportFormat.value, include_master: exportMaster.value })) } catch (e) { message.error(errorText(e)) } finally { exporting.value = false }
}
function openMove() {
 if (!props.selectedMasters.length || props.selectedMasters.length > 500) return message.warning('请选择 1–500 个主邮箱')
 moveIDs.value = [...props.selectedMasters]; moveFolder.value = folder.value || 0; showMove.value = true
}
async function moveMasters() {
 batchBusy.value = true
 try { await api('admin/mail/masters/batch', 'POST', { ids: moveIDs.value, action: 'move', folder_id: moveFolder.value }); showMove.value = false; message.success(`已移动 ${moveIDs.value.length} 个主邮箱`); emit('refresh'); await load() } catch (e) { message.error(errorText(e)) } finally { batchBusy.value = false }
}
async function previewDelete(refresh = false) {
 const ids = refresh ? deleteIDs.value : props.selectedMasters
 if (!ids.length || ids.length > 500) return message.warning('请选择 1–500 个主邮箱')
 batchBusy.value = true; deleteConfirmed.value = false
 try { const data = await api<{ masters: number; children: number; reserved: number; sold: number; version: string }>('admin/mail/masters/batch', 'POST', { ids, action: 'preview_delete' }); deleteIDs.value = [...ids]; deletePreview.value = data; showDelete.value = true } catch (e) { message.error(errorText(e)) } finally { batchBusy.value = false }
}
async function deleteMasters() {
 if (!deletePreview.value || !deleteConfirmed.value) return
 batchBusy.value = true
 try {
  await api('admin/mail/masters/batch', 'POST', { ids: deleteIDs.value, action: 'delete', version: deletePreview.value.version })
  showDelete.value = false; emit('update:selectedMasters', props.selectedMasters.filter(id => !deleteIDs.value.includes(id))); emit('update:selectedChildren', []); masterPage.value = 1; childPage.value = 1; message.success('所选主邮箱及其子邮箱已删除'); emit('refresh'); await load()
 } catch (e) { deleteConfirmed.value = false; message.error(errorText(e)) } finally { batchBusy.value = false }
}
watch(() => props.revision, load)
watch(() => props.folders, () => { if (folder.value && !props.folders.some(v => v.id === folder.value)) selectFolder(null) })
watch(showMaster, value => { if (!value) masterRaw.value = '' })
onMounted(load)
</script>

<template>
 <div class="inventory">
  <aside class="group-sidebar inventory-groups" aria-label="库存分组">
   <div class="group-sidebar-header"><strong>库存分组</strong><NButton quaternary aria-label="新建分组" title="新建分组" @click="editFolder()"><Plus :size="16" /></NButton></div>
   <div class="group-list">
    <button v-for="(group,index) in groupItems" :key="group.id === null ? 'all' : group.id" class="group-nav-item" :class="{active:folder===group.id}" :aria-pressed="folder===group.id" :title="`${group.name} · ${group.masters} 个主邮箱 / ${group.children} 个子邮箱`" @click="selectFolder(group.id)">
     <span class="group-color" :class="group.id===null?'all':group.id===0?'unassigned':`tone-${index%4}`"><Layers3 v-if="group.id===null" :size="14" /><FolderOpen v-else-if="folder===group.id" :size="14" /><Folder v-else :size="14" /></span>
     <span class="group-nav-name">{{ group.name }}</span><span class="group-count">{{ (inventoryKind==='child'?group.children:group.masters).toLocaleString() }}</span>
    </button>
   </div>
   <button class="group-add" @click="editFolder()"><Plus :size="14" />新建分组</button>
   <p class="group-help">子邮箱随所属主邮箱分组<br>数量包含在库、预留与已售</p>
  </aside>
  <div class="inventory-content">
  <div class="inventory-heading"><div><h2 :title="currentGroup">{{ currentGroup }}</h2><span class="muted">{{ inventoryKind==='child'?'子邮箱':'主邮箱' }}库存</span></div><div v-if="folder" class="row group-actions"><NButton size="small" quaternary @click="editFolder(true)">重命名分组</NButton><NButton size="small" quaternary type="error" @click="deleteFolder">删除分组</NButton></div></div>
  <div class="toolbar"><NInput v-model:value="search" clearable placeholder="搜索邮箱或备注" @keyup.enter="filterChanged" /><NButton :loading="loading" @click="filterChanged">查询</NButton><span class="inventory-hint muted">映射自动关联取信链接，第三方链接可自定义填写。</span></div>
  <div class="batch-toolbar"><span class="muted">导出格式</span><NSelect v-model:value="exportFormat" :options="[{label:'TXT 资料与链接',value:'txt'},{label:'仅邮箱地址',value:'emails'},{label:'CSV',value:'csv'},{label:'JSON',value:'json'}]" /><NCheckbox v-if="inventoryKind==='master' && exportFormat==='txt'" v-model:checked="exportMaster" :disabled="exporting">包含主邮箱资料</NCheckbox><NButton :disabled="!selectedMasters.length" :loading="batchBusy" @click="openMove">移动选中主邮箱</NButton><NButton :loading="linkSyncBusy" @click="previewLinkSync()">一键补齐链接</NButton><NButton type="error" secondary :disabled="!selectedMasters.length" :loading="batchBusy" @click="previewDelete()">删除预览</NButton></div>
  <div class="inventory-switch" role="group" aria-label="库存类型"><button :aria-pressed="inventoryKind==='master'" :class="{active:inventoryKind==='master'}" @click="inventoryKind='master'">主邮箱 <span>{{ masterTotal.toLocaleString() }}</span></button><button :aria-pressed="inventoryKind==='child'" :class="{active:inventoryKind==='child'}" @click="inventoryKind='child'">子邮箱 <span>{{ childTotal.toLocaleString() }}</span></button></div>
  <div class="inventory-grid">
   <section v-show="inventoryKind==='master'">
    <div class="section-head"><NSelect v-model:value="masterFlag" class="filter" aria-label="主邮箱库存筛选" :options="[{label:'全部主邮箱',value:''},{label:'有可售库存',value:'sellable'},{label:'资料待补全',value:'incomplete'},{label:'部分已售',value:'partial'},{label:'全部已售',value:'sold'}]" @update:value="filterChanged" /><div class="row"><NButton size="small" type="primary" :disabled="!selectedMasters.length" @click="emit('submit-outbound', [...selectedMasters])">提交出库</NButton><NButton size="small" :disabled="!selectedMasters.length" :loading="exporting" @click="exportSelected('master')">导出选中</NButton><NButton size="small" type="primary" secondary @click="editMaster()">新增主邮箱</NButton></div></div>
    <div class="inventory-table-host"><NDataTable size="small" flex-height table-layout="fixed" children-key="tree_children" :columns="masterColumns" :data="masters" :loading="loading" :row-key="(row:SalesMaster)=>row.id" :checked-row-keys="selectedMasters" :scroll-x="1180" @update:checked-row-keys="selectMasters" /></div>
    <div class="inventory-pagination"><span>共 {{ masterTotal.toLocaleString() }} 个主邮箱 · 每页 {{ pageSize }} 条</span><NPagination v-model:page="masterPage" :item-count="masterTotal" :page-size="pageSize" simple @update:page="load" /></div>
   </section>
   <section v-show="inventoryKind==='child'">
    <div class="section-head"><NSelect v-model:value="childFlag" class="filter" aria-label="子邮箱库存筛选" :options="[{label:'全部子邮箱',value:''},{label:'可售库存',value:'sellable'},{label:'在库但不可售',value:'unsellable'},{label:'异常',value:'abnormal'},{label:'禁用',value:'disabled'},{label:'售后',value:'after_sale'},{label:'孤立子邮箱',value:'orphan'}]" @update:value="filterChanged" /><div class="row"><NButton size="small" :disabled="!selectedChildren.length" :loading="exporting" @click="exportSelected('child')">导出选中</NButton><NButton size="small" type="primary" secondary @click="editChild()">新增子邮箱</NButton></div></div>
    <div class="inventory-table-host"><NDataTable size="small" flex-height table-layout="fixed" :columns="childColumns" :data="children" :loading="loading" :row-key="(row:SalesChild)=>row.id" :checked-row-keys="selectedChildren" :scroll-x="1162" @update:checked-row-keys="selectChildren" /></div>
    <div class="inventory-pagination"><span>共 {{ childTotal.toLocaleString() }} 个子邮箱 · 每页 {{ pageSize }} 条</span><NPagination v-model:page="childPage" :item-count="childTotal" :page-size="pageSize" simple @update:page="load" /></div>
   </section>
  </div>
  <div class="selection-summary">已选主邮箱 {{ selectedMasters.length }} 个，子邮箱 {{ selectedChildren.length }} 个 <NButton size="tiny" text @click="emit('update:selectedMasters', []); emit('update:selectedChildren', [])">清空选择</NButton></div>
  </div>
  <NModal v-model:show="showMaster" preset="card" :title="masterID?'编辑主邮箱':'新增主邮箱'" style="width:min(680px,calc(100vw - 32px))" :mask-closable="!saving">
   <NForm label-placement="top"><NFormItem label="主邮箱原始资料"><NInput v-model:value="masterRaw" type="textarea" :rows="4" placeholder="邮箱----密码----Client ID----Refresh Token" /></NFormItem><p class="muted">首字段为主邮箱地址，编辑时保持地址不变。完整资料用于整组出库。</p><NFormItem label="分组"><NSelect v-model:value="masterFolder" :options="folderOptions" /></NFormItem><NFormItem label="备注"><NInput v-model:value="masterNote" maxlength="1000" type="textarea" /></NFormItem><template v-if="masterID"><NFormItem label="主邮箱状态"><NSelect v-model:value="masterStatus" :options="[{label:'在库',value:'available'},{label:'已售',value:'sold'}]" /></NFormItem><NFormItem v-if="masterStatus!==originalMasterStatus" label="状态修改原因"><NInput v-model:value="masterReason" maxlength="500" /></NFormItem><p class="muted">恢复主邮箱在库状态后，已退库的子邮箱才能重新可售；将主邮箱改为已售会同时出售其在库子邮箱并生成出库记录。</p></template></NForm>
   <template #footer><div class="row end"><NButton :disabled="saving" @click="showMaster=false">取消</NButton><NButton type="primary" :loading="saving" @click="saveMaster">保存</NButton></div></template>
  </NModal>
  <NModal v-model:show="showChild" preset="card" :title="childID?'编辑子邮箱':'新增子邮箱'" style="width:min(680px,calc(100vw - 32px))" :mask-closable="!saving">
   <NForm label-placement="top"><NFormItem label="子邮箱"><NInput v-model:value="childEmail" :disabled="!!childID" /></NFormItem><NFormItem v-if="!childID" label="所属主邮箱"><NSelect v-model:value="childParent" filterable remote :loading="parentLoading" :options="parentOptions" placeholder="输入主邮箱搜索" @search="searchParents" /></NFormItem><NFormItem v-if="childID && childDetails?.source_mapped" label="链接模式"><NSelect v-model:value="childLinkMode" :disabled="childState!=='available'" :options="[{label:'系统关联（自动生成）',value:'source'},{label:'自定义链接',value:'custom'}]" /></NFormItem><template v-if="childLinkMode==='source'"><NFormItem label="取信链接"><NInput :value="originalLinkMode==='source' ? childDetails?.link || '' : '保存后按原来源自动生成'" readonly /></NFormItem><p class="muted">{{ childDetails?.link_reason || `${childDetails?.link_platform || '沿用来源平台'} · ${childDetails?.link_expires_at ? date(childDetails.link_expires_at) : '沿用来源有效期'}` }}</p><NAlert v-if="childDetails?.link_local_only" type="info" :show-icon="false">当前链接仅本机可访问，对外交付前请在设置中配置公开根地址。</NAlert></template><NFormItem v-else label="接码链接"><NInput v-model:value="childLink" :disabled="childState!=='available'" placeholder="https://…" /></NFormItem><p v-if="childState!=='available'" class="muted">预留或已售邮箱的接码链接无法修改。</p><NFormItem label="备注"><NInput v-model:value="childNote" type="textarea" maxlength="1000" /></NFormItem><template v-if="childID"><NFormItem label="库存标记"><div class="row"><NCheckbox v-model:checked="childFlags.abnormal">异常</NCheckbox><NCheckbox v-model:checked="childFlags.disabled">禁用</NCheckbox><NCheckbox v-model:checked="childFlags.after_sale">售后</NCheckbox></div></NFormItem><NFormItem label="标记变更原因"><NInput v-model:value="flagReason" maxlength="500" placeholder="变更标记时必填" /></NFormItem></template></NForm>
   <template #footer><div class="row end"><NButton :disabled="saving" @click="showChild=false">取消</NButton><NButton type="primary" :loading="saving" @click="saveChild">保存</NButton></div></template>
  </NModal>
  <NModal v-model:show="showLinkSync" preset="card" title="预览补齐取信链接" style="width:min(680px,calc(100vw - 32px))" :mask-closable="!linkSyncBusy"><template v-if="linkSyncPreview"><p>{{ linkSyncIDs.length ? `范围：选中 ${linkSyncIDs.length} 个子邮箱` : '范围：全部已映射子邮箱' }}。仅补齐仍在库的空链接。</p><div class="row"><NTag type="success">可关联 {{ linkSyncPreview.ready }}</NTag><NTag>跳过 {{ linkSyncPreview.skipped }}</NTag><NTag type="warning">待处理 {{ linkSyncPreview.failed }}</NTag></div><p class="muted">已填写、已关联、预留和已售记录保持不变。取消或退库不会撤销客户已经获得的原取信链接。</p><div class="sync-issues"><p v-for="item in linkSyncPreview.items.filter(v=>v.status==='failed').slice(0,200)" :key="item.id">{{ item.email }}：{{ item.reason }}</p></div></template><template #footer><div class="row end"><NButton :disabled="linkSyncBusy" @click="showLinkSync=false">取消</NButton><NButton :loading="linkSyncBusy" @click="previewLinkSync(true)">重新预览</NButton><NButton type="primary" :disabled="!linkSyncPreview?.ready" :loading="linkSyncBusy" @click="commitLinkSync">确认补齐</NButton></div></template></NModal>
  <NModal v-model:show="showFolder" preset="card" :title="folderEditID?'重命名分组':'新建分组'" style="width:min(420px,calc(100vw - 32px))"><NInput v-model:value="folderName" maxlength="50" placeholder="分组名称" @keyup.enter="saveFolder" /><template #footer><div class="row end"><NButton type="primary" :loading="saving" @click="saveFolder">保存</NButton></div></template></NModal>
  <NModal v-model:show="showMove" preset="card" title="移动主邮箱" style="width:min(460px,calc(100vw - 32px))" :mask-closable="!batchBusy"><p>将选中的 {{ moveIDs.length }} 个主邮箱及其子邮箱移到目标分组。</p><NSelect v-model:value="moveFolder" :options="folderOptions" /><template #footer><div class="row end"><NButton :disabled="batchBusy" @click="showMove=false">取消</NButton><NButton type="primary" :loading="batchBusy" @click="moveMasters">确认移动</NButton></div></template></NModal>
  <NModal v-model:show="showDelete" preset="card" title="预览删除范围" style="width:min(520px,calc(100vw - 32px))" :mask-closable="!batchBusy"><template v-if="deletePreview"><p>本次会永久删除 {{ deletePreview.masters }} 个主邮箱及 {{ deletePreview.children }} 个子邮箱。</p><NAlert v-if="deletePreview.reserved || deletePreview.sold" type="warning" :show-icon="false">范围内有 {{ deletePreview.reserved }} 个预留、{{ deletePreview.sold }} 个已售子邮箱。请先取消预留或完成退库，再重新预览。</NAlert><NAlert v-else type="warning" :show-icon="false">删除后无法从库存恢复。运行中的检测任务涉及这些邮箱时，需要先停止任务。</NAlert><NCheckbox v-model:checked="deleteConfirmed" :disabled="!!(deletePreview.reserved || deletePreview.sold)" style="margin-top:16px">确认永久删除以上邮箱</NCheckbox></template><template #footer><div class="row end"><NButton :disabled="batchBusy" @click="showDelete=false">取消</NButton><NButton :loading="batchBusy" @click="previewDelete(true)">重新预览</NButton><NButton type="error" :disabled="!deleteConfirmed || !!deletePreview?.reserved || !!deletePreview?.sold" :loading="batchBusy" @click="deleteMasters">确认删除</NButton></div></template></NModal>
 </div>
</template>

<style scoped>
.inventory{display:flex;flex:1;min-width:0;min-height:0;overflow:hidden}
.inventory-groups{width:208px;flex:none;overflow:auto;scrollbar-width:thin}.inventory-groups .group-nav-item{padding:0 10px}.group-help{margin:16px 8px 0;font-size:11px;line-height:1.9;color:var(--muted)}
.inventory-content{flex:1;min-width:0;min-height:0;display:flex;flex-direction:column;padding:20px 22px 0}
.inventory-heading{display:flex;justify-content:space-between;align-items:center;gap:12px;min-height:40px;margin-bottom:14px}.inventory-heading>div:first-child{display:flex;gap:12px;align-items:baseline;min-width:0}.inventory-heading h2{font-size:17px;margin:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.inventory-heading .muted{flex:none;font-size:12px}
.toolbar,.batch-toolbar,.row,.section-head,.selection-summary{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.toolbar{padding:0;border:0}.toolbar :deep(.n-input){max-width:330px;flex:1;min-width:160px}.inventory-hint{font-size:11px;margin-left:8px}
.batch-toolbar{padding:12px 0;border-bottom:1px solid var(--border)}.batch-toolbar :deep(.n-select){width:175px}.batch-toolbar>.muted{font-size:12px}
.inventory-switch{display:flex;gap:6px;padding:4px;background:var(--hover);border:1px solid var(--border);border-radius:10px;width:fit-content;margin-top:14px;flex:none}.inventory-switch button{display:flex;align-items:center;gap:10px;min-height:40px;padding:0 18px;border:0;border-radius:7px;background:transparent;color:var(--muted);cursor:pointer;font-size:13px}.inventory-switch button.active{background:var(--surface);color:var(--blue);box-shadow:0 1px 4px #17244012}.inventory-switch span{font-size:11px;font-variant-numeric:tabular-nums}
.inventory-grid,.inventory-grid>section{display:flex;flex-direction:column;flex:1;min-height:0;min-width:0}.section-head{justify-content:space-between;margin:12px 0}.section-head .filter{width:210px}.inventory-table-host{display:flex;flex:1;min-height:100px;min-width:0}.inventory-table-host>.n-data-table{flex:1;min-width:0;min-height:0}
.inventory :deep(.n-data-table-th){height:44px;font-size:12px;background:var(--hover)}.inventory :deep(.n-data-table-td){height:84px;padding:10px 12px;font-size:12px}.inventory :deep(.n-data-table-td .n-tag){max-width:100%}.inventory :deep(.n-tag__content){overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.inventory :deep(.mail-cell){display:flex;flex-direction:column;justify-content:center;gap:4px;min-width:0;height:62px;overflow:hidden}.inventory :deep(.mail-cell strong),.inventory :deep(.mail-cell small),.inventory :deep(.mail-cell>span){display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;line-height:18px}.inventory :deep(.mail-cell strong){font-size:13px;font-weight:550}.inventory :deep(.mail-cell small){font-size:11px;color:var(--muted)}.inventory :deep(.identity-cell){max-width:296px}.inventory :deep(.link-cell){gap:1px}.inventory :deep(.link-cell-head){display:flex;align-items:center;justify-content:space-between;gap:8px;min-height:26px}.inventory :deep(.link-actions){display:flex;gap:2px}.inventory :deep(.link-actions .n-button){width:28px;height:28px;min-height:28px;padding:0}.inventory :deep(.mail-cell .link-warning){color:#c07820}.inventory :deep(.stock-number){font-variant-numeric:tabular-nums}
.inventory-pagination{display:flex;align-items:center;justify-content:space-between;gap:10px;min-height:58px;flex:none;font-size:11px;color:var(--muted)}.inventory-pagination .n-pagination{margin:0}.selection-summary{padding:8px 12px;min-height:44px;border-top:1px solid var(--border);background:var(--active);color:var(--blue);font-size:12px;margin:0 -22px}.selection-summary .n-button{margin-left:auto}.inventory-heading,.toolbar,.batch-toolbar,.section-head,.selection-summary{flex-shrink:0}.end{justify-content:flex-end}.muted{font-size:12px;line-height:1.7}.sync-issues{max-height:280px;overflow:auto;overflow-wrap:anywhere;font-size:12px}
@media(min-width:768px) and (max-height:800px){.inventory-content{padding-top:14px}.inventory-heading{margin-bottom:8px}.batch-toolbar{padding:8px 0}.inventory-switch{margin-top:8px}.section-head{margin:8px 0}.inventory-pagination{min-height:50px}}
@media(max-width:1100px){.inventory-groups{width:180px}.inventory-content{padding:16px 16px 0}.inventory-hint{display:none}.selection-summary{margin:0 -16px}.group-actions{gap:0}.group-actions .n-button{padding:0 6px}}
@media(max-width:767px){.inventory{display:block;overflow:visible}.inventory-groups{width:100%;overflow:visible}.inventory-groups .group-list{display:flex;overflow-x:auto}.inventory-groups .group-help{display:none}.inventory-content{padding:16px 14px 0}.inventory-heading{align-items:flex-start;flex-wrap:wrap;margin-bottom:12px}.inventory-heading h2{max-width:230px}.inventory-heading>.group-actions{width:100%;justify-content:flex-end}.inventory-switch{width:100%;margin-top:12px}.inventory-switch button{flex:1;justify-content:center;padding:0 8px;min-height:44px}.toolbar :deep(.n-input){max-width:none;min-width:0}.batch-toolbar{gap:8px}.batch-toolbar>.n-button{font-size:12px;padding:0 10px}.batch-toolbar :deep(.n-select){width:calc(100% - 65px)}.section-head{align-items:center;gap:10px}.section-head .filter{width:100%}.section-head>.row{width:100%;justify-content:flex-end}.inventory-table-host{flex:none;height:520px}.inventory-pagination{flex-wrap:wrap;padding:8px 0;min-height:66px;justify-content:flex-end}.inventory-pagination>span{margin-right:auto}.selection-summary{margin:0 -14px;padding:8px 12px;font-size:11px;gap:4px}.inventory :deep(.link-actions .n-button){min-height:36px;width:36px}.inventory :deep(.link-cell){height:64px}.inventory :deep(.link-cell-head){min-height:28px}
}
</style>
