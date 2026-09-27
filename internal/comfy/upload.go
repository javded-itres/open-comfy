package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

type UploadResult struct {
	Name      string `json:"name"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

func (c *Client) UploadImage(ctx context.Context, filename string, data []byte) (UploadResult, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("image", filename)
	if err != nil {
		return UploadResult{}, err
	}
	if _, err := fw.Write(data); err != nil {
		return UploadResult{}, err
	}
	_ = w.WriteField("overwrite", "true")
	w.Close()
	resp, err := c.do(ctx, http.MethodPost, "/upload/image", &buf, w.FormDataContentType())
	if err != nil {
		return UploadResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return UploadResult{}, fmt.Errorf("upload %d: %s", resp.StatusCode, raw)
	}
	var out UploadResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return UploadResult{}, err
	}
	return out, nil
}

func ComfyImageName(u UploadResult) string {
	if u.Subfolder == "" {
		return u.Name
	}
	return u.Subfolder + "/" + u.Name
}
