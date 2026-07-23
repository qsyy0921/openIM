package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMinIOStorePutDownloadDeleteContract(t *testing.T) {
	endpoint := os.Getenv("PLATFORM_TEST_MINIO_URL")
	accessKey := os.Getenv("PLATFORM_TEST_MINIO_ACCESS_KEY")
	secretKey := os.Getenv("PLATFORM_TEST_MINIO_SECRET_KEY")
	bucket := os.Getenv("PLATFORM_TEST_MINIO_BUCKET")
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		t.Skip("isolated MinIO contract environment is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := NewMinIOStore(ctx, MinIOConfig{
		Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey, Bucket: bucket,
	})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("isolated enterprise knowledge object contract")
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	objectKey := fmt.Sprintf("contract/%d", time.Now().UnixNano())
	checksum := checksumText(string(content))
	if err := store.Put(ctx, bucket, objectKey, source, int64(len(content)), checksum, "text/plain"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "download.txt")
	if err := store.Download(ctx, bucket, objectKey, destination); err != nil {
		t.Fatal(err)
	}
	downloaded, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != string(content) {
		t.Fatal("downloaded object bytes differ from the source")
	}
	if err := store.Delete(ctx, bucket, objectKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Download(ctx, bucket, objectKey, filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("deleted object remained downloadable")
	}
}
