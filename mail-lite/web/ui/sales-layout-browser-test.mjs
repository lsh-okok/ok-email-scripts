// Layout and loading recovery checks against the isolated TestBrowserFixture.
// Seeds fake inventory through the API; never run against a real service/database.
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18093'
const fixtureURL = new URL(base)
assert.ok(['127.0.0.1', 'localhost'].includes(fixtureURL.hostname) && fixtureURL.port === '18093', 'Layout writes are restricted to the local 18093 browser fixture')
const output = path.join(os.tmpdir(), 'mail-lite-sales-layout')
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1486, height: 1136 } })
const page = await context.newPage(), errors = [], failures = []
page.setDefaultTimeout(15000)
page.on('pageerror', e => errors.push(e.message))
page.on('response', r => { if (r.status() >= 400 && r.url().includes('/admin/assets/')) failures.push(r.url()) })
const sales = () => page.getByRole('heading', { name: '销售工作台', exact: true })
const pane = () => page.locator('.n-tab-pane').filter({visible:true}).last()
const activeInventory = () => page.locator('.inventory-grid>section').filter({ visible: true })
let csrf = '', layoutGroup
const suffix = Date.now().toString(), layoutGroupName = `QA 布局 ${suffix}`
async function api(route, method = 'GET', data) {
 const result = await page.evaluate(async ({ route, method, data, csrf }) => {
  const response = await fetch('/api/' + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: data === undefined ? undefined : JSON.stringify(data) })
  return { status: response.status, body: await response.json() }
 }, { route, method, data, csrf })
 assert.ok(result.status < 400, `${method} ${route}: ${result.status} ${result.body.error || ''}`)
 return result.body
}
async function seedLayoutInventory() {
 const fixtureMarker = await page.request.get(base + '/__qa/stats')
 assert.equal(fixtureMarker.status(), 200, 'The isolated browser-fixture marker is required before seeding')
 assert.equal(typeof await fixtureMarker.json(), 'object')
 csrf = (await api('session')).csrf
 assert.ok(csrf, 'Authenticated fixture session must provide CSRF')
 layoutGroup = await api('admin/mail/folders', 'POST', { name: layoutGroupName })
 const longNote = '布局验收：长备注应截断显示，不能撑开表格或者改变行高。'.repeat(20)
 const records = Array.from({ length: 32 }, (_, i) => {
  const master = `qa-layout-${String(i).padStart(2, '0')}-${suffix}-long-master-mailbox@example.net`
  const child = `qa-layout-${String(i).padStart(2, '0')}-${suffix}-long-child-mailbox@example.net`
  return [{ email: master, raw: master + '----fake-layout-password', note: longNote }, { email: child, parent_email: master, link: `https://qa.example.net/receive/${suffix}/${i}/${'long-path-'.repeat(i % 3 ? 10 : 1)}`, note: i % 3 ? longNote : '短备注' }]
 }).flat()
 const preview = await api('admin/mail/import/preview', 'POST', { folder_id: layoutGroup.id, files: [{ name: `layout-${suffix}.json`, content: JSON.stringify(records) }] })
 assert.equal(preview.report.added_masters, 32)
 assert.equal(preview.report.added_children, 32)
 await api('admin/mail/import/commit', 'POST', { token: preview.token })
 // Include the fixture's original source link so source/custom modes share the
 // same fixed row allocation; this never sends mail or performs HTTP detection.
 await api('admin/mail/mapping', 'POST', { account_ids: ['personal'], include_links: true })
 const mapping = await api('admin/mail/mapping/preview')
 const personal = mapping.accounts.find(account => account.source_id === 'personal')
 assert.ok(personal?.mailbox_id, 'Fixture source master must be mapped')
 await api('admin/mail/masters/batch', 'POST', { ids: [personal.mailbox_id], action: 'move', folder_id: layoutGroup.id })
 const children = await api(`admin/mail/children?folder=${layoutGroup.id}&limit=100`)
 assert.ok(children.total >= 33)
 const source = children.items.find(child => child.source_mapped && child.status === 'available')
 assert.ok(source, 'Fixture inventory must include a source-backed child')
 if (source.link_mode !== 'source') await api(`admin/mail/children/${source.id}`, 'PUT', { link_mode: 'source' })
 await api(`admin/mail/children/${children.items.find(child => !child.source_mapped).id}`, 'PUT', { note: longNote })
}
async function selectLayoutGroup() {
 const group = page.locator('.inventory-groups .group-nav-item').filter({ has: page.getByText(layoutGroupName, { exact: true }) })
 await group.waitFor()
 if (await group.getAttribute('aria-pressed') !== 'true') {
  await Promise.all([
   page.waitForResponse(response => { const url = new URL(response.url()); return url.pathname === '/api/admin/mail/children' && url.searchParams.get('folder') === String(layoutGroup.id) && response.ok() }),
   page.waitForResponse(response => { const url = new URL(response.url()); return url.pathname === '/api/admin/mail/masters' && url.searchParams.get('folder') === String(layoutGroup.id) && response.ok() }),
   group.click(),
  ])
 }
 await page.getByRole('heading', { name: layoutGroupName, exact: true }).waitFor()
 await page.waitForFunction(() => [...document.querySelectorAll('.inventory-grid>section')].filter(section => getComputedStyle(section).display !== 'none').every(section => section.querySelectorAll('tbody tr').length === 25))
}
async function assertPanelFillsMain(label) {
 const dimensions = await page.evaluate(() => {
  const main = document.querySelector('main'), panel = document.querySelector('.sales-panel')
  const m = main.getBoundingClientRect(), p = panel.getBoundingClientRect(), css = getComputedStyle(main)
  return { mainLeft: m.left + parseFloat(css.paddingLeft), mainRight: m.right - parseFloat(css.paddingRight), mainBottom: m.bottom - parseFloat(css.paddingBottom), panelLeft: p.left, panelRight: p.right, panelBottom: p.bottom, viewportHeight: innerHeight, documentHeight: document.documentElement.scrollHeight }
 })
 assert.ok(Math.abs(dimensions.panelLeft - dimensions.mainLeft) <= 2 && Math.abs(dimensions.panelRight - dimensions.mainRight) <= 2, `${label}: sales panel must fill the main content width ${JSON.stringify(dimensions)}`)
 assert.ok(Math.abs(dimensions.panelBottom - dimensions.mainBottom) <= 2, `${label}: sales panel must reach the main bottom padding ${JSON.stringify(dimensions)}`)
 assert.ok(dimensions.documentHeight <= dimensions.viewportHeight + 2, `${label}: desktop content must scroll inside its workspace ${JSON.stringify(dimensions)}`)
}
async function assertInventoryGeometry(label, desktop) {
 const section = activeInventory()
 const geometry = await section.evaluate(section => {
  const rect = element => { const r = element.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, height: r.height, left: r.left, right: r.right } }
  const host = section.querySelector('.inventory-table-host'), table = host.querySelector('.n-data-table')
  const rows = [...section.querySelectorAll('tbody tr')].filter(row => row.querySelector('.identity-cell'))
  const identity = rows.map(row => row.querySelector('.identity-cell').closest('td').getBoundingClientRect().width)
  const pagination = section.querySelector('.inventory-pagination'), selection = section.closest('.inventory').querySelector('.selection-summary')
  const email = rows.map(row => row.querySelector('.identity-cell strong')).find(element => element?.title.length > 45), emailCSS = email && getComputedStyle(email)
  return { host: rect(host), table: rect(table), pagination: rect(pagination), selection: rect(selection), rowHeights: rows.map(row => row.getBoundingClientRect().height), identityWidths: identity, rowCount: rows.length, emailTruncated: email && email.scrollWidth > email.clientWidth, emailTitle: email?.title, emailWhiteSpace: emailCSS?.whiteSpace, emailOverflow: emailCSS?.textOverflow, viewportHeight: innerHeight }
 })
 assert.equal(geometry.rowCount, 25, `${label}: first page should contain 25 fixture records`)
 assert.ok(geometry.identityWidths.every(width => Math.abs(width - 320) <= 1.5), `${label}: mailbox column must remain 320px ${JSON.stringify(geometry.identityWidths)}`)
 assert.ok(geometry.rowHeights.every(height => height >= 84 && height <= 86), `${label}: all rows should remain 84–86px ${JSON.stringify(geometry.rowHeights)}`)
 assert.ok(Math.max(...geometry.rowHeights) - Math.min(...geometry.rowHeights) <= 1, `${label}: link mode and long notes must not change row height`)
 assert.equal(geometry.emailWhiteSpace, 'nowrap')
 assert.equal(geometry.emailOverflow, 'ellipsis')
 assert.ok(geometry.emailTruncated && geometry.emailTitle.length > 45, `${label}: long addresses need ellipsis with the complete address available`)
 assert.ok(Math.abs(geometry.host.height - geometry.table.height) <= 3 && geometry.host.height >= 100, `${label}: table should fill its allocated host ${JSON.stringify(geometry)}`)
 if (desktop) {
  assert.ok(Math.abs(geometry.host.bottom - geometry.pagination.top) <= 3, `${label}: table should fill remaining space immediately above pagination`)
  assert.ok(geometry.pagination.bottom <= geometry.selection.top + 2 && geometry.selection.top - geometry.pagination.bottom <= 5, `${label}: pagination and selection summary should stay adjacent`)
  assert.ok(geometry.pagination.bottom <= geometry.viewportHeight && geometry.selection.bottom <= geometry.viewportHeight, `${label}: pagination and selection must stay in the viewport`)
 }
 const scrolling = await section.locator('.inventory-table-host').evaluate(async host => {
  const candidates = [...host.querySelectorAll('*')].filter(element => /auto|scroll/.test(getComputedStyle(element).overflowY) && element.scrollHeight > element.clientHeight + 100 && element.clientHeight > 50)
  const scroller = candidates.sort((a, b) => b.clientHeight - a.clientHeight)[0]
  if (!scroller) return null
  scroller.scrollTop = 0
  const row = scroller.querySelector('tbody tr'), before = row?.getBoundingClientRect().top
  scroller.scrollTop = 170
  await new Promise(resolve => requestAnimationFrame(resolve))
  const result = { scrollTop: scroller.scrollTop, movement: before === undefined ? 0 : before - row.getBoundingClientRect().top, remaining: scroller.scrollHeight - scroller.clientHeight }
  scroller.scrollTop = 0
  return result
 })
 assert.ok(scrolling && scrolling.scrollTop >= 100 && scrolling.movement >= 100, `${label}: vertical scrolling must move table rows inside the host ${JSON.stringify(scrolling)}`)
 if (!desktop) {
  const horizontal = await section.locator('.inventory-table-host').evaluate(async host => {
   const scroller = [...host.querySelectorAll('*')].find(element => /auto|scroll/.test(getComputedStyle(element).overflowX) && element.scrollWidth > element.clientWidth + 100 && element.clientWidth > 50)
   if (!scroller) return null
   scroller.scrollLeft = 140
   await new Promise(resolve => requestAnimationFrame(resolve))
   const result = { scrollLeft: scroller.scrollLeft, width: scroller.clientWidth, total: scroller.scrollWidth }
   scroller.scrollLeft = 0
   return result
  })
  assert.ok(horizontal && horizontal.scrollLeft >= 100, `${label}: narrow tables need working internal horizontal scrolling ${JSON.stringify(horizontal)}`)
 }
 assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${label}: table scrolling must not widen the document`)
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
try {
 await page.goto(base + '/admin/links')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.locator('.sidebar').waitFor()
 await seedLayoutInventory()
 await page.locator('.sidebar').getByRole('link', { name: '销售工作台', exact: true }).click()
 await sales().waitFor(); await page.getByText(/^更新于 /).waitFor()
 assert.deepEqual(failures, [], 'All lazy chunks must be served by the Go executable')
 for (const width of [360, 390, 768, 1486, 3263]) for (const theme of ['light', 'dark']) {
  await page.setViewportSize({ width, height: 1000 })
  await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme)
  await page.reload(); await sales().waitFor(); await tab('工作流概览'); await page.getByText(/^更新于 /).waitFor()
  assert.equal(await page.locator('.sales-summary').count(),0,'Sales statistics belong on Home')
  assert.equal(await page.locator('.n-tabs-nav').isVisible(),false,'Horizontal sales menu should be removed')
  assert.equal(await page.locator('.workflow-card').count(), 4)
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${width}/${theme}/overview overflow`)
  if (width >= 768) await assertPanelFillsMain(`${width}/${theme}/overview`)
  if (width === 360 || width === 390 || width === 1486 || width === 3263) await page.screenshot({ path: path.join(output, `overview-${width}-${theme}.png`), fullPage: true })
  await tab('库存管理')
  await selectLayoutGroup()
  for (const kind of ['子邮箱', '主邮箱']) {
   const button = page.locator('.inventory-switch').getByRole('button', { name: new RegExp('^' + kind + ' ') })
   await button.click(); assert.equal(await button.getAttribute('aria-pressed'), 'true')
   assert.equal(await page.locator('.inventory-grid>section').filter({ visible: true }).count(), 1)
   assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${width}/${theme}/${kind} overflow`)
   if (width >= 768) await assertPanelFillsMain(`${width}/${theme}/${kind}`)
   await assertInventoryGeometry(`${width}/${theme}/${kind}`, width >= 768)
   if (width === 360 || width === 390 || width === 1486 || width === 3263) await page.screenshot({ path: path.join(output, `inventory-${kind === '子邮箱' ? 'child' : 'master'}-${width}-${theme}.png`), fullPage: true })
  }
  await page.locator('.inventory-switch').getByRole('button', { name: /^子邮箱 / }).click()
  if (width === 360 || width === 390 || width === 1486 || width === 3263) await page.screenshot({ path: path.join(output, `inventory-${width}-${theme}.png`), fullPage: true })
  for (const name of ['账号映射', '出库记录', '导入库存', '检测任务', '检测配置']) {
   await tab(name)
   assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${width}/${theme}/${name} overflow`)
   if (width >= 768) await assertPanelFillsMain(`${width}/${theme}/${name}`)
  }
 }
 // Short desktop windows still keep pagination/selection visible while the
 // table, rather than the whole document, absorbs the vertical scroll.
 for (const theme of ['light', 'dark']) {
  await page.setViewportSize({ width: 1440, height: 720 })
  await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme)
  await page.reload(); await sales().waitFor(); await tab('工作流概览'); await page.getByText(/^更新于 /).waitFor()
  await tab('库存管理'); await selectLayoutGroup()
  for (const kind of ['子邮箱', '主邮箱']) {
   await page.locator('.inventory-switch').getByRole('button', { name: new RegExp('^' + kind + ' ') }).click()
   await assertPanelFillsMain(`1440x720/${theme}/${kind}`)
   await assertInventoryGeometry(`1440x720/${theme}/${kind}`, true)
  }
  await page.screenshot({ path: path.join(output, `inventory-short-1440-${theme}.png`), fullPage: true })
 }
 // Submenu state, active item and direct URLs survive page reloads and browser history.
 await page.setViewportSize({width:1486,height:900})
 await tab('账号映射'); await page.getByText('启用的主账号',{exact:true}).waitFor()
 await tab('出库记录'); await page.getByRole('heading',{name:'创建出库单',exact:true}).waitFor()
 await page.reload(); await page.getByRole('heading',{name:'创建出库单',exact:true}).waitFor()
 assert.equal(await page.locator('.sidebar-sales-sections a[aria-current="page"]').count(),1)
 assert.equal(await page.locator('.sidebar-sales-sections a[aria-current="page"]').innerText(),'出库记录')
 await tab('库存管理'); await page.goBack(); await page.getByRole('heading',{name:'创建出库单',exact:true}).waitFor()
 await page.goForward(); await page.locator('.inventory-switch').waitFor()
 const collapse=page.getByRole('button',{name:'收起销售工作台子菜单',exact:true})
 await collapse.focus(); await page.keyboard.press('Space')
 assert.equal(await page.locator('.sidebar-sales-sections').isVisible(),false)
 await page.goto(base+'/admin/home'); await page.reload()
 assert.equal(await page.getByRole('button',{name:'展开销售工作台子菜单',exact:true}).getAttribute('aria-expanded'),'false')
 // Home contains all sales statistics and the existing mailbox/access statistics.
 for(const width of [360,390,768,1486]) for(const theme of ['light','dark']) {
  await page.setViewportSize({width,height:1000})
  await page.evaluate(theme=>localStorage.setItem('mail-lite-theme',theme),theme)
  await page.goto(base+'/admin/home')
  await page.locator('.sales-summary .updated-at').filter({hasText:/^更新于 /}).waitFor()
  assert.equal(await page.locator('.sales-summary-stats>.stat').count(),4)
  assert.ok(await page.locator('.stock-legend').isVisible())
  assert.ok(await page.getByRole('heading',{name:'每条取信链接的访问统计',exact:true}).isVisible())
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),`${width}/${theme}/home overflow`)
  if(width===360||width===1486)await page.screenshot({path:path.join(output,`home-${width}-${theme}.png`),fullPage:true})
 }
 await page.route('**/api/admin/mail/stats',r=>r.fulfill({status:503,contentType:'application/json',body:'{"error":"QA sales stats unavailable"}'}))
 await page.reload(); await page.getByText(/销售统计加载失败/).waitFor()
 assert.ok(await page.locator('.dashboard-stats').isVisible())
 await page.unroute('**/api/admin/mail/stats'); await page.getByRole('button',{name:'重试销售统计',exact:true}).click()
 await page.getByText(/销售统计加载失败/).waitFor({state:'hidden'})
 await page.locator('.sales-summary').getByRole('link',{name:'管理销售库存',exact:true}).click()
 await page.locator('.inventory-switch').waitFor()
 await tab('工作流概览')
 // One unavailable API leaves the successful panels visible, with an actionable retry.
 await page.route('**/api/admin/mail/inspections?*', r => r.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"QA temporary unavailable"}' }))
 await page.reload(); await sales().waitFor()
 await page.getByText(/部分数据加载失败：检测任务/).waitFor()
 assert.ok(await page.locator('.workflow-grid').isVisible())
 await page.unroute('**/api/admin/mail/inspections?*')
 await page.getByRole('button', { name: '重新加载', exact: true }).click()
 await page.getByText(/部分数据加载失败/).waitFor({ state: 'hidden' })

 // Start a fresh document to avoid the module cache, then simulate the original lazy-loading failure.
 await page.setViewportSize({ width: 1486, height: 1136 })
 await page.goto(base + '/admin/links')
 await page.route('**/admin/assets/Sales-*.js', r => r.fulfill({ status: 404, body: 'QA missing chunk' }))
 await page.locator('.sidebar').getByRole('link', { name: '销售工作台', exact: true }).click()
 await page.getByText('页面资源加载失败，请重新加载页面后重试。', { exact: false }).waitFor()
 const reload = page.getByRole('link', { name: '重新加载页面', exact: true })
 assert.equal(await reload.getAttribute('href'), '/admin/sales')
 await page.unroute('**/admin/assets/Sales-*.js')
 await reload.click(); await sales().waitFor()
 assert.deepEqual(errors, [])
 console.log('PASS: isolated API-seeded 32+ master/child inventory; sidebar navigation; embedded lazy chunks; 10 sales and 8 Home viewport/theme layouts plus 1440x720; full-width/full-height desktop panels; fixed 320px mailbox columns and 84–86px rows; internal table scrolling with visible pagination/selection; collapsible sidebar, URL reload/history; Home sales summaries and retry; partial API failure and retry; missing route chunk recovery. Screenshots: ' + output)
} catch (e) {
 await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
 throw e
} finally { await browser.close() }
