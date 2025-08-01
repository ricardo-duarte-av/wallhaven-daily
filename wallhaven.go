package main

import (
        "encoding/json"
        "fmt"
        "io/ioutil"
        "log"
        "net/http"
        "strconv"
        "time"
)

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

func (cfg *Config) FetchNewWallhavenImages(db *Database, toprange string) ([]WallhavenImage, error) {
        api := fmt.Sprintf(
                "https://wallhaven.cc/api/v1/search?apikey=%s&categories=%s&purity=%s&sorting=%s&topRange=%s&order=%s",
                cfg.Wallhaven.APIToken,
                cfg.Wallhaven.Categories,
                cfg.Wallhaven.Purity,
                cfg.Wallhaven.Sorting,
                //cfg.Wallhaven.Toprange,
                toprange,
                cfg.Wallhaven.Order,
        )
        log.Printf("Search API URL: %v", api)
        req, _ := http.NewRequest("GET", api, nil)
        req.Header.Set("User-Agent", cfg.Wallhaven.UserAgent)
        client := &http.Client{Timeout: 15 * time.Second}
        
        // Use rate-limited request with retries
        resp, err := makeRateLimitedRequest(req, client, 3)
        if err != nil {
                return nil, err
        }
        defer resp.Body.Close()
        body, _ := ioutil.ReadAll(resp.Body)
        
        // Debug logging
        log.Printf("Search API Response Status: %d", resp.StatusCode)
        log.Printf("Search API Response Headers: %v", resp.Header)
        if len(body) > 200 {
                log.Printf("Search API Response Body (first 200 chars): %s", string(body[:200]))
        } else {
                log.Printf("Search API Response Body: %s", string(body))
        }
        
        var searchRes WallhavenSearchResponse
        if err := json.Unmarshal(body, &searchRes); err != nil {
                log.Printf("JSON unmarshal error: %v", err)
                return nil, err
        }

        var images []WallhavenImage
        for _, img := range searchRes.Data {
                sent, err := db.IsSent(img.ID)
                if err != nil {
                        log.Printf("DB error for image %s: %v", img.ID, err)
                        continue
                }
                if sent {
                        continue
                }
                image, err := FetchWallhavenImage(cfg, img.ID)
                if err != nil {
                        log.Printf("Failed to fetch image info: %v", err)
                        continue
                }
                images = append(images, image)
                
                // Add a small delay between image requests to be respectful to the API
                time.Sleep(500 * time.Millisecond)
        }
        return images, nil
}

func FetchWallhavenImage(cfg *Config, id string) (WallhavenImage, error) {
        api := fmt.Sprintf("https://wallhaven.cc/api/v1/w/%s?apikey=%s", id, cfg.Wallhaven.APIToken)
        log.Printf("Image API URL: %v", api)
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
        
        // Debug logging
        log.Printf("Image API Response Status: %d", resp.StatusCode)
        log.Printf("Image API Response Headers: %v", resp.Header)
        if len(body) > 200 {
                log.Printf("Image API Response Body (first 200 chars): %s", string(body[:200]))
        } else {
                log.Printf("Image API Response Body: %s", string(body))
        }
        
        var imgRes WallhavenImageResponse
        if err := json.Unmarshal(body, &imgRes); err != nil {
                log.Printf("Image JSON unmarshal error: %v", err)
                return WallhavenImage{}, err
        }
        return imgRes.Data, nil
}
