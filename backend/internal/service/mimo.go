package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	MiMoModeDesktop   = "desktop"
	MiMoModeTokenPlan = "token_plan"
)

func (a *Account) IsMiMoDesktop() bool {
	return a != nil && a.Platform == PlatformMiMo && a.GetCredential("account_mode") == MiMoModeDesktop
}

// ValidateMiMoAccount fails closed: membership sessions, subscription keys and
// pay-as-you-go keys must never silently fall back to a different billing mode.
func ValidateMiMoAccount(a *Account) error {
	if a == nil || a.Platform != PlatformMiMo {
		return nil
	}
	invalid := func(message string) error { return infraerrors.BadRequest("INVALID_MIMO_ACCOUNT", message) }
	if a.Type != AccountTypeAPIKey {
		return invalid("MiMo accounts require the managed credential account type")
	}
	if a.GetCredential(MiMoLoginSessionCredential) != "" {
		return invalid("Complete Xiaomi authorization before storing the account")
	}
	mode := a.GetCredential("account_mode")
	profile := LookupProviderProfile(PlatformMiMo)
	endpoints, ok := profile.Modes[mode]
	if !ok {
		return invalid("MiMo account_mode must be desktop, token_plan or payg")
	}
	protocol := a.GetCredential("api_protocol")
	if protocol != "" && protocol != APIProtocolAdaptive && endpoints.BaseURLs[protocol] == "" {
		return invalid("MiMo mode does not support this native API protocol")
	}
	key := strings.TrimSpace(a.GetCredential("api_key"))
	cookie := strings.TrimSpace(a.GetCredential("mimo_session_cookie"))
	if mode == MiMoModeDesktop {
		if key != "" {
			return invalid("Desktop membership uses a session cookie, not an API key")
		}
		if cookie == "" || strings.ContainsAny(cookie, "\r\n\x00") {
			return invalid("MiMo Desktop session cookie is required and must be a single line")
		}
		req := &http.Request{Header: http.Header{"Cookie": []string{cookie}}}
		if len(req.Cookies()) == 0 {
			return invalid("MiMo Desktop requires a valid Cookie header value")
		}
	} else {
		if cookie != "" {
			return invalid("MiMo API accounts must not contain Desktop session cookies")
		}
		validKey := strings.HasPrefix(key, "sk-") && len(key) > 3
		if mode == MiMoModeTokenPlan {
			validKey = (strings.HasPrefix(key, "tp-") && len(key) > 3) || (strings.HasPrefix(key, "ttp-") && len(key) > 4)
		}
		if !validKey || strings.ContainsAny(key, " \t\r\n") {
			return invalid("MiMo key does not match the selected mode (payg: sk-, Token Plan: tp-/ttp-)")
		}
	}
	urls := []string{a.GetCredential("base_url")}
	if bases, ok := a.Credentials["api_base_urls"].(map[string]any); ok {
		for protocol, raw := range bases {
			base, _ := raw.(string)
			if base != "" && endpoints.BaseURLs[protocol] == "" {
				return invalid("MiMo mode contains an unsupported protocol endpoint")
			}
			urls = append(urls, base)
		}
	}
	for _, base := range urls {
		if strings.TrimSpace(base) == "" {
			continue
		}
		u, err := url.Parse(base)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return invalid("Invalid MiMo endpoint URL")
		}
		host := strings.ToLower(u.Hostname())
		// Custom gateways remain supported. Official Xiaomi endpoints must belong to
		// the selected product, including regional Token Plan / Desktop deployments.
		if host == "xiaomimimo.com" || strings.HasSuffix(host, ".xiaomimimo.com") {
			valid := mode == AccountModePayG && host == "api.xiaomimimo.com"
			if mode == MiMoModeTokenPlan {
				valid = host == "token-plan-cn.xiaomimimo.com" || host == "token-plan-sgp.xiaomimimo.com" || host == "token-plan-ams.xiaomimimo.com"
			}
			if mode == MiMoModeDesktop {
				valid = strings.HasPrefix(host, "mimo-server-") && u.Path == "/api/route/chat/completions"
			}
			if !valid {
				return invalid("MiMo endpoint belongs to a different product; change the endpoint together with the account mode")
			}
		}
	}
	return nil
}

func (a *Account) miMoCredential() (string, string, error) {
	if err := ValidateMiMoAccount(a); err != nil {
		return "", "", err
	}
	if a.IsMiMoDesktop() {
		return a.GetCredential("mimo_session_cookie"), "mimo_session_cookie", nil
	}
	return a.GetCredential("api_key"), "apikey", nil
}

func (a *Account) applyMiMoAuth(headers http.Header) {
	if !a.IsMiMoDesktop() {
		return
	}
	headers.Del("Authorization")
	headers.Del("X-Api-Key")
	headers.Set("Cookie", a.GetCredential("mimo_session_cookie"))
	headers.Set("X-Mimo-Source", "mimocode-cli-free")
}

// The Desktop catalog is a Xiaomi envelope, not an OpenAI /models response.
// Only text models belong to the chat gateway; image/audio products are separate.
func normalizeMiMoDesktopModels(body []byte) ([]byte, error) {
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Models []struct {
				Name        string `json:"modelName"`
				Type        string `json:"modelType"`
				Description string `json:"description"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("MiMo catalog returned code %d", payload.Code)
	}
	models := make([]map[string]string, 0)
	for _, model := range payload.Data.Models {
		if model.Type == "TEXT" && model.Name != "" {
			models = append(models, map[string]string{"id": model.Name, "name": model.Description})
		}
	}
	return json.Marshal(map[string]any{"data": models})
}
