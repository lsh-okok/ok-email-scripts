// Run against the opt-in TestBrowserFixture only; it uses an isolated temporary database.
// MAIL_LITE_QA_ADDR=127.0.0.1:18096 go test ./internal/core -run '^TestBrowserFixture$' -count=1 -v -timeout=20m
// QA_BASE_URL=http://127.0.0.1:18096 node web/ui/sales-inventory-outbound-browser-test.mjs
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18096'
const marker = await fetch(base + '/__qa/stats')
assert.equal(marker.status, 200, 'This test requires the isolated browser fixture')
assert.match(marker.headers.get('content-type'), /application\/json/)
const output = process.env.QA_OUTPUT_DIR || path.join(os.tmpdir(), 'mail-lite-inventory-outbound')
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const page = await browser.newPage({ viewport: { width: 1206, height: 976 }, acceptDownloads: true })
page.setDefaultTimeout(15000)
const errors = [], submitted = []
page.on('pageerror', error => errors.push(error.message))
page.on('request', request => {
 if (new URL(request.url()).pathname === '/api/admin/mail/outbounds' && request.method() === 'POST') submitted.push(request.postDataJSON())
})
let csrf = ''
const pane = () => page.locator('.n-tab-pane').filter({ visible: true }).last()
const modal = () => page.locator('.n-modal').filter({ visible: true }).last()
const item = (label, root = modal()) => root.locator('.n-form-item').filter({ has: page.getByText(label, { exact: true }) })
async function fill(label, value, root = modal()) { await item(label, root).locator('input,textarea').first().fill(value) }
async function select(label, text, root = modal()) {
 await item(label, root).locator('.n-base-selection').click()
 await page.locator('.n-base-select-option').filter({ hasText: new RegExp('^' + text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$') }).filter({ visible: true }).last().click()
}
async function api(route, method = 'GET', data) {
 const result = await page.evaluate(async ({ route, method, data, csrf }) => {
  const response = await fetch('/api/' + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: data === undefined ? undefined : JSON.stringify(data) })
  return { status: response.status, body: await response.json() }
 }, { route, method, data, csrf })
 assert.ok(result.status < 400, `${method} ${route}: ${result.status} ${result.body.error || ''}`)
 return result.body
}
async function tab(name) {
 const toggle = page.getByRole('button', { name: '展开销售工作台子菜单', exact: true })
 if (await toggle.isVisible()) await toggle.click()
 await page.locator('.sidebar-sales-sections').getByRole('link', { name, exact: true }).click()
 await pane().waitFor()
}
async function closeModal() {
 await modal().locator('.n-card-header__close').click()
 await page.waitForFunction(() => !document.querySelector('.n-modal'))
}
async function search(email, kind = 'master') {
 await pane().locator('.inventory-switch').getByRole('button', { name: kind === 'master' ? /^主邮箱 / : /^子邮箱 / }).click()
 const loaded = page.waitForResponse(response => new URL(response.url()).pathname === '/api/admin/mail/' + (kind === 'master' ? 'masters' : 'children') && new URL(response.url()).searchParams.get('q') === (email || null))
 await pane().getByPlaceholder('搜索邮箱或备注').fill(email)
 await pane().getByRole('button', { name: '查询', exact: true }).click()
 await loaded
}
async function check(email) {
 await pane().locator('.n-data-table-tr').filter({ has: page.getByText(email, { exact: true }) }).getByRole('checkbox').check()
}
async function chooseMaster(email) { await search(email); await check(email) }
async function orders() { return api('admin/mail/outbounds?limit=100') }
async function downloadText(button) {
 const [download] = await Promise.all([page.waitForEvent('download'), button.click()])
 const chunks = []; for await (const chunk of await download.createReadStream()) chunks.push(chunk)
 return Buffer.concat(chunks).toString('utf8')
}
function compact(text, lineCount) {
 assert.equal(text, text.trim(), 'Export must not have leading/trailing whitespace or blank lines')
 const lines = text.split('\n')
 assert.equal(lines.length, lineCount)
 assert.ok(lines.every(line => line.length > 0 && line === line.trim()), 'Export must not have blank rows or padded fields')
}
async function noOverflow(label) {
 assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), label + ': page must not overflow horizontally')
 const rect = await pane().getByRole('button', { name: '提交出库', exact: true }).boundingBox()
 assert.ok(rect && rect.x >= 0 && rect.x + rect.width <= page.viewportSize().width, label + ': toolbar action must remain in the viewport')
}

try {
 await page.goto(base + '/admin/sales?tab=inventory')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button', { name: '登录管理台', exact: true }).click()
 await page.getByRole('heading', { name: '销售工作台', exact: true }).waitFor()
 csrf = (await api('session')).csrf
 const suffix = Date.now().toString()
 const preview = await api('admin/mail/import/preview', 'POST', { folder_id: 0, files: Array.from({ length: 27 }, (_, i) => ({
  name: `group-${i}.txt`, content: [`qa-master-${i}-${suffix}@example.net----qa-password`, ...Array.from({ length: 3 }, (_, n) => `qa-child-${i}-${n}-${suffix}@example.net----https://qa.example.net/${i}/${n}`)].join('\n'),
 })) })
 assert.equal(preview.report.added_masters, 27); assert.equal(preview.report.added_children, 81)
 await api('admin/mail/import/commit', 'POST', { token: preview.token })
 await page.getByRole('button', { name: '刷新数据', exact: true }).click()
 await pane().getByText('共 27 个主邮箱 · 每页 25 条', { exact: true }).waitFor()
 assert.equal(await pane().getByRole('button', { name: '提交出库', exact: true }).isDisabled(), true)
 const first = (await api('admin/mail/masters?limit=25&page=1')).items
 const second = (await api('admin/mail/masters?limit=25&page=2')).items
 const chosen = [first[0], second[0]], reserveMaster = first[1], blockedMaster = first[2], otherMaster = first[3]
 const reserveChildren = (await api(`admin/mail/children?parent=${reserveMaster.id}&limit=100`)).items

 // Keep a child selection and an existing outbound form draft, independently of group checkout.
 await search(reserveChildren[0].email, 'child'); await check(reserveChildren[0].email)
 await tab('出库记录'); await fill('客户', 'Existing outbound draft', pane()); await fill('备注', 'Keep this draft', pane())
 await tab('库存管理'); await search(''); await check(chosen[0].email)
 const pageTwo = page.waitForResponse(response => new URL(response.url()).pathname === '/api/admin/mail/masters' && new URL(response.url()).searchParams.get('page') === '2')
 await pane().locator('section:visible .inventory-pagination .n-pagination .n-pagination-item--button').last().click()
 await pageTwo; await check(chosen[1].email)
 await pane().getByText('已选主邮箱 2 个，子邮箱 1 个', { exact: false }).waitFor()
 // Inventory exports use the same compact format, with an explicit credentials choice.
 const inventoryMaster = pane().getByRole('checkbox', { name: '包含主邮箱资料', exact: true })
 assert.equal(await inventoryMaster.isChecked(), true)
 const inventoryExport = pane().locator('section:visible').getByRole('button', { name: '导出选中', exact: true })
 const fullInventory = await downloadText(inventoryExport)
 compact(fullInventory, 8)
 for (const master of chosen) assert.ok(fullInventory.includes(master.email + '----qa-password'))
 await inventoryMaster.uncheck()
 const childInventory = await downloadText(inventoryExport)
 compact(childInventory, 6)
 for (const master of chosen) assert.ok(!childInventory.includes(master.email))
 const button = pane().getByRole('button', { name: '提交出库', exact: true })
 assert.equal(await button.locator('..').getByRole('button', { name: '导出选中', exact: true }).count(), 1)
 const before = (await orders()).total
 await button.click(); await modal().getByText(/已选 2 个主邮箱，将其全部子邮箱整组出库/).waitFor()

 for (const width of [1206, 676, 360, 1432]) {
  await page.setViewportSize({ width, height: 976 })
  await noOverflow(String(width))
  const bounds = await modal().boundingBox()
  assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width, 'Confirmation must fit at ' + width)
  if ([1206, 360].includes(width)) {
   await page.waitForTimeout(400) // Let the modal's opening transition finish before visual review.
   await page.screenshot({ path: path.join(output, `group-outbound-${width}.png`), fullPage: true })
  }
 }
 await page.setViewportSize({ width: 1206, height: 976 })
 await modal().getByRole('button', { name: '取消', exact: true }).click()
 await page.waitForFunction(() => !document.querySelector('.n-modal'))
 assert.equal((await orders()).total, before); assert.equal(submitted.length, 0, 'Cancel must not submit')

 // Lose a successful response, then retry: one order, all children from both selected pages.
 await button.click(); await fill('客户', 'QA whole-group buyer')
 let loseNext = true, lostID
 await page.route('**/api/admin/mail/outbounds', async route => {
  if (route.request().method() !== 'POST' || !loseNext) return route.continue()
  loseNext = false
  const response = await route.fetch(); assert.equal(response.status(), 201)
  lostID = (await response.json()).id
  await route.abort('connectionfailed')
 })
 await modal().getByRole('button', { name: '确认出库', exact: true }).click()
 await page.getByText(/相同内容重试会复用本次请求/).waitFor()
 assert.equal((await orders()).total, before + 1)
 await modal().getByRole('button', { name: '确认出库', exact: true }).click()
 await modal().getByRole('button', { name: '下载交付文件', exact: true }).waitFor()
 await page.unroute('**/api/admin/mail/outbounds')
 assert.equal(submitted.length, 2); assert.deepEqual(submitted[0], submitted[1])
 assert.deepEqual(submitted[0].master_ids, chosen.map(v => v.id).sort((a,b) => a-b))
 assert.deepEqual(submitted[0].ids, []); assert.equal(submitted[0].selection, 'group')
 assert.equal(submitted[0].include_master, true)
 assert.equal((await orders()).total, before + 1)
 const sold = await api(`admin/mail/outbounds/${lostID}`)
 assert.equal(sold.items.length, 6); assert.equal(sold.status, 'sold')
 assert.deepEqual([...new Set(sold.items.map(v => v.parent_email))].sort(), chosen.map(v => v.email).sort())
 const text = await downloadText(modal().getByRole('button', { name: '下载交付文件', exact: true }))
 compact(text, 8)
 for (const master of chosen) assert.ok(text.includes(master.email + '----qa-password'))
 for (const child of sold.items) assert.ok(text.includes(child.email + '----https://qa.example.net/'))
 await modal().getByRole('checkbox', { name: '包含主邮箱资料', exact: true }).uncheck()
 const childDelivery = await downloadText(modal().getByRole('button', { name: '下载交付文件', exact: true }))
 compact(childDelivery, 6)
 for (const master of chosen) assert.ok(!childDelivery.includes(master.email))
 for (const child of sold.items) assert.ok(childDelivery.includes(child.email + '----https://qa.example.net/'))
 await closeModal()
 await pane().getByText('已选主邮箱 0 个，子邮箱 1 个', { exact: false }).waitFor()
 await tab('出库记录')
 assert.equal(await item('客户', pane()).locator('input').inputValue(), 'Existing outbound draft')
 assert.equal(await item('备注', pane()).locator('input').inputValue(), 'Keep this draft')
 const soldRow = pane().locator('.n-data-table-tr').filter({ hasText: sold.order_no })
 await soldRow.getByRole('button', { name: '下载', exact: true }).click()
 const rowDelivery = await downloadText(page.getByText('仅子邮箱与链接', { exact: true }).filter({ visible: true }))
 compact(rowDelivery, 6)
 assert.equal(rowDelivery, childDelivery)

 // Reserve keeps every child together, with the existing cancel/delivery lifecycle.
 await tab('库存管理'); await chooseMaster(reserveMaster.email)
 await button.click(); await select('出库方式', '预留'); await fill('客户', 'QA group reservation')
 await modal().getByRole('button', { name: '确认预留', exact: true }).click()
 await modal().getByRole('button', { name: '确认交付', exact: true }).waitFor()
 const reservedID = (await orders()).items[0].id
 const reserved = await api(`admin/mail/outbounds/${reservedID}`)
 assert.equal(reserved.status, 'reserved'); assert.equal(reserved.items.length, 3)
 assert.ok((await api(`admin/mail/children?parent=${reserveMaster.id}`)).items.every(v => v.status === 'reserved'))
 await closeModal()
 await api(`admin/mail/outbounds/${reservedID}/action`, 'POST', { action: 'cancel', reason: 'QA release reservation' })

 // A blocked child rejects the entire selection, including an otherwise valid group.
 const blocked = (await api(`admin/mail/children?parent=${blockedMaster.id}`)).items[0]
 await api('admin/mail/children/flags', 'POST', { ids: [blocked.id], flag: 'disabled', value: true, reason: 'QA unavailable child' })
 await chooseMaster(blockedMaster.email); await chooseMaster(otherMaster.email)
 const beforeRejected = (await orders()).total
 await button.click(); await select('出库方式', '出售')
 await modal().getByRole('button', { name: '确认出库', exact: true }).click()
 await page.getByText(/不能整组出售/).waitFor()
 assert.equal((await orders()).total, beforeRejected)
 for (const master of [blockedMaster, otherMaster]) assert.ok((await api(`admin/mail/children?parent=${master.id}`)).items.every(v => v.status === 'available'))
 await modal().getByRole('button', { name: '取消', exact: true }).click()
 await pane().getByText('已选主邮箱 2 个，子邮箱 1 个', { exact: false }).waitFor()

 // The original random outbound form still submits its independent draft and selected child.
 await tab('出库记录'); await select('范围', '选中子邮箱 (1)', pane())
 await pane().getByRole('button', { name: '创建出库单', exact: true }).click()
 await modal().getByRole('button', { name: '确认交付', exact: true }).waitFor()
 const random = (await orders()).items[0]
 assert.equal(random.customer, 'Existing outbound draft'); assert.equal(random.items, 1)
 assert.equal(random.selection, 'random'); assert.equal(random.status, 'reserved')
 assert.deepEqual(errors, [])
 console.log('PASS: compact multi-group inventory and delivery TXT exports with/without master credentials, row download choice; cross-page sale, cancel, lost-response idempotency, reserve, atomic rejection and independent random checkout; four viewport widths. Screenshots: ' + output)
} finally { await browser.close() }
