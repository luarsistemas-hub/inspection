package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Gotenberg is a private, bounded Chromium renderer adapter.
type Gotenberg struct {
	BaseURL string
	Client  *http.Client
}

func (g Gotenberg) Render(ctx context.Context, html io.Reader, assets AssetBundle) (io.ReadCloser, error) {
	if g.BaseURL == "" || html == nil {
		return nil, fmt.Errorf("gotenberg: invalid renderer input")
	}
	source, err := io.ReadAll(io.LimitReader(html, 4<<20+1))
	if err != nil {
		return nil, err
	}
	if len(source) > 4<<20 {
		return nil, fmt.Errorf("gotenberg: HTML exceeds 4 MiB")
	}
	if strings.Contains(strings.ToLower(string(source)), "http://") || strings.Contains(strings.ToLower(string(source)), "https://") {
		return nil, fmt.Errorf("gotenberg: external asset URL forbidden")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	page, err := writer.CreateFormFile("files", "index.html")
	if err != nil {
		return nil, err
	}
	if _, err = page.Write(source); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := assets[name]
		if name == "" || strings.Contains(name, "..") {
			return nil, fmt.Errorf("gotenberg: invalid asset name")
		}
		file, err := writer.CreateFormFile("files", name)
		if err != nil {
			return nil, err
		}
		if _, err = file.Write(data); err != nil {
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.BaseURL, "/")+"/forms/chromium/convert/html", &buffer)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/pdf")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gotenberg: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		response.Body.Close()
		return nil, fmt.Errorf("gotenberg: response status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20+1))
	response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("gotenberg: read response: %w", err)
	}
	if len(data) > 64<<20 || len(data) < 5 || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("gotenberg: invalid PDF response")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
