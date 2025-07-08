package main

import (
        "bytes"
        "context"
        "fmt"
        "image"
        _ "image/jpeg"
        _ "image/png"
        "io"
        "io/ioutil"
        "log"
        "net/http"
        "path"
        "strings"
        "time"
        "os"

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
                return &MatrixBot{
                        client:    client,
                        roomID:    id.RoomID(cfg.Matrix.RoomID),
                        tokenFile: cfg.Matrix.TokenFile,
                }, nil
        }

        client, err = mautrix.NewClient(cfg.Matrix.ServerURL, id.UserID(cfg.Matrix.User), token)
        if err != nil {
                return nil, err
        }
        return &MatrixBot{
                client:    client,
                roomID:    id.RoomID(cfg.Matrix.RoomID),
                tokenFile: cfg.Matrix.TokenFile,
        }, nil
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

func (m *MatrixBot) SendImage(img WallhavenImage, cfg *Config, openaiDescription string) error {
        ctx := context.Background()

        filename := path.Base(img.Path)

        // Download thumbnail to temp file for OpenAI
        tmpfile, err := ioutil.TempFile("", "thumb-*.jpg")
        if err != nil {
                log.Printf("Error with TempFile: %v", err)
                return err
        }
        defer os.Remove(tmpfile.Name())

        resp, err := http.Get(img.Thumbs.Original)
        if err != nil {
                log.Printf("Error getting image: %v", err)
                return err
        }
        defer resp.Body.Close()
        io.Copy(tmpfile, resp.Body)
        tmpfile.Close()

        // Get OpenAI description
        //openaiDescription, err := GetOpenAIDescription(cfg, tmpfile.Name())
        //if err != nil {
        //      openaiDescription = "(No AI description available)"
        //}

        // Log what will be sent
        caption := buildCaption(img, openaiDescription)
        log.Printf("Sending image to Matrix:\n%s\n", caption)

        // Upload original image
        mainResp, err := m.client.UploadLink(ctx, img.Path)
        if err != nil {
            if httpErr, ok := err.(*mautrix.HTTPError); ok {
                log.Printf("Matrix image upload error: %s - %s", httpErr.Message, httpErr.ResponseBody)
            } else {
                log.Printf("Matrix image upload error: %v", err)
            }
            return err
        }

        // Upload thumbnail
        thumbResp, err := m.client.UploadLink(ctx, img.Thumbs.Original)
        if err != nil {
            if httpErr, ok := err.(*mautrix.HTTPError); ok {
                log.Printf("Matrix image upload error: %s - %s", httpErr.Message, httpErr.ResponseBody)
            } else {
                log.Printf("Matrix image upload error: %v", err)
            }
            return err                
        }
        log.Printf("Matrix: thumbnail uploaded.")

        // Compute blurhash
        blurhashStr, err := computeBlurhashFromURL(img.Thumbs.Original)
        if err != nil {
                blurhashStr = ""
        }
        log.Printf("Matrix: blurhash calculated.")

        info := map[string]interface{}{
                "mimetype":      img.FileType,
                "size":          img.FileSize,
                "thumbnail_url":  thumbResp.ContentURI,
                "thumbnail_info": map[string]interface{}{"mimetype": img.FileType},
                "blurhash":      blurhashStr,
        }

        content := map[string]interface{}{
                "msgtype":  "m.image",
                "filename": filename,
                "body":     caption,
                "url":      mainResp.ContentURI,
                "info":     info,
        }

        _, err = m.client.SendMessageEvent(ctx, m.roomID, event.EventMessage, content)
        log.Printf("Matrix: sent.")
        if err != nil {
            if httpErr, ok := err.(*mautrix.HTTPError); ok {
                log.Printf("Matrix HTTP error: %s - %s", httpErr.Message, httpErr.ResponseBody)
            } else {
                log.Printf("Matrix send error: %v", err)
            }
        }
        log.Printf("Return this: %v", err)
        return err
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



// Downloads the image from the given URL, decodes, and produces a blurhash.
func computeBlurhashFromURL(imageURL string) (string, error) {
        resp, err := http.Get(imageURL)
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()
        imgData, err := io.ReadAll(resp.Body)
        if err != nil {
                return "", err
        }
        img, _, err := image.Decode(bytes.NewReader(imgData))
        if err != nil {
                return "", err
        }
        return blurhash.Encode(4, 3, img)
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
