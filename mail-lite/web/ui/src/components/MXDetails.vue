<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NSpin, NTag } from 'naive-ui'
import { api, date, errorText } from '../api'
const props = defineProps<{ id: string }>()
interface Overview { domain: string; retention_days: number; smtp_enabled: boolean; smtp_running: boolean; hostname: string; listen: string; starttls: boolean; messages: number; last_received: string }
const info = ref<Overview | null>(null), error = ref(''), loading = ref(false)
async function load() { loading.value = true; error.value = ''; try { info.value = await api<Overview>(`admin/accounts/${props.id}/mx`) } catch (e) { error.value = errorText(e) } finally { loading.value = false } }
onMounted(load)
</script>
<template>
 <section class="mx-details">
  <div class="detail-section-title"><h3>MX 域名收信</h3><NButton text :loading="loading" @click="load">刷新状态</NButton></div>
  <NAlert v-if="error" type="error">{{ error }}</NAlert><NSpin v-if="loading && !info" />
  <template v-if="info">
   <NTag :type="info.smtp_running ? 'success' : 'warning'" :bordered="false">{{ info.smtp_running ? 'SMTP 收信服务运行中' : 'SMTP 收信服务未启用或未运行' }}</NTag>
   <div class="detail-grid mx-grid">
    <div><span>收信域名</span><strong>{{ info.domain }}</strong></div>
    <div><span>地址范围</span><strong>任意地址@{{ info.domain }}</strong></div>
    <div><span>邮件保留</span><strong>{{ info.retention_days ? `${info.retention_days} 天` : '永久保存' }}</strong></div>
    <div><span>已保存邮件</span><strong>{{ info.messages.toLocaleString() }} 封</strong></div>
    <div><span>最近收信</span><strong>{{ date(info.last_received) }}</strong></div>
    <div><span>传输加密</span><strong>{{ info.starttls ? '支持 STARTTLS' : '尚未配置 STARTTLS 证书' }}</strong></div>
   </div>
   <div class="detail-section-title"><h3>DNS 配置</h3></div>
   <p>先将收信主机的 A / AAAA 记录指向服务器公网 IP，再添加下列 MX 记录：</p>
   <div class="mx-dns"><span>名称：{{ info.domain }}</span><span>类型：MX</span><span>优先级：10</span><code>目标：{{ info.hostname || '请先配置收信主机名' }}</code></div>
   <NAlert type="info" :show-icon="false">公网 TCP 25 端口必须可达。收信主机使用 DNS 解析，不经过网页代理。配置 DNS 后，请从外部邮箱发信验证。</NAlert>
   <p>收信无需预先创建地址。在链接管理中为具体地址生成独立链接后，即可通过取信页面和 API 读取。停用域名会停止接收新邮件并禁止取件。</p>
  </template>
 </section>
</template>
<style scoped>
.mx-details{margin:20px 0;overflow-wrap:anywhere}.mx-grid{margin:18px 0}.mx-details p{line-height:1.7;color:var(--text-secondary);font-size:13px}.mx-dns{display:grid;gap:8px;padding:14px;background:var(--bg-soft);border:1px solid var(--border);border-radius:10px;margin:12px 0;font-size:13px}.mx-dns code{white-space:normal}
</style>
