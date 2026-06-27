package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectStore is the minimal object-store surface the bucket persistence needs:
// fetch and replace a single key. Defined as an interface so tests can swap an
// in-memory fake for the real S3/MinIO client.
type ObjectStore interface {
	// Get returns the object bytes and found=true, or (nil, false, nil) when the
	// key is absent.
	Get(ctx context.Context, key string) (data []byte, found bool, err error)
	// Put writes data to key, replacing any existing object. A single S3
	// PutObject is atomic per key.
	Put(ctx context.Context, key string, data []byte) error
}

// s3Store is an S3/MinIO-compatible ObjectStore, constructed with the minio-go
// client to match the convention used elsewhere in the suite (vulos-mail's
// internal/blob.S3, vulos' cluster/lease stores).
type s3Store struct {
	cli    *minio.Client
	bucket string
}

// NewS3Store connects to the configured object store. It mirrors the Vulos
// storage-seam safety note: a plaintext (http / no-TLS) endpoint is only
// permitted for loopback or private addresses — never a public host.
func NewS3Store(ctx context.Context, cfg StorageConfig) (ObjectStore, error) {
	host, secure, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	cli, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	// Best-effort bucket existence check; create when missing (dev MinIO).
	if exists, berr := cli.BucketExists(ctx, cfg.Bucket); berr == nil && !exists {
		if merr := cli.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); merr != nil {
			return nil, fmt.Errorf("create bucket %q: %w", cfg.Bucket, merr)
		}
	}
	return &s3Store{cli: cli, bucket: cfg.Bucket}, nil
}

func (s *s3Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	obj, err := s.cli.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func (s *s3Store) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.cli.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

// isNotFound reports whether err is a minio "object/key absent" error.
func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.StatusCode == http.StatusNotFound ||
		resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket"
}

// parseEndpoint splits a storage endpoint into the host[:port] + TLS flag the
// minio-go client expects, accepting "https://host", "http://host", or a bare
// "host[:port]". A plaintext endpoint is rejected unless it targets a loopback
// or private address (Vulos storage-seam safety: never speak unencrypted S3 to
// a public host).
func parseEndpoint(raw string) (host string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.New("storage: empty endpoint")
	}
	switch {
	case strings.HasPrefix(raw, "https://"):
		host, secure = strings.TrimPrefix(raw, "https://"), true
	case strings.HasPrefix(raw, "http://"):
		host, secure = strings.TrimPrefix(raw, "http://"), false
	default:
		// Bare host[:port] — assume plaintext (matches dev MinIO defaults).
		host, secure = raw, false
	}
	host = strings.TrimSuffix(host, "/")
	if host == "" {
		return "", false, errors.New("storage: endpoint has no host")
	}
	if !secure && !isLoopbackOrPrivate(host) {
		return "", false, fmt.Errorf("storage: refusing plaintext (non-https) endpoint %q to a non-private host; use https:// or a loopback/private address", raw)
	}
	return host, secure, nil
}

// isLoopbackOrPrivate reports whether hostport names a loopback/private target
// for which plaintext transport is acceptable. IPs are checked for loopback,
// private (RFC1918), and link-local ranges; hostnames are allowed only when they
// are "localhost" or a single label with no dot (e.g. a docker-compose service
// name like "minio") — public hosts always have a dotted name.
func isLoopbackOrPrivate(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]") // unwrap IPv6 literal
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	// A hostname with no dot cannot be a public FQDN (e.g. "minio", "board-store").
	return !strings.Contains(host, ".")
}
