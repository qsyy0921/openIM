package knowledge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type ObjectStore interface {
	Put(context.Context, string, string, string, int64, string, string) error
	Download(context.Context, string, string, string) error
	Delete(context.Context, string, string) error
}

type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
}

type MinIOStore struct {
	client *minio.Client
	bucket string
}

func NewMinIOStore(ctx context.Context, config MinIOConfig) (*MinIOStore, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("knowledge MinIO endpoint must be an origin URL")
	}
	if strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" ||
		len(config.Bucket) < 3 || len(config.Bucket) > 63 {
		return nil, errors.New("knowledge MinIO credentials or bucket are invalid")
	}
	client, err := minio.New(parsed.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: parsed.Scheme == "https",
	})
	if err != nil {
		return nil, fmt.Errorf("configure knowledge MinIO client: %w", err)
	}
	exists, err := client.BucketExists(ctx, config.Bucket)
	if err != nil {
		return nil, fmt.Errorf("inspect knowledge MinIO bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, config.Bucket, minio.MakeBucketOptions{}); err != nil {
			exists, existsErr := client.BucketExists(ctx, config.Bucket)
			if existsErr != nil || !exists {
				return nil, fmt.Errorf("create private knowledge MinIO bucket: %w", err)
			}
		}
	}
	policy, err := client.GetBucketPolicy(ctx, config.Bucket)
	if err == nil {
		if strings.TrimSpace(policy) != "" && strings.TrimSpace(policy) != "{}" {
			return nil, errors.New("knowledge MinIO bucket must not have a bucket policy")
		}
	} else {
		response := minio.ToErrorResponse(err)
		if response.Code != "NoSuchBucketPolicy" && response.StatusCode != http.StatusNotFound {
			return nil, fmt.Errorf("verify private knowledge MinIO bucket policy: %w", err)
		}
	}
	return &MinIOStore{client: client, bucket: config.Bucket}, nil
}

func (s *MinIOStore) Bucket() string {
	return s.bucket
}

func (s *MinIOStore) Put(ctx context.Context, bucket, objectKey, path string, size int64, checksum, contentType string) error {
	if bucket != s.bucket || objectKey == "" || size < 1 || size > MaxSourceBytes || !validSHA256(checksum) {
		return errors.New("knowledge object put contract is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open knowledge source for upload: %w", err)
	}
	defer file.Close()
	info, err := s.client.PutObject(ctx, bucket, objectKey, file, size, minio.PutObjectOptions{
		ContentType: contentType,
		UserMetadata: map[string]string{
			"source-sha256": strings.TrimPrefix(checksum, "sha256:"),
		},
	})
	if err != nil {
		return fmt.Errorf("upload knowledge source object: %w", err)
	}
	if info.Size != size {
		return errors.New("knowledge source object size was not acknowledged")
	}
	stat, err := s.client.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("verify knowledge source object: %w", err)
	}
	if stat.Size != size || !strings.EqualFold(stat.Metadata.Get("X-Amz-Meta-Source-Sha256"), strings.TrimPrefix(checksum, "sha256:")) {
		return errors.New("knowledge source object metadata does not match the upload")
	}
	return nil
}

func (s *MinIOStore) Download(ctx context.Context, bucket, objectKey, destination string) error {
	if bucket != s.bucket || objectKey == "" || destination == "" {
		return errors.New("knowledge object download contract is invalid")
	}
	if _, err := os.Stat(destination); err == nil {
		return errors.New("knowledge object download destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect knowledge download destination: %w", err)
	}
	if err := s.client.FGetObject(ctx, bucket, objectKey, destination, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("download knowledge source object: %w", err)
	}
	return nil
}

func (s *MinIOStore) Delete(ctx context.Context, bucket, objectKey string) error {
	if bucket != s.bucket || objectKey == "" {
		return errors.New("knowledge object delete contract is invalid")
	}
	if err := s.client.RemoveObject(ctx, bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete knowledge source object: %w", err)
	}
	_, err := s.client.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{})
	if err == nil {
		return errors.New("knowledge source object still exists after delete")
	}
	response := minio.ToErrorResponse(err)
	if response.Code != "NoSuchKey" && response.StatusCode != 404 {
		return fmt.Errorf("verify knowledge source object deletion: %w", err)
	}
	return nil
}

var _ ObjectStore = (*MinIOStore)(nil)
