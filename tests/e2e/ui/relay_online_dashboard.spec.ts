import { test, expect } from '@playwright/test'

const token = process.env.DO_AI_RELAY_TOKEN || 'doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff'
const baseURL = process.env.DO_AI_ONLINE_RELAY_URL || 'http://47.110.255.240:18787'

const stamp = Date.now()
const sessionId = `do-online-gui-${stamp}`
const sessionName = `online-gui-${stamp}`
const hostName = `online-gui-host-${stamp}`
const lastText = `online gui hello ${stamp}`

async function pushHeartbeat() {
  const resp = await fetch(`${baseURL}/api/v1/heartbeat`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Relay-Token': token,
    },
    body: JSON.stringify({
      session_id: sessionId,
      session_name: sessionName,
      host: hostName,
      cwd: '/tmp/online-gui',
      command: 'codex',
      state: 'running',
      updated_at: Math.floor(Date.now() / 1000),
      idle_seconds: 2,
      last_text: lastText,
    }),
  })
  if (!resp.ok) {
    throw new Error(`heartbeat failed: ${resp.status}`)
  }
}

test('online relay dashboard shows injected session', async ({ page }) => {
  await pushHeartbeat()
  await page.goto(baseURL)

  await expect(page.getByText('do-ai 在线会话看板')).toBeVisible()
  await expect(page.getByText(sessionName)).toBeVisible()
  await expect(page.getByText(hostName)).toBeVisible()
  await expect(page.getByText(lastText)).toBeVisible()
})
