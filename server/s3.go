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
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var s3Client = sync.OnceValues(func() (*minio.Client, error) {
	k := getSecrets().S3
	return minio.New(k.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(k.AccessKeyID, k.SecretAccessKey, ""),
		Secure:       true,
		Region:       "auto", // R2's only region; set so the client doesn't look it up
		BucketLookup: minio.BucketLookupPath,
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
	for o := range s3().ListObjects(ctx, bucketFor(prefix), minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		if !strings.HasSuffix(o.Key, "/") {
			out = append(out, S3Object{o.Key, o.Size, o.LastModified})
		}
	}
	return out, nil
}

// s3PresignPut is a presigned PUT for browser uploads straight to R2 (admin). R2 has no object ACLs: who can
// read an object is decided by its bucket and, for the private bucket, by the Worker.
// Content-Type is signed, so the browser must send exactly this header.
func s3PresignPut(key, contentType string) (string, error) {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	ctx, cancel := s3ctx()
	defer cancel()
	u, err := s3().PresignHeader(ctx, http.MethodPut, bucketFor(key), key, time.Hour, url.Values{}, h)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// s3PresignGet is a presigned GET for admin tools (pdf.js previews). Never shown on public pages.
func s3PresignGet(key string) (string, error) {
	ctx, cancel := s3ctx()
	defer cancel()
	u, err := s3().PresignedGetObject(ctx, bucketFor(key), key, time.Hour, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func s3Exists(key string) bool {
	ctx, cancel := s3ctx()
	defer cancel()
	_, err := s3().StatObject(ctx, bucketFor(key), key, minio.StatObjectOptions{})
	return err == nil
}

// s3Move is a variable so tests can record moves instead of calling the bucket.
var s3Move = s3MoveObject

func s3MoveObject(fromKey, toKey string) error {
	if fromKey == toKey {
		return nil
	}
	ctx, cancel := s3ctx()
	defer cancel()
	if _, err := s3().CopyObject(ctx, minio.CopyDestOptions{Bucket: bucketFor(toKey), Object: toKey}, minio.CopySrcOptions{Bucket: bucketFor(fromKey), Object: fromKey}); err != nil {
		return err
	}
	return s3().RemoveObject(ctx, bucketFor(fromKey), fromKey, minio.RemoveObjectOptions{})
}

func s3Remove(key string) error {
	ctx, cancel := s3ctx()
	defer cancel()
	return s3().RemoveObject(ctx, bucketFor(key), key, minio.RemoveObjectOptions{})
}

func s3PutObject(key string, body []byte, contentType string) error {
	ctx, cancel := s3ctx()
	defer cancel()
	_, err := s3().PutObject(ctx, bucketFor(key), key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: contentType})
	return err
}
