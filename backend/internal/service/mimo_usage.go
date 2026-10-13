package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

// MiMo Desktop membership exposes one weekly window through the same business
// session used for chat. The Desktop client reads GET /api/user/usage and shows
// data.percent as the share still remaining; data.resetAt is the window end in
// unix seconds. A null resetDate means the session has no active subscription.
const (
	mimoDesktopUsagePath = "/api/user/usage"

	// Desktop 会员额度耗尽的 403：{"error":{"code":"subscription_quota_exhausted","biz_code":30011}}
	mimoQuotaExhaustedCode    = "subscription_quota_exhausted"
	mimoQuotaExhaustedBizCode = 30011
)

// mimoDesktopUsageURL derives the usage endpoint from the configured Desktop
// chat endpoint so regional or custom gateways keep their own host.
func mimoDesktopUsageURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "https://mimo-server-cn.xiaomimimo.com" + mimoDesktopUsagePath
	}
	return u.Scheme + "://" + u.Host + mimoDesktopUsagePath
}

func (s *CNProviderQuotaService) queryMiMoDesktopUsage(ctx context.Context, account *Account) (*CNProviderQuotaProbeResult, error) {
	cookie := strings.TrimSpace(account.GetCredential("mimo_session_cookie"))
	if cookie == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CN_QUOTA_NO_APIKEY", "MiMo Desktop session cookie is empty")
	}
	targetURL, err := cnValidateProbeURL(s.cfg, mimoDesktopUsageURL(account.GetOpenAIBaseURL()))
	if err != nil {
		return nil, infraerrors.New(http.StatusForbidden, "CN_QUOTA_URL_REJECTED", err.Error())
	}

	callCtx, cancel := context.WithTimeout(ctx, cnQuotaUpstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CN_QUOTA_REQUEST_BUILD_FAILED", "build request: %v", err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)

	resp, err := s.httpUpstream.Do(req, s.resolveProxyURL(ctx, account), account.ID, maxInt(account.Concurrency, 1))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CN_QUOTA_REQUEST_FAILED", "upstream request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, cnQuotaMaxBodyBytes))

	now := time.Now().UTC()
	result := &CNProviderQuotaProbeResult{
		Provider:   PlatformMiMo,
		Source:     "mimo_desktop_usage",
		FetchedAt:  now.Unix(),
		StatusCode: resp.StatusCode,
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		result.Error = fmt.Sprintf("Authentication failed (HTTP %d); re-authorize the Xiaomi account", resp.StatusCode)
		return result, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("API error (HTTP %d): %s", resp.StatusCode, truncate(strings.TrimSpace(string(bodyBytes)), 240))
		return result, nil
	}

	tier, errMsg := parseMiMoDesktopUsage(bodyBytes)
	if errMsg != "" {
		result.Error = errMsg
		return result, nil
	}
	result.Tiers = []CNQuotaTier{tier}
	result.Success = true
	result.CredentialValid = true

	updates := cnQuotaExtraUpdates(PlatformMiMo, result.Tiers, now)
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
		slog.Warn("cn_quota_persist_failed", "account_id", account.ID, "provider", PlatformMiMo, "error", err)
	} else {
		result.Persisted = true
	}
	return result, nil
}

// parseMiMoDesktopUsage converts the Desktop envelope into the shared weekly tier.
// percent is remaining (not used), so used = 100 - percent.
func parseMiMoDesktopUsage(body []byte) (CNQuotaTier, string) {
	if code := gjson.GetBytes(body, "code"); !code.Exists() || code.Int() != 0 {
		msg := strings.TrimSpace(gjson.GetBytes(body, "message").String())
		if msg == "" {
			msg = "unknown MiMo usage error"
		}
		return CNQuotaTier{}, fmt.Sprintf("API error (%d): %s", code.Int(), msg)
	}
	data := gjson.GetBytes(body, "data")
	if resetDate := data.Get("resetDate"); resetDate.Exists() && resetDate.Type == gjson.Null {
		return CNQuotaTier{}, "MiMo Desktop account has no active subscription"
	}
	remaining, ok := cnParseF64(data.Get("percent").Value())
	if !ok || remaining < 0 {
		return CNQuotaTier{}, "MiMo usage response has no valid percent"
	}
	used := 100 - remaining
	if used < 0 {
		used = 0
	}
	return CNQuotaTier{
		Window:      "weekly",
		UsedPercent: used,
		ResetAt:     cnNormalizeResetTime(data.Get("resetAt").Value()),
	}, ""
}

// isMiMoQuotaExhausted403 recognizes Desktop membership exhaustion. It recovers
// when the weekly window resets, so it must not feed the 403 disable counter.
func isMiMoQuotaExhausted403(account *Account, responseBody []byte) bool {
	if !account.IsMiMoDesktop() {
		return false
	}
	errNode := gjson.GetBytes(responseBody, "error")
	return strings.EqualFold(strings.TrimSpace(errNode.Get("code").String()), mimoQuotaExhaustedCode) ||
		errNode.Get("biz_code").Int() == mimoQuotaExhaustedBizCode
}
