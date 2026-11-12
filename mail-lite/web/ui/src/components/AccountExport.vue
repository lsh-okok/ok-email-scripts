<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { NModal, NCard, NButton, NForm, NProgress, NAlert, useMessage } from 'naive-ui'
import { Download } from 'lucide-vue-next'
import { session, errorText } from '../api'

defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()
const message = useMessage()
const exporting = ref(false), bytes = ref(0), contentLength = ref(0), error = ref('')
let controller: AbortController | undefined
const percentage = () => contentLength.value ? Math.min(100, Math.round(bytes.value / contentLength.value * 100)) : 0

onUnmounted(() => controller?.abort())

async function download() {
 exporting.value = true; bytes.value = 0; contentLength.value = 0; error.value = ''; controller = new AbortController()
 try {
  const response = await fetch('/api/admin/accounts/export', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf }, body: JSON.stringify({ scope: 'all', format: 'txt', include_credentials: true }), signal: controller.signal })
  if (!response.ok) { if (response.status === 401) session.authenticated = false; throw new Error((await response.json()).error || '导出失败') }
  contentLength.value = Number(response.headers.get('Content-Length')) || 0
  const reader = response.body!.getReader(), chunks: Uint8Array<ArrayBuffer>[] = []
  while (true) { const { done, value } = await reader.read(); if (done) break; chunks.push(new Uint8Array(value)); bytes.value += value.length }
  const url = URL.createObjectURL(new Blob(chunks, { type: response.headers.get('Content-Type') || 'text/plain;charset=utf-8' })), link = document.createElement('a')
  link.href = url; link.download = response.headers.get('Content-Disposition')?.match(/filename="([^"]+)"/)?.[1] || 'mail-lite-accounts.txt'; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  message.success(`已导出 ${Number(response.headers.get('X-Export-Count')).toLocaleString()} 个邮箱`); emit('update:show', false)
 } catch (e) { error.value = errorText(e) } finally { exporting.value = false; controller = undefined }
}
</script>
<template>
 <NModal :show="show" :mask-closable="!exporting" @update:show="v => !exporting && emit('update:show', v)"><NCard class="form-modal" title="导出邮箱" :closable="!exporting" @close="emit('update:show', false)">
  <NForm @submit.prevent="download">
   <p class="export-description">将所有已导入邮箱导出为 TXT 导入格式。Outlook 每行包含：邮箱----密码----Client ID----Refresh Token；iCloud 每行包含：邮箱----应用专用密码；MX 每行包含：postmaster@域名----MX----保留天数。</p>
   <div class="export-summary"><Download :size="19" /><div><strong>导出全部邮箱</strong><span>包含完整凭据，可直接重新导入</span></div></div>
   <div v-if="exporting" class="export-progress"><NProgress type="line" :percentage="percentage()" processing :show-indicator="!!contentLength" /><small class="muted">{{ bytes ? `已接收 ${(bytes / 1024).toFixed(1)} KiB` : '正在整理导出数据…' }}</small></div>
   <NAlert v-if="error" type="error" style="margin-top:16px">{{ error }}</NAlert>
   <div class="form-actions"><NButton :disabled="exporting" @click="emit('update:show', false)">取消</NButton><NButton type="primary" attr-type="submit" :loading="exporting">下载全部邮箱</NButton></div>
  </NForm>
 </NCard></NModal>
</template>
