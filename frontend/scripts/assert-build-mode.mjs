import { readFileSync } from 'node:fs'

const expected = process.argv[2]
if (expected !== 'demo' && expected !== 'live') {
  throw new Error('expected build mode argument: demo or live')
}

const html = readFileSync(new URL('../dist/index.html', import.meta.url), 'utf8')
const marker = `<meta name="proxy-console-mode" content="${expected}"`
if (!html.includes(marker)) {
  throw new Error(`built index does not declare ${expected} mode`)
}

process.stdout.write(`verified ${expected} build mode\n`)
