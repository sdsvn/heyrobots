package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Endpoints the model lists come from; tests point them at fakes.
var (
	AnthropicModelsURL = "https://api.anthropic.com/v1/models?limit=1000"
	GeminiModelsURL    = "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000"
)

// List asks a provider which models the saved key can use, sorted by name,
// so the person can switch between them. Your own ACP agent picks its own
// model, so it has none to list.
func List(ctx context.Context, c Choice, key string, hc *http.Client) ([]string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	var req *http.Request
	var err error
	switch c.Provider {
	case Anthropic:
		if key == "" {
			return nil, errors.New("no Anthropic API key is saved")
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, AnthropicModelsURL, nil)
		if err == nil {
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case OpenAI:
		base := NormalizeURL(c.BaseURL)
		if base == "" {
			return nil, errors.New("no address for this provider")
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
		if err == nil && key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	case Gemini:
		if key == "" {
			return nil, errors.New("no Gemini API key is saved")
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, GeminiModelsURL+"&key="+url.QueryEscape(key), nil)
	case ClaudeCode:
		return []string{}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", c.Provider)
	}
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(msg))}
	}
	var body struct {
		// Anthropic and OpenAI-compatible providers.
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		// Gemini.
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("the provider's model list couldn't be read: %w", err)
	}
	out := []string{}
	for _, d := range body.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	for _, m := range body.Models {
		// Only models that can hold a conversation.
		if slices.Contains(m.Methods, "generateContent") {
			out = append(out, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
