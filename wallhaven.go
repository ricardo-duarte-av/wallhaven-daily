package main

import (
        "encoding/json"
        "fmt"
        "io/ioutil"
        "log"
        "net/http"
        "strconv"
        "sync"
        "time"
)

// Rate limiter for Wallhaven API calls
var wallhavenRateLimiter = struct {
        mu       sync.Mutex
        lastCall time.Time
        minDelay time.Duration
}{
        minDelay: 500 * time.Millisecond, // Minimum 500ms between API calls
}

// waitForRateLimit ensures we don't exceed Wallhaven API rate limits
func waitForRateLimit() {
        wallhavenRateLimiter.mu.Lock()
        defer wallhavenRateLimiter.mu.Unlock()
        
        elapsed := time.Since(wallhavenRateLimiter.lastCall)
        if elapsed < wallhavenRateLimiter.minDelay {
                time.Sleep(wallhavenRateLimiter.minDelay - elapsed)
        }
        wallhavenRateLimiter.lastCall = time.Now()
}

type WallhavenImage struct {
        ID        string   `json:"id"`
        URL       string   `json:"url"`
        Uploader  struct {
                Username string `json:"username"`
        } `json:"uploader"`
        Resolution string   `json:"resolution"`
        FileSize   int      `json:"file_size"`
        FileType   string   `json:"file_type"`
        Path       string   `json:"path"`
        Thumbs     struct {
                Original string `json:"original"`
                Large    string `json:"large"`
                Small    string `json:"small"`
        } `json:"thumbs"`
        Tags []struct {
                Name string `json:"name"`
        } `json:"tags"`
}

type WallhavenImageResponse struct {
        Data WallhavenImage `json:"data"`
}

type WallhavenSearchResponse struct {
        Data []struct {
                ID string `json:"id"`
        } `json:"data"`
}

// RateLimitInfo holds rate limit information from response headers
type RateLimitInfo struct {
        Limit     int
        Remaining int
}

// ParseRateLimitHeaders extracts rate limit information from response headers
func ParseRateLimitHeaders(resp *http.Response) RateLimitInfo {
        info := RateLimitInfo{}
        if limitStr := resp.Header.Get("X-Ratelimit-Limit"); limitStr != "" {
                if limit, err := strconv.Atoi(limitStr); err == nil {
                        info.Limit = limit
                }
        }
        if remainingStr := resp.Header.Get("X-Ratelimit-Remaining"); remainingStr != "" {
                if remaining, err := strconv.Atoi(remainingStr); err == nil {
                        info.Remaining = remaining
                }
        }
        return info
}

// CalculateAdaptiveDelay calculates delay based on remaining rate limit
// Returns delay in seconds
func CalculateAdaptiveDelay(remaining, limit int) int {
        if limit == 0 {
                // If we don't know the limit, use default
                return 1
        }
        
        // Calculate percentage remaining
        percentage := float64(remaining) / float64(limit)
        
        switch {
        case percentage > 0.66: // > 66% remaining
                return 1 // Fast when plenty of quota
        case percentage > 0.33: // 33-66% remaining
                return 2 // Slightly slower
        case percentage > 0.20: // 20-33% remaining
                return 5 // Moderate slowdown
        case percentage > 0.10: // 10-20% remaining
                return 10 // Significant slowdown
        case percentage > 0.05: // 5-10% remaining
                return 20 // Very slow
        default: // < 5% remaining
                return 45 // Very conservative
        }
}

// Rate limiting helper functions
func handleRateLimit(resp *http.Response) error {
        if resp.StatusCode == 429 {
                retryAfter := resp.Header.Get("Retry-After")
                if retryAfter != "" {
                        if seconds, err := strconv.Atoi(retryAfter); err == nil {
                                log.Printf("Rate limited. Waiting %d seconds before retry...", seconds)
                                time.Sleep(time.Duration(seconds) * time.Second)
                                return nil
                        }
                }
                // Default wait time if Retry-After header is missing or invalid
                log.Printf("Rate limited. Waiting 60 seconds before retry...")
                time.Sleep(60 * time.Second)
                return nil
        }
        return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
}

func makeRateLimitedRequest(req *http.Request, client *http.Client, maxRetries int) (*http.Response, error) {
        for attempt := 0; attempt < maxRetries; attempt++ {
                resp, err := client.Do(req)
                if err != nil {
                        return nil, err
                }
                
                if resp.StatusCode == 429 {
                        if err := handleRateLimit(resp); err != nil {
                                resp.Body.Close()
                                return nil, err
                        }
                        resp.Body.Close()
                        log.Printf("Retrying request (attempt %d/%d)...", attempt+1, maxRetries)
                        continue
                }
                
                if resp.StatusCode >= 200 && resp.StatusCode < 300 {
                        return resp, nil
                }
                
                resp.Body.Close()
                if attempt == maxRetries-1 {
                        return nil, fmt.Errorf("HTTP %d after %d attempts: %s", resp.StatusCode, maxRetries, resp.Status)
                }
                
                log.Printf("HTTP %d, retrying in 5 seconds (attempt %d/%d)...", resp.StatusCode, attempt+1, maxRetries)
                time.Sleep(5 * time.Second)
        }
        
        return nil, fmt.Errorf("max retries exceeded")
}

// FetchNewWallhavenImageIDs returns only the image IDs that need to be processed (not already sent)
func (cfg *Config) FetchNewWallhavenImageIDs(db *Database, toprange string) ([]string, RateLimitInfo, error) {
        api := fmt.Sprintf(
                "https://wallhaven.cc/api/v1/search?apikey=%s&categories=%s&purity=%s&sorting=%s&topRange=%s&order=%s",
                cfg.Wallhaven.APIToken,
                cfg.Wallhaven.Categories,
                cfg.Wallhaven.Purity,
                cfg.Wallhaven.Sorting,
                toprange,
                cfg.Wallhaven.Order,
        )
        if cfg.Debug {
                log.Printf("Search API URL: %v", api)
        }
        req, _ := http.NewRequest("GET", api, nil)
        req.Header.Set("User-Agent", cfg.Wallhaven.UserAgent)
        client := &http.Client{Timeout: 15 * time.Second}
        
        // Use rate-limited request with retries
        resp, err := makeRateLimitedRequest(req, client, 3)
        if err != nil {
                return nil, RateLimitInfo{}, err
        }
        defer resp.Body.Close()
        body, _ := ioutil.ReadAll(resp.Body)
        
        // Parse rate limit info from headers
        rateLimitInfo := ParseRateLimitHeaders(resp)
        
        // Lightweight debug logging (avoid dumping full headers/body)
        if cfg.Debug {
                log.Printf("Search API Response Status: %d (rate limit remaining %d/%d)",
                        resp.StatusCode, rateLimitInfo.Remaining, rateLimitInfo.Limit)
        }
        
        var searchRes WallhavenSearchResponse
        if err := json.Unmarshal(body, &searchRes); err != nil {
                log.Printf("JSON unmarshal error: %v", err)
                return nil, rateLimitInfo, err
        }

        log.Printf("Search returned %d image IDs to check", len(searchRes.Data))
        var imageIDs []string
        skippedCount := 0
        for idx, img := range searchRes.Data {
                if idx > 0 && idx%10 == 0 {
                        log.Printf("Progress: Checked %d/%d search results (%d new images, %d already sent)", 
                                idx, len(searchRes.Data), len(imageIDs), skippedCount)
                }
                sent, err := db.IsSent(img.ID)
                if err != nil {
                        log.Printf("DB error for image %s: %v", img.ID, err)
                        skippedCount++
                        continue
                }
                if sent {
                        // Skip silently - already sent
                        skippedCount++
                        continue
                }
                imageIDs = append(imageIDs, img.ID)
        }
        log.Printf("Found %d new images to process (skipped %d already sent)", len(imageIDs), skippedCount)
        return imageIDs, rateLimitInfo, nil
}

func FetchWallhavenImage(cfg *Config, id string) (WallhavenImage, error) {
        // Rate limit: ensure we don't make too many requests too quickly
        waitForRateLimit()
        
        api := fmt.Sprintf("https://wallhaven.cc/api/v1/w/%s?apikey=%s", id, cfg.Wallhaven.APIToken)
        if cfg.Debug {
                log.Printf("Image API URL: %v", api)
        }
        req, _ := http.NewRequest("GET", api, nil)
        req.Header.Set("User-Agent", cfg.Wallhaven.UserAgent)
        client := &http.Client{Timeout: 10 * time.Second}
        
        // Use rate-limited request with retries
        resp, err := makeRateLimitedRequest(req, client, 3)
        if err != nil {
                log.Printf("Image url: %v", api)
                log.Printf("Request failed: %v", err)
                return WallhavenImage{}, err
        }
        defer resp.Body.Close()
        body, _ := ioutil.ReadAll(resp.Body)
        
        // Lightweight debug logging (avoid dumping full headers/body)
        if cfg.Debug {
                log.Printf("Image API Response Status: %d", resp.StatusCode)
        }
        
        var imgRes WallhavenImageResponse
        if err := json.Unmarshal(body, &imgRes); err != nil {
                log.Printf("Not sending image %s: JSON unmarshal error: %v", id, err)
                return WallhavenImage{}, err
        }
        return imgRes.Data, nil
}
