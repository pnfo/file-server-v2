package main

// Read/write bucket access for sync and admin only. Public downloads never go through here (see sign.go).

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/cors"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var s3Client = sync.OnceValues(func() (*minio.Client, error) {
	k := getSecrets().S3
	return minio.New(s3Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(k.AccessKeyID, k.SecretAccessKey, ""),
		Secure:       true,
		Region:       "us-east-1",           // Spaces ignores it; set so the client doesn't look it up
		BucketLookup: minio.BucketLookupDNS, // tipitaka.sgp1.digitaloceanspaces.com, as the AWS SDK used
	})
})

func s3() *minio.Client {
	c, err := s3Client()
	if err != nil {
		panic(err) // only fails on a malformed endpoint
	}
	return c
}

func s3ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Minute)
}

type S3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

func s3ListAll(prefix string) ([]S3Object, error) {
	ctx, cancel := s3ctx()
	defer cancel()
	var out []S3Object
	for o := range s3().ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		if !strings.HasSuffix(o.Key, "/") {
			out = append(out, S3Object{o.Key, o.Size, o.LastModified})
		}
	}
	return out, nil
}

// s3PresignPut is a presigned PUT for browser uploads straight to Spaces (admin). Private unless publicRead.
// Content-Type (and x-amz-acl) are signed, so the browser must send exactly these headers.
func s3PresignPut(key, contentType string, publicRead bool) (string, error) {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	if publicRead {
		h.Set("x-amz-acl", "public-read")
	}
	ctx, cancel := s3ctx()
	defer cancel()
	u, err := s3().PresignHeader(ctx, http.MethodPut, bucket, key, time.Hour, url.Values{}, h)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// s3PresignGet is a presigned GET for admin tools (pdf.js previews). Never shown on public pages.
func s3PresignGet(key string) (string, error) {
	ctx, cancel := s3ctx()
	defer cancel()
	u, err := s3().PresignedGetObject(ctx, bucket, key, time.Hour, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func s3Exists(key string) bool {
	ctx, cancel := s3ctx()
	defer cancel()
	_, err := s3().StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	return err == nil
}

func s3Move(fromKey, toKey string) error {
	if fromKey == toKey {
		return nil
	}
	ctx, cancel := s3ctx()
	defer cancel()
	// new objects are private by default, like the ACL: 'private' the Node server passed
	if _, err := s3().CopyObject(ctx, minio.CopyDestOptions{Bucket: bucket, Object: toKey}, minio.CopySrcOptions{Bucket: bucket, Object: fromKey}); err != nil {
		return err
	}
	return s3().RemoveObject(ctx, bucket, fromKey, minio.RemoveObjectOptions{})
}

func s3Remove(key string) error {
	ctx, cancel := s3ctx()
	defer cancel()
	return s3().RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func s3PutObject(key string, body []byte, contentType string) error {
	ctx, cancel := s3ctx()
	defer cancel()
	_, err := s3().PutObject(ctx, bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: contentType})
	return err
}

// CORSRule has the field names the AWS SDK returned, which the admin page shows and reads back.
type CORSRule struct {
	ID             string   `json:"ID,omitempty"`
	AllowedHeaders []string `json:"AllowedHeaders,omitempty"`
	AllowedMethods []string `json:"AllowedMethods"`
	AllowedOrigins []string `json:"AllowedOrigins"`
	ExposeHeaders  []string `json:"ExposeHeaders,omitempty"`
	MaxAgeSeconds  int      `json:"MaxAgeSeconds,omitempty"`
}

func s3GetCors() []CORSRule {
	ctx, cancel := s3ctx()
	defer cancel()
	cfg, err := s3().GetBucketCors(ctx, bucket)
	out := []CORSRule{}
	if err != nil || cfg == nil {
		return out
	}
	for _, r := range cfg.CORSRules {
		out = append(out, CORSRule{r.ID, r.AllowedHeader, r.AllowedMethod, r.AllowedOrigin, r.ExposeHeader, r.MaxAgeSeconds})
	}
	return out
}

func s3PutCors(origins []string) error {
	ctx, cancel := s3ctx()
	defer cancel()
	return s3().SetBucketCors(ctx, bucket, cors.NewConfig([]cors.Rule{{
		AllowedOrigin: origins, AllowedMethod: []string{"GET", "PUT", "HEAD"}, AllowedHeader: []string{"*"},
		ExposeHeader: []string{"ETag", "Content-Length", "Content-Range", "Accept-Ranges"}, MaxAgeSeconds: 3600,
	}}))
}
