package minio

import (
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"combox-backend/internal/config"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	bucket string
	core   *minio.Core
	c      *minio.Client
}

func New(cfg config.MinIOConfig) (*Client, error) {
	endpoint := strings.TrimSpace(cfg.APIInternal)
	bucket := strings.TrimSpace(cfg.Bucket)
	user := strings.TrimSpace(cfg.RootUser)
	pass := strings.TrimSpace(cfg.RootPassword)

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: cfg.Secure,
		Region: strings.TrimSpace(cfg.Region),
	}
	core, err := minio.NewCore(endpoint, opts)
	if err != nil {
		return nil, err
	}
	c, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, err
	}
	return &Client{bucket: bucket, core: core, c: c}, nil
}

func (c *Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.bucket
}

func (c *Client) NewMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error) {
	if c == nil || c.core == nil {
		return "", nil
	}
	opts := minio.PutObjectOptions{ContentType: strings.TrimSpace(contentType)}
	return c.core.NewMultipartUpload(ctx, c.bucket, strings.TrimSpace(objectKey), opts)
}

func (c *Client) PresignUploadPart(ctx context.Context, objectKey, uploadID string, partNumber int, expires time.Duration) (string, error) {
	if c == nil || c.c == nil {
		return "", nil
	}
	params := make(url.Values)
	params.Set("uploadId", strings.TrimSpace(uploadID))
	params.Set("partNumber", strconv.Itoa(partNumber))
	u, err := c.c.Presign(ctx, "PUT", c.bucket, strings.TrimSpace(objectKey), expires, params)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

type CompletePart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

func (c *Client) CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []CompletePart, contentType string) error {
	if c == nil || c.core == nil {
		return nil
	}
	out := make([]minio.CompletePart, 0, len(parts))
	for _, p := range parts {
		etag := strings.TrimSpace(p.ETag)
		if etag == "" || p.PartNumber <= 0 {
			continue
		}
		out = append(out, minio.CompletePart{PartNumber: p.PartNumber, ETag: etag})
	}
	opts := minio.PutObjectOptions{ContentType: strings.TrimSpace(contentType)}
	_, err := c.core.CompleteMultipartUpload(ctx, c.bucket, strings.TrimSpace(objectKey), strings.TrimSpace(uploadID), out, opts)
	return err
}

func (c *Client) PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error) {
	if c == nil || c.c == nil {
		return "", nil
	}
	u, err := c.c.PresignedGetObject(ctx, c.bucket, strings.TrimSpace(objectKey), expires, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (c *Client) GetObject(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	if c == nil || c.c == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	obj, err := c.c.GetObject(ctx, c.bucket, strings.TrimSpace(objectKey), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func (c *Client) PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error {
	if c == nil || c.c == nil {
		return nil
	}
	opts := minio.PutObjectOptions{ContentType: strings.TrimSpace(contentType)}
	_, err := c.c.PutObject(ctx, c.bucket, strings.TrimSpace(objectKey), body, size, opts)
	return err
}
