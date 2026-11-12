<script setup lang="ts">
import { ref, watch } from 'vue'
import { NButton, useMessage } from 'naive-ui'
import { Copy, Eye, EyeOff } from 'lucide-vue-next'
import { copyText, errorText } from '../api'
const props = defineProps<{ label: string; value: string; secret?: boolean }>()
const visible = ref(true), message = useMessage()
watch(() => props.value, () => { visible.value = true })
async function copy() { try { await copyText(props.value); message.success(`${props.label} 已复制`) } catch (e) { message.error(errorText(e)) } }
</script>
<template>
 <section class="credential-field">
  <div class="credential-label"><strong>{{ label }}</strong><div class="row">
   <NButton v-if="secret && value" quaternary :aria-label="`${visible ? '隐藏' : '显示'} ${label}`" @click="visible=!visible"><EyeOff v-if="visible" :size="16" /><Eye v-else :size="16" /></NButton>
   <NButton quaternary :disabled="!value" :aria-label="`复制 ${label}`" @click="copy"><Copy :size="15" /><span class="ml">复制</span></NButton>
  </div></div>
  <pre class="credential-value" :class="{masked:!visible}" :data-credential="label">{{ value ? visible ? value : '••••••••••••••••••••••••' : '未配置' }}</pre>
 </section>
</template>
