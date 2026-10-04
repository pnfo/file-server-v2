import { defineConfig } from 'astro/config'
import node from '@astrojs/node'
import tailwindcss from '@tailwindcss/vite'
import fs from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'

// pdf.js fetches its image decoders (JBig2, JPEG 2000: wasm), CMaps, standard fonts and ICC profiles at runtime.
// Copy them to public/fs-static/pdfjs-<version>/ (git-ignored) before dev/build; admin.ts passes the URLs to
// getDocument. The version in the path keeps nginx's 30-day /fs-static/ cache from mixing pdf.js releases.
const pdfjsAssets = {
  name: 'pdfjs-assets',
  hooks: {
    'astro:config:setup': () => {
      const pkg = path.dirname(createRequire(import.meta.url).resolve('pdfjs-dist/package.json'))
      const { version } = JSON.parse(fs.readFileSync(path.join(pkg, 'package.json'), 'utf8'))
      const out = 'public/fs-static'
      for (const d of fs.readdirSync(out)) if (d.startsWith('pdfjs-') && d !== `pdfjs-${version}`) fs.rmSync(path.join(out, d), { recursive: true })
      for (const d of ['wasm', 'cmaps', 'standard_fonts', 'iccs']) {
        fs.cpSync(path.join(pkg, d), path.join(out, `pdfjs-${version}`, d), {
          recursive: true, filter: f => !path.basename(f).startsWith('quickjs'), // quickjs: pdf.js scripting, not used
        })
      }
    },
  },
}

// One server for every site (/library, /cloud, and test aliases like /library-v2).
// The site is the first path segment, so `base` stays '/'. Built assets live under /fs-assets/
// so nginx can route them without touching the site prefixes.
export default defineConfig({
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  integrations: [pdfjsAssets],
  build: { assets: 'fs-assets' },
  // Origin checks are done in middleware: behind nginx the request URL is http://127.0.0.1,
  // which makes Astro's built-in check reject legitimate admin POSTs.
  security: { checkOrigin: false },
  server: { port: Number(process.env.PORT) || 4321 },
  vite: {
    plugins: [tailwindcss()],
    // pre-bundle pdf.js for the dev server; otherwise its first dynamic import in admin fails while Vite re-optimizes
    optimizeDeps: { include: ['pdfjs-dist'] },
    // emit .mjs assets (the pdf.js worker) as .js: nginx 1.18's mime.types has no mjs entry, serves them as
    // application/octet-stream, and the browser refuses to load them as a module worker
    // (Astro 7 only honours this per environment; it overrides top-level output naming with its own)
    environments: {
      client: {
        build: {
          rolldownOptions: {
            output: {
              assetFileNames: (info) =>
                info.names?.[0]?.endsWith('.mjs') ? 'fs-assets/[name].[hash].js' : 'fs-assets/[name].[hash][extname]',
            },
          },
        },
      },
    },
  },
})
