package main

import (
    "io"
    "net/http"
    "os"
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
