package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type openAICompatibleConfig struct {
	BaseURL string
	Model   string
	APIKey  string
}

// openAICompatibleClient is a generic Chat Completions client shared across providers.
type openAICompatibleClient struct {
	baseURL string
	model   string
	apiKey  string
	hc      *http.Client
}

func newOpenAICompatibleClient(_ context.Context, cfg openAICompatibleConfig) (LLMClient, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("openai client base url is empty")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("openai client model is empty")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("openai client api key is empty")
	}
	return &openAICompatibleClient{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		model:   strings.TrimSpace(cfg.Model),
		apiKey:  strings.TrimSpace(cfg.APIKey),
		hc:      &http.Client{Timeout: 120 * time.Second},
	}, nil
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Temperature float32             `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (c *openAICompatibleClient) Generate(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	body := openAIChatRequest{
		Model: c.model,
		Messages: []openAIChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
		Temperature: 0.2,
		MaxTokens:   2048,
		Stream:      false,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(raw),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed openAIChatResponse
	_ = json.Unmarshal(respBody, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", fmt.Errorf("openai-compatible http %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return "", fmt.Errorf("openai-compatible http %d: %s", resp.StatusCode, truncateForLog(string(respBody), 500))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("openai-compatible api: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty completion response")
	}
	out := parsed.Choices[0].Message.Content
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("empty completion content")
	}
	return out, nil
}
