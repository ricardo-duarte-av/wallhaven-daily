package main

import (
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strings"
)

// BuildNtfyStatus constructs the ntfy notification message.
// It uses the same pattern as Mastodon, including tags at the end, with an empty line before.
func BuildNtfyStatus(img WallhavenImage, aiDescription string) string {
    return fmt.Sprintf(
        "Description: %s",
        aiDescription,
    )
}

// SendNtfyImageNotification sends an image file to ntfy with the given message and tags
func SendNtfyImageNotification(cfg *Config, localImagePath, message string, tags []string, whurl string) error {
    url := fmt.Sprintf("%s/%s", cfg.Ntfy.Server, cfg.Ntfy.Topic)
    file, err := os.Open(localImagePath)
    if err != nil {
        return err
    }
    defer file.Close()

    req, err := http.NewRequest("PUT", url, file)
    if err != nil {
        return err
    }

    // Set required headers
    req.Header.Set("Filename", filepath.Base(localImagePath))
    req.Header.Set("Message", message)
    req.Header.Set("Priority", "low")
    req.Header.Set("Title", whurl)
    if len(tags) > 0 {
        // tags can be comma separated
        req.Header.Set("Tags", strings.Join(tags, ","))
    }

    // Set Content-Type based on file extension (optional)
    switch strings.ToLower(filepath.Ext(localImagePath)) {
    case ".jpg", ".jpeg":
        req.Header.Set("Content-Type", "image/jpeg")
    case ".png":
        req.Header.Set("Content-Type", "image/png")
    case ".gif":
        req.Header.Set("Content-Type", "image/gif")
    default:
        req.Header.Set("Content-Type", "application/octet-stream")
    }

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 300 {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("ntfy image notification failed: %s - %s", resp.Status, string(body))
    }
    return nil
}

// Helper to produce tag hashtags (same as Mastodon/Matrix)
func NtfyTags(img WallhavenImage) []string {
    var tags []string
    for _, tag := range img.Tags {
        tagName := strings.ReplaceAll(tag.Name, " ", "")
        tags = append(tags, tagName) // ntfy tags cannot include '#' or whitespace
    }
    return tags
}
