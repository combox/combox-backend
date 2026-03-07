package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"time"

	"combox-backend/internal/config"
	miniorepo "combox-backend/internal/repository/minio"
	pgrepo "combox-backend/internal/repository/postgres"
	mediasvc "combox-backend/internal/service/media"
)

type mediaStoreAdapter struct{ c *miniorepo.Client }

func (a mediaStoreAdapter) Bucket() string {
	return a.c.Bucket()
}

func (a mediaStoreAdapter) NewMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error) {
	return a.c.NewMultipartUpload(ctx, objectKey, contentType)
}

func (a mediaStoreAdapter) PresignUploadPart(ctx context.Context, objectKey, uploadID string, partNumber int, expires time.Duration) (string, error) {
	return a.c.PresignUploadPart(ctx, objectKey, uploadID, partNumber, expires)
}

func (a mediaStoreAdapter) CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []mediasvc.CompletePart, contentType string) error {
	converted := make([]miniorepo.CompletePart, 0, len(parts))
	for _, p := range parts {
		converted = append(converted, miniorepo.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	return a.c.CompleteMultipartUpload(ctx, objectKey, uploadID, converted, contentType)
}

func (a mediaStoreAdapter) PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error) {
	return a.c.PresignGetObject(ctx, objectKey, expires)
}

func (a mediaStoreAdapter) GetObject(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	return a.c.GetObject(ctx, objectKey)
}

func (a mediaStoreAdapter) PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error {
	return a.c.PutObject(ctx, objectKey, contentType, body, size)
}

func (a mediaStoreAdapter) DeleteObject(ctx context.Context, objectKey string) error {
	return a.c.DeleteObject(ctx, objectKey)
}

func main() {
	limit := flag.Int("limit", 5000, "Max number of attachments to scan in one run")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx := context.Background()

	postgresClient, err := pgrepo.New(ctx, cfg.Postgres.DSN)
	if err != nil {
		log.Fatalf("init postgres: %v", err)
	}
	defer postgresClient.Close()

	minioClient, err := miniorepo.New(cfg.MinIO)
	if err != nil {
		log.Fatalf("init minio: %v", err)
	}

	mediaService, err := mediasvc.New(pgrepo.NewMediaRepository(postgresClient), mediaStoreAdapter{c: minioClient})
	if err != nil {
		log.Fatalf("init media service: %v", err)
	}

	result, err := mediaService.BackfillAttachmentMeta(ctx, mediasvc.BackfillMetaInput{Limit: *limit})
	if err != nil {
		log.Fatalf("backfill failed: %v", err)
	}

	fmt.Printf("scanned=%d updated=%d failures=%d\n", result.Scanned, result.Updated, result.Failures)
}
