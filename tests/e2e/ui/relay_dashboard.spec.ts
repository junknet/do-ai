import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import path from 'node:path'
import { promises as fs } from 'node:fs'
import { fileURLToPath } from 'node:url'

const token = 'doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff'
const port = 19788
const baseURL = `http://127.0.0.1:${port}`

let relayProc: ReturnType<typeof spawn> | null = null
const currentDir = path.dirname(fileURLToPath(import.meta.url))

async function waitForHealthz(retry = 50) {
  for (let i = 0; i < retry; i += 1) {
    try {
      const resp = await fetch(`${baseURL}/healthz`)
      if (resp.ok) return
    } catch {
      // ignore
    }
    await new Promise((r) => setTimeout(r, 150))
  }
  throw new Error('relay healthz timeout')
}

test.beforeAll(async () => {
  const root = path.resolve(currentDir, '..', '..', '..')
  const binDir = path.join(root, 'tmp')
  const binPath = path.join(binDir, 'do-ai-gui-test')

  await fs.mkdir(binDir, { recursive: true })
  await new Promise<void>((resolve, reject) => {
    const build = spawn('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', binPath, './src'], {
      cwd: root,
      stdio: 'inherit',
    })
    build.on('exit', (code) => {
      if (code === 0) resolve()
      else reject(new Error(`go build failed: ${code}`))
    })
  })

  relayProc = spawn(binPath, ['relay', '--listen', `127.0.0.1:${port}`, '--token', token], {
    cwd: root,
    stdio: 'inherit',
  })

  await waitForHealthz()

  await fetch(`${baseURL}/api/v1/heartbeat`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Relay-Token': token,
    },
    body: JSON.stringify({
      session_id: 'do-gui-test-1',
      session_name: 'gui-case',
      host: 'host-gui',
      cwd: '/tmp/gui',
      command: 'codex',
      state: 'running',
      updated_at: Math.floor(Date.now() / 1000),
      idle_seconds: 3,
      last_text: 'gui hello',
    }),
  })
})

test.afterAll(async () => {
  if (relayProc) {
    relayProc.kill('SIGTERM')
    relayProc = null
  }
})

test('dashboard shows online session', async ({ page }) => {
  await page.goto(baseURL)

  await expect(page.getByText('do-ai 在线会话看板')).toBeVisible()
  await expect(page.getByText('gui-case')).toBeVisible()
  await expect(page.getByText('host-gui')).toBeVisible()
  await expect(page.getByText('gui hello')).toBeVisible()

  await page.fill('input[placeholder*="过滤"]', 'gui-case')
  await expect(page.getByText('gui-case')).toBeVisible()
})
