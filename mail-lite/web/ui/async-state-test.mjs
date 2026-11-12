import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, h, nextTick, reactive, ref, watch } from 'vue'

// Exercise the actual setup scripts with Vue's refs/watchers and controlled API
// responses. No browser fixture, live mailbox, or database is needed.
const cacheSource = await fs.readFile(new URL('./src/mailSession.ts', import.meta.url), 'utf8')
const cacheJS = ts.transpileModule(cacheSource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const cache = await import('data:text/javascript;base64,' + Buffer.from(cacheJS).toString('base64'))

async function setupComponent(path, exposed, overrides = {}) {
 const source = (await fs.readFile(new URL(path, import.meta.url), 'utf8')).match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*\r?\n/gm, '')
 const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
 const scope = effectScope(), unmounts = [], activations = [], deactivations = [], errors = []
 const env = {
  computed, h, nextTick, reactive, ref, watch, URLSearchParams, ...cache,
  onMounted: () => {}, onUnmounted: fn => unmounts.push(fn),
  onActivated: fn => activations.push(fn), onDeactivated: fn => deactivations.push(fn), onBeforeRouteLeave: () => {},
  useMessage: () => ({ error: value => errors.push(value), success: () => {}, warning: () => {} }), useDialog: () => ({}),
  useRoute: () => ({ query: {} }), useRouter: () => ({}),
  errorText: error => error.message, query: values => new URLSearchParams(Object.entries(values).filter(([, value]) => value !== undefined && value !== null && value !== '')).toString(),
  date: value => value, salesStatus: value => value, salesSections: [], resolveSalesSection: () => 'overview',
  ...overrides,
 }
 scope.run(() => vm.runInNewContext(js + ';globalThis.subject={' + exposed.join(',') + '}', env))
 return { subject: env.subject, errors, activate: () => activations.forEach(fn => fn()), deactivate: () => deactivations.forEach(fn => fn()), dispose: () => { unmounts.forEach(fn => fn()); scope.stop() } }
}

function controlledAPI() {
 const requests = []
 return { requests, api: path => new Promise((resolve, reject) => requests.push({ path, resolve, reject })) }
}
async function flush() { for (let i = 0; i < 6; i++) { await Promise.resolve(); await nextTick() } }

// Closing A and opening B must isolate both the detail and loading state.
const orders = controlledAPI()
const sales = await setupComponent('./src/pages/Sales.vue', ['openOutbound', 'outboundDetail', 'showOutbound', 'detailLoading'], { api: orders.api })
let first = sales.subject.openOutbound(1)
sales.subject.showOutbound.value = false
let second = sales.subject.openOutbound(2)
orders.requests[1].resolve({ id: 2, order_no: 'B' }); await second
orders.requests[0].resolve({ id: 1, order_no: 'A' }); await first
assert.equal(sales.subject.outboundDetail.value.id, 2, 'late A must not replace B')
assert.equal(sales.subject.detailLoading.value, false)

first = sales.subject.openOutbound(3)
sales.subject.showOutbound.value = false
second = sales.subject.openOutbound(4)
orders.requests[2].resolve({ id: 3 }); await first
assert.equal(sales.subject.outboundDetail.value, null, 'old response must not fill a newer loading modal')
assert.equal(sales.subject.detailLoading.value, true, 'old finally must not stop B loading')
orders.requests[3].resolve({ id: 4 }); await second
assert.equal(sales.subject.outboundDetail.value.id, 4)

const closed = sales.subject.openOutbound(5)
sales.subject.showOutbound.value = false
orders.requests[4].resolve({ id: 5 }); await closed
assert.equal(sales.subject.outboundDetail.value, null, 'closed modal must stay empty after a late response')
assert.equal(sales.subject.detailLoading.value, false)
const failed = sales.subject.openOutbound(6)
sales.subject.showOutbound.value = false
orders.requests[5].reject(new Error('stale failure')); await failed
assert.deepEqual(sales.errors, [], 'closed requests must not display stale errors')
const unmounted = sales.subject.openOutbound(7)
sales.dispose()
orders.requests[6].resolve({ id: 7 }); await unmounted
assert.equal(sales.subject.outboundDetail.value, null, 'unmounted view must ignore outstanding details')

// Saving a group invalidates all cached mail, while account generation stays the
// same. The active workspace must restart from page one with the new settings.
cache.clearMailSession()
const mailAPI = controlledAPI(), props = reactive({ account: { id: 'a', generation: 1 }, pane: 'messages' })
const workspace = await setupComponent('./src/components/MailWorkspace.vue', ['items', 'load', 'nextCursor', 'error', 'moreError'], { api: mailAPI.api, defineProps: () => props, defineEmits: () => () => {} })
const mail = value => ({ ref: value, subject: value, from: 'sender@example.net', to: 'recipient@example.net', folder: 'inbox', received_at: '2026-11-09T00:00:00Z', preview: value })
const page = (refs, next = '', more = false) => ({ items: refs.map(mail), next_cursor: next, has_more: more })
const refs = () => Array.from(workspace.subject.items.value, item => item.ref)
mailAPI.requests[0].resolve(page(['old-1', 'old-2'], 'old-next', true)); await flush()
assert.deepEqual(refs(), ['old-1', 'old-2'])
const oldPage = workspace.subject.load(true)
assert.match(mailAPI.requests[1].path, /cursor=old-next/)
cache.invalidateAllMail()
assert.equal(mailAPI.requests.length, 3, 'active mailbox must reload when group/settings invalidate the cache')
assert.ok(!mailAPI.requests[2].path.includes('cursor='), 'reload must discard the old cursor')
assert.deepEqual(refs(), [], 'old mail must not remain while fetching with changed connection settings')
mailAPI.requests[1].resolve(page(['stale-page'])); await oldPage
mailAPI.requests[2].resolve(page(['fresh-1', 'fresh-2'], 'fresh-next', true)); await flush()
assert.deepEqual(refs(), ['fresh-1', 'fresh-2'])
const freshPage = workspace.subject.load(true)
assert.match(mailAPI.requests[3].path, /cursor=fresh-next/)
mailAPI.requests[3].resolve(page(['fresh-3'])); await freshPage
assert.deepEqual(refs(), ['fresh-1', 'fresh-2', 'fresh-3'], 'new pages must retain the freshly loaded prefix')
assert.equal(workspace.subject.error.value, '')
assert.equal(workspace.subject.moreError.value, '')

workspace.deactivate()
cache.invalidateAllMail()
assert.equal(mailAPI.requests.length, 4, 'hidden mailbox must not issue a new request on invalidation')
workspace.activate()
assert.equal(mailAPI.requests.length, 5)
assert.ok(!mailAPI.requests[4].path.includes('cursor='), 'reactivation must restart from page one')
mailAPI.requests[4].resolve(page(['active'], 'active-next', true)); await flush()
cache.invalidateAccountMail('a')
await workspace.subject.load(true)
assert.equal(mailAPI.requests.length, 6)
assert.ok(!mailAPI.requests[5].path.includes('cursor='), 'missing page cache must never accept the visible stale cursor')
mailAPI.requests[5].resolve(page(['reloaded'])); await flush()
assert.deepEqual(refs(), ['reloaded'])
workspace.dispose()
cache.invalidateAllMail()
assert.equal(mailAPI.requests.length, 6, 'unmounted workspace must remove its invalidation listener')
assert.deepEqual(workspace.errors, [])
console.log('PASS: outbound detail request races/close/unmount; group invalidation reloads active mail from page one, rejects stale pages, preserves fresh pagination and defers hidden views')
