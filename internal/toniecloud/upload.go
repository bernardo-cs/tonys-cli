package toniecloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UploadResult describes a completed upload + chapter creation.
type UploadResult struct {
	FileID string `json:"fileId"`
	Title  string `json:"title"`
}

// UploadFile uploads a local audio file and appends it as a new chapter on the
// tonie. If title is empty the file's base name (without extension) is used.
// This is the three-step flow: POST /file → presigned S3 upload → POST chapters.
func (c *Client) UploadFile(ctx context.Context, t CreativeTonie, path, title string) (UploadResult, error) {
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return UploadResult{}, err
	}
	defer f.Close()

	contentType := mime.TypeByExtension(filepath.Ext(path))
	return c.UploadReader(ctx, t, f, title, contentType)
}

// UploadReader is like UploadFile but reads content from r, so callers can pipe
// data (e.g. from stdin). contentType may be empty.
func (c *Client) UploadReader(ctx context.Context, t CreativeTonie, r io.Reader, title, contentType string) (UploadResult, error) {
	fr, err := c.CreateFileUpload(ctx)
	if err != nil {
		return UploadResult{}, fmt.Errorf("request upload slot: %w", err)
	}
	if err := c.uploadToS3(ctx, fr, r, contentType); err != nil {
		return UploadResult{}, fmt.Errorf("upload to storage: %w", err)
	}
	if err := c.AddChapter(ctx, t, fr.FileID, title); err != nil {
		return UploadResult{}, fmt.Errorf("attach chapter: %w", err)
	}
	return UploadResult{FileID: fr.FileID, Title: title}, nil
}

// uploadToS3 posts the content to the presigned S3 endpoint. Per the S3
// browser-POST contract, every policy field must precede the "file" part, which
// must come last. We buffer the body so a Content-Length is sent (S3 POST
// rejects chunked uploads).
func (c *Client) uploadToS3(ctx context.Context, fr FileUploadRequest, r io.Reader, contentType string) error {
	key := fr.Request.Fields["key"]

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for field, value := range fr.Request.Fields {
		if err := w.WriteField(field, value); err != nil {
			return err
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, key))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, r); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fr.Request.URL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("User-Agent", UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return newAPIError(http.MethodPost, fr.Request.URL, resp.StatusCode, body)
	}
	return nil
}

// WaitForTranscoding polls the tonie until transcoding finishes, a transcoding
// error is reported, or the timeout/context expires, returning the final tonie
// state. A zero interval defaults to 2s; a non-positive timeout means "no overall
// deadline" (still cancellable via ctx). This prevents an unattended `--wait`
// from spinning forever when the backend stalls or fails a transcode.
func (c *Client) WaitForTranscoding(ctx context.Context, householdID, tonieID string, interval, timeout time.Duration) (CreativeTonie, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var last CreativeTonie
	for {
		t, err := c.CreativeTonie(ctx, householdID, tonieID)
		if err != nil {
			return t, err
		}
		last = t
		if len(t.TranscodingErrors) > 0 {
			return t, fmt.Errorf("transcoding failed for tonie %q: %v", t.Name, t.TranscodingErrors)
		}
		if !t.Transcoding {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("timed out waiting for transcoding: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}
