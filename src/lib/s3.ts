// Read/write bucket access for sync and admin only. Public downloads never go through here (see sign.ts).
import {
  S3Client, ListObjectsV2Command, PutObjectCommand, GetObjectCommand, CopyObjectCommand,
  DeleteObjectCommand, HeadObjectCommand, PutBucketCorsCommand, GetBucketCorsCommand,
} from '@aws-sdk/client-s3'
import { getSignedUrl } from '@aws-sdk/s3-request-presigner'
import { BUCKET, S3_ENDPOINT, getSecrets } from './config.ts'

let client: S3Client | undefined
const s3 = () => client ??= new S3Client({
  endpoint: S3_ENDPOINT,
  region: 'us-east-1', // Spaces ignores it, the SDK needs one
  credentials: getSecrets().s3,
  requestChecksumCalculation: 'WHEN_REQUIRED', // Spaces rejects the SDK's default CRC32 headers on presigned PUTs
  responseChecksumValidation: 'WHEN_REQUIRED',
})

export interface S3Object { key: string; size: number; lastModified?: Date }

export async function listAll(prefix: string): Promise<S3Object[]> {
  const out: S3Object[] = []
  let ContinuationToken: string | undefined
  do {
    const res = await s3().send(new ListObjectsV2Command({ Bucket: BUCKET, Prefix: prefix, ContinuationToken }))
    for (const o of res.Contents || []) if (o.Key && !o.Key.endsWith('/')) out.push({ key: o.Key, size: o.Size || 0, lastModified: o.LastModified })
    ContinuationToken = res.NextContinuationToken
  } while (ContinuationToken)
  return out
}

/** Presigned PUT for browser uploads straight to Spaces (admin). Private ACL. */
export function presignPut(key: string, contentType: string, opts: { publicRead?: boolean } = {}) {
  return getSignedUrl(s3(), new PutObjectCommand({
    Bucket: BUCKET, Key: key, ContentType: contentType, ...(opts.publicRead ? { ACL: 'public-read' } : {}),
  }), { expiresIn: 3600, signableHeaders: new Set(['content-type', ...(opts.publicRead ? ['x-amz-acl'] : [])]) })
}

/** Presigned GET for admin tools (pdf.js previews). Never shown on public pages. */
export const presignGet = (key: string) => getSignedUrl(s3(), new GetObjectCommand({ Bucket: BUCKET, Key: key }), { expiresIn: 3600 })

export async function exists(key: string) {
  try { await s3().send(new HeadObjectCommand({ Bucket: BUCKET, Key: key })); return true } catch { return false }
}

export async function move(fromKey: string, toKey: string) {
  if (fromKey === toKey) return
  await s3().send(new CopyObjectCommand({
    Bucket: BUCKET, Key: toKey, CopySource: `${BUCKET}/${fromKey.split('/').map(encodeURIComponent).join('/')}`, ACL: 'private',
  }))
  await s3().send(new DeleteObjectCommand({ Bucket: BUCKET, Key: fromKey }))
}

export const remove = (key: string) => s3().send(new DeleteObjectCommand({ Bucket: BUCKET, Key: key }))

export const putObject = (key: string, body: Buffer | string, contentType: string, publicRead = false) =>
  s3().send(new PutObjectCommand({ Bucket: BUCKET, Key: key, Body: body, ContentType: contentType, ...(publicRead ? { ACL: 'public-read' } : {}) }))

export async function getCors() {
  try { return (await s3().send(new GetBucketCorsCommand({ Bucket: BUCKET }))).CORSRules || [] } catch { return [] }
}
export const putCors = (origins: string[]) => s3().send(new PutBucketCorsCommand({
  Bucket: BUCKET,
  CORSConfiguration: { CORSRules: [{ AllowedOrigins: origins, AllowedMethods: ['GET', 'PUT', 'HEAD'], AllowedHeaders: ['*'], ExposeHeaders: ['ETag', 'Content-Length', 'Content-Range', 'Accept-Ranges'], MaxAgeSeconds: 3600 }] },
}))
