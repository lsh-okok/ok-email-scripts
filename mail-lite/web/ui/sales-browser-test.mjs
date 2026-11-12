// Run after building web/dist and starting the opt-in TestBrowserFixture.
// MAIL_LITE_QA_ADDR=127.0.0.1:18093 go test ./internal/core -run '^TestBrowserFixture$' -count=1 -v -timeout=20m
// QA_BASE_URL=http://127.0.0.1:18093 node web/ui/sales-browser-test.mjs
// All inventory and SMTP credentials below are fake. No mail or external detection task is started.
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18093'
const output = process.env.QA_OUTPUT_DIR || path.join(os.tmpdir(), 'mail-lite-sales-browser')
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'light', acceptDownloads: true })
const page = await context.newPage(), pageErrors = [], requests = [], suffix = process.env.QA_SUFFIX || Date.now().toString()
const master = `qa-master-${suffix}@example.net`, children = Array.from({ length: 6 }, (_, i) => `qa-child-${i}-${suffix}@example.net`)
const uploadedMaster = `qa-file-${suffix}@example.net`, uploadedChild = `qa-file-child-${suffix}@example.net`
let csrf = '', sendRequests = 0
page.on('pageerror', error => pageErrors.push(error.message))
page.setDefaultTimeout(15000)
await fs.mkdir(output, { recursive: true })

function pane() { return page.locator('.n-tab-pane').filter({ visible: true }).last() }
function modal() { return page.locator('.n-modal').filter({ visible: true }).last() }
function item(label, root = pane()) { return root.locator('.n-form-item').filter({ has: page.getByText(label, { exact: true }) }) }
async function fill(label, value, root = pane()) { await item(label, root).locator('input,textarea').first().fill(String(value)) }
async function select(label, value, root = pane()) {
 await item(label, root).locator('.n-base-selection').click()
 await page.locator('.n-base-select-option').filter({ hasText: new RegExp('^' + value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$') }).filter({ visible: true }).last().click()
}
async function tab(name) {
 const key={'工作流概览':'overview','库存管理':'inventory','账号映射':'mapping','出库记录':'outbound','导入库存':'import','检测任务':'inspection','检测配置':'settings'}[name]
 if (page.viewportSize().width >= 768) {
  const toggle=page.getByRole('button',{name:'展开销售工作台子菜单',exact:true})
  if(await toggle.isVisible()) await toggle.click()
  await page.locator('.sidebar-sales-sections').getByRole('link',{name,exact:true}).click()
 } else {
  await page.locator('.sales-mobile-navigation .n-base-selection').click()
  await page.locator('.n-base-select-option').filter({hasText:new RegExp('^'+name+'$')}).filter({visible:true}).last().click()
 }
 await page.waitForURL(url => (url.searchParams.get('tab')||'overview')===key)
 await pane().waitFor()
}
async function closeModal() { await modal().locator('.n-card-header__close').click(); await page.waitForFunction(() => !document.querySelector('.n-modal')) }
async function api(route, method = 'GET', data) {
 const result = await page.evaluate(async ({ route, method, data, csrf }) => {
  const response = await fetch('/api/' + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: data === undefined ? undefined : JSON.stringify(data) })
  return { status: response.status, body: await response.json() }
 }, { route, method, data, csrf })
 assert.ok(result.status < 400, `${method} ${route}: ${result.status} ${result.body.error || ''}`)
 return result.body
}
async function untilApi(route, predicate) {
 for (let i = 0; i < 30; i++) { const data = await api(route); if (predicate(data)) return data; await page.waitForTimeout(100) }
 assert.fail('State did not settle: ' + route)
}
async function assertNoOverflow(label) {
 const dimensions = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
 assert.ok(dimensions.scrollWidth <= dimensions.width + 1, `${label}: document overflow ${JSON.stringify(dimensions)}`)
}
async function screenshot(filename) {
 await page.waitForTimeout(400)
 await page.waitForFunction(() => !document.querySelector('.n-message'), null, { timeout: 10000 })
 await page.evaluate(() => document.querySelectorAll('.n-data-table, .n-data-table *').forEach(element => { if (element.scrollWidth > element.clientWidth) element.scrollLeft = 0 }))
 await page.screenshot({ path: path.join(output, filename), fullPage: true })
}
async function downloadText(button) {
 const [download] = await Promise.all([page.waitForEvent('download'), button.click()])
 const chunks = [], stream = await download.createReadStream()
 for await (const chunk of stream) chunks.push(chunk)
 return { name: download.suggestedFilename(), text: Buffer.concat(chunks).toString('utf8') }
}
async function inventorySearch(value, kind = 'child') {
 await tab('库存管理'); await pane().locator('.inventory-switch').getByRole('button', { name: kind === 'master' ? /^主邮箱 / : /^子邮箱 / }).click()
 await pane().getByPlaceholder('搜索邮箱或备注').fill(value); await pane().getByRole('button', { name: '查询', exact: true }).click()
 await page.waitForTimeout(180)
}
async function selectInventory(value, kind) {
 await inventorySearch(value, kind)
 await pane().getByRole('button', { name: '清空选择', exact: true }).click()
 const row = pane().locator('.n-data-table-tr').filter({ has: page.getByText(value, { exact: true }) })
 await row.waitFor(); await row.getByRole('checkbox').check()
 await page.getByText(kind === 'master' ? '已选主邮箱 1 个，子邮箱 0 个' : '已选主邮箱 0 个，子邮箱 1 个', { exact: false }).waitFor()
}
function inventoryGroup(name) {
 return pane().locator('.inventory-groups .group-nav-item').filter({ has: page.getByText(name, { exact: true }) })
}
async function selectInventoryGroup(name) {
 const button = inventoryGroup(name)
 if (await button.getAttribute('aria-pressed') === 'true') return
 const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/admin/mail/children' && r.request().method() === 'GET')
 await button.click(); await response
 assert.equal(await button.getAttribute('aria-pressed'), 'true')
}
async function assertGroupCount(name, count) {
 await inventoryGroup(name).locator('.group-count').getByText(count.toLocaleString(), { exact: true }).waitFor()
}
async function createSelectedReserve(value) {
 await selectInventory(value, 'child'); await tab('出库记录')
 await select('方式', '预留'); await select('抽取', '随机'); await select('范围', '选中子邮箱 (1)'); await fill('子邮箱数量', 1)
 await pane().getByRole('button', { name: '创建出库单', exact: true }).click()
 await modal().getByRole('button', { name: '确认交付', exact: true }).waitFor()
 const all = await api('admin/mail/outbounds?limit=100')
 return all.items[0].id
}

try {
 if (process.env.QA_DIST_FILES === '1') {
  const dist = path.resolve(import.meta.dirname, '../dist')
  await page.route(base + '/admin/**', async requestRoute => {
   const pathname = decodeURIComponent(new URL(requestRoute.request().url()).pathname)
   const filename = pathname.startsWith('/admin/assets/') ? path.resolve(dist, pathname.slice('/admin/'.length)) : path.join(dist, 'index.html')
   assert.ok(filename.startsWith(dist + path.sep), 'Static path escaped the local distribution')
   await requestRoute.fulfill({ path: filename })
  })
 }
 await page.goto(base + '/admin/sales')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByRole('heading', { name: '销售工作台', exact: true }).waitFor()
 csrf = (await api('session')).csrf
 // Guards make an accidental send/run fail before it reaches the fixture.
 for (const route of ['**/api/admin/mail/inspections', '**/api/admin/mail/inspection-schedule/run']) await page.route(route, async requestRoute => {
  if (requestRoute.request().method() === 'POST') { sendRequests++; await requestRoute.abort('blockedbyclient') } else await requestRoute.continue()
 })

 if (process.env.QA_PHASE === 'screenshots') {
  const preview = await api('admin/mail/import/preview', 'POST', { folder_id: 0, files: [{ name: 'screenshots.txt', content: 'demo-master@example.net----qa-password\nalias-one@example.net----https://qa.example.net/one\nalias-two@example.net----https://qa.example.net/two' }] })
  await api('admin/mail/import/commit', 'POST', { token: preview.token })
  for (const width of [360, 1440]) {
   await page.setViewportSize({ width, height: width === 360 ? 844 : 1000 })
   await page.goto(base + '/admin/sales'); await page.getByRole('heading', { name: '销售工作台', exact: true }).waitFor()
   await tab('工作流概览'); await assertNoOverflow(`${width}/overview`); await screenshot(`sales-overview-${width}.png`)
   await tab('库存管理'); await assertNoOverflow(`${width}/inventory`)
   assert.equal(await pane().locator('.mail-cell').first().evaluate(element => getComputedStyle(element).display), 'flex', 'Inventory email cells must separate mailbox and supporting text')
   assert.equal(await pane().locator('.mail-cell small').first().evaluate(element => getComputedStyle(element).whiteSpace), 'nowrap', 'Inventory supporting text must truncate inside its table column')
   await screenshot(`sales-inventory-${width}.png`)
  }
  assert.deepEqual(pageErrors, []); console.log('PASS: settled overview/inventory screenshots at 360px and 1440px; no overflow or browser errors. Screenshots: ' + output)
 } else {

 if (process.env.QA_PHASE !== 'group') {
 for (const width of [360, 390, 768, 1440]) {
  await page.setViewportSize({ width, height: 1000 })
  for (const theme of ['light', 'dark']) {
   await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme)
   await page.goto(base + '/admin/sales'); await page.getByRole('heading', { name: '销售工作台', exact: true }).waitFor()
   for (const name of ['工作流概览', '库存管理', '账号映射', '出库记录', '导入库存', '检测任务', '检测配置']) { await tab(name); await page.waitForTimeout(80); await assertNoOverflow(`${width}/${theme}/${name}`) }
   await page.screenshot({ path: path.join(output, `sales-settings-${width}-${theme}.png`), fullPage: true })
  }
 }
 await page.setViewportSize({ width: 1440, height: 1000 })
 await tab('工作流概览')
 await pane().getByRole('button', { name: /同步销售库存/ }).click()
 await pane().getByText('启用的主账号', { exact: true }).waitFor()
 assert.ok((await api('admin/mail/mapping/preview')).summary.active_accounts > 0)

 // Paste import: duplicate and invalid lines remain visible in the preview.
 await tab('导入库存'); await pane().getByRole('button', { name: '打开导入窗口', exact: true }).click()
 await fill('TXT 内容', [master + '----qa-password', ...children.map((email, i) => `${email}----https://qa.example.net/inbox/${i}`), children[0] + '----https://qa.example.net/duplicate', 'invalid-child'].join('\n'), modal())
 const previewResponse = page.waitForResponse(r => r.url().endsWith('/api/admin/mail/import/preview') && r.request().method() === 'POST')
 await modal().getByRole('button', { name: '生成预览', exact: true }).click()
 const preview = await (await previewResponse).json()
 assert.equal(preview.report.added_masters, 1); assert.equal(preview.report.added_children, 6); assert.equal(preview.report.duplicates, 1); assert.equal(preview.report.invalid, 1)
 await modal().getByText(/子邮箱或 HTTP\(S\) 接码链接无效/).waitFor()
 await modal().getByRole('button', { name: '确认导入', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))

 // Upload all supported extensions, including a conflicting child ownership.
 await pane().getByRole('button', { name: '打开导入窗口', exact: true }).click()
 const csvMaster = `qa-csv-${suffix}@example.net`, jsonMaster = `qa-json-${suffix}@example.net`
 await modal().locator('input[type=file]').setInputFiles([
  { name: 'inventory.txt', mimeType: 'text/plain', buffer: Buffer.from(`${uploadedMaster}----qa-password\n${uploadedChild}----https://qa.example.net/file\n${children[0]}----https://qa.example.net/conflict`) },
  { name: 'inventory.csv', mimeType: 'text/csv', buffer: Buffer.from(`email,parent_email,link,raw,note\n${csvMaster},,,${csvMaster}----qa-password,csv\nqa-csv-child-${suffix}@example.net,${csvMaster},https://qa.example.net/csv,,`) },
  { name: 'inventory.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify([{ email: jsonMaster, parent_email: '', link: '', raw: jsonMaster + '----qa-password' }, { email: `qa-json-child-${suffix}@example.net`, parent_email: jsonMaster, link: 'https://qa.example.net/json' }])) },
 ])
 await modal().getByText(/已选 3 个文件/).waitFor()
 const filesPreviewResponse = page.waitForResponse(r => r.url().endsWith('/api/admin/mail/import/preview') && r.request().method() === 'POST')
 await modal().getByRole('button', { name: '生成预览', exact: true }).click()
 const filesPreview = await (await filesPreviewResponse).json()
 assert.equal(filesPreview.report.added_masters, 3); assert.equal(filesPreview.report.added_children, 3); assert.equal(filesPreview.report.conflicts, 1)
 await modal().getByText(/地址身份或主邮箱归属冲突/).waitFor()
 await modal().getByRole('button', { name: '确认导入', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))

 // Map active account records, then complete a sales detection link through the UI.
 await tab('账号映射'); await pane().getByRole('button', { name: '提交映射', exact: true }).click()
 await untilApi('admin/mail/mapping/preview', data => data.summary.mapped_links > 0)
 await inventorySearch('recipient000@example.net')
 await pane().locator('.n-data-table-tr').filter({ hasText: 'recipient000@example.net' }).getByRole('button', { name: '编辑', exact: true }).click()
 await select('链接模式', '自定义链接', modal()); await fill('接码链接', 'https://qa.example.net/mapped', modal()); await modal().getByRole('button', { name: '保存', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 const mapped = await api('admin/mail/children?q=recipient000%40example.net')
 assert.equal(mapped.items[0].link, 'https://qa.example.net/mapped'); assert.equal(mapped.items[0].sellable, true)

 // A lost successful response must retry with the same key and create only one order.
 await selectInventory(children[0], 'child'); await tab('出库记录')
 await select('范围', '选中子邮箱 (1)'); await fill('子邮箱数量', 1)
 const beforeOrders = await api('admin/mail/outbounds?limit=100'), keys = []; let loseNext = true, lostID
 await page.route('**/api/admin/mail/outbounds', async requestRoute => {
  if (requestRoute.request().method() !== 'POST') return requestRoute.continue()
  keys.push(requestRoute.request().postDataJSON().request_key)
  if (loseNext) { loseNext = false; const response = await requestRoute.fetch(); assert.equal(response.status(), 201); lostID = (await response.json()).id; await requestRoute.abort('connectionfailed') } else await requestRoute.continue()
 })
 await pane().getByRole('button', { name: '创建出库单', exact: true }).click()
 await page.getByText(/相同内容重试会复用本次请求/).waitFor()
 assert.equal((await api('admin/mail/outbounds?limit=100')).total, beforeOrders.total + 1)
 await pane().getByRole('button', { name: '创建出库单', exact: true }).click(); await modal().getByRole('button', { name: '确认交付', exact: true }).waitFor()
 assert.equal(keys.length, 2); assert.equal(keys[0], keys[1]); assert.equal((await api('admin/mail/outbounds?limit=100')).total, beforeOrders.total + 1)
 await page.unroute('**/api/admin/mail/outbounds')
 requests.push('lost-response idempotency')
 const delivered = await downloadText(modal().getByRole('button', { name: '下载交付文件', exact: true }))
 assert.ok(delivered.name.endsWith('.txt')); assert.ok(delivered.text.includes(children[0] + '----https://qa.example.net/inbox/0'))
 await modal().getByRole('button', { name: '确认交付', exact: true }).click(); await page.getByRole('button', { name: '确认交付', exact: true }).last().click()
 await untilApi(`admin/mail/outbounds/${lostID}`, data => data.status === 'sold')
 await modal().getByRole('button', { name: '选择全部未退库项', exact: true }).click(); await fill('操作原因', 'QA partial return', modal())
 await modal().getByRole('button', { name: /^退库选中 1 个$/ }).click(); await modal().getByRole('button', { name: '确认', exact: true }).click()
 const returned = await untilApi(`admin/mail/outbounds/${lostID}`, data => data.status === 'returned')
 assert.equal(returned.downloadable, false); assert.ok(returned.items.every(v => !v.link)); await closeModal()

 const cancelID = await createSelectedReserve(children[1])
 await fill('操作原因', 'QA cancel reservation', modal()); await modal().getByRole('button', { name: '取消此预留单', exact: true }).click(); await modal().getByRole('button', { name: '确认', exact: true }).click()
 await untilApi(`admin/mail/outbounds/${cancelID}`, data => data.status === 'cancelled'); await closeModal()
 }

 // Entire group sale, partial return, full return and restoring the master for resale.
 await selectInventory(master, 'master'); await tab('出库记录'); await select('方式', '出售'); await select('抽取', '整组')
 await pane().getByRole('button', { name: '创建出库单', exact: true }).click(); await modal().getByRole('button', { name: '选择全部未退库项', exact: true }).waitFor()
 const group = (await api('admin/mail/outbounds?limit=100')).items[0]
 const groupExport = await downloadText(modal().getByRole('button', { name: '下载交付文件', exact: true })); assert.ok(groupExport.text.startsWith(master + '----qa-password\n'))
 const firstItem = modal().locator('.n-data-table-tr').filter({ has: page.getByText(children[0], { exact: true }) })
 await firstItem.getByRole('checkbox').check(); await fill('操作原因', 'QA group partial return', modal())
 await modal().getByRole('button', { name: /^退库选中 1 个$/ }).click(); await modal().getByRole('button', { name: '确认', exact: true }).click()
 await untilApi(`admin/mail/outbounds/${group.id}`, data => data.status === 'partial_return')
 await modal().getByRole('button', { name: '选择全部未退库项', exact: true }).click(); await fill('操作原因', 'QA group full return', modal())
 await modal().getByRole('button', { name: /^退库选中 5 个$/ }).click(); await modal().getByRole('button', { name: '确认', exact: true }).click()
 await untilApi(`admin/mail/outbounds/${group.id}`, data => data.status === 'returned'); await closeModal()
 await inventorySearch(master, 'master')
 await pane().locator('.n-data-table-tr').filter({ has: page.getByText(master, { exact: true }) }).getByRole('button', { name: '编辑', exact: true }).click()
 await select('主邮箱状态', '在库', modal()); await fill('状态修改原因', 'QA restore group availability', modal()); await modal().getByRole('button', { name: '保存', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 assert.equal((await api('admin/mail/masters?q=' + encodeURIComponent(master))).items[0].sellable, 6)
 await screenshot('sales-inventory-1440.png')

 // Group navigation includes empty groups; children inherit their master's group.
 const folderName = 'QA sales ' + suffix, renamedFolder = folderName + ' renamed'
 await selectInventory(uploadedMaster, 'master')
 const beforeGroups = await api('admin/mail/folders')
 await assertGroupCount('全部库存', beforeGroups.total_masters); await assertGroupCount('未分类', beforeGroups.unclassified)
 await pane().getByRole('button', { name: '新建分组', exact: true }).first().click(); await modal().getByPlaceholder('分组名称').fill(folderName); await modal().getByRole('button', { name: '保存', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 await untilApi('admin/mail/folders', data => data.items.some(v => v.name === folderName))
 await assertGroupCount(folderName, 0); await selectInventoryGroup(folderName)
 await inventorySearch('', 'master')
 await pane().locator('.inventory-pagination').filter({ visible: true }).getByText('共 0 个主邮箱 · 每页 25 条', { exact: true }).waitFor()
 await pane().locator('.inventory-switch').getByRole('button', { name: /^子邮箱 / }).click(); await assertGroupCount(folderName, 0)
 await pane().locator('.inventory-pagination').filter({ visible: true }).getByText('共 0 个子邮箱 · 每页 25 条', { exact: true }).waitFor()
 await assertGroupCount('全部库存', beforeGroups.total_children); await assertGroupCount('未分类', beforeGroups.unclassified_children)
 await pane().getByRole('button', { name: '移动选中主邮箱', exact: true }).click(); await modal().locator('.n-base-selection').click(); await page.getByText(folderName, { exact: true }).filter({ visible: true }).last().click(); await modal().getByRole('button', { name: '确认移动', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 const movedGroups = await untilApi('admin/mail/folders', data => data.items.some(v => v.name === folderName && v.masters === 1 && v.children === 1))
 await assertGroupCount(folderName, 1)
 await pane().locator('.inventory-grid>section').last().getByText(uploadedChild, { exact: true }).waitFor()
 await pane().locator('.inventory-pagination').filter({ visible: true }).getByText('共 1 个子邮箱 · 每页 25 条', { exact: true }).waitFor()
 await inventorySearch(uploadedChild)
 await tab('检测任务'); await tab('库存管理')
 assert.equal(await inventoryGroup(folderName).getAttribute('aria-pressed'), 'true', 'Selected group survives sales submenu navigation')
 assert.equal(await pane().getByPlaceholder('搜索邮箱或备注').inputValue(), uploadedChild, 'Search survives sales submenu navigation')
 assert.equal(await pane().locator('.inventory-switch').getByRole('button', { name: /^子邮箱 / }).getAttribute('aria-pressed'), 'true')
 await pane().locator('.inventory-grid>section').last().getByText(uploadedChild, { exact: true }).waitFor()
 await inventorySearch('', 'master'); await assertGroupCount(folderName, 1)
 await pane().locator('.inventory-grid>section').first().getByText(uploadedMaster, { exact: true }).waitFor()
 await pane().locator('.inventory-pagination').filter({ visible: true }).getByText('共 1 个主邮箱 · 每页 25 条', { exact: true }).waitFor()
 assert.equal(movedGroups.items.find(v => v.name === folderName).children, 1)
 await pane().getByRole('button', { name: '重命名分组', exact: true }).click(); await modal().getByPlaceholder('分组名称').fill(renamedFolder); await modal().getByRole('button', { name: '保存', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 await untilApi('admin/mail/folders', data => data.items.some(v => v.name === renamedFolder))
 await pane().locator('.inventory-heading').getByRole('heading', { name: renamedFolder, exact: true }).waitFor(); await assertGroupCount(renamedFolder, 1)
 await pane().locator('.batch-toolbar .n-base-selection').click(); await page.locator('.n-base-select-option').filter({ hasText: /^JSON$/ }).filter({ visible: true }).click()
 const inventoryExport = await downloadText(pane().locator('.inventory-grid>section').first().getByRole('button', { name: '导出选中', exact: true }))
 assert.ok(inventoryExport.name.endsWith('.json')); assert.ok(JSON.parse(inventoryExport.text).some(v => v.email === uploadedChild))
 await pane().getByRole('button', { name: '删除预览', exact: true }).click(); await modal().getByText(/本次会永久删除 1 个主邮箱及 1 个子邮箱/).waitFor()
 await modal().getByRole('checkbox', { name: '确认永久删除以上邮箱', exact: true }).check(); await modal().getByRole('button', { name: '确认删除', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 assert.equal((await api('admin/mail/masters?q=' + encodeURIComponent(uploadedMaster))).total, 0)
 await assertGroupCount(renamedFolder, 0)
 await pane().getByRole('button', { name: '删除分组', exact: true }).click(); await modal().getByRole('button', { name: '删除并移到未分类', exact: true }).click()
 await untilApi('admin/mail/folders', data => !data.items.some(v => v.name === renamedFolder))
 await inventoryGroup(renamedFolder).waitFor({ state: 'detached' }); assert.equal(await inventoryGroup('全部库存').getAttribute('aria-pressed'), 'true')

 // Save fake SMTP fields only. This test never clicks a send/run confirmation.
 await tab('检测配置')
 const defaultSMTP = pane().locator('.settings-box').filter({ has: page.getByRole('heading', { name: '默认 SMTP 配置', exact: true }) })
 await fill('SMTP 服务器', 'smtp.example.net', defaultSMTP); await fill('端口', 465, defaultSMTP); await fill('发件邮箱', `qa-smtp-${suffix}@example.net`, defaultSMTP); await fill('SMTP 授权码', 'qa-fake-password', defaultSMTP)
 await defaultSMTP.getByRole('button', { name: '保存默认 SMTP', exact: true }).click()
 await untilApi('admin/mail/settings', data => data.configured && data.password_set)
 await pane().getByRole('button', { name: '刷新配置', exact: true }).click(); await page.waitForTimeout(150)
 assert.equal(await item('SMTP 授权码', defaultSMTP).locator('input').inputValue(), '')
 assert.equal(await item('SMTP 服务器', defaultSMTP).locator('input').inputValue(), 'smtp.example.net')
 await pane().getByRole('button', { name: '新增发件账号', exact: true }).click()
 await fill('SMTP 服务器', 'smtp.example.net', modal()); await fill('发件邮箱', `qa-pool-${suffix}@example.net`, modal()); await fill('SMTP 授权码', 'qa-pool-fake-password', modal())
 await modal().getByRole('button', { name: '保存账号', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 const senders = await api('admin/mail/senders'); assert.equal(senders.items.length, 1); assert.equal(senders.items[0].password_set, true); assert.equal(senders.items[0].password, undefined)
 await tab('检测任务'); await select('检测类型', '收信验证（发送邮件）')
 const tasksBefore = (await api('admin/mail/inspections')).total
 await pane().getByRole('button', { name: '确认发信并提交', exact: true }).click(); await modal().getByText('确认发送测试邮件', { exact: true }).waitFor()
 await modal().getByRole('button', { name: '取消', exact: true }).click()
 assert.equal((await api('admin/mail/inspections')).total, tasksBefore); assert.equal(sendRequests, 0)

 // The import and editing dialogs also fit a narrow phone viewport.
 await page.setViewportSize({ width: 360, height: 844 }); await tab('导入库存'); await pane().getByRole('button', { name: '打开导入窗口', exact: true }).click()
 await assertNoOverflow('360/import-dialog'); await page.screenshot({ path: path.join(output, 'sales-import-360.png'), fullPage: true }); await closeModal()
 await inventorySearch(children[0]); await pane().locator('.n-data-table-tr').filter({ hasText: children[0] }).getByRole('button', { name: '编辑', exact: true }).click()
 await assertNoOverflow('360/child-editor'); await page.screenshot({ path: path.join(output, 'sales-child-360.png'), fullPage: true }); await closeModal()
 await tab('工作流概览'); await screenshot('sales-overview-360.png')
 assert.deepEqual(pageErrors, [])
 console.log(process.env.QA_PHASE === 'group' ? 'PASS resumed group return/recovery, fake SMTP and sender settings, canceled inbox confirmation, narrow import/edit dialogs; no browser exceptions or send requests.' : 'PASS: 56 sales viewport/theme/tab checks; paste and TXT/CSV/JSON upload reports; account mapping and link completion; successful POST response loss reuses one request key/order; download, delivery, cancellation, partial/full returns and group recovery; fake SMTP/default/pool storage; inbox send confirmation cancellation; no external inspection or SMTP execution; no browser exceptions.')
 console.log('Screenshots: ' + output)
 }
} catch (error) {
 await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
 console.error('Sales browser test failed at ' + page.url())
 throw error
} finally { await browser.close() }
