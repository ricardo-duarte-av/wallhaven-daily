package main

import (
    "log"
    "os"
    "sync"
    "path/filepath"
    "strings"
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

    maxConcurrentImages := 3
    semaphore := make(chan struct{}, maxConcurrentImages)

    for {
        var allImages []WallhavenImage
        for _, rangeOpt := range cfg.Wallhaven.Toprange {
            images, err := cfg.FetchNewWallhavenImages(db, rangeOpt)
            if err != nil {
                log.Printf("Failed to fetch images for range %s: %v", rangeOpt, err)
                continue
            }
            allImages = append(allImages, images...)
        }

        var wg sync.WaitGroup

        for _, img := range allImages {
            wg.Add(1)
            semaphore <- struct{}{} // Acquire a slot

            go func(img WallhavenImage) {
                defer wg.Done()
                defer func() { <-semaphore }() // Release the slot

                // Download thumbnail for OpenAI
                thumbPath, err := DownloadToTempFile(img.Thumbs.Original, "thumb")
                if err != nil {
                    log.Printf("Could not download thumbnail: %v", err)
                    return
                }
                defer os.Remove(thumbPath)

                // Download full image for Mastodon and ntfy
                imagePath, err := DownloadToTempFile(img.Path, "image")
                if err != nil {
                    log.Printf("Could not download image for Mastodon: %v", err)
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
                }
            }(img)
        }

        wg.Wait() // Wait for all image goroutines to finish
        logWait(cfg.WaitTime)
    }
}
