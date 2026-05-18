package moderation

import (
	"context"
	"strings"
)

// Config is what the worker reads at boot. Comes from env via the
// pkg/config layer; the names mirror env vars 1:1 for grep-ability.
type Config struct {
	Enabled         bool
	Providers       []string // ordered fallback chain: e.g. ["anthropic","openai"]
	IntervalSeconds int      // sweep cadence
	Threshold       float64  // any category score >= this fires a report
	AnthropicKey    string
	AnthropicModel  string
	OpenAIKey       string
	OpenAIModel     string
	OpenAIBaseURL   string // override for OpenAI-compat local servers (Ollama/vLLM)
	GeminiKey       string
	GeminiModel     string
	LocalEndpoint   string // OpenAI-compat local endpoint (Ollama etc.). If set, registers a "local" provider.
	LocalModel      string
}

// Build assembles concrete Provider instances from the config in the
// order they should be tried. Returns a non-empty slice if at least
// one provider was successfully configured, or ErrNoProviders
// otherwise (worker then logs + exits cleanly so a misconfigured
// MODERATION_ENABLED=true doesn't crash the api).
func Build(c Config) ([]Provider, error) {
	if !c.Enabled {
		return nil, ErrNoProviders
	}
	out := make([]Provider, 0, len(c.Providers))
	for _, name := range c.Providers {
		switch strings.TrimSpace(strings.ToLower(name)) {
		case "anthropic", "claude":
			if c.AnthropicKey == "" {
				continue
			}
			out = append(out, NewAnthropicProvider(c.AnthropicKey, c.AnthropicModel))
		case "openai", "gpt":
			if c.OpenAIKey == "" {
				continue
			}
			out = append(out, NewOpenAIProvider(c.OpenAIKey, c.OpenAIModel, c.OpenAIBaseURL))
		case "gemini", "google":
			if c.GeminiKey == "" {
				continue
			}
			out = append(out, NewGeminiProvider(c.GeminiKey, c.GeminiModel))
		case "local":
			if c.LocalEndpoint == "" {
				continue
			}
			// Local = OpenAI-compat server. apiKey may be empty for
			// most self-hosted setups (Ollama, vLLM); pass an empty
			// string and the provider will skip the Authorization
			// header.
			out = append(out, NewOpenAIProvider("", c.LocalModel, c.LocalEndpoint))
		default:
			return nil, ErrUnknownKind
		}
	}
	if len(out) == 0 {
		return nil, ErrNoProviders
	}
	return out, nil
}

// First returns the first provider that returns a Verdict without
// erroring. Used for the cheap default (primary + fallback chain).
// For ensemble voting, the worker should call each provider directly
// and aggregate — not worth a separate method for two cases.
func First(ctx context.Context, providers []Provider, imageURL string) (*Verdict, []error) {
	var errs []error
	for _, p := range providers {
		v, err := p.ModerateImage(ctx, imageURL)
		if err == nil {
			return v, errs
		}
		errs = append(errs, err)
	}
	return nil, errs
}
