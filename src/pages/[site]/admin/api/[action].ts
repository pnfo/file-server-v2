import type { APIRoute } from 'astro'
import { AdminError, commitUpload, createFolder, deleteEntry, previewUploadUrls, saveMeta, savePreviews, sourceUrl, startUpload } from '../../../../lib/admin.ts'
import { getCors, putCors } from '../../../../lib/s3.ts'
import { json } from '../../../../lib/site.ts'
import { readCountsFile, syncFromS3 } from '../../../../lib/sync.ts'

// Admin JSON API. Middleware has already checked the session cookie and the x-fs-admin header.
export const POST: APIRoute = async ({ locals, params, request }) => {
  const s = locals.site
  if (!s) return json({ error: 'not found' }, 404)
  const store = s.store
  let b: any = {}
  try { b = await request.json() } catch {}
  const id = Number(b.id)
  try {
    switch (params.action) {
      case 'save': return json(await saveMeta(store, id, b))
      case 'publish': return json(await saveMeta(store, id, { isPublished: !!b.isPublished }))
      case 'delete': await deleteEntry(store, id); return json({ ok: true })
      case 'folder': return json(createFolder(store, Number(b.parentId) || 0, b.name))
      case 'upload-url': return json(await startUpload(store, { parentId: Number(b.parentId) || 0, fileName: String(b.fileName || ''), name: b.name, version: b.version, contentType: b.contentType }))
      case 'commit': return json(await commitUpload(store, { ...b, id, parentId: Number(b.parentId) || 0 }))
      case 'preview-urls': return json({ urls: await previewUploadUrls(store, id, Number(b.count)) })
      case 'previews': return json(await savePreviews(store, id, Array.isArray(b.pages) ? b.pages : [], Number(b.numPages) || null))
      case 'source-url': return json({ url: await sourceUrl(store, id) })
      case 'sync': {
        const counts = b.withCounts && s.cfg.idToInfoFile ? readCountsFile(s.cfg.idToInfoFile) : undefined
        return json(await syncFromS3(store, { dryRun: !!b.dryRun, counts }))
      }
      case 'cors-get': return json({ rules: await getCors() })
      case 'cors-set': {
        // add to the existing origins: setting it from prod must not drop http://localhost:4321 (dev, e2e) or vice versa
        const existing = (await getCors()).flatMap(r => r.AllowedOrigins ?? [])
        const origins = [...new Set([...existing, 'https://tipitaka.lk', new URL(request.url).origin, ...(Array.isArray(b.origins) ? b.origins : [])])]
          .filter(o => /^https?:\/\/[^/]+$/.test(o))
        await putCors(origins)
        return json({ rules: await getCors() })
      }
      default: return json({ error: `unknown action ${params.action}` }, 404)
    }
  } catch (err) {
    if (!(err instanceof AdminError)) console.error(`admin ${params.action} failed`, err)
    return json({ error: err instanceof Error ? err.message : String(err) }, err instanceof AdminError ? 400 : 500)
  }
}
