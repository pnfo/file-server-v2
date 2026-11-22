// Builds the browser assets for the Go server into server/dist/ (deployed as dist/ next to the binary):
//   fs-assets/   site JS (esbuild) and CSS (Tailwind), content-hashed, served with a 1-year cache
//   fs-static/   favicon and fonts
//   library/, cloud/   per-site public files (favicon, og image)
//   manifest.json      logical name -> hashed URL, read by the server at startup
// Usage: node scripts/build-assets.mjs
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import * as esbuild from 'esbuild'

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..')
const out = path.join(root, 'server/dist')
const assetsDir = path.join(out, 'fs-assets')
const hash = buf => createHash('sha256').update(buf).digest('base64url').slice(0, 8)

fs.rmSync(out, { recursive: true, force: true })
fs.mkdirSync(assetsDir, { recursive: true })

const js = await esbuild.build({
  entryPoints: { site: 'src/client/site.ts' },
  absWorkingDir: root, outdir: assetsDir, bundle: true, splitting: true, format: 'esm', minify: true,
  target: 'es2022', entryNames: '[name].[hash]', chunkNames: '[name].[hash]', metafile: true,
  logLevel: 'warning',
})
const manifest = {}
for (const [file, info] of Object.entries(js.metafile.outputs)) {
  if (info.entryPoint && /^src\/client\/site\.ts$/.test(info.entryPoint)) manifest[`${path.basename(info.entryPoint, '.ts')}.js`] = `/fs-assets/${path.basename(file)}`
}

// CSS: Tailwind scans the repo (git-ignored files excluded), so classes in server/templates, server/*.go and
// src/client are all picked up.
const tailwind = path.join(root, 'node_modules/.bin/tailwindcss')
for (const [name, input] of [['site', 'src/styles/global.css']]) {
  const tmp = path.join(out, `${name}.tmp.css`)
  execFileSync(tailwind, ['-i', input, '-o', tmp, '--minify'], { cwd: root, stdio: ['ignore', 'ignore', 'inherit'] })
  const buf = fs.readFileSync(tmp)
  const file = `${name}.${hash(buf)}.css`
  fs.renameSync(tmp, path.join(assetsDir, file))
  manifest[`${name}.css`] = `/fs-assets/${file}`
}

// static files
const copy = (from, to) => fs.cpSync(from, to, { recursive: true })
copy(path.join(root, 'public/fs-static'), path.join(out, 'fs-static'))
for (const site of ['library', 'cloud']) copy(path.join(root, 'public', site), path.join(out, site))

fs.writeFileSync(path.join(out, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
console.log(manifest)
