package main

import (
        "encoding/json"
        "fmt"
        "io/ioutil"
        "log"
        "net/http"
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

func (cfg *Config) FetchNewWallhavenImages(db *Database, toprange string) ([]WallhavenImage, error) {
        api := fmt.Sprintf(
                "https://wallhaven.cc/api/v1/search?apikey=%s&categories=%s&purity=%s&sorting=%s&topRange=%s&order=%s&ai_art_filter=%s",
                cfg.Wallhaven.APIToken,
                cfg.Wallhaven.Categories,
                cfg.Wallhaven.Purity,
                cfg.Wallhaven.Sorting,
                //cfg.Wallhaven.Toprange,
                toprange,
                cfg.Wallhaven.Order,
                cfg.Wallhaven.AIFilter,
        )
        //log.Printf("API URL: %v", api)
        req, _ := http.NewRequest("GET", api, nil)
        req.Header.Set("User-Agent", cfg.Wallhaven.UserAgent)
        client := &http.Client{Timeout: 15 * time.Second}
        resp, err := client.Do(req)
        if err != nil {
                return nil, err
        }
        defer resp.Body.Close()
        body, _ := ioutil.ReadAll(resp.Body)
        var searchRes WallhavenSearchResponse
        if err := json.Unmarshal(body, &searchRes); err != nil {
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
        }
        return images, nil
}

func FetchWallhavenImage(cfg *Config, id string) (WallhavenImage, error) {
        api := fmt.Sprintf("https://wallhaven.cc/api/v1/w/%s?apikey=%s", id, cfg.Wallhaven.APIToken)
        req, _ := http.NewRequest("GET", api, nil)
        req.Header.Set("User-Agent", cfg.Wallhaven.UserAgent)
        client := &http.Client{Timeout: 10 * time.Second}
        resp, err := client.Do(req)
        if err != nil {
                log.Printf("Image url: %v", api)
                log.Printf("Result: %v", resp)
                return WallhavenImage{}, err
        }
        defer resp.Body.Close()
        body, _ := ioutil.ReadAll(resp.Body)
        var imgRes WallhavenImageResponse
        if err := json.Unmarshal(body, &imgRes); err != nil {
                return WallhavenImage{}, err
        }
        return imgRes.Data, nil
}
