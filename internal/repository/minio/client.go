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
	"github.com/minio/minio-go/v7/pkg/encrypt"
)

type Client struct {
	bucket       string
	core         *minio.Core
	c            *minio.Client
	publicScheme string
	publicHost   string
	sseMode      string
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

	publicScheme := ""
	publicHost := ""
	if base := strings.TrimSpace(cfg.PublicBase); base != "" {
		if !strings.Contains(base, "://") {
			base = "https://" + base
		}
		if parsed, parseErr := url.Parse(base); parseErr == nil {
			publicScheme = strings.TrimSpace(parsed.Scheme)
			publicHost = strings.TrimSpace(parsed.Host)
		}
	}

	return &Client{
		bucket:       bucket,
		core:         core,
		c:            c,
		publicScheme: publicScheme,
		publicHost:   publicHost,
		sseMode:      strings.ToLower(strings.TrimSpace(cfg.SSEMode)),
	}, nil
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
	opts := c.putOptions(contentType)
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
	return c.publicURL(u).String(), nil
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
	opts := c.putOptions(contentType)
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
	return c.publicURL(u).String(), nil
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
	opts := c.putOptions(contentType)
	_, err := c.c.PutObject(ctx, c.bucket, strings.TrimSpace(objectKey), body, size, opts)
	return err
}

func (c *Client) DeleteObject(ctx context.Context, objectKey string) error {
	if c == nil || c.c == nil {
		return nil
	}
	return c.c.RemoveObject(ctx, c.bucket, strings.TrimSpace(objectKey), minio.RemoveObjectOptions{})
}

func (c *Client) putOptions(contentType string) minio.PutObjectOptions {
	opts := minio.PutObjectOptions{ContentType: strings.TrimSpace(contentType)}
	if c == nil {
		return opts
	}
	if c.sseMode == "s3" {
		opts.ServerSideEncryption = encrypt.NewSSE()
	}
	return opts
}

func (c *Client) publicURL(input *url.URL) *url.URL {
	if input == nil {
		return input
	}
	if strings.TrimSpace(c.publicScheme) == "" || strings.TrimSpace(c.publicHost) == "" {
		return input
	}
	copyURL := *input
	copyURL.Scheme = c.publicScheme
	copyURL.Host = c.publicHost
	return &copyURL
}
