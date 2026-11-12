// Run against the opt-in TestBrowserFixture and its isolated temporary database.
// MAIL_LITE_QA_ADDR=127.0.0.1:18103 go test ./internal/core -run '^TestBrowserFixture$' -v -count=1 -timeout=20m
// QA_BASE_URL=http://127.0.0.1:18103 node node-controls-browser-test.mjs
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const { chromium } = await import('playwright').catch(() => import('playwright-core'))
const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18103'
assert.equal(new URL(base).hostname, '127.0.0.1', 'Use the isolated loopback fixture')
const output = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../.cache/screenshots')
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, colorScheme: 'light' })
const page = await context.newPage(), errors = [], screenshots = []
page.setDefaultTimeout(15000)
page.on('pageerror', error => errors.push(error.message))

async function adminAPI(route, method = 'GET', body) {
  const result = await page.evaluate(async ({ route, method, body }) => {
    const session = await fetch('/api/session').then(response => response.json())
    const response = await fetch('/api/' + route, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf },
      body: body === undefined ? undefined : JSON.stringify(body)
    })
    return { status: response.status, body: await response.json() }
  }, { route, method, body })
  assert.equal(result.status, 200, `Admin request ${method} ${route} failed`)
  return result.body
}

function cardFor(name) {
  return page.locator('.node-card').filter({ has: page.getByRole('heading', { name, exact: true }) })
}

function onlineCount() {
  return page.locator('.stat').filter({ hasText: '在线节点' }).locator('.stat-number').innerText().then(Number)
}

async function heartbeat(node, expected = 200) {
  const response = await context.request.post(base + '/internal/heartbeat', {
    headers: { 'X-Node-ID': node.id, Authorization: 'Bearer ' + node.token },
    data: { cursor: 0, error: '' }
  })
  assert.equal(response.status(), expected, 'Original node credential had unexpected access')
  return response.json()
}

async function refresh() {
  const response = page.waitForResponse(response => new URL(response.url()).pathname === '/api/admin/nodes' && response.request().method() === 'GET')
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  assert.equal((await response).status(), 200)
}

async function capture(name) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${name}: horizontal overflow`)
  const target = path.join(output, `node-controls-${name}.png`)
  await page.screenshot({ path: target, fullPage: true })
  screenshots.push(target)
}

try {
  await page.goto(base + '/admin/nodes')
  await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
  await page.getByRole('button', { name: '登录管理台' }).click()
  await page.getByRole('heading', { name: '节点管理', exact: true }).waitFor()

  for (const width of [1280, 390]) for (const theme of ['light', 'dark']) {
    await page.setViewportSize({ width, height: 1000 })
    await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme)
    await page.reload()
    await page.getByRole('heading', { name: '节点管理', exact: true }).waitFor()
    await refresh()
    assert.equal(await page.locator('html').getAttribute('data-theme'), theme)
    const baselineOnline = await onlineCount()
    const name = `浏览器节点 ${width} ${theme} ${Date.now()}`
    await page.getByRole('button', { name: '接入节点', exact: true }).click()
    await page.getByPlaceholder('例如：香港节点 01').fill(name)
    const creation = page.waitForResponse(response => new URL(response.url()).pathname === '/api/admin/nodes' && response.request().method() === 'POST')
    await page.getByRole('button', { name: '生成接入凭据', exact: true }).click()
    const createdResponse = await creation
    assert.equal(createdResponse.status(), 201)
    const node = await createdResponse.json() // Keep credentials only in memory; never print them or screenshot this modal.
    assert.ok(node.id && node.token, 'Created node lacks its one-time credentials')
    const snippet = await page.locator('.code-block').innerText()
    assert.ok(snippet.includes('MASTER_URL=' + base), 'Credential snippet has an incorrect primary URL')
    assert.ok(snippet.includes('ALLOW_INSECURE_NODE_HTTP=true'), 'HTTP primary lacks the explicit HTTP setting')
    assert.ok(snippet.includes('NODE_ID=' + node.id), 'Credential snippet does not match the created node')
    assert.ok(snippet.includes('NODE_TOKEN=' + node.token), 'Credential snippet does not match the created credential')
    await page.getByRole('button', { name: '已保存', exact: true }).click()
    await page.locator('.code-block').waitFor({ state: 'hidden' })
    const card = cardFor(name)
    await card.getByRole('button', { name: '停用节点', exact: true }).waitFor()

    await heartbeat(node)
    await refresh()
    await card.getByText('在线', { exact: true }).waitFor()
    assert.equal(await onlineCount(), baselineOnline + 1, 'Connected node was not counted online')

    const disableResponse = page.waitForResponse(response => new URL(response.url()).pathname === `/api/admin/nodes/${node.id}/status` && response.request().method() === 'PUT')
    await card.getByRole('button', { name: '停用节点', exact: true }).click()
    const disabled = await (await disableResponse).json()
    assert.equal(disabled.id, node.id)
    assert.equal(disabled.disabled, true)
    assert.ok(!disabled.token, 'Status update exposed the node credential')
    await card.getByText('已停用', { exact: true }).waitFor()
    await card.getByRole('button', { name: '启用节点', exact: true }).waitFor()
    assert.equal(await onlineCount(), baselineOnline, 'Disabled node was incorrectly counted online')
    assert.equal((await heartbeat(node, 403)).error, 'node_disabled')
    await capture(`${width}-${theme}-disabled`)

    const enableResponse = page.waitForResponse(response => new URL(response.url()).pathname === `/api/admin/nodes/${node.id}/status` && response.request().method() === 'PUT')
    await card.getByRole('button', { name: '启用节点', exact: true }).click()
    const enabled = await (await enableResponse).json()
    assert.equal(enabled.id, node.id, 'Enabling replaced the existing node')
    assert.equal(enabled.disabled, false)
    assert.ok(!enabled.token, 'Enabling exposed the existing node credential')
    await card.getByRole('button', { name: '停用节点', exact: true }).waitFor()
    await heartbeat(node) // The unchanged, original token works again.
    await refresh()
    await card.getByText('在线', { exact: true }).waitFor()
    const listed = (await adminAPI('admin/nodes')).items.find(item => item.id === node.id)
    assert.equal(listed.disabled, false)
    assert.ok(!listed.token, 'Node list exposed the credential')
    await capture(`${width}-${theme}-enabled`)

    await card.getByRole('button', { name: '永久撤销凭据', exact: true }).click()
    const revokeDialog = page.locator('.n-dialog')
    await revokeDialog.getByText(/临时下线请使用“停用节点”/).waitFor()
    const revokeResponse = page.waitForResponse(response => new URL(response.url()).pathname === `/api/admin/nodes/${node.id}` && response.request().method() === 'DELETE')
    await revokeDialog.getByRole('button', { name: '永久撤销', exact: true }).click()
    assert.equal((await revokeResponse).status(), 200)
    await card.getByText('已撤销', { exact: true }).waitFor()
    assert.equal(await card.getByRole('button', { name: '启用节点', exact: true }).count(), 0)
    assert.equal(await card.getByRole('button', { name: '停用节点', exact: true }).count(), 0)
    await heartbeat(node, 403)
    await capture(`${width}-${theme}-revoked`)

    await card.getByRole('button', { name: '删除记录', exact: true }).click()
    const deleteDialog = page.locator('.n-dialog')
    await deleteDialog.getByText(/原凭据仍然失效/).waitFor()
    const deleteResponse = page.waitForResponse(response => new URL(response.url()).pathname === `/api/admin/nodes/${node.id}/record` && response.request().method() === 'DELETE')
    await deleteDialog.getByRole('button', { name: '删除记录', exact: true }).click()
    assert.equal((await deleteResponse).status(), 200)
    await card.waitFor({ state: 'detached' })
    assert.ok(!(await adminAPI('admin/nodes')).items.some(item => item.id === node.id), 'Deleted node remained in the server list')
    await heartbeat(node, 403)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${width}/${theme}: overflow after deletion`)
  }
  assert.deepEqual(errors, [], 'Browser reported JavaScript errors')
  console.log(JSON.stringify({ result: 'pass', viewports: [1280, 390], themes: ['light', 'dark'], disposable_nodes_created_and_deleted: 4, screenshots, javascript_errors: errors }, null, 2))
} catch (error) {
  await page.screenshot({ path: path.join(output, 'node-controls-failure.png'), fullPage: true }).catch(() => {})
  throw error
} finally {
  await browser.close()
}
