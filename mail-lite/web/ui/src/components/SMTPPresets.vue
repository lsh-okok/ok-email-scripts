<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NFormItem, NSelect } from 'naive-ui'

type Connection = { host: string; port: number; security: string }
type Preset = Connection & { value: string; label: string; hint: string }
const props = defineProps<Connection>()
const emit = defineEmits<{ apply: [Connection] }>()
const presets: Preset[] = [
 { value: '163', label: '163 邮箱', host: 'smtp.163.com', port: 465, security: 'ssl', hint: '在网页版邮箱「设置 → POP3/SMTP/IMAP」开启 SMTP 服务，并使用生成的客户端授权码。' },
 { value: '126', label: '126 邮箱', host: 'smtp.126.com', port: 465, security: 'ssl', hint: '在网页版邮箱「设置 → POP3/SMTP/IMAP」开启 SMTP 服务，并使用生成的客户端授权码。' },
 { value: 'yeah', label: 'Yeah 邮箱', host: 'smtp.yeah.net', port: 465, security: 'ssl', hint: '在网页版邮箱开启 SMTP 服务，并使用客户端授权码。' },
 { value: 'qq', label: 'QQ / Foxmail 邮箱', host: 'smtp.qq.com', port: 465, security: 'ssl', hint: '在 QQ 邮箱「设置 → 账号」开启 POP3/SMTP 或 IMAP/SMTP 服务，使用生成的授权码。Foxmail 地址也使用此服务器。' },
 { value: 'yahoo', label: 'Yahoo Mail', host: 'smtp.mail.yahoo.com', port: 465, security: 'ssl', hint: '在 Yahoo 账号安全设置中生成应用专用密码，SMTP 使用该密码登录。用户名填写完整 Yahoo 邮箱地址。' },
 { value: 'icloud', label: 'iCloud 邮箱', host: 'smtp.mail.me.com', port: 587, security: 'starttls', hint: '先为 Apple 账户开启双重认证，再在 account.apple.com 的「登录和安全 → App 专用密码」生成密码。发件邮箱填写完整的 iCloud 邮箱地址。' },
 { value: 'gmail', label: 'Gmail', host: 'smtp.gmail.com', port: 465, security: 'ssl', hint: '先开启 Google 两步验证，再生成应用专用密码。若账号或组织策略未提供应用专用密码，则无法使用此密码方式登录 SMTP。' },
 { value: 'aliyun', label: '阿里个人邮箱', host: 'smtp.aliyun.com', port: 465, security: 'ssl', hint: '在邮箱设置中开启 SMTP 服务，并使用三方客户端授权码。此预设适用于阿里个人邮箱，企业邮箱请按管理员提供的信息自定义填写。' },
]
const options = [{ label: '自定义 SMTP（手动填写）', value: 'custom' }, ...presets.map(({ label, value }) => ({ label, value }))]
const selected = ref('custom')
const active = computed(() => presets.find(preset => preset.value === selected.value))
watch(() => [props.host, props.port, props.security] as const, ([host, port, security]) => {
 selected.value = presets.find(preset => preset.host === host.trim().toLowerCase() && preset.port === port && preset.security === security)?.value || 'custom'
}, { immediate: true })
function apply(value: string) {
 selected.value = value
 const preset = presets.find(item => item.value === value)
 if (preset) emit('apply', { host: preset.host, port: preset.port, security: preset.security })
}
</script>

<template>
 <div class="smtp-presets">
  <NFormItem label="常用邮箱配置" :show-feedback="false">
   <NSelect :value="selected" :options="options" @update:value="apply" />
  </NFormItem>
  <p class="muted preset-description">选择邮箱服务可自动填入服务器、端口和加密方式；发件邮箱与授权码需自行填写。</p>
  <div v-if="active" class="preset-help" role="note">
   <strong>{{ active.host }} · {{ active.port }} · {{ active.security === 'ssl' ? 'SSL / TLS' : 'STARTTLS' }}</strong>
   <p>{{ active.hint }}</p>
  </div>
 </div>
</template>

<style scoped>
.smtp-presets{margin-top:16px}.preset-description{font-size:12px;line-height:1.7;margin:8px 0 0}.preset-help{margin-top:12px;padding:12px 14px;border:1px solid var(--border);border-radius:8px;background:var(--bg);font-size:12px;line-height:1.8;overflow-wrap:anywhere}.preset-help strong{font-weight:600}.preset-help p{margin:4px 0 0}
</style>
