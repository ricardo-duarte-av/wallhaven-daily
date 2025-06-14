package main

import (
        "bytes"
        "encoding/base64"
        "encoding/json"
        "fmt"
        "io"
        "log"
        "net/http"
        "os"
)

type OpenAIResponse struct {
        Choices []struct {
                Message struct {
                        Content string `json:"content"`
                } `json:"message"`
        } `json:"choices"`
}

func GetOpenAIDescription(cfg *Config, imagePath string) (string, error) {
        file, err := os.Open(imagePath)
        if err != nil {
                return "", fmt.Errorf("failed to open image file: %w", err)
        }
        defer file.Close()
        imageData, err := io.ReadAll(file)
        if err != nil {
                return "", fmt.Errorf("failed to read image file: %w", err)
        }

        imageBase64 := base64.StdEncoding.EncodeToString(imageData)

        payload := map[string]interface{}{
                "model": "gpt-4o",
                "messages": []map[string]interface{}{
                        {
                                "role": "user",
                                "content": []map[string]interface{}{
                                        {"type": "text", "text": "Describe this image in less than 200 characters."},
                                        {"type": "image_url", "image_url": map[string]interface{}{
                                                "url": fmt.Sprintf("data:image/jpeg;base64,%s", imageBase64),
                                        }},
                                },
                        },
                },
                "max_tokens": 120,
        }

        jsonPayload, err := json.Marshal(payload)
        if err != nil {
                return "", fmt.Errorf("failed to marshal openai request: %w", err)
        }

        req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonPayload))
        if err != nil {
                return "", fmt.Errorf("failed to create openai request: %w", err)
        }
        req.Header.Set("Authorization", "Bearer "+cfg.OpenAIKey)
        req.Header.Set("Content-Type", "application/json")

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
                return "", fmt.Errorf("failed to send request to openai: %w", err)
        }
        defer resp.Body.Close()

        body, _ := io.ReadAll(resp.Body)

        if resp.StatusCode != 200 {
                log.Printf("OpenAI API error status %d: %s", resp.StatusCode, string(body))
                return "", fmt.Errorf("openai api error: %s", string(body))
        }

        var openaiResp OpenAIResponse
        if err := json.Unmarshal(body, &openaiResp); err != nil {
                log.Printf("OpenAI API unmarshal error: %v, body: %s", err, string(body))
                return "", fmt.Errorf("failed to decode openai response: %w", err)
        }
        if len(openaiResp.Choices) == 0 {
                log.Printf("OpenAI API returned no choices. Body: %s", string(body))
                return "", fmt.Errorf("no description returned from openai")
        }

        return openaiResp.Choices[0].Message.Content, nil
}
