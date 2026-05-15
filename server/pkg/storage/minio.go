// Package storage wraps the MinIO/S3 client used for chat attachments.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	mc        *minio.Client
	bucket    string
	publicURL string
}

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	PublicURL string
}

func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("minio access/secret key required")
	}
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("init minio: %w", err)
	}

	c := &Client{mc: mc, bucket: cfg.Bucket, publicURL: cfg.PublicURL}

	exists, err := mc.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket exists check: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket: %w", err)
		}
	}

	// MVP: bucket is public-read so img tags can fetch directly without
	// per-request presigning. Tighten before production (presigned GETs +
	// access checks against conversation membership).
	policy := publicReadPolicy(cfg.Bucket)
	if err := mc.SetBucketPolicy(ctx, cfg.Bucket, policy); err != nil {
		return nil, fmt.Errorf("set bucket policy: %w", err)
	}
	return c, nil
}

func publicReadPolicy(bucket string) string {
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]any{"AWS": []string{"*"}},
				"Action":    []string{"s3:GetObject"},
				"Resource":  []string{"arn:aws:s3:::" + bucket + "/*"},
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// Healthz pings MinIO by checking the bucket. Cheap, exercises auth +
// network in one call. Used by /healthz.
func (c *Client) Healthz(ctx context.Context) error {
	_, err := c.mc.BucketExists(ctx, c.bucket)
	return err
}

// Bucket returns the bucket name this client operates against.
func (c *Client) Bucket() string { return c.bucket }

// PresignPut returns a URL the client can PUT to within ttl. The client
// must use Content-Type that matches the eventual download.
func (c *Client) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.mc.PresignedPutObject(ctx, c.bucket, key, ttl)
	if err != nil {
		return "", err
	}
	return rewriteHost(u, c.publicURL).String(), nil
}

// PublicURL builds the canonical, anonymously fetchable URL for a key.
func (c *Client) PublicURL(key string) string {
	return fmt.Sprintf("%s/%s/%s", c.publicURL, c.bucket, key)
}

// StatObject fetches size/content-type from MinIO. Used after the client
// reports an upload as done — confirms the object actually arrived.
func (c *Client) StatObject(ctx context.Context, key string) (size int64, mime string, err error) {
	info, err := c.mc.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, "", err
	}
	return info.Size, info.ContentType, nil
}

// rewriteHost swaps the presigned URL's host with the user-facing public
// URL so the browser can actually reach it (the server may use a different
// internal endpoint inside Docker, e.g. "minio:9000").
func rewriteHost(presigned *url.URL, publicBase string) *url.URL {
	pub, err := url.Parse(publicBase)
	if err != nil || pub.Host == "" {
		return presigned
	}
	out := *presigned
	out.Scheme = pub.Scheme
	out.Host = pub.Host
	return &out
}
