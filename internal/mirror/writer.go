package mirror

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Writer writes file content to a mirror destination (local directory or S3).
type Writer interface {
	// Write writes content to the given relative path (e.g. "a/b/c/filename").
	// contentLength is the body size in bytes; use -1 if unknown (S3 requires a known length and may buffer).
	// contentType is the MIME type from the download response (e.g. Content-Type header); empty means unspecified.
	Write(ctx context.Context, relativePath string, body io.Reader, contentLength int64, contentType string) error
}

// ParseMirrorURL parses --mirror and returns a Writer and the normalized mirror root string for the DB.
// Schemes: file:// (local directory), s3:// (bucket/prefix).
// awsProfile is optional; when set (e.g. from --aws-profile), config is loaded with that profile (for SSO and named profiles).
func ParseMirrorURL(ctx context.Context, mirrorURL string, awsProfile string) (Writer, string, error) {
	mirrorURL = strings.TrimSuffix(mirrorURL, "/")
	if mirrorURL == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(mirrorURL, "file://") {
		u, err := url.Parse(mirrorURL)
		if err != nil {
			return nil, "", err
		}
		// file:///path -> path is in u.Path; file://host/path -> u.Host + u.Path
		dir := u.Path
		if u.Host != "" {
			dir = filepath.Join(u.Host, u.Path)
		}
		dir = filepath.Clean(dir)
		if dir == "." {
			dir = ""
		}
		// Normalize back to file:// form for DB
		normalized := "file://" + filepath.ToSlash(filepath.Clean(dir))
		normalized = strings.TrimSuffix(normalized, "/")
		return &FileWriter{Root: dir}, normalized, nil
	}
	if strings.HasPrefix(mirrorURL, "s3://") {
		trimmed := strings.TrimPrefix(mirrorURL, "s3://")
		parts := strings.SplitN(trimmed, "/", 2)
		bucket := parts[0]
		prefix := ""
		if len(parts) > 1 {
			prefix = strings.Trim(parts[1], "/")
		}
		loadOpts := []func(*config.LoadOptions) error{}
		if awsProfile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigProfile(awsProfile))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, "", err
		}
		// Discover bucket region to avoid PermanentRedirect (bucket in different region than default).
		region, err := manager.GetBucketRegion(ctx, s3.NewFromConfig(cfg), bucket)
		if err != nil {
			return nil, "", err
		}
		loadOpts = append(loadOpts, config.WithRegion(region))
		cfg, err = config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, "", err
		}
		client := s3.NewFromConfig(cfg)
		normalized := "s3://" + bucket
		if prefix != "" {
			normalized += "/" + prefix
		}
		return &S3Writer{Client: client, Bucket: bucket, Prefix: prefix}, normalized, nil
	}
	return nil, "", nil
}

// UnsetEnvCredsForProfile unsets AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, and
// AWS_SESSION_TOKEN so the default credential chain uses the profile (e.g. SSO)
// instead of stale or invalid static keys. Call this before loading AWS config when
// using a named profile. The env vars remain unset for the process lifetime.
func UnsetEnvCredsForProfile() {
	const (
		keyID  = "AWS_ACCESS_KEY_ID"
		secret = "AWS_SECRET_ACCESS_KEY"
		token  = "AWS_SESSION_TOKEN"
	)
	os.Unsetenv(keyID)
	os.Unsetenv(secret)
	os.Unsetenv(token)
}

// FileWriter writes to a local directory.
type FileWriter struct {
	Root string
}

// Write implements Writer by writing to Root/relativePath, creating parent dirs as needed.
func (w *FileWriter) Write(ctx context.Context, relativePath string, body io.Reader, contentLength int64, contentType string) error {
	fullPath := filepath.Join(w.Root, filepath.FromSlash(relativePath))
	if err := mkdirAll(filepath.Dir(fullPath)); err != nil {
		return err
	}
	return writeFile(fullPath, body)
}

// S3Writer uploads to S3.
type S3Writer struct {
	Client *s3.Client
	Bucket string
	Prefix string
}

// Write implements Writer by uploading to s3://Bucket/Prefix/relativePath.
func (w *S3Writer) Write(ctx context.Context, relativePath string, body io.Reader, contentLength int64, contentType string) error {
	key := w.ObjectKey(relativePath)
	input := &s3.PutObjectInput{
		Bucket: aws.String(w.Bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if contentLength >= 0 {
		input.ContentLength = aws.Int64(contentLength)
	}
	_, err := w.Client.PutObject(ctx, input)
	return err
}

// ObjectKey returns the S3 key for relativePath (same layout as Write and SyncContentType).
func (w *S3Writer) ObjectKey(relativePath string) string {
	key := relativePath
	if w.Prefix != "" {
		key = w.Prefix + "/" + relativePath
	}
	return key
}

// encodeS3CopySource builds the CopySource value (bucket/key, URL-encoded key) for CopyObject.
func encodeS3CopySource(bucket, key string) string {
	return bucket + "/" + strings.ReplaceAll(url.QueryEscape(key), "+", "%20")
}

// SyncContentType sets the object's Content-Type via in-place CopyObject, preserving metadata
// from HeadObject. Returns updated=true when CopyObject ran. Returns (false, nil) when the
// object already has that Content-Type. newContentType must be non-empty after TrimSpace.
func (w *S3Writer) SyncContentType(ctx context.Context, relativePath, newContentType string) (updated bool, err error) {
	newContentType = strings.TrimSpace(newContentType)
	if newContentType == "" {
		return false, fmt.Errorf("empty content type")
	}
	key := w.ObjectKey(relativePath)
	head, err := w.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(w.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(aws.ToString(head.ContentType)) == newContentType {
		return false, nil
	}

	copyIn := &s3.CopyObjectInput{
		Bucket:            aws.String(w.Bucket),
		Key:               aws.String(key),
		CopySource:        aws.String(encodeS3CopySource(w.Bucket, key)),
		MetadataDirective: types.MetadataDirectiveReplace,
		ContentType:       aws.String(newContentType),
	}
	if len(head.Metadata) > 0 {
		copyIn.Metadata = maps.Clone(head.Metadata)
	}
	if head.CacheControl != nil {
		copyIn.CacheControl = head.CacheControl
	}
	if head.ContentDisposition != nil {
		copyIn.ContentDisposition = head.ContentDisposition
	}
	if head.ContentEncoding != nil {
		copyIn.ContentEncoding = head.ContentEncoding
	}
	if head.ContentLanguage != nil {
		copyIn.ContentLanguage = head.ContentLanguage
	}
	if es := aws.ToString(head.ExpiresString); es != "" {
		if t, perr := http.ParseTime(es); perr == nil {
			copyIn.Expires = &t
		}
	}
	if head.WebsiteRedirectLocation != nil {
		copyIn.WebsiteRedirectLocation = head.WebsiteRedirectLocation
	}
	if head.ServerSideEncryption != "" {
		copyIn.ServerSideEncryption = head.ServerSideEncryption
	}
	if head.SSEKMSKeyId != nil {
		copyIn.SSEKMSKeyId = head.SSEKMSKeyId
	}
	if head.BucketKeyEnabled != nil {
		copyIn.BucketKeyEnabled = head.BucketKeyEnabled
	}
	if head.StorageClass != "" {
		copyIn.StorageClass = head.StorageClass
	}
	if head.ObjectLockLegalHoldStatus != "" {
		copyIn.ObjectLockLegalHoldStatus = head.ObjectLockLegalHoldStatus
	}
	if head.ObjectLockMode != "" {
		copyIn.ObjectLockMode = head.ObjectLockMode
	}
	if head.ObjectLockRetainUntilDate != nil {
		copyIn.ObjectLockRetainUntilDate = head.ObjectLockRetainUntilDate
	}

	_, err = w.Client.CopyObject(ctx, copyIn)
	if err != nil {
		return false, err
	}
	return true, nil
}

var (
	mkdirAll  = func(path string) error { return os.MkdirAll(path, 0755) }
	writeFile = writeFileImpl
)

func writeFileImpl(path string, body io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, body)
	return err
}
