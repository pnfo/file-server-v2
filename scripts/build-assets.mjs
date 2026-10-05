// Builds the browser assets for the Go server into server/dist/ (deployed as dist/ next to the binary):
//   fs-assets/   site/admin JS (esbuild) and CSS (Tailwind), content-hashed, served with a 1-year cache
//   fs-static/   favicon, fonts and the pdf.js decoders/fonts/worker the admin previews load
//   library/, cloud/   per-site public files (favicon, og image)
//   manifest.json      logical name -> hashed URL, read by the server at startup
// Usage: node scripts/build-assets.mjs
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import * as esbuild from 'esbuild'

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..')
const out = path.join(root, 'server/dist')
const assetsDir = path.join(out, 'fs-assets')
const require = createRequire(import.meta.url)
const hash = buf => createHash('sha256').update(buf).digest('base64url').slice(0, 8)

fs.rmSync(out, { recursive: true, force: true })
fs.mkdirSync(assetsDir, { recursive: true })

// pdf.js worker: `import url from '...pdf.worker.min.mjs?url'` (Vite syntax in admin.ts) -> a hashed copy and its URL.
// Emitted as .js: nginx 1.18's mime.types has no mjs entry, and browsers refuse octet-stream module workers.
const urlImports = {
  name: 'url-imports',
  setup(build) {
    build.onResolve({ filter: /\?url$/ }, args => ({
      path: require.resolve(args.path.replace(/\?url$/, ''), { paths: [args.resolveDir] }), namespace: 'asset-url',
    }))
    build.onLoad({ filter: /.*/, namespace: 'asset-url' }, args => {
      const buf = fs.readFileSync(args.path)
      const name = `${path.basename(args.path).replace(/\.m?js$/, '')}.${hash(buf)}.js`
      fs.writeFileSync(path.join(assetsDir, name), buf)
      return { contents: `export default ${JSON.stringify(`/fs-assets/${name}`)}`, loader: 'js' }
    })
  },
}

const js = await esbuild.build({
  entryPoints: { site: 'src/client/site.ts', admin: 'src/client/admin.ts' },
  absWorkingDir: root, outdir: assetsDir, bundle: true, splitting: true, format: 'esm', minify: true,
  target: 'es2022', entryNames: '[name].[hash]', chunkNames: '[name].[hash]', metafile: true, plugins: [urlImports],
  logLevel: 'warning',
})
const manifest = {}
for (const [file, info] of Object.entries(js.metafile.outputs)) {
  if (info.entryPoint && /^src\/client\/(site|admin)\.ts$/.test(info.entryPoint)) manifest[`${path.basename(info.entryPoint, '.ts')}.js`] = `/fs-assets/${path.basename(file)}`
}

// CSS: Tailwind scans the repo (git-ignored files excluded), so classes in server/templates, server/*.go and
// src/client are all picked up.
const tailwind = path.join(root, 'node_modules/.bin/tailwindcss')
for (const [name, input] of [['site', 'src/styles/global.css'], ['admin', 'src/styles/admin-bundle.css']]) {
  const tmp = path.join(out, `${name}.tmp.css`)
  execFileSync(tailwind, ['-i', input, '-o', tmp, '--minify'], { cwd: root, stdio: ['ignore', 'ignore', 'inherit'] })
  const buf = fs.readFileSync(tmp)
  const file = `${name}.${hash(buf)}.css`
  fs.renameSync(tmp, path.join(assetsDir, file))
  manifest[`${name}.css`] = `/fs-assets/${file}`
}

// static files; pdf.js assets go under a versioned path (nginx caches /fs-static/ for 30 days)
const copy = (from, to, filter) => fs.cpSync(from, to, { recursive: true, filter })
copy(path.join(root, 'public/fs-static'), path.join(out, 'fs-static'), f => !path.basename(f).startsWith('pdfjs-'))
for (const site of ['library', 'cloud']) copy(path.join(root, 'public', site), path.join(out, site))
const pdfjs = path.dirname(require.resolve('pdfjs-dist/package.json'))
const { version } = JSON.parse(fs.readFileSync(path.join(pdfjs, 'package.json'), 'utf8'))
for (const d of ['wasm', 'cmaps', 'standard_fonts', 'iccs']) {
  copy(path.join(pdfjs, d), path.join(out, 'fs-static', `pdfjs-${version}`, d), f => !path.basename(f).startsWith('quickjs'))
}

fs.writeFileSync(path.join(out, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
console.log(manifest)
