<script setup lang="ts">
import { NDrawer, NDrawerContent } from 'naive-ui'
import type { Account } from '../api'
import AccountDetails from './AccountDetails.vue'
defineProps<{ account: Account | null }>()
const emit = defineEmits<{ close: []; refresh: [id: string] }>()
</script>
<template>
 <NDrawer :show="!!account" :width="680" class="credentials-drawer" @update:show="v=>!v&&emit('close')">
  <NDrawerContent title="邮箱详情" closable><AccountDetails :account="account" @refresh="emit('refresh',$event)" /></NDrawerContent>
 </NDrawer>
</template>
