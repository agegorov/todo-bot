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
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	endpoint string
	http     *http.Client
}

func New(endpoint string) *Client {
	return &Client{endpoint: endpoint, http: &http.Client{}}
}

type transcribeResponse struct {
	Text string `json:"text"`
}

// Transcribe конвертирует аудио в WAV и отправляет на whisper сервер.
func (c *Client) Transcribe(ctx context.Context, audioPath string) (string, error) {
	wavPath, err := convertToWav(audioPath)
	if err != nil {
		return "", fmt.Errorf("конвертация аудио: %w", err)
	}
	defer os.Remove(wavPath)

	f, err := os.Open(wavPath)
	if err != nil {
		return "", fmt.Errorf("open wav: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", filepath.Base(wavPath))
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
		return "", fmt.Errorf("decode response: %w", err)
	}
	return strings.TrimSpace(result.Text), nil
}

// convertToWav конвертирует OGG/Opus (Telegram) → WAV 16kHz mono через ffmpeg.
func convertToWav(src string) (string, error) {
	dst := strings.TrimSuffix(src, filepath.Ext(src)) + ".wav"
	cmd := exec.Command("ffmpeg",
		"-y",           // перезаписать если есть
		"-i", src,      // входной файл
		"-ar", "16000", // 16kHz — оптимально для whisper
		"-ac", "1",     // моно
		"-f", "wav",
		dst,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg: %w\n%s", err, out)
	}
	return dst, nil
}
