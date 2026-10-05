// Bucket checks for the end-to-end test (read and list only; the test deletes through the admin API).
import fs from 'node:fs'
import { HeadObjectCommand, ListObjectsV2Command, S3Client } from '@aws-sdk/client-s3'

const BUCKET = 'tipitaka'
const { s3: credentials } = JSON.parse(fs.readFileSync(new URL('../../config/secrets.json', import.meta.url), 'utf-8'))
const client = new S3Client({ endpoint: 'https://sgp1.digitaloceanspaces.com', region: 'us-east-1', credentials })

export async function exists(key) {
  try { await client.send(new HeadObjectCommand({ Bucket: BUCKET, Key: key })); return true } catch { return false }
}

export async function listAll(prefix) {
  const out = []
  let ContinuationToken
  do {
    const res = await client.send(new ListObjectsV2Command({ Bucket: BUCKET, Prefix: prefix, ContinuationToken }))
    for (const o of res.Contents || []) out.push({ key: o.Key, size: o.Size })
    ContinuationToken = res.NextContinuationToken
  } while (ContinuationToken)
  return out
}
