<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NProgress, NButton, NTag, NModal, NCard, NInput, NForm, NFormItem, NAlert, NEmpty, useMessage, useDialog } from 'naive-ui'
import { Plus, Server, RefreshCw, Settings as SettingsIcon } from 'lucide-vue-next'
import { api, date, errorText, type Node, type Settings } from '../api'

const router = useRouter(), msg = useMessage(), dialog = useDialog()
const nodes = ref<Node[]>([]), cursor = ref(0), busy = ref(false), creating = ref(false)
const name = ref(''), saving = ref(false), created = ref<Node | null>(null), error = ref(''), publicBase = ref('')
const changing = ref<Record<string, boolean>>({})
let timer: ReturnType<typeof setInterval>

async function load() {
  busy.value = true
  try {
    const data = await api<{ items: Node[]; cursor: number }>('admin/nodes')
    nodes.value = data.items
    cursor.value = data.cursor
    error.value = ''
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}

async function create() {
  saving.value = true
  try {
    publicBase.value = (await api<Settings>('admin/settings')).public_base_url
    created.value = await api<Node>('admin/nodes', 'POST', { name: name.value })
    creating.value = false
    name.value = ''
    await load()
  } catch (e) {
    msg.error(errorText(e))
  } finally {
    saving.value = false
  }
}

function online(n: Node) {
  return !n.revoked && !n.disabled && !!n.last_seen && Date.now() - new Date(n.last_seen).valueOf() < 15000
}

async function toggleDisabled(n: Node) {
  if (changing.value[n.id]) return
  changing.value[n.id] = true
  const disabled = !n.disabled
  try {
    const updated = await api<Node>('admin/nodes/' + n.id + '/status', 'PUT', { disabled })
    nodes.value = nodes.value.map(node => node.id === n.id ? updated : node)
    msg.success(disabled ? '节点已停用，接入凭据和原配置已保留' : '已启用，子节点运行时会在约 5 秒内自动重连，无需修改配置')
    await load()
  } catch (e) {
    msg.error(errorText(e))
  } finally {
    delete changing.value[n.id]
  }
}

function revoke(n: Node) {
  dialog.warning({
    title: '永久撤销接入凭据',
    content: `永久撤销“${n.name}”的接入凭据？原凭据将失效，恢复接入需要创建新凭据并修改子节点配置。临时下线请使用“停用节点”。`,
    positiveText: '永久撤销',
    negativeText: '取消',
    onPositiveClick: async () => {
      changing.value[n.id] = true
      try {
        await api('admin/nodes/' + n.id, 'DELETE')
        msg.success('接入凭据已永久撤销')
        await load()
      } catch (e) {
        msg.error(errorText(e))
        return false
      } finally {
        delete changing.value[n.id]
      }
    }
  })
}

function remove(n: Node) {
  dialog.warning({
    title: '删除已撤销节点记录',
    content: `删除“${n.name}”的已撤销记录？删除后该记录将从列表移除，原凭据仍然失效。`,
    positiveText: '删除记录',
    negativeText: '取消',
    onPositiveClick: async () => {
      changing.value[n.id] = true
      try {
        await api('admin/nodes/' + n.id + '/record', 'DELETE')
        nodes.value = nodes.value.filter(node => node.id !== n.id)
        msg.success('已撤销节点记录已删除')
        await load()
      } catch (e) {
        msg.error(errorText(e))
        return false
      } finally {
        delete changing.value[n.id]
      }
    }
  })
}

function snippet(n: Node) {
  const masterURL = publicBase.value || location.origin
  const allowHTTP = masterURL.startsWith('http://') ? '\nALLOW_INSECURE_NODE_HTTP=true' : ''
  return `NODE_ROLE=replica\nMASTER_URL=${masterURL}\nNODE_ID=${n.id}\nNODE_TOKEN=${n.token}${allowHTTP}\nDATA_DIR=/data\nLISTEN_ADDR=:8080`
}

onMounted(() => { load(); timer = setInterval(load, 5000) })
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="page-title">
    <div><h1>节点管理</h1><p>分散取信请求，在多个域名下提供访问。</p></div>
    <div class="actions">
      <NButton @click="router.push('/settings')"><SettingsIcon :size="16" style="margin-right:7px" />系统设置</NButton>
      <NButton type="primary" @click="creating = true"><Plus :size="18" style="margin-right:6px" />接入节点</NButton>
    </div>
  </div>
  <div class="stats">
    <div class="stat surface">
      <div><div class="stat-label">已接入节点</div><div class="stat-number">{{ nodes.filter(n => !n.revoked).length }}</div></div>
      <div class="stat-icon"><Server :size="21" /></div>
    </div>
    <div class="stat surface">
      <div><div class="stat-label">在线节点</div><div class="stat-number">{{ nodes.filter(online).length }}</div></div><span class="dot" />
    </div>
    <div class="stat surface">
      <div><div class="stat-label">主节点同步游标</div><div class="stat-number" style="font-size:24px">{{ cursor.toLocaleString() }}</div></div>
    </div>
  </div>
  <NAlert type="info" :show-icon="false">
    <div>临时下线请使用“停用节点”，原凭据和配置会保留；点击“启用节点”后，运行中的子节点会自动重连。</div>
    <div>服务器宕机时，恢复服务器和子节点程序即可自动上线。后台启用用于恢复接入，不能启动已关机的服务器。</div>
    <div>首次同步完成后开放取信，默认每 5 秒同步；连续 2 分钟无法同步时停止取信。子节点不开放管理及删信。</div>
  </NAlert>
  <div class="section-title">
    <h2>子节点</h2>
    <NButton quaternary :loading="busy" @click="load"><RefreshCw :size="16" style="margin-right:8px" />刷新状态</NButton>
  </div>
  <NAlert v-if="error" type="error">{{ error }}</NAlert>
  <div class="node-grid">
    <article v-for="n in nodes" :key="n.id" class="node-card surface">
      <div class="row between">
        <h3>{{ n.name }}</h3>
        <NTag :bordered="false" size="small" :type="n.revoked || n.disabled ? 'default' : online(n) ? 'success' : 'warning'">
          {{ n.revoked ? '已撤销' : n.disabled ? '已停用' : n.syncing ? '初次同步' : online(n) ? '在线' : '未连接 / 离线' }}
        </NTag>
      </div>
      <div class="subline">{{ n.id }}</div>
      <div class="node-meta">
        <div class="node-ip-row"><span>连接 IP</span><code class="node-ip">{{ n.ip || '尚未连接' }}</code></div>
        <div><span>最近同步</span>{{ date(n.last_seen) }}</div>
        <div><span>同步游标</span>{{ n.cursor.toLocaleString() }} / {{ cursor.toLocaleString() }}</div>
        <div><span>待同步事件</span>{{ Math.max(0, cursor - n.cursor).toLocaleString() }}</div>
      </div>
      <div v-if="n.syncing && !n.disabled && !n.revoked" style="margin-bottom:18px">
        <NProgress type="line" :percentage="n.snapshot_total ? Math.round((n.snapshot_done || 0) / n.snapshot_total * 100) : 0" processing />
        <small class="muted">快照 {{ (n.snapshot_done || 0).toLocaleString() }} / {{ (n.snapshot_total || 0).toLocaleString() }} 条</small>
      </div>
      <NAlert v-if="n.error && !n.disabled && !n.revoked" type="warning" :show-icon="false">{{ n.error }}</NAlert>
      <p v-if="n.disabled && !n.revoked" class="node-state-note muted">接入已暂停。启用后沿用原配置，无需重新填写凭据。</p>
      <p v-else-if="n.revoked" class="node-state-note muted">原凭据已永久失效，重新接入需要创建新凭据；可删除这条记录。</p>
      <div v-if="!n.revoked" class="node-actions">
        <NButton block secondary :type="n.disabled ? 'primary' : 'warning'" :loading="changing[n.id]" @click="toggleDisabled(n)">
          {{ n.disabled ? '启用节点' : '停用节点' }}
        </NButton>
        <NButton block quaternary type="error" :disabled="changing[n.id]" @click="revoke(n)">永久撤销凭据</NButton>
      </div>
      <NButton v-else block secondary type="error" :loading="changing[n.id]" @click="remove(n)">删除记录</NButton>
    </article>
  </div>
  <section v-if="!nodes.length && !busy" class="surface empty">
    <NEmpty description="还没有子节点"><template #extra><NButton type="primary" @click="creating = true">接入第一个节点</NButton></template></NEmpty>
  </section>
  <NModal v-model:show="creating">
    <NCard class="form-modal" title="接入子节点" closable @close="creating = false">
      <NForm @submit.prevent="create">
        <NFormItem label="节点名称"><NInput v-model:value="name" placeholder="例如：香港节点 01" maxlength="80" /></NFormItem>
        <p class="muted">每个节点使用独立凭据。创建后在子节点程序或容器中填写对应环境变量。</p>
        <div class="form-actions"><NButton @click="creating = false">取消</NButton><NButton type="primary" attr-type="submit" :loading="saving">生成接入凭据</NButton></div>
      </NForm>
    </NCard>
  </NModal>
  <NModal :show="!!created" @update:show="v => !v && (created = null)">
    <NCard class="form-modal" title="保存节点凭据" closable @close="created = null">
      <NAlert type="warning" :show-icon="false">凭据只在这里展示一次，请保存到子节点环境配置中。</NAlert>
      <pre v-if="created" class="code-block">{{ snippet(created) }}</pre>
      <p class="muted">主节点地址推荐使用 HTTPS 根地址；受控局域网使用 HTTP 时需保留允许 HTTP 的配置。各节点使用独立数据目录；子节点无需管理员密码。启动后可通过 /health/ready 检查同步状态。</p>
      <div class="form-actions"><NButton type="primary" @click="created = null">已保存</NButton></div>
    </NCard>
  </NModal>
</template>

<style scoped>
.node-ip-row>span{flex-shrink:0}
.node-ip{min-width:0;overflow-wrap:anywhere;text-align:right;font:inherit;color:var(--text)}
.node-actions{display:grid;gap:8px}
.node-state-note{font-size:12px;line-height:1.7;margin:0 0 16px}
</style>
