package main

import (
    "image"
    "io"
    "net/http"
    "os"

    "github.com/disintegration/imaging"
)

func DownloadToTempFile(url string, prefix string) (string, error) {
    resp, err := http.Get(url)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    tmpFile, err := os.CreateTemp("", prefix+"-*.jpg")
    if err != nil {
        return "", err
    }
    defer tmpFile.Close()

    _, err = io.Copy(tmpFile, resp.Body)
    if err != nil {
        os.Remove(tmpFile.Name())
        return "", err
    }
    return tmpFile.Name(), nil
}

// CreateThumbnailMax800 reads the image at sourcePath, resizes it so the longest
// dimension is 800px (aspect ratio preserved), and writes a JPEG to a temp file.
// Returns the temp file path. Caller should remove it when done.
func CreateThumbnailMax800(sourcePath string) (thumbPath string, err error) {
    img, err := imaging.Open(sourcePath)
    if err != nil {
        return "", err
    }
    bounds := img.Bounds()
    w, h := bounds.Dx(), bounds.Dy()
    var thumb *image.NRGBA
    if w >= h {
        thumb = imaging.Resize(img, 800, 0, imaging.Lanczos)
    } else {
        thumb = imaging.Resize(img, 0, 800, imaging.Lanczos)
    }
    tmpFile, err := os.CreateTemp("", "thumb800-*.jpg")
    if err != nil {
        return "", err
    }
    defer tmpFile.Close()
    if err := imaging.Encode(tmpFile, thumb, imaging.JPEG, imaging.JPEGQuality(88)); err != nil {
        os.Remove(tmpFile.Name())
        return "", err
    }
    return tmpFile.Name(), nil
}
