<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, reactive, ref } from 'vue'
import { NAlert, NButton, NCheckbox, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, NSwitch, NTag, useDialog, useMessage, type DataTableColumns } from 'naive-ui'
import { api, date, errorText } from '../api'
import SMTPPresets from './SMTPPresets.vue'

type SMTP = { configured: boolean; host: string; port: number; from: string; security: string; password_set: boolean }
type Sender = { id: number; host: string; port: number; from: string; security: string; enabled: boolean; password_set: boolean; last_used_at?: string }
type Schedule = { enabled: boolean; run_at: string; scope: string; folder_id: number; sample: number; include_sold: boolean; options: { timeout: number; concurrency: number; wait: number; interval: number }; timezone: string; next_run_at?: string; last_run_at?: string; last_run_task_id?: number; last_run_status: string; last_error: string }
const props = defineProps<{ synchronize: () => Promise<void> }>()
const message = useMessage(), dialog = useDialog()
const busy = ref(false), savingSMTP = ref(false), savingSender = ref(false), savingSchedule = ref(false), running = ref(false)
const loaded = ref(false), error = ref(''), removingSender = ref(0)
const ready = computed(() => loaded.value && !busy.value && !error.value)
const mutating = computed(() => savingSMTP.value || savingSender.value || savingSchedule.value || running.value || !!removingSender.value)
const canAct = computed(() => ready.value && !mutating.value)
let loadSequence = 0, disposed = false
const smtp = reactive<SMTP & { password: string; clear_password: boolean }>({ configured: false, host: '', port: 465, from: '', security: 'ssl', password_set: false, password: '', clear_password: false })
const senders = ref<Sender[]>([])
const folderOptions = ref<{ label: string; value: number }[]>([{ label: '未分组', value: 0 }])
const showSender = ref(false), senderID = ref(0)
const sender = reactive({ host: '', port: 465, from: '', security: 'ssl', password: '', enabled: true })
const schedule = reactive<Schedule>({ enabled: false, run_at: '09:00', scope: 'all', folder_id: 0, sample: 1, include_sold: false, options: { timeout: 10, concurrency: 5, wait: 60, interval: 1000 }, timezone: '', last_run_status: '', last_error: '' })
const confirmedSchedule = ref(false)
const securityOptions = [{ label: 'SSL / TLS', value: 'ssl' }, { label: 'STARTTLS', value: 'starttls' }]
const canSaveSchedule = computed(() => canAct.value && (!schedule.enabled || confirmedSchedule.value))
const scheduleScopeLabel = computed(() => schedule.scope === 'folder' ? `检测分组：${folderOptions.value.find(folder => folder.value === schedule.folder_id)?.label || `分组 #${schedule.folder_id}`}` : '全部启用主邮箱下的子邮箱')
const senderColumns: DataTableColumns<Sender> = [
  { title: '发件邮箱', key: 'from', minWidth: 200 },
  { title: '服务器', key: 'host', minWidth: 180, render: row => `${row.host}:${row.port}` },
  { title: '状态', key: 'enabled', width: 90, render: row => h(NTag, { size: 'small', type: row.enabled ? 'success' : 'default', bordered: false }, { default: () => row.enabled ? '启用' : '停用' }) },
  { title: '最近使用', key: 'last_used_at', width: 180, render: row => date(row.last_used_at) },
  { title: '操作', key: 'actions', width: 150, render: row => h('div', { class: 'actions' }, [h(NButton, { size: 'small', quaternary: true, disabled: !canAct.value, onClick: () => editSender(row) }, { default: () => '编辑' }), h(NButton, { size: 'small', quaternary: true, type: 'error', disabled: !canAct.value, loading: removingSender.value === row.id, onClick: () => removeSender(row) }, { default: () => '删除' })]) },
]
async function load() {
  if (disposed || mutating.value) return
  const serial = ++loadSequence
  busy.value = true; error.value = ''
  try {
    const [s, pool, plan, folders] = await Promise.all([
      api<SMTP>('admin/mail/settings'), api<{ items: Sender[] }>('admin/mail/senders'),
      api<Schedule>('admin/mail/inspection-schedule'), api<{ items: { id: number; name: string }[] }>('admin/mail/folders'),
    ])
    if (disposed || serial !== loadSequence) return
    Object.assign(smtp, s, { password: '', clear_password: false }); senders.value = pool.items
    Object.assign(schedule, plan, { include_sold: false }); confirmedSchedule.value = false
    folderOptions.value = [{ label: '未分组', value: 0 }, ...folders.items.map(folder => ({ label: folder.name, value: folder.id }))]
    loaded.value = true
  } catch (e) { if (!disposed && serial === loadSequence) error.value = errorText(e) }
  finally { if (!disposed && serial === loadSequence) busy.value = false }
}
async function saveSMTP() {
  if (!canAct.value) return
  savingSMTP.value = true
  try {
    const result = await api<SMTP>('admin/mail/settings', 'PUT', { host: smtp.host.trim(), port: smtp.port, from: smtp.from.trim(), security: smtp.security, password: smtp.password, clear_password: smtp.clear_password })
    Object.assign(smtp, result, { password: '', clear_password: false }); message.success('默认 SMTP 配置已保存')
  } catch (e) { message.error(errorText(e)) } finally { savingSMTP.value = false }
}
function editSender(row?: Sender) {
  if (!canAct.value) return
  senderID.value = row?.id || 0
  Object.assign(sender, row ? { host: row.host, port: row.port, from: row.from, security: row.security, password: '', enabled: row.enabled } : { host: '', port: 465, from: '', security: 'ssl', password: '', enabled: true })
  showSender.value = true
}
async function saveSender() {
  if (!canAct.value) return
  savingSender.value = true
  try {
    await api(senderID.value ? `admin/mail/senders/${senderID.value}` : 'admin/mail/senders', senderID.value ? 'PUT' : 'POST', { ...sender, host: sender.host.trim(), from: sender.from.trim() })
    showSender.value = false; sender.password = ''; senders.value = (await api<{ items: Sender[] }>('admin/mail/senders')).items; message.success('发件账号已保存')
  } catch (e) { message.error(errorText(e)) } finally { savingSender.value = false }
}
function removeSender(row: Sender) {
  if (!canAct.value) return
  dialog.warning({ title: '删除发件账号', content: `删除 ${row.from}？后续检测将不再使用此账号。`, positiveText: '删除', negativeText: '取消', onPositiveClick: async () => {
    if (!canAct.value) return false
    removingSender.value = row.id
    try { await api(`admin/mail/senders/${row.id}`, 'DELETE'); senders.value = senders.value.filter(s => s.id !== row.id); message.success('发件账号已删除') } catch (e) { message.error(errorText(e)); return false }
    finally { removingSender.value = 0 }
  } })
}
async function saveSchedule() {
  if (!canSaveSchedule.value) return
  savingSchedule.value = true
  try {
    await props.synchronize()
    const value = await api<Schedule>('admin/mail/inspection-schedule', 'PUT', { enabled: schedule.enabled, run_at: schedule.run_at, scope: schedule.scope, folder_id: schedule.folder_id, sample: schedule.sample, include_sold: false, options: schedule.options })
    Object.assign(schedule, value); confirmedSchedule.value = false; message.success('定时检测已保存')
  } catch (e) { message.error(errorText(e)) } finally { savingSchedule.value = false }
}
function runNow() {
  if (!canAct.value) return
  dialog.warning({ title: '立即执行收信检测', content: '使用已保存的范围和发件池向子邮箱发送测试邮件，并检查收件结果。确认立即执行？', positiveText: '确认发送并检测', negativeText: '取消', onPositiveClick: async () => {
    if (!canAct.value) return false
    running.value = true
    try { await props.synchronize(); Object.assign(schedule, await api<Schedule>('admin/mail/inspection-schedule/run', 'POST')); message.success('检测任务已创建，可在检测记录中查看') } catch (e) { message.error(errorText(e)) } finally { running.value = false }
  } })
}
onMounted(load)
onUnmounted(() => { disposed = true; loadSequence++; loaded.value = false })
</script>

<template>
 <div class="verification-settings">
  <div class="section-head"><div><h2>检测配置</h2><p class="muted">配置发件账号与每日收信检测；计划执行时会自动同步邮箱与链接。</p></div><NButton quaternary :loading="busy" :disabled="mutating" @click="load">刷新配置</NButton></div>
  <NAlert v-if="error" type="error" :show-icon="false" style="margin:12px 0" role="alert">检测配置加载失败：{{ error }}。请重新加载后再操作。<NButton text type="error" :loading="busy" @click="load">重新加载配置</NButton></NAlert>
  <section class="settings-box">
   <div class="section-head"><div><h3>SMTP 发件池</h3><p class="muted">检测邮件轮换使用启用的发件账号。授权码加密保存，编辑时留空可保留。</p></div><NButton secondary type="primary" :disabled="!canAct" @click="editSender()">新增发件账号</NButton></div>
   <NDataTable :columns="senderColumns" :data="senders" :loading="busy" :bordered="false" :row-key="(row:Sender)=>row.id" :scroll-x="800" />
  </section>
  <section class="settings-box">
   <h3>默认 SMTP 配置</h3><p class="muted">首次设置时可使用默认账号；添加发件池后将优先使用池中的启用账号。</p>
   <SMTPPresets :host="smtp.host" :port="smtp.port" :security="smtp.security" @apply="connection => Object.assign(smtp, connection)" />
   <NForm label-placement="top" class="settings-grid" :disabled="!canAct">
    <NFormItem label="SMTP 服务器"><NInput v-model:value="smtp.host" placeholder="smtp.example.com" /></NFormItem>
    <NFormItem label="端口"><NInputNumber v-model:value="smtp.port" :min="1" :max="65535" /></NFormItem>
    <NFormItem label="发件邮箱"><NInput v-model:value="smtp.from" placeholder="sender@example.com" /></NFormItem>
    <NFormItem label="加密方式"><NSelect v-model:value="smtp.security" :options="securityOptions" /></NFormItem>
    <NFormItem label="SMTP 授权码"><NInput v-model:value="smtp.password" type="password" show-password-on="click" autocomplete="new-password" :placeholder="smtp.password_set ? '已配置，留空保留' : '请输入授权码'" :disabled="smtp.clear_password" /></NFormItem>
    <NFormItem label="移除授权码"><NCheckbox v-model:checked="smtp.clear_password">清除已保存的授权码</NCheckbox></NFormItem>
   </NForm><NButton type="primary" :loading="savingSMTP" :disabled="!canAct" @click="saveSMTP">保存默认 SMTP</NButton>
  </section>
  <section class="settings-box">
   <div class="section-head"><div><h3>每日收信检测</h3><p class="muted">按设置的范围发送测试邮件。时区：{{ schedule.timezone || '系统时区' }}。</p></div><NSwitch v-model:value="schedule.enabled" :disabled="!canAct"><template #checked>已启用</template><template #unchecked>已停用</template></NSwitch></div>
   <NForm label-placement="top" class="settings-grid" :disabled="!canAct">
    <NFormItem label="每日执行时间"><NInput v-model:value="schedule.run_at" placeholder="09:00" /></NFormItem>
    <NFormItem label="检测范围"><NSelect v-model:value="schedule.scope" :options="[{ label: '全部启用邮箱', value: 'all' }, { label: '指定检测分组', value: 'folder' }]" /></NFormItem>
    <NFormItem v-if="schedule.scope==='folder'" label="检测分组"><NSelect v-model:value="schedule.folder_id" :options="folderOptions" /></NFormItem>
    <NFormItem label="每组抽样"><NSelect v-model:value="schedule.sample" :options="[{label:'全部检测',value:0},{label:'1 个',value:1},{label:'3 个',value:3},{label:'5 个',value:5}]" /></NFormItem>
    <NFormItem label="单次请求超时（秒）"><NInputNumber v-model:value="schedule.options.timeout" :min="1" :max="60" /></NFormItem>
    <NFormItem label="并发数"><NInputNumber v-model:value="schedule.options.concurrency" :min="1" :max="10" /></NFormItem>
    <NFormItem label="收信等待（秒）"><NInputNumber v-model:value="schedule.options.wait" :min="5" :max="300" /></NFormItem>
    <NFormItem label="发送间隔（毫秒）"><NInputNumber v-model:value="schedule.options.interval" :min="200" :max="10000" :step="100" /></NFormItem>
   </NForm>
   <div class="schedule-checks"><NCheckbox v-if="schedule.enabled" v-model:checked="confirmedSchedule" :disabled="!canAct">确认按此计划自动发送测试邮件</NCheckbox></div>
   <NAlert v-if="schedule.last_error" type="error" :show-icon="false" style="margin:12px 0">{{ schedule.last_error }}</NAlert>
   <p class="muted">检测范围：{{ scheduleScopeLabel }}<br>上次执行：{{ date(schedule.last_run_at) }} · {{ schedule.last_run_status || '尚未运行' }}<span v-if="schedule.last_run_task_id"> · 任务 #{{ schedule.last_run_task_id }}</span><br>下次执行：{{ date(schedule.next_run_at) }}</p>
   <div class="actions"><NButton type="primary" :loading="savingSchedule" :disabled="!canSaveSchedule" @click="saveSchedule">保存定时检测</NButton><NButton secondary :loading="running" :disabled="!canAct" @click="runNow">立即执行已保存的计划</NButton></div>
  </section>
  <NModal v-model:show="showSender" preset="card" :title="senderID ? '编辑发件账号' : '新增发件账号'" style="width:min(600px,calc(100vw - 32px))">
   <SMTPPresets :host="sender.host" :port="sender.port" :security="sender.security" @apply="connection => Object.assign(sender, connection)" />
   <NForm label-placement="top" class="settings-grid" :disabled="!canAct">
    <NFormItem label="SMTP 服务器"><NInput v-model:value="sender.host" placeholder="smtp.example.com" /></NFormItem><NFormItem label="端口"><NInputNumber v-model:value="sender.port" :min="1" :max="65535" /></NFormItem>
    <NFormItem label="发件邮箱"><NInput v-model:value="sender.from" placeholder="sender@example.com" /></NFormItem><NFormItem label="加密方式"><NSelect v-model:value="sender.security" :options="securityOptions" /></NFormItem>
    <NFormItem label="SMTP 授权码"><NInput v-model:value="sender.password" type="password" show-password-on="click" autocomplete="new-password" :placeholder="senderID ? '留空保留现有授权码' : '请输入授权码'" /></NFormItem><NFormItem label="状态"><NSwitch v-model:value="sender.enabled"><template #checked>启用</template><template #unchecked>停用</template></NSwitch></NFormItem>
   </NForm><template #footer><div class="actions end"><NButton @click="showSender=false">取消</NButton><NButton type="primary" :loading="savingSender" :disabled="!canAct" @click="saveSender">保存账号</NButton></div></template>
  </NModal>
 </div>
</template>

<style scoped>
.settings-box{border:1px solid var(--border);border-radius:12px;padding:20px;margin-top:18px}.settings-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0 16px;margin-top:16px}.section-head{display:flex;justify-content:space-between;gap:16px;align-items:flex-start;margin-bottom:12px}.section-head p,.settings-box>p{font-size:12px;line-height:1.7;margin:6px 0 12px}.section-head h2,.section-head h3,.settings-box>h3{margin:0}.actions{display:flex;gap:8px;flex-wrap:wrap}.end{justify-content:flex-end}.schedule-checks{display:flex;gap:16px;flex-wrap:wrap}.settings-grid :deep(.n-input-number){width:100%}@media(max-width:1100px){.settings-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:600px){.settings-grid{grid-template-columns:1fr}.section-head{flex-direction:column}.settings-box{padding:14px}}
</style>
