package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// Client calls a self-hosted whisper.cpp HTTP server.
// Compatible with https://github.com/ggerganov/whisper.cpp server mode.
type Client struct {
	endpoint string
	http     *http.Client
}

func New(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		http:     &http.Client{},
	}
}

type transcribeResponse struct {
	Text string `json:"text"`
}

// Transcribe sends an audio file to whisper server and returns Russian text.
func (c *Client) Transcribe(ctx context.Context, audioPath string) (string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("open audio: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)

	part, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	_ = w.WriteField("language", "ru")
	_ = w.WriteField("response_format", "json")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/inference", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper status %d: %s", resp.StatusCode, b)
	}

	var result transcribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode whisper response: %w", err)
	}
	return result.Text, nil
}
