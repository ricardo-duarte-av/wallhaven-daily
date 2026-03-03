package main

import (
    "log"
    "os"
    "sync"
    "path/filepath"
    "strings"
    "time"
)

func main() {
    exePath, err := os.Executable()
    if err != nil {
        log.Fatalf("Failed to get executable path: %v", err)
    }
    exeDir := filepath.Dir(exePath)

    // If running via go run, exeDir will be /tmp/go-build... -- fallback to working dir or source dir
    if strings.Contains(exeDir, "/go-build") || strings.HasPrefix(exeDir, os.TempDir()) {
        // Use the directory of the main.go (assume it's where the config is)
        cwd, err := os.Getwd()
        if err != nil {
            log.Fatalf("Failed to get working directory: %v", err)
        }
        exeDir = cwd
    }

    if err := os.Chdir(exeDir); err != nil {
        log.Fatalf("Failed to change working directory: %v", err)
    }

    log.Printf("Switch to dir: %v", exeDir)

    cfg, err := LoadConfig("config.yaml")
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    db, err := NewDatabase(cfg.Database)
    if err != nil {
        log.Fatalf("Failed to open database: %v", err)
    }

    matrixBot, err := NewMatrixBot(cfg)
    if err != nil {
        log.Fatalf("Matrix login failed: %v", err)
    }

    for {
        for i, rangeOpt := range cfg.Wallhaven.Toprange {
            log.Printf("Fetching images for range: %s", rangeOpt)
            imageIDs, rateLimitInfo, err := cfg.FetchNewWallhavenImageIDs(db, rangeOpt)
            if err != nil {
                log.Printf("Failed to fetch image IDs for range %s: %v", rangeOpt, err)
                continue
            }
            
            log.Printf("Found %d new images to process for range %s", len(imageIDs), rangeOpt)
            
            // Process images in parallel with rate limit awareness
            maxWorkers := cfg.MaxConcurrentImages
            if maxWorkers <= 0 {
                maxWorkers = 3 // Default to 3 concurrent images
            }
            log.Printf("Processing %d images with %d concurrent workers", len(imageIDs), maxWorkers)
            
            // Create a semaphore to limit concurrent workers
            semaphore := make(chan struct{}, maxWorkers)
            var wg sync.WaitGroup
            
            for _, imageID := range imageIDs {
                wg.Add(1)
                semaphore <- struct{}{} // Acquire a slot
                
                go func(id string) {
                    defer wg.Done()
                    defer func() { <-semaphore }() // Release the slot
                    processAndSendImage(cfg, db, matrixBot, id)
                }(imageID)
            }
            
            wg.Wait() // Wait for all images to be processed
            log.Printf("Completed processing all images for range %s", rangeOpt)
            
            // Add adaptive delay between search API calls based on rate limit remaining
            if i < len(cfg.Wallhaven.Toprange)-1 {
                delay := CalculateAdaptiveDelay(rateLimitInfo.Remaining, rateLimitInfo.Limit)
                log.Printf("Rate limit: %d/%d remaining. Waiting %d seconds before next search API call...", 
                    rateLimitInfo.Remaining, rateLimitInfo.Limit, delay)
                time.Sleep(time.Duration(delay) * time.Second)
            }
        }

        logWait(cfg.WaitTime)
    }
}

func processAndSendImage(cfg *Config, db *Database, matrixBot *MatrixBot, imageID string) {
    log.Printf("Processing image %s", imageID)
    
    // Fetch full image details
    img, err := FetchWallhavenImage(cfg, imageID)
    if err != nil {
        log.Printf("Not sending image %s: failed to fetch image info: %v", imageID, err)
        return
    }
    
    log.Printf("Processing image %s (Path: %s, Thumbnail: %s)", img.ID, img.Path, img.Thumbs.Original)

    // Validate URLs before attempting download
    if img.Thumbs.Original == "" {
        log.Printf("Not sending image %s to Matrix/Mastodon/ntfy: thumbnail URL is empty", img.ID)
        return
    }
    if img.Path == "" {
        log.Printf("Not sending image %s to Matrix/Mastodon/ntfy: image URL (Path) is empty", img.ID)
        return
    }

    // Download thumbnail for OpenAI
    thumbPath, err := DownloadToTempFile(img.Thumbs.Original, "thumb")
    if err != nil {
        log.Printf("Not sending image %s to Matrix/Mastodon/ntfy: could not download thumbnail from %s: %v", img.ID, img.Thumbs.Original, err)
        return
    }
    defer os.Remove(thumbPath)

    // Download full image for Matrix, Mastodon and ntfy
    imagePath, err := DownloadToTempFile(img.Path, "image")
    if err != nil {
        log.Printf("Not sending image %s to Matrix/Mastodon/ntfy: could not download full image from %s: %v", img.ID, img.Path, err)
        return
    }
    defer os.Remove(imagePath)

    // OpenAI Description
    openaiDescription, err := GetOpenAIDescription(cfg, thumbPath)
    if err != nil {
        log.Printf("OpenAI error: %v", err)
        openaiDescription = ""
    }

    // Parallel posting to Matrix, Mastodon, ntfy
    var postWg sync.WaitGroup
    postWg.Add(3)

    go func() {
        defer postWg.Done()
        if err := matrixBot.SendImage(img, cfg, openaiDescription); err != nil {
            log.Printf("Failed to send image %s to Matrix: %v", img.ID, err)
        }
    }()
    go func() {
        defer postWg.Done()
        if err := PostToMastodon(cfg, img, openaiDescription, imagePath); err != nil {
            log.Printf("Failed to post image %s to Mastodon: %v", img.ID, err)
        }
    }()
    go func() {
        defer postWg.Done()
        ntfyStatus := BuildNtfyStatus(img, openaiDescription)
        ntfyTags := NtfyTags(img)
        if err := SendNtfyImageNotification(cfg, imagePath, ntfyStatus, ntfyTags, img.URL); err != nil {
            log.Printf("Failed to send ntfy notification for %s: %v", img.ID, err)
        }
    }()

    postWg.Wait()

    // Mark as sent in the DB
    if err := db.MarkSent(img.ID); err != nil {
        log.Printf("Failed to mark image %s as sent: %v", img.ID, err)
    } else {
        log.Printf("Successfully sent image %s to Matrix/Mastodon/ntfy and marked as sent", img.ID)
    }
}
