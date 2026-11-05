package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
)

const geminiAICodeBaseURL = "https://aicode.googleapis.com"
const geminiAICodeUserAgent = "antigravity/cli/1.5.0 (aidev_client; os_type=darwin; arch=arm64; cl=1013440158; auth_method=consumer)"

// An old Code Assist refresh token cannot be upgraded to an AI Code token.
// Imported Antigravity credentials retain their client identity even with a stale project_id.
func (a *Account) IsGeminiAICode() bool {
	return a != nil && a.Platform == PlatformGemini && a.Type == AccountTypeOAuth &&
		(a.GetCredential("oauth_type") == "aicode" || a.GetCredential("client_id") == geminicli.AICodeOAuthClientID)
}

type geminiAICodeStreamKey struct{}

func buildGeminiAICodeRequest(ctx context.Context, account *Account, token, model, action string, payload []byte) (*http.Request, error) {
	if action != "generateContent" && action != "streamGenerateContent" {
		return nil, fmt.Errorf("AI Code does not support action %q", action)
	}
	var body map[string]any
	if err := json.Unmarshal(normalizeGeminiRequestForAIStudio(payload), &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("AI Code request must be an object")
	}
	// model and aicode are mutually exclusive members of the inference oneof.
	delete(body, "model")
	delete(body, "project")
	body["aicode"] = map[string]string{"experience": strings.TrimPrefix(model, "models/")}
	tier := strings.TrimSpace(account.GetCredential("aicode_tier"))
	if tier == "" {
		tier = "g1-pro-tier"
	}
	body["entitlement"] = map[string]string{"userTier": tier}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	// Only generateContent is verified by the reference implementation. Adapt its
	// complete response to one SSE event when the downstream expects streaming.
	ctx = context.WithValue(ctx, geminiAICodeStreamKey{}, action == "streamGenerateContent")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiAICodeBaseURL+"/v1alpha:generateContent", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", geminiAICodeUserAgent)
	return req, nil
}

// Keep proxy, concurrency, TLS and cancellation in the caller's transport. Only
// retry a tier mismatch once, using Google's resolved tier, never license errors.
func doGeminiAICodeRequest(req *http.Request, do func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if req.URL.Host != "aicode.googleapis.com" {
		return do(req)
	}
	for attempt := 0; ; attempt++ {
		resp, err := do(req)
		if err != nil {
			return resp, err
		}
		stream, _ := req.Context().Value(geminiAICodeStreamKey{}).(bool)
		if resp.StatusCode != http.StatusForbidden && !(resp.StatusCode == http.StatusOK && stream) {
			return resp, nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(raw) > 64<<20 {
			return nil, fmt.Errorf("AI Code response exceeds 64 MiB")
		}
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		if resp.StatusCode == http.StatusForbidden && attempt == 0 && req.GetBody != nil {
			var result struct {
				Error struct {
					Details []struct {
						Type     string            `json:"@type"`
						Reason   string            `json:"reason"`
						Metadata map[string]string `json:"metadata"`
					} `json:"details"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &result) == nil {
				for _, detail := range result.Error.Details {
					tier := detail.Metadata["resolved_tier"]
					if detail.Type != "type.googleapis.com/google.rpc.ErrorInfo" || detail.Reason != "USER_TIER_NOT_ENTITLED" || tier == "" {
						continue
					}
					original, err := req.GetBody()
					if err != nil {
						return nil, err
					}
					var body map[string]any
					err = json.NewDecoder(original).Decode(&body)
					original.Close()
					if err != nil {
						return nil, err
					}
					entitlement, _ := body["entitlement"].(map[string]any)
					if entitlement["userTier"] == tier {
						break
					}
					body["entitlement"] = map[string]string{"userTier": tier}
					updated, err := json.Marshal(body)
					if err != nil {
						return nil, err
					}
					retry, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), bytes.NewReader(updated))
					if err != nil {
						return nil, err
					}
					retry.Header = req.Header.Clone()
					req = retry
					resp.Body.Close()
					goto retry
				}
			}
		}
		if resp.StatusCode == http.StatusOK && stream {
			var compact bytes.Buffer
			if err := json.Compact(&compact, raw); err != nil {
				return nil, fmt.Errorf("invalid AI Code response: %w", err)
			}
			raw = append(append([]byte("data: "), compact.Bytes()...), '\n', '\n')
			resp.Body = io.NopCloser(bytes.NewReader(raw))
			resp.Header.Set("Content-Type", "text/event-stream")
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
		}
		return resp, nil
	retry:
	}
}

func (s *GeminiMessagesCompatService) listGeminiAICodeModels(ctx context.Context, account *Account) (*UpstreamHTTPResult, error) {
	if s.tokenProvider == nil {
		return nil, fmt.Errorf("gemini token provider not configured")
	}
	token, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geminiAICodeBaseURL+"/v1alpha:fetchConfig", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", geminiAICodeUserAgent)
	result, err := s.doGeminiModelsGET(req, account)
	if err != nil || result.StatusCode != http.StatusOK {
		return result, err
	}
	var config struct {
		Experiences []struct {
			Name string `json:"name"`
		} `json:"availableExperiences"`
	}
	if err := json.Unmarshal(result.Body, &config); err != nil {
		return nil, err
	}
	models := make([]map[string]any, 0, len(config.Experiences))
	for _, experience := range config.Experiences {
		if experience.Name == "" {
			continue
		}
		models = append(models, map[string]any{"name": "models/" + strings.TrimPrefix(experience.Name, "models/"), "displayName": experience.Name, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"}})
	}
	return geminiModelsJSONResult(http.StatusOK, map[string]any{"models": models})
}
