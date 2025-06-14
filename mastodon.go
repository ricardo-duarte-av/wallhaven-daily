package main

import (
        "bytes"
        "encoding/json"
        "fmt"
        "image/jpeg"
        "io"
        "mime/multipart"
        "net/http"
        "os"
        "strings"
        "path/filepath"

        "github.com/disintegration/imaging"
)

const (
    maxFileSizeBytes = 16 * 1024 * 1024       // 16MB
    maxPixels        = 8_300_000              // 8.3MP
)


type MastodonConfig struct {
        Server      string `yaml:"mastodon_server"`
        AccessToken string `yaml:"mastodon_token"`
}

func PostToMastodon(cfg *Config, img WallhavenImage, openaiDescription, localImagePath  string) error {
        mediaID, err := mastodonUploadMedia(cfg, localImagePath)
        if err != nil {
                return fmt.Errorf("error uploading image to mastodon: %w", err)
        }
        status := buildMastodonStatus(img, openaiDescription)

        endpoint := fmt.Sprintf("%s/api/v1/statuses", cfg.Mastodon.Server)
        body, _ := json.Marshal(map[string]interface{}{
                "status":      status,
                "media_ids":   []string{mediaID},
                "visibility":  "public",
        })

        req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(body))
        if err != nil {
                return err
        }
        req.Header.Set("Authorization", "Bearer "+cfg.Mastodon.AccessToken)
        req.Header.Set("Content-Type", "application/json")
        resp, err := http.DefaultClient.Do(req)
        if err != nil {
                return err
        }
        defer resp.Body.Close()
        if resp.StatusCode >= 300 {
                b, _ := io.ReadAll(resp.Body)
                return fmt.Errorf("mastodon post error: %s", string(b))
        }
        return nil
}

func buildMastodonStatus(img WallhavenImage, aiDescription string) string {
    // Extract tag names and format as hashtags
    var tags []string
    for _, tag := range img.Tags {
        // Replace spaces with underscores for valid hashtags, if needed
        tagName := strings.ReplaceAll(tag.Name, " ", "")
        tags = append(tags, "#"+tagName)
    }

    tagStr := strings.Join(tags, " ")

    // Format the status
    return fmt.Sprintf(
        "Link: %s\nUploader: %s\nResolution: %s\nType: %s\nSize: %.2f MB\nDescription: %s\n\n%s",
        img.URL,
        img.Uploader.Username,
        img.Resolution,
        img.FileType,
        float64(img.FileSize)/(1024*1024),
        aiDescription,
        tagStr,
    )
}

func mastodonUploadMedia(cfg *Config, localImagePath string) (string, error) {
    // Step 1: Get info about the image
    processedPath, err := ensureMastodonMediaCompliant(localImagePath)
    if err != nil {
        return "", fmt.Errorf("preparing image for Mastodon: %w", err)
    }
    defer func() {
        if processedPath != localImagePath {
            os.Remove(processedPath)
        }
    }()

    endpoint := fmt.Sprintf("%s/api/v2/media", cfg.Mastodon.Server)
    file, err := os.Open(processedPath)
    if err != nil {
        return "", err
    }
    defer file.Close()

    var buf bytes.Buffer
    writer := multipart.NewWriter(&buf)
    part, err := writer.CreateFormFile("file", filepath.Base(processedPath))
    if err != nil {
        return "", err
    }
    _, err = io.Copy(part, file)
    if err != nil {
        return "", err
    }
    writer.Close()

    req, err := http.NewRequest("POST", endpoint, &buf)
    if err != nil {
        return "", err
    }
    req.Header.Set("Authorization", "Bearer "+cfg.Mastodon.AccessToken)
    req.Header.Set("Content-Type", writer.FormDataContentType())
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 300 {
        b, _ := io.ReadAll(resp.Body)
        return "", fmt.Errorf("mastodon upload error: %s", string(b))
    }
    var result struct {
        ID string `json:"id"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", err
    }
    return result.ID, nil
}


// ensureMastodonMediaCompliant checks image size and pixel count, and resizes/compresses if needed.
// Returns path to file to upload (may be the original file, or a processed temp file).
func ensureMastodonMediaCompliant(path string) (string, error) {
    fileInfo, err := os.Stat(path)
    if err != nil {
        return "", err
    }
    if fileInfo.Size() <= maxFileSizeBytes {
        img, err := imaging.Open(path)
        if err != nil {
            return "", err
        }
        bounds := img.Bounds()
        pixels := bounds.Dx() * bounds.Dy()
        if pixels <= maxPixels {
            // Already compliant
            return path, nil
        }
    }

    // At this point, we need to resize and/or compress.
    img, err := imaging.Open(path)
    if err != nil {
        return "", err
    }
    bounds := img.Bounds()
    width, height := bounds.Dx(), bounds.Dy()
    pixels := width * height

    // Resize if needed
    if pixels > maxPixels {
        scale := float64(maxPixels) / float64(pixels)
        factor := sqrt(scale)
        newW := int(float64(width) * factor)
        newH := int(float64(height) * factor)
        img = imaging.Resize(img, newW, newH, imaging.Lanczos)
    }

    // Save to temp file with compression (start at quality 85 and retry down to 60 if still too big)
    tmpFile, err := os.CreateTemp("", "mastodon-img-*.jpg")
    if err != nil {
        return "", err
    }
    defer tmpFile.Close()

    quality := 85
    for quality >= 60 {
        tmpFile.Seek(0, 0)
        tmpFile.Truncate(0)
        err = jpeg.Encode(tmpFile, img, &jpeg.Options{Quality: quality})
        if err != nil {
            os.Remove(tmpFile.Name())
            return "", err
        }
        stat, _ := tmpFile.Stat()
        if stat.Size() <= maxFileSizeBytes {
            return tmpFile.Name(), nil
        }
        quality -= 10
    }

    // If still too big at quality 60, fail
    os.Remove(tmpFile.Name())
    return "", fmt.Errorf("unable to reduce image to Mastodon's 16MB limit, even after resizing and compression")
}

// sqrt helper (since math.Sqrt works with float64)
func sqrt(x float64) float64 {
    // Use Newton's method
    z := x
    for i := 0; i < 8; i++ {
        z -= (z*z - x) / (2 * z)
    }
    return z
}
