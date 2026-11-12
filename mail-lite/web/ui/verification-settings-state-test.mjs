import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, h, reactive, ref } from 'vue'

// Exercise the real component setup with controlled failures. No server,
// database, SMTP connection or real mailbox is used.
const source = (await fs.readFile(new URL('./src/components/VerificationSettings.vue', import.meta.url), 'utf8'))
 .match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*\r?\n/gm, '')
const javascript = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const plain = value => JSON.parse(JSON.stringify(value))
async function flush() { for (let index = 0; index < 8; index++) await Promise.resolve() }
const fields = ['load', 'ready', 'error', 'schedule', 'smtp', 'senders', 'showSender', 'confirmedSchedule', 'saveSMTP', 'saveSender', 'editSender', 'removeSender', 'saveSchedule', 'runNow']
function setup() {
 const scope = effectScope(), requests = [], dialogs = [], unmounts = [], errors = [], synchronizations = []
 const env = {
  computed, h, reactive, ref, NButton: 'button', NTag: 'tag', date: value => value,
  onMounted: () => {}, onUnmounted: fn => unmounts.push(fn),
  useMessage: () => ({ success: () => {}, error: error => errors.push(error) }),
  useDialog: () => ({ warning: options => dialogs.push(options) }),
  defineProps: () => ({ synchronize: async () => { synchronizations.push(true) } }),
  errorText: error => error.message,
  api: (path, method = 'GET', body) => new Promise((resolve, reject) => requests.push({ path, method, body: body === undefined ? undefined : plain(body), resolve, reject })),
 }
 scope.run(() => vm.runInNewContext(javascript + ';globalThis.subject={' + fields.join(',') + '}', env))
 return { subject: env.subject, requests, dialogs, errors, synchronizations, dispose: () => { unmounts.forEach(fn => fn()); scope.stop() } }
}
const plan = { enabled: true, run_at: '13:45', scope: 'folder', folder_id: 7, sample: 3, include_sold: false, options: { timeout: 15, concurrency: 3, wait: 90, interval: 1200 }, timezone: 'Asia/Shanghai', last_run_status: '' }
function resolveLoad(requests, replacement = {}, failedPath = '') {
 const replies = {
  'admin/mail/settings': { configured: true, host: 'smtp.example.test', port: 587, from: 'fake@example.test', security: 'starttls', password_set: true },
  'admin/mail/senders': { items: [{ id: 1, host: 'smtp.example.test', port: 587, from: 'fake@example.test', security: 'starttls', enabled: true, password_set: true }] },
  'admin/mail/inspection-schedule': { ...plan, ...replacement },
  'admin/mail/folders': { items: [{ id: 7, name: 'Verification group' }] },
 }
 for (const request of requests) {
  assert.equal(request.method, 'GET')
  if (request.path === failedPath) request.reject(new Error('模拟配置接口 503'))
  else request.resolve(structuredClone(replies[request.path]))
 }
}
async function assertActionsBlocked(test) {
 const count = test.requests.length, dialogCount = test.dialogs.length, syncCount = test.synchronizations.length
 const s = test.subject
 s.confirmedSchedule.value = true
 const attempts = [s.saveSchedule(), s.saveSMTP(), s.saveSender()]
 s.editSender(); s.removeSender({ id: 1, from: 'fake@example.test' }); s.runNow()
 await flush()
 assert.equal(test.requests.length, count, 'Unloaded settings must not send mutations')
 assert.equal(test.dialogs.length, dialogCount, 'Unloaded settings must not open destructive/send confirmations')
 assert.equal(test.synchronizations.length, syncCount, 'Unloaded settings must not start a synchronization')
 assert.equal(s.showSender.value, false)
 await Promise.all(attempts)
}

// A single failed initial GET must not enable writes of the default disabled
// schedule, even when the other three GETs returned their actual values.
const initial = setup()
await assertActionsBlocked(initial)
const firstLoad = initial.subject.load()
await assertActionsBlocked(initial)
resolveLoad(initial.requests.slice(0, 4), {}, 'admin/mail/senders')
await firstLoad
assert.equal(initial.subject.ready.value, false)
assert.match(initial.subject.error.value, /503/)
await assertActionsBlocked(initial)

// Retry must restore the exact saved time and group, then allow saving those
// values. Previously this action silently wrote enabled=false and 09:00.
const retryLoad = initial.subject.load()
resolveLoad(initial.requests.slice(4, 8))
await retryLoad
assert.equal(initial.subject.ready.value, true)
assert.equal(initial.subject.error.value, '')
assert.equal(initial.subject.schedule.run_at, '13:45')
assert.equal(initial.subject.schedule.scope, 'folder')
assert.equal(initial.subject.schedule.folder_id, 7)
initial.subject.confirmedSchedule.value = true
const saved = initial.subject.saveSchedule()
await flush()
const saveRequest = initial.requests.at(-1)
assert.equal(saveRequest.method, 'PUT')
assert.equal(saveRequest.path, 'admin/mail/inspection-schedule')
assert.deepEqual(saveRequest.body, { enabled: true, run_at: '13:45', scope: 'folder', folder_id: 7, sample: 3, include_sold: false, options: plan.options })
saveRequest.resolve(structuredClone(plan)); await saved

// Refresh failure must block stale values too. A confirmation opened before
// the failure must not send after its configuration becomes untrusted.
initial.subject.runNow()
const pendingRun = initial.dialogs.at(-1)
initial.subject.removeSender({ id: 1, from: 'fake@example.test' })
const pendingDelete = initial.dialogs.at(-1)
const start = initial.requests.length, failedRefresh = initial.subject.load()
resolveLoad(initial.requests.slice(start), { run_at: '18:20' }, 'admin/mail/inspection-schedule')
await failedRefresh
assert.equal(initial.subject.ready.value, false)
assert.equal(initial.subject.schedule.run_at, '13:45', 'Partial refresh must not apply any response')
await assertActionsBlocked(initial)
const noMutationCount = initial.requests.length
const lateConfirmations = [pendingRun.onPositiveClick(), pendingDelete.onPositiveClick()]
await flush()
assert.equal(initial.requests.length, noMutationCount, 'Late confirmation must not act after a failed refresh')
await Promise.all(lateConfirmations)
initial.dispose()

// A slower earlier refresh cannot replace the newer configuration or restore
// readiness after the view has been closed.
const races = setup(), older = races.subject.load(), newer = races.subject.load()
resolveLoad(races.requests.slice(4, 8), { run_at: '16:10' }); await newer
resolveLoad(races.requests.slice(0, 4), { run_at: '08:00' }); await older
assert.equal(races.subject.schedule.run_at, '16:10')
assert.equal(races.subject.ready.value, true)
const lastStart = races.requests.length, lastLoad = races.subject.load()
races.dispose()
resolveLoad(races.requests.slice(lastStart), { run_at: '22:00' }); await lastLoad
assert.equal(races.subject.schedule.run_at, '16:10')
assert.equal(races.subject.ready.value, false)
await assertActionsBlocked(races)
assert.deepEqual(races.errors, [])
console.log('PASS: initial/refresh partial failures block saves and sends; retry retains saved schedule; stale loads, late confirmations and unmount cannot act')
