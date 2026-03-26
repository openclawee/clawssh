// LLM transport: use one OpenAI-compatible client for all providers.
package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// LLMClient performs a single non-streaming completion with system + user strings.
type LLMClient interface {
	Generate(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// NewLLMClientFromEnv builds a single OpenAI-compatible client.
//
// Primary override mode:
//   - CLAWSSH_OPENAI_BASE_URL (e.g. https://api.minimaxi.com/v1)
//   - CLAWSSH_OPENAI_MODEL
//   - CLAW_AI_API_KEY (or OPENAI_API_KEY)
//
// Provider presets (CLAWSSH_LLM_PROVIDER) are kept for convenience and map to
// OpenAI-compatible endpoints.
func NewLLMClientFromEnv(ctx context.Context) (LLMClient, error) {
	baseURL := strings.TrimSpace(os.Getenv("CLAWSSH_OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("CLAWSSH_OPENAI_MODEL"))
	key := firstNonEmpty(
		os.Getenv("CLAW_AI_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
	)

	if baseURL != "" {
		if key == "" {
			return nil, fmt.Errorf("set CLAW_AI_API_KEY or OPENAI_API_KEY for CLAWSSH_OPENAI_BASE_URL mode")
		}
		if model == "" {
			return nil, fmt.Errorf("set CLAWSSH_OPENAI_MODEL when using CLAWSSH_OPENAI_BASE_URL")
		}
		return newOpenAICompatibleClient(ctx, openAICompatibleConfig{
			BaseURL: baseURL,
			Model:   model,
			APIKey:  key,
		})
	}

	p := strings.ToLower(strings.TrimSpace(os.Getenv("CLAWSSH_LLM_PROVIDER")))
	if p == "" {
		// 未显式指定时：若只有 OPENAI_API_KEY，默认走 OpenAI，避免误连 Gemini 导致 401/不可用
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
			p = "openai"
		} else {
			p = "gemini"
		}
	}
	switch p {
	case "gemini", "google":
		key = firstNonEmpty(os.Getenv("CLAW_AI_API_KEY"), os.Getenv("GOOGLE_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("set CLAW_AI_API_KEY or GOOGLE_API_KEY for Gemini")
		}
		model = strings.TrimSpace(os.Getenv("CLAWSSH_GEMINI_MODEL"))
		if model == "" {
			model = "gemini-2.0-flash"
		}
		return newOpenAICompatibleClient(ctx, openAICompatibleConfig{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
			Model:   model,
			APIKey:  key,
		})
	case "anthropic", "claude":
		key = firstNonEmpty(os.Getenv("CLAW_AI_API_KEY"), os.Getenv("ANTHROPIC_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("set CLAW_AI_API_KEY or ANTHROPIC_API_KEY for Anthropic")
		}
		model = strings.TrimSpace(os.Getenv("CLAWSSH_ANTHROPIC_MODEL"))
		if model == "" {
			model = "claude-3-5-sonnet-20241022"
		}
		// Anthropic native API is not OpenAI-compatible by default; users can
		// point CLAWSSH_OPENAI_BASE_URL to an Anthropic-compatible gateway if needed.
		return newOpenAICompatibleClient(ctx, openAICompatibleConfig{
			BaseURL: "https://api.anthropic.com/v1",
			Model:   model,
			APIKey:  key,
		})
	case "minimax":
		key = firstNonEmpty(os.Getenv("CLAW_AI_API_KEY"), os.Getenv("MINIMAX_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("set CLAW_AI_API_KEY or MINIMAX_API_KEY for MiniMax")
		}
		model = strings.TrimSpace(os.Getenv("CLAWSSH_MINIMAX_MODEL"))
		if model == "" {
			model = "MiniMax-M2.7"
		}
		return newOpenAICompatibleClient(ctx, openAICompatibleConfig{
			BaseURL: "https://api.minimaxi.com/v1",
			Model:   model,
			APIKey:  key,
		})
	case "openai":
		key = firstNonEmpty(os.Getenv("CLAW_AI_API_KEY"), os.Getenv("OPENAI_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("set CLAW_AI_API_KEY or OPENAI_API_KEY for OpenAI")
		}
		model = strings.TrimSpace(os.Getenv("CLAWSSH_OPENAI_MODEL"))
		if model == "" {
			model = "gpt-4o-mini"
		}
		return newOpenAICompatibleClient(ctx, openAICompatibleConfig{
			BaseURL: "https://api.openai.com/v1",
			Model:   model,
			APIKey:  key,
		})
	default:
		return nil, fmt.Errorf("unknown CLAWSSH_LLM_PROVIDER %q (use gemini, anthropic, minimax, or openai)", p)
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}
