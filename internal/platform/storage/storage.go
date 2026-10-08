// Package storage is FundZim's S3-compatible object storage abstraction.
//
// Three logical storage classes map onto three buckets and three separately scoped credentials, each
// credential reaching exactly one bucket (design-baseline §12 I-19 as amended by I-26, ADR-008, ADR-009):
//
//	Class                         Bucket            Prefix        Credential
//	PublicCampaignMedia           public-media      media/        public-media
//	PrivateIdentityDocuments      private-kyc       kyc/          private-kyc      (kyc module only)
//	PrivateComplianceDocuments    private-evidence  compliance/   private-evidence (storage module only)
//
// Each class is served by its own client built from its own credential, so no code path holds a key that
// can read another class. Objects are addressed by Ref (class + random key); this package never produces
// public or presigned URLs in Stage 3. Presigned staff access (≤ 5 min, permission + justification,
// audited) is a Stage 5 feature.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Class is a logical storage class.
type Class string

const (
	PublicCampaignMedia        Class = "PUBLIC_CAMPAIGN_MEDIA"
	PrivateIdentityDocuments   Class = "PRIVATE_IDENTITY_DOCUMENTS"
	PrivateComplianceDocuments Class = "PRIVATE_COMPLIANCE_DOCUMENTS"
)

// Prefix returns the key prefix that confines a class inside its bucket.
func (c Class) Prefix() string {
	switch c {
	case PublicCampaignMedia:
		return "media/"
	case PrivateIdentityDocuments:
		return "kyc/"
	case PrivateComplianceDocuments:
		return "compliance/"
	}
	return ""
}

// Private reports whether objects of the class must never be publicly readable.
func (c Class) Private() bool { return c != PublicCampaignMedia }

// Ref identifies an object. Keys are random (UUIDv7), never derived from names or ID numbers.
type Ref struct {
	Class Class
	Key   string
}

// ErrWrongClass is returned when a Ref's key is outside its class prefix.
var ErrWrongClass = errors.New("storage: key outside class prefix")

// ErrClassUnavailable is returned when the client was built without the class's credential.
var ErrClassUnavailable = errors.New("storage: class not configured")

// Credential is one scoped credential and the bucket it may use.
type Credential struct {
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

// Options configure New. Pass only the credentials the calling component is entitled to.
type Options struct {
	Endpoint       string
	Region         string
	ForcePathStyle bool
	Credentials    map[Class]Credential
}

type classClient struct {
	s3     *s3.Client
	bucket string
}

// Client performs object operations per class.
type Client struct{ classes map[Class]classClient }

// New builds one S3 client per configured class.
func New(o Options) (*Client, error) {
	c := &Client{classes: map[Class]classClient{}}
	for class, cred := range o.Credentials {
		if class.Prefix() == "" {
			return nil, fmt.Errorf("storage: unknown class %q", class)
		}
		if cred.Bucket == "" || cred.AccessKeyID == "" || cred.SecretAccessKey == "" {
			return nil, fmt.Errorf("storage: incomplete credential for %s", class)
		}
		cfg := aws.Config{
			Region:      o.Region,
			Credentials: credentials.NewStaticCredentialsProvider(cred.AccessKeyID, cred.SecretAccessKey, ""),
		}
		cli := s3.NewFromConfig(cfg, func(opt *s3.Options) {
			opt.BaseEndpoint = aws.String(o.Endpoint)
			opt.UsePathStyle = o.ForcePathStyle
			opt.RetryMaxAttempts = 2
		})
		c.classes[class] = classClient{s3: cli, bucket: cred.Bucket}
	}
	return c, nil
}

// NewRef returns a fresh random key for class.
func NewRef(class Class) Ref { return Ref{Class: class, Key: class.Prefix() + ids.New()} }

func (c *Client) client(ref Ref) (classClient, error) {
	cc, ok := c.classes[ref.Class]
	if !ok {
		return classClient{}, ErrClassUnavailable
	}
	if !strings.HasPrefix(ref.Key, ref.Class.Prefix()) || strings.Contains(ref.Key, "..") {
		return classClient{}, ErrWrongClass
	}
	return cc, nil
}

// Check verifies that class is reachable with its own credential by listing at most one key under the
// class prefix (the scoped policies allow listing only within the prefix).
func (c *Client) Check(ctx context.Context, class Class) error {
	cc, ok := c.classes[class]
	if !ok {
		return ErrClassUnavailable
	}
	_, err := cc.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(cc.bucket), Prefix: aws.String(class.Prefix()), MaxKeys: aws.Int32(1),
	})
	return err
}

// Classes lists the configured classes.
func (c *Client) Classes() []Class {
	out := make([]Class, 0, len(c.classes))
	for k := range c.classes {
		out = append(out, k)
	}
	return out
}

// Put stores body under ref. Private objects are written with private ACL semantics (bucket policy denies
// anonymous access; no ACL grants are ever set).
func (c *Client) Put(ctx context.Context, ref Ref, body io.Reader, size int64, contentType string) error {
	cc, err := c.client(ref)
	if err != nil {
		return err
	}
	_, err = cc.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cc.bucket), Key: aws.String(ref.Key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType),
	})
	return err
}

// Get opens an object. The caller must close the reader.
func (c *Client) Get(ctx context.Context, ref Ref) (io.ReadCloser, error) {
	cc, err := c.client(ref)
	if err != nil {
		return nil, err
	}
	out, err := cc.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(cc.bucket), Key: aws.String(ref.Key)})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

// Delete removes an object (used for probes and, later, retention jobs).
func (c *Client) Delete(ctx context.Context, ref Ref) error {
	cc, err := c.client(ref)
	if err != nil {
		return err
	}
	_, err = cc.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(cc.bucket), Key: aws.String(ref.Key)})
	return err
}
