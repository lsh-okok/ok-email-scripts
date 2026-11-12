<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NInputNumber, NSpin, NSwitch, useMessage } from 'naive-ui'
import { KeyRound, Network, Settings as SettingsIcon } from 'lucide-vue-next'
import { api, errorText, type Settings } from '../api'

const router = useRouter(), msg = useMessage()
const settings = reactive<Settings>({ scan_limit: 100, refresh_enabled: true, refresh_cron: '0 2 * * *', refresh_days: 30, public_base_url: '', time_zone: 'Asia/Shanghai' })
const loading = ref(true), loaded = ref(false), saving = ref(false), loadError = ref(''), settingsError = ref('')
const passwords = reactive({ current_password: '', new_password: '', confirm_password: '' })
const changingPassword = ref(false), passwordError = ref('')

async function loadSettings() {
 loading.value = true; loadError.value = ''
 try { Object.assign(settings, await api<Settings>('admin/settings')); loaded.value = true }
 catch (e) { loadError.value = errorText(e) }
 finally { loading.value = false }
}
async function saveSettings() {
 if (!loaded.value || saving.value) return
 saving.value = true; settingsError.value = ''
 try { Object.assign(settings, await api<Settings>('admin/settings', 'PUT', settings)); msg.success('系统设置已保存') }
 catch (e) { settingsError.value = errorText(e) }
 finally { saving.value = false }
}
async function changePassword() {
 if (changingPassword.value) return
 passwordError.value = ''
 if (!passwords.current_password) { passwordError.value = '请输入当前密码'; return }
 if ([...passwords.new_password].length < 12 || new TextEncoder().encode(passwords.new_password).length > 72) { passwordError.value = '新密码至少 12 个字符，且不能超过 72 字节'; return }
 if (passwords.new_password !== passwords.confirm_password) { passwordError.value = '两次输入的新密码不一致'; return }
 if (passwords.current_password === passwords.new_password) { passwordError.value = '新密码不能与当前密码相同'; return }
 changingPassword.value = true
 try {
  await api('admin/password', 'PUT', passwords)
  passwords.current_password = ''; passwords.new_password = ''; passwords.confirm_password = ''
  msg.success('管理员密码已修改，请使用新密码重新登录', { duration: 6000 })
 } catch (e) { passwordError.value = errorText(e) }
 finally { changingPassword.value = false }
}
onMounted(loadSettings)
</script>

<template>
 <div class="settings-page">
  <div class="page-title"><div><h1>系统设置</h1><p>管理访问地址、邮件调度和管理员密码。</p></div><NButton @click="router.push('/nodes')"><Network :size="16" class="icon-before" />节点管理</NButton></div>
  <div class="settings-layout">
   <NCard class="settings-panel" :bordered="false">
    <template #header><span class="settings-heading"><SettingsIcon :size="20" />常规设置</span></template>
    <NSpin :show="loading">
     <NAlert v-if="loadError" type="error" class="settings-notice">{{ loadError }}<div><NButton size="small" @click="loadSettings">重新加载</NButton></div></NAlert>
     <NForm :disabled="!loaded || saving" @submit.prevent="saveSettings">
      <NFormItem label="公开访问根地址"><NInput v-model:value="settings.public_base_url" aria-label="公开访问根地址" placeholder="https://mail.example.com" /></NFormItem>
      <p class="settings-hint">留空时使用当前访问地址，用于生成取信链接。</p>
      <NFormItem label="每个文件夹扫描邮件数"><NInputNumber v-model:value="settings.scan_limit" aria-label="每个文件夹扫描邮件数" :min="20" :max="1000" :precision="0" style="width:100%" /></NFormItem>
      <div class="settings-switch"><div><strong>定时刷新 Outlook Token</strong><p class="muted">按计划检查并刷新符合间隔要求的账号。</p></div><NSwitch v-model:value="settings.refresh_enabled" aria-label="启用定时刷新 Outlook Token" :disabled="!loaded || saving" /></div>
      <NFormItem label="Cron 表达式（分 时 日 月 周）"><NInput v-model:value="settings.refresh_cron" aria-label="Cron 表达式" placeholder="0 2 * * *" /></NFormItem>
      <div class="settings-form-grid">
       <NFormItem label="调度时区"><NInput v-model:value="settings.time_zone" aria-label="调度时区" placeholder="Asia/Shanghai" /></NFormItem>
       <NFormItem label="距上次刷新至少几天"><NInputNumber v-model:value="settings.refresh_days" aria-label="距上次刷新至少几天" :min="1" :max="365" :precision="0" style="width:100%" /></NFormItem>
      </div>
      <p class="settings-hint">默认上海时间每天 02:00 检查，距上次成功刷新满 30 天才执行。手动刷新立即进入任务队列。</p>
      <NAlert v-if="settingsError" type="error" :show-icon="false" class="settings-notice">{{ settingsError }}</NAlert>
      <div class="form-actions"><NButton type="primary" attr-type="submit" :loading="saving" :disabled="!loaded || loading">保存常规设置</NButton></div>
     </NForm>
    </NSpin>
   </NCard>
   <NCard class="settings-panel" :bordered="false">
    <template #header><span class="settings-heading"><KeyRound :size="20" />管理员密码</span></template>
    <p class="settings-description">使用当前密码验证身份后，设置新的管理台登录密码。</p>
    <NForm :disabled="changingPassword" @submit.prevent="changePassword">
     <NFormItem label="当前密码"><NInput v-model:value="passwords.current_password" type="password" show-password-on="click" :input-props="{ 'aria-label': '当前密码', autocomplete: 'current-password' }" placeholder="输入当前管理员密码" /></NFormItem>
     <NFormItem label="新密码"><NInput v-model:value="passwords.new_password" type="password" show-password-on="click" :input-props="{ 'aria-label': '新密码', autocomplete: 'new-password' }" placeholder="至少 12 个字符" /></NFormItem>
     <NFormItem label="确认新密码"><NInput v-model:value="passwords.confirm_password" type="password" show-password-on="click" :input-props="{ 'aria-label': '确认新密码', autocomplete: 'new-password' }" placeholder="再次输入新密码" /></NFormItem>
     <NAlert type="info" :show-icon="false" class="settings-notice">修改后，所有设备上的管理台登录会话将失效，请使用新密码重新登录。</NAlert>
     <NAlert v-if="passwordError" type="error" :show-icon="false" class="settings-notice">{{ passwordError }}</NAlert>
     <div class="form-actions"><NButton type="primary" attr-type="submit" :loading="changingPassword">修改管理员密码</NButton></div>
    </NForm>
   </NCard>
  </div>
 </div>
</template>

<style scoped>
.settings-page{max-width:1280px;margin:0 auto}
.settings-layout{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr);gap:24px;align-items:start}
.settings-panel{background:var(--surface);border:1px solid var(--border);border-radius:14px;min-width:0}
.settings-heading{display:flex;align-items:center;gap:10px;font-size:16px}
.settings-heading svg{color:var(--blue);flex-shrink:0}
.settings-description{margin:0 0 24px;color:var(--muted);font-size:13px;line-height:1.8}
.settings-hint{margin:-8px 0 22px;color:var(--muted);font-size:12px;line-height:1.8}
.settings-switch{display:flex;justify-content:space-between;align-items:center;gap:16px;margin:0 0 24px;padding:16px 0;border-top:1px solid var(--border);border-bottom:1px solid var(--border)}
.settings-switch strong{font-size:13px;font-weight:500}.settings-switch p{margin:6px 0 0;font-size:12px;line-height:1.7}
.settings-switch .n-switch{flex-shrink:0}
.settings-form-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:16px}
.settings-notice{margin-bottom:16px}
.settings-panel .form-actions{position:static}
@media(max-width:1100px){.settings-layout{grid-template-columns:minmax(0,1fr);gap:18px}}
@media(max-width:767px){.settings-page .page-title{align-items:flex-start}.settings-page .page-title>.n-button{flex-shrink:0;padding:0 10px}.settings-page .page-title p{line-height:1.7}.settings-form-grid{grid-template-columns:minmax(0,1fr);gap:0}.settings-panel :deep(.n-card-header){padding:20px 16px}.settings-panel :deep(.n-card__content){padding:0 16px 20px}.settings-panel .form-actions>.n-button{width:100%}}
</style>
