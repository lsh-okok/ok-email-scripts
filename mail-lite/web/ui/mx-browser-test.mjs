// Run against TestMXBrowserFixture; SMTP and HTTP are local test services only.
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import net from 'node:net'
const { chromium } = await import('playwright').catch(() => import('playwright-core'))
const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18096'
assert.equal(new URL(base).hostname, '127.0.0.1', 'Use the isolated loopback fixture')
const output = path.join(os.tmpdir(), 'mail-lite-mx-browser')
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), errors = []
page.on('pageerror', error => errors.push(error.message))
page.setDefaultTimeout(15000)
async function api(route, method = 'GET', body) {
 const result = await page.evaluate(async ({route, method, body}) => {
  const session = await fetch('/api/session').then(r => r.json())
  const response = await fetch('/api/' + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf }, body: body === undefined ? undefined : JSON.stringify(body) })
  return { status: response.status, body: await response.json() }
 }, { route, method, body })
 assert.ok([200, 202].includes(result.status), `${route}: ${JSON.stringify(result.body)}`)
 return result.body
}
async function deliver() {
 const socket = net.createConnection({ host: '127.0.0.1', port: Number(process.env.QA_SMTP_PORT || 18526) })
 socket.setTimeout(10000, () => socket.destroy(new Error('SMTP timeout')))
 let buffer = '', pending
 socket.on('data', data => { buffer += data; if (pending && /(?:^|\r\n)\d{3} [^\r\n]*\r\n$/.test(buffer)) { const value = buffer; buffer = ''; const resolve = pending; pending = null; resolve(value) } })
 const response = () => new Promise((resolve, reject) => { pending = resolve; socket.once('error', reject); if (buffer && /(?:^|\r\n)\d{3} [^\r\n]*\r\n$/.test(buffer)) { const value = buffer; buffer = ''; pending = null; resolve(value) } })
 async function command(text, code) { const next = response(); socket.write(text + '\r\n'); assert.ok((await next).trim().split('\r\n').at(-1).startsWith(String(code)), text) }
 try {
  assert.ok((await response()).startsWith('220'))
  await command('EHLO browser.qa.test', 250)
  await command('MAIL FROM:<verify@sender.test>', 250)
  await command('RCPT TO:<alice+qa@browser.test>', 250)
  await command('DATA', 354)
  await command('From: verify@sender.test\r\nTo: forged@browser.test\r\nSubject: MX browser verification\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBrowser code: 246810\r\n.', 250)
  await command('QUIT', 221)
 } finally { socket.destroy() }
}
try {
 await page.goto(base + '/admin/accounts')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByRole('heading', { name: '邮箱管理', exact: true }).waitFor()
 await page.getByRole('button', { name: '添加邮箱', exact: true }).click()
 const form = page.locator('.form-modal')
 await form.locator('.n-select').first().click()
 await page.getByText('MX 域名收信', { exact: true }).click()
 await form.getByPlaceholder('example.com', { exact: true }).fill('browser.test')
 assert.equal(await form.getByPlaceholder('xxxx-xxxx-xxxx-xxxx').count(), 0)
 await form.getByRole('button', { name: '保存邮箱', exact: true }).click()
 await page.getByRole('button', { name: '打开邮箱 postmaster@browser.test', exact: true }).waitFor()
 const accounts = await api('admin/accounts?provider=mx'), account = accounts.items[0]
 assert.equal(account.mx_retention_days, 30)
 // Generate real links through the normal import job, before SMTP delivery.
 const job = await api('admin/import', 'POST', { kind: 'links', account_id: account.id, text: 'alice+qa@browser.test\nforged@browser.test' })
 for (let attempt = 0; attempt < 100; attempt++) {
  const jobs = await api('admin/jobs'), entry = jobs.items.find(item => item.id === job.id)
  if (entry && !['queued', 'running'].includes(entry.status)) { assert.equal(entry.failed, 0); break }
  await page.waitForTimeout(100)
 }
 const links = await api('admin/links?account_id=' + account.id)
 assert.equal(links.total, 2)
 const link = links.items.find(item => item.recipient === 'alice+qa@browser.test')
 const forged = links.items.find(item => item.recipient === 'forged@browser.test')
 await deliver()
 const mail = await context.request.get(base + new URL(link.json_url, base).pathname + '?details=1')
 assert.equal(mail.status(), 200)
 assert.match((await mail.json()).items[0].text, /246810/)
 const other = await context.request.get(base + new URL(forged.json_url, base).pathname + '?details=1')
 assert.equal((await other.json()).items.length, 0)
 const mapping = await api('admin/mail/mapping', 'POST', { account_ids: [account.id], include_links: true })
 assert.equal(mapping.created_links, 2)
 const children = await api('admin/mail/children')
 assert.ok(children.items.some(child => child.email === 'alice+qa@browser.test' && child.link_mode === 'source'))
 await page.reload()
 await page.getByRole('button', { name: '查看邮件 MX browser verification', exact: true }).waitFor()
 await page.getByRole('button', { name: '查看邮件 MX browser verification', exact: true }).click()
 await page.frameLocator('iframe[title="邮件正文"]').getByText('Browser code: 246810').waitFor()
 await page.getByRole('tab', { name: '邮箱详情', exact: true }).click()
 await page.getByText('SMTP 收信服务运行中', { exact: true }).waitFor()
 await page.getByRole('heading', { name: 'DNS 配置', exact: true }).waitFor()
 for (const width of [360, 390, 768, 1440]) for (const theme of ['light', 'dark']) {
  await page.setViewportSize({ width, height: 1000 })
  await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme)
  await page.reload()
  await page.getByRole('button', { name: '打开邮箱 postmaster@browser.test', exact: true }).click()
  if (width <= 1100) {
   await page.getByRole('button', { name: '查看邮件 MX browser verification', exact: true }).click()
  }
  await page.getByRole('tab', { name: '邮箱详情', exact: true }).click()
  await page.getByText('SMTP 收信服务运行中', { exact: true }).waitFor()
  await page.getByRole('heading', { name: 'DNS 配置', exact: true }).scrollIntoViewIfNeeded()
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${width}/${theme}: overflow`)
  await page.screenshot({ path: path.join(output, `${width}-${theme}.png`), fullPage: true })
 }
 // The public HTML page uses the same envelope isolation and rendered body.
 await page.goto(base + new URL(link.url, base).pathname)
 await page.locator('.mail-text').getByText('Browser code: 246810').waitFor()
 assert.deepEqual(errors, [])
 console.log(JSON.stringify({ ok: true, widths: [360, 390, 768, 1440], themes: ['light', 'dark'], screenshots: output }))
} finally {
 await context.request.post(base + '/__qa/mx/stop').catch(() => {})
 await browser.close()
}
