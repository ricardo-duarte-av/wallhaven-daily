package main

import (
        "bytes"
        "context"
        "fmt"
        "image"
        _ "image/jpeg"
        "image/gif"
        _ "image/png"
        "io"
        "io/ioutil"
        "log"
        "net/http"
        "path"
        "strings"
        "time"
        "errors"

        "github.com/buckket/go-blurhash"
        "maunium.net/go/mautrix"
        "maunium.net/go/mautrix/event"
        "maunium.net/go/mautrix/id"
)

type MatrixBot struct {
        client    *mautrix.Client
        roomID    id.RoomID
        tokenFile string
}

func NewMatrixBot(cfg *Config) (*MatrixBot, error) {
        var token string
        token, err := loadToken(cfg.Matrix.TokenFile)
        var client *mautrix.Client
        if err != nil || token == "" {
                client, err = mautrix.NewClient(cfg.Matrix.ServerURL, "", "")
                if err != nil {
                        return nil, err
                }
                resp, err := client.Login(context.Background(), &mautrix.ReqLogin{
                        Type: "m.login.password",
                        Identifier: mautrix.UserIdentifier{
                                Type: "m.id.user",
                                User: cfg.Matrix.User,
                        },
                        Password: cfg.Matrix.Password,
                })
                if err != nil {
                        return nil, err
                }
                token = resp.AccessToken
                saveToken(cfg.Matrix.TokenFile, token)
                client, err = mautrix.NewClient(cfg.Matrix.ServerURL, id.UserID(cfg.Matrix.User), token)
                if err != nil {
                        return nil, err
                }
                bot := &MatrixBot{
                        client:    client,
                        roomID:    id.RoomID(cfg.Matrix.RoomID),
                        tokenFile: cfg.Matrix.TokenFile,
                }
                
                // Validate newly created token
                ctx := context.Background()
                whoami, err := client.Whoami(ctx)
                if err != nil {
                        log.Printf("Matrix: Warning - newly created token validation failed (whoami): %v", err)
                        log.Printf("Matrix: Bot created but token may be invalid. Room: %s", cfg.Matrix.RoomID)
                } else {
                        log.Printf("Matrix: Bot created successfully with new login. User: %s, Room: %s", whoami.UserID, cfg.Matrix.RoomID)
                }
                
                return bot, nil
        }

        client, err = mautrix.NewClient(cfg.Matrix.ServerURL, id.UserID(cfg.Matrix.User), token)
        if err != nil {
                return nil, err
        }
        
        // Validate token by calling Whoami
        ctx := context.Background()
        whoami, err := client.Whoami(ctx)
        if err != nil {
                // Check if it's an invalid token error (401/M_UNKNOWN_TOKEN)
                var httpErr *mautrix.HTTPError
                if errors.As(err, &httpErr) && httpErr.Response != nil && httpErr.Response.StatusCode == 401 {
                        log.Printf("Matrix: Token validation failed (401/M_UNKNOWN_TOKEN), re-authenticating...")
                        
                        // Re-authenticate to get a new token
                        tempClient, err := mautrix.NewClient(cfg.Matrix.ServerURL, "", "")
                        if err != nil {
                                return nil, fmt.Errorf("failed to create client for re-auth: %w", err)
                        }
                        
                        resp, err := tempClient.Login(ctx, &mautrix.ReqLogin{
                                Type: "m.login.password",
                                Identifier: mautrix.UserIdentifier{
                                        Type: "m.id.user",
                                        User: cfg.Matrix.User,
                                },
                                Password: cfg.Matrix.Password,
                        })
                        if err != nil {
                                return nil, fmt.Errorf("re-authentication failed: %w", err)
                        }
                        
                        // Save the new token
                        token = resp.AccessToken
                        if err := saveToken(cfg.Matrix.TokenFile, token); err != nil {
                                log.Printf("Matrix: Warning - failed to save new token: %v", err)
                        } else {
                                log.Printf("Matrix: New token saved to %s", cfg.Matrix.TokenFile)
                        }
                        
                        // Create client with new token
                        client, err = mautrix.NewClient(cfg.Matrix.ServerURL, id.UserID(cfg.Matrix.User), token)
                        if err != nil {
                                return nil, fmt.Errorf("failed to create client with new token: %w", err)
                        }
                        
                        // Validate the new token
                        whoami, err = client.Whoami(ctx)
                        if err != nil {
                                log.Printf("Matrix: Warning - new token validation failed: %v", err)
                        } else {
                                log.Printf("Matrix: Re-authenticated successfully. User: %s, Room: %s", whoami.UserID, cfg.Matrix.RoomID)
                        }
                } else {
                        log.Printf("Matrix: Warning - token validation failed (whoami): %v", err)
                        log.Printf("Matrix: Bot created but token may be invalid. Room: %s", cfg.Matrix.RoomID)
                }
        } else {
                log.Printf("Matrix: Bot created successfully. User: %s, Room: %s", whoami.UserID, cfg.Matrix.RoomID)
        }
        
        bot := &MatrixBot{
                client:    client,
                roomID:    id.RoomID(cfg.Matrix.RoomID),
                tokenFile: cfg.Matrix.TokenFile,
        }
        
        return bot, nil
}

func loadToken(filename string) (string, error) {
        data, err := ioutil.ReadFile(filename)
        if err != nil {
                return "", err
        }
        return string(data), nil
}

func saveToken(filename, token string) error {
        return ioutil.WriteFile(filename, []byte(token), 0600)
}

func (m *MatrixBot) SendImage(img WallhavenImage, cfg *Config, openaiDescription string, imagePath, thumbPath string) error {
        ctx := context.Background()

        log.Printf("Matrix: Starting to send image %s to room %s", img.ID, m.roomID)
        filename := path.Base(img.Path)

        // Load main image from local file (already downloaded by main)
        mainImgData, err := ioutil.ReadFile(imagePath)
        if err != nil {
                log.Printf("Error reading main image from %s: %v", imagePath, err)
                return err
        }
        mainImg, _, err := image.Decode(bytes.NewReader(mainImgData))
        if err != nil {
                log.Printf("Error decoding main image: %v", err)
                return err
        }

        // Load our custom thumbnail (800px max dimension) from local file
        thumbImgData, err := ioutil.ReadFile(thumbPath)
        if err != nil {
                log.Printf("Error reading thumbnail from %s: %v", thumbPath, err)
                return err
        }
        thumbImg, _, err := image.Decode(bytes.NewReader(thumbImgData))
        if err != nil {
                log.Printf("Error decoding thumbnail: %v", err)
                return err
        }

        // Log what will be sent
        caption := buildCaption(img, openaiDescription)
        log.Printf("Sending image to Matrix:\n%s\n", caption)

        // Upload original image
        log.Printf("Matrix: Attempting to upload original image from %s", img.Path)
        mainResp, err := m.client.UploadLink(ctx, img.Path)
        if err != nil {
            log.Printf("Matrix image upload error type: %T", err)
                var httpErr *mautrix.HTTPError
                if errors.As(err, &httpErr) {
                    log.Printf("Matrix image upload error: Message=%q", httpErr.Message)
                    log.Printf("Matrix image upload error: ResponseBody=%q", httpErr.ResponseBody)
                    log.Printf("Matrix image upload error: RespError=%#v", httpErr.RespError)
                    log.Printf("Matrix image upload error: WrappedError=%v", httpErr.WrappedError)
                    if httpErr.Response != nil {
                        log.Printf("Matrix image upload error: HTTP Status=%d", httpErr.Response.StatusCode)
                    }
            } else {
                log.Printf("Matrix image upload error: %v", err)
            }
            log.Printf("Matrix: Returning on UploadLink.")
            return err
        }
        log.Printf("Matrix: Original image uploaded successfully, URI: %s", mainResp.ContentURI)

        // Upload our custom thumbnail (800px max) as bytes
        log.Printf("Matrix: Uploading custom thumbnail (800px max) from %s", thumbPath)
        thumbResp, err := m.client.UploadBytes(ctx, thumbImgData, "image/jpeg")
        if err != nil {
            if httpErr, ok := err.(*mautrix.HTTPError); ok {
                log.Printf("Matrix thumbnail upload error: %s - %s", httpErr.Message, httpErr.ResponseBody)
            } else {
                log.Printf("Matrix thumbnail upload error: %v", err)
            }
            log.Printf("Matrix: Returning on Thumbnail.")
            return err
        }
        log.Printf("Matrix: Thumbnail uploaded successfully, URI: %s", thumbResp.ContentURI)

        // Compute blurhash from already decoded thumbnail
        blurhashStr, err := computeBlurhash(thumbImg)
        if err != nil {
                blurhashStr = ""
                if cfg.Debug {
                        log.Printf("Warning: Could not compute blurhash: %v", err)
                }
        } else if cfg.Debug {
                log.Printf("Matrix: blurhash calculated.")
        }

        // Get image dimensions from already decoded images
        mainBounds := mainImg.Bounds()
        mainWidth, mainHeight := mainBounds.Dx(), mainBounds.Dy()
        thumbBounds := thumbImg.Bounds()
        thumbWidth, thumbHeight := thumbBounds.Dx(), thumbBounds.Dy()

        // Check if image is animated (check main image, not thumbnail)
        isAnimated := isImageAnimated(mainImgData)

        thumbnailInfo := map[string]interface{}{
                "mimetype": "image/jpeg",
                "w":        thumbWidth,
                "h":        thumbHeight,
        }

        info := map[string]interface{}{
                "mimetype":       img.FileType,
                "size":           img.FileSize,
                "thumbnail_url":  thumbResp.ContentURI,
                "thumbnail_info": thumbnailInfo,
                "xyz.amorgan.blurhash": blurhashStr,
                "w":              mainWidth,
                "h":              mainHeight,
                "is_animated":    isAnimated,
        }

        content := map[string]interface{}{
                "msgtype":  "m.image",
                "filename": filename,
                "body":     caption,
                "url":      mainResp.ContentURI,
                "info":     info,
        }

        log.Printf("Matrix: Sending message to room %s", m.roomID)
        _, err = m.client.SendMessageEvent(ctx, m.roomID, event.EventMessage, content)
        if err != nil {
            if httpErr, ok := err.(*mautrix.HTTPError); ok {
                log.Printf("Matrix HTTP error: %s - %s", httpErr.Message, httpErr.ResponseBody)
            } else {
                log.Printf("Matrix send error: %v", err)
            }
            log.Printf("Matrix: Failed to send message")
            return err
        }
        log.Printf("Matrix: Message sent successfully to room %s", m.roomID)
        return nil
}

func buildCaption(img WallhavenImage, openaiDescription string) string {
        var tags []string
        for _, tag := range img.Tags {
                tags = append(tags, tag.Name)
        }
        base := fmt.Sprintf(
                "Link: %s\nUploader: %s\nResolution: %s\nType: %s\nSize: %s\nTags: %s",
                img.URL, img.Uploader.Username, img.Resolution, img.FileType, humanFileSize(img.FileSize), strings.Join(tags, ", "),
        )

        desc := strings.TrimSpace(openaiDescription)
        if len(desc) >= 50 {
                return base + "\nDescription: " + desc
        }
        return base
}



// Downloads and decodes an image from a URL, returning both the raw bytes and decoded image.
// This allows reuse of the downloaded data for multiple operations.
func downloadAndDecodeImage(imageURL string) ([]byte, image.Image, error) {
        resp, err := http.Get(imageURL)
        if err != nil {
                return nil, nil, err
        }
        defer resp.Body.Close()
        imgData, err := io.ReadAll(resp.Body)
        if err != nil {
                return nil, nil, err
        }
        img, _, err := image.Decode(bytes.NewReader(imgData))
        if err != nil {
                return nil, nil, err
        }
        return imgData, img, nil
}

// Computes blurhash from an already decoded image.
func computeBlurhash(img image.Image) (string, error) {
        return blurhash.Encode(4, 3, img)
}

// Checks if an image is animated (e.g., animated GIF).
// Returns true if the image is animated, false otherwise.
func isImageAnimated(imageData []byte) bool {
        // Check if it's a GIF by trying to decode it as a GIF
        gifImg, err := gif.DecodeAll(bytes.NewReader(imageData))
        if err != nil {
                // Not a GIF or can't decode as GIF, assume not animated
                return false
        }
        // GIF is animated if it has more than one frame
        return len(gifImg.Image) > 1
}

// Formats the byte size into a human-readable string (KB, MB, etc).
func humanFileSize(bytes int) string {
        const (
                KB = 1 << 10
                MB = 1 << 20
                GB = 1 << 30
        )
        switch {
        case bytes >= GB:
                return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
        case bytes >= MB:
                return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
        case bytes >= KB:
                return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
        default:
                return fmt.Sprintf("%d bytes", bytes)
        }
}


// Add this helper to main.go for wait logging:
func logWait(waitTime int) {
        log.Printf("Waiting %d seconds before next run...\n", waitTime)
        time.Sleep(time.Duration(waitTime) * time.Second)
}
