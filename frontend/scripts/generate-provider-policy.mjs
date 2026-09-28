import { readFile, writeFile, access } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
const root = new URL('../../', import.meta.url)
const canonical = new URL('backend/internal/providerpolicy/domains.json', root)
const output = new URL('frontend/src/shared/api/generated/provider-policy.json', root)
let data
try { await access(canonical); data = await readFile(canonical, 'utf8') }
catch { data = await readFile(output, 'utf8') }
const policy = JSON.parse(data)
const validHost = /^(?:\*\.)?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{1,62}$/
for (const kind of ['images', 'tickets']) {
  if (!Array.isArray(policy[kind]) || !policy[kind].length || policy[kind].some(host => !validHost.test(host))) throw new Error(`Invalid ${kind} domain policy`)
}
const check = process.argv.includes('--check')
async function emit(target, expected) {
 if (check) {
  if (await readFile(target, 'utf8') !== expected) throw new Error(`Stale generated provider policy: ${fileURLToPath(target)}`)
 } else await writeFile(target, expected)
}
await emit(output, JSON.stringify(policy, null, 2) + '\n')
for (const relative of ['frontend/nginx.conf', 'deploy/nginx/worknet.team.conf']) {
 const target = new URL(relative, root)
 try { await access(target) } catch { continue }
 const config = await readFile(target, 'utf8')
 const updated = config.replace(/img-src [^;]+;/g, `img-src 'self' data: ${policy.images.map(host => `https://${host}`).join(' ')};`)
 await emit(target, updated)
}
console.log(`Provider domain policy generated from ${fileURLToPath(canonical)}`)
