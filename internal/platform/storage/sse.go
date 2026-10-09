package storage

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 is the S3 SSE-C key checksum mandated by the protocol, not a security primitive
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("storage: object not found")

// SSEKey is a 32-byte customer-provided key for S3 SSE-C (AES-256 performed by the object store). A nil key
// means the object is stored without SSE-C. The key is sent with every request for the object, so the
// endpoint must use TLS outside local development (config enforces https outside development and test).
type SSEKey []byte

func (k SSEKey) headers() (alg, key, md *string, err error) {
	if k == nil {
		return nil, nil, nil, nil
	}
	if len(k) != 32 {
		return nil, nil, nil, errors.New("storage: SSE-C key must be 32 bytes")
	}
	sum := md5.Sum(k) //nolint:gosec // protocol checksum
	return aws.String("AES256"), aws.String(base64.StdEncoding.EncodeToString(k)),
		aws.String(base64.StdEncoding.EncodeToString(sum[:])), nil
}

// RefFor builds a Ref for a class-relative key (e.g. "quarantine/<uuid>" → "kyc/quarantine/<uuid>").
func RefFor(class Class, relKey string) Ref { return Ref{Class: class, Key: class.Prefix() + relKey} }

// PutSSE stores body (size bytes) under ref, encrypted with SSE-C when key is non-nil.
func (c *Client) PutSSE(ctx context.Context, ref Ref, body io.ReadSeeker, size int64, contentType string, key SSEKey) error {
	cc, err := c.client(ref)
	if err != nil {
		return err
	}
	alg, k, md, err := key.headers()
	if err != nil {
		return err
	}
	_, err = cc.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cc.bucket), Key: aws.String(ref.Key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType),
		SSECustomerAlgorithm: alg, SSECustomerKey: k, SSECustomerKeyMD5: md,
	})
	return err
}

// GetSSE opens an object stored with SSE-C key (nil for none). A missing object returns ErrNotFound.
func (c *Client) GetSSE(ctx context.Context, ref Ref, key SSEKey) (io.ReadCloser, error) {
	cc, err := c.client(ref)
	if err != nil {
		return nil, err
	}
	alg, k, md, err := key.headers()
	if err != nil {
		return nil, err
	}
	out, err := cc.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(cc.bucket), Key: aws.String(ref.Key),
		SSECustomerAlgorithm: alg, SSECustomerKey: k, SSECustomerKeyMD5: md})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

// CopySSE copies src to dst inside one class (server side), re-encrypting with the same SSE-C key.
func (c *Client) CopySSE(ctx context.Context, src, dst Ref, key SSEKey) error {
	if src.Class != dst.Class {
		return ErrWrongClass
	}
	cc, err := c.client(src)
	if err != nil {
		return err
	}
	if _, err := c.client(dst); err != nil {
		return err
	}
	alg, k, md, err := key.headers()
	if err != nil {
		return err
	}
	_, err = cc.s3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(cc.bucket), Key: aws.String(dst.Key), CopySource: aws.String(cc.bucket + "/" + src.Key),
		CopySourceSSECustomerAlgorithm: alg, CopySourceSSECustomerKey: k, CopySourceSSECustomerKeyMD5: md,
		SSECustomerAlgorithm: alg, SSECustomerKey: k, SSECustomerKeyMD5: md,
	})
	if err != nil && isNotFound(err) {
		return ErrNotFound
	}
	return err
}

// Bucket returns the bucket name configured for class ("" when the class is not configured). Tests use it
// to probe anonymous access; application code never builds bucket URLs.
func (c *Client) Bucket(class Class) string { return c.classes[class].bucket }

func isNotFound(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchObject":
			return true
		}
	}
	return false
}

// String implements fmt.Stringer without revealing key material.
func (k SSEKey) String() string { return fmt.Sprintf("SSEKey[%d bytes, redacted]", len(k)) }
