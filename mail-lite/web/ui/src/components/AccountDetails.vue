<script setup lang="ts">
import { computed, onActivated, onDeactivated, onUnmounted, ref, watch } from 'vue'
import { NButton, NTag, NSpin, NAlert, useMessage } from 'naive-ui'
import { KeyRound, RefreshCw, Copy } from 'lucide-vue-next'
import { api, copyText, providerName, date, errorText, type Account, type AccountCredentials } from '../api'
import CredentialField from './CredentialField.vue'
import MXDetails from './MXDetails.vue'

const props = defineProps<{ account: Account | null }>()
const emit = defineEmits<{ refresh: [id: string] }>()
const message = useMessage(), data = ref<AccountCredentials | null>(null), loading = ref(false), error = ref('')
let serial = 0, active = true

async function load() {
 const account = props.account, current = ++serial
 if (!account || !active) return
 loading.value = true; data.value = null; error.value = ''
 try { const response = await api<AccountCredentials>(`admin/accounts/${account.id}/credentials`, 'POST', {}); if (current === serial) data.value = response }
 catch (e) { if (current === serial) error.value = errorText(e) }
 finally { if (current === serial) loading.value = false }
}
async function copyLine() {
 if (!data.value) return
 const d = data.value, line = d.provider === 'mx' ? `${d.email}----MX----${d.mx_retention_days ?? 30}` : d.provider === 'outlook' ? `${d.email}----${d.password}----${d.client_id}----${d.refresh_token}` : `${d.email}----${d.password}`
 try { await copyText(line); message.success('可重新导入的邮箱凭据已复制') } catch (e) { message.error(errorText(e)) }
}
function valueLines(values?: string[]) { return (values || []).filter(Boolean).join('\n') }
const accountProxyText = computed(() => valueLines(data.value?.proxies))
const effectiveProxyText = computed(() => valueLines(data.value?.effective_proxies))
const tokenNames: Record<string, string> = {graph: 'Graph 读取', write: 'Graph 删信', imap: 'IMAP 读取'}
const statusNames: Record<string, string> = {active: '已启用', disabled: '已停用'}
watch(() => props.account, account => { serial++; data.value = null; if (account && active) load() }, { immediate: true })
function onDataChanged() { if (props.account && active) load() }
window.addEventListener('data-changed', onDataChanged)
onDeactivated(() => { active=false; serial++; data.value=null; loading.value=false; error.value='' })
onActivated(() => { active=true; if(props.account&&!data.value&&!loading.value)void load() })
onUnmounted(() => { serial++; window.removeEventListener('data-changed', onDataChanged) })
</script>
<template>
 <section class="account-details-content">
   <div class="credential-account"><span class="detail-icon"><KeyRound :size="23" /></span><div><h2>{{ data?.email || account?.email }}</h2><span class="muted">{{ (data?.provider || account?.provider) === 'mx' ? '自有域名 SMTP 收信' : (data?.provider || account?.provider) === 'outlook' ? 'Outlook OAuth 授权' : 'iCloud IMAP 授权' }}</span></div><NTag v-if="data" size="small" :bordered="false" :type="data.status==='active'?'success':'default'">{{ statusNames[data.status] || data.status }}</NTag></div>
   <NSpin v-if="loading" class="credential-loading" />
   <NAlert v-if="error" type="error">{{ error }}<NButton text @click="load">重新加载</NButton></NAlert>
   <template v-if="data">
    <div class="detail-section-title account-detail-title"><h3>账号信息</h3><span class="muted">{{ data.provider === 'outlook' ? '主节点完整配置' : '主邮箱完整配置' }}</span></div>
    <div class="detail-grid">
     <div><span>邮箱地址</span><strong>{{ data.email }}</strong></div>
     <div><span>邮箱类型</span><strong>{{ providerName(data.provider) }}</strong></div>
     <div><span>分组</span><strong>{{ data.group_name || '未分组' }}</strong></div>
     <div><span>绑定链接</span><strong>{{ data.link_count.toLocaleString() }} 条</strong></div>
     <div><span>账号 ID</span><code>{{ data.id }}</code></div>
     <div><span>配置版本</span><strong>{{ data.version }} · 代数 {{ data.generation }}</strong></div>
    </div>
    <div v-if="data.note" class="detail-note"><span>备注</span><p>{{ data.note }}</p></div>
    <MXDetails v-if="data.provider==='mx'" :id="data.id" :key="data.id" />
    <div v-if="data.provider!=='mx'" class="credential-summary"><div><span>最近刷新</span><strong>{{ data.provider === 'outlook' ? data.last_refresh ? date(data.last_refresh) : '尚未刷新' : '使用应用专用密码' }}</strong></div><NTag size="small" :bordered="false" :type="data.refresh_error?'warning':data.status==='disabled'?'default':'success'">{{ data.refresh_error ? '授权异常' : statusNames[data.status] || data.status }}</NTag></div>
    <NAlert v-if="data.refresh_error" type="warning" style="margin-bottom:16px">{{ data.refresh_error }}</NAlert>
    <template v-if="data.provider==='outlook'">
     <div class="detail-section-title"><h3>Outlook 授权信息</h3><span class="muted">用于 Graph / IMAP 取信</span></div>
     <CredentialField label="Client ID" :value="data.client_id" />
     <CredentialField label="Refresh Token" :value="data.refresh_token" secret />
     <div class="detail-section-title"><h3>Access Token 缓存</h3><span class="muted">{{ data.access_tokens.length }} 个有效缓存</span></div>
     <p v-if="!data.access_tokens.length" class="credential-empty">尚未获取 Access Token。取信或刷新 Token 后可在这里查看。</p>
     <div v-for="token in data.access_tokens" :key="token.kind" class="access-token">
      <div class="row between wrap"><NTag size="small" :bordered="false">{{ tokenNames[token.kind] || token.kind }}</NTag><small class="muted">到期：{{ date(token.expires_at) }}</small></div>
      <div class="credential-scope"><span>Microsoft 返回的权限范围</span><code v-if="token.scope">{{ token.scope }}</code><span v-else>接口未返回权限范围，无法确认具体权限。</span></div>
      <CredentialField :label="`Access Token · ${tokenNames[token.kind] || token.kind}`" :value="token.access_token" secret />
     </div>
    </template>
    <template v-else-if="data.provider==='icloud'">
     <div class="detail-section-title"><h3>iCloud IMAP 信息</h3><span class="muted">应用专用密码连接</span></div>
     <CredentialField label="应用专用密码" :value="data.password" secret />
    </template>
    <template v-if="data.provider!=='mx'"><div class="detail-section-title"><h3>代理配置</h3><span class="muted">连接邮箱时实际使用的地址</span></div>
    <CredentialField v-if="accountProxyText" label="账号覆盖代理" :value="accountProxyText" secret />
    <CredentialField v-if="effectiveProxyText" label="实际生效代理" :value="effectiveProxyText" secret />
    <p v-if="!accountProxyText && !effectiveProxyText" class="credential-empty">未配置代理，连接将直接访问邮箱服务。</p>
    <div class="credential-note">这里展示主节点当前保存的完整配置。Refresh Token 轮换后会随之更新，Access Token 仅在有效期内显示。</div></template>
   </template>
<div class="credential-footer"><NButton :disabled="!data" @click="copyLine"><Copy :size="16" class="icon-before" />复制导入行</NButton><NButton v-if="account?.provider==='outlook'" type="primary" @click="emit('refresh',account.id)"><RefreshCw :size="16" class="icon-before" />刷新 Token</NButton><NButton quaternary :loading="loading" @click="load"><RefreshCw :size="16" class="icon-before" />重新加载</NButton></div>
 </section>
</template>
