//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type miMoUsageUpstream struct {
	status int
	body   string
	req    *http.Request
}

func (u *miMoUsageUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.req = req
	return &http.Response{StatusCode: u.status, Body: io.NopCloser(strings.NewReader(u.body)), Header: make(http.Header), Request: req}, nil
}

func (u *miMoUsageUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

type miMoUsageRepo struct {
	AccountRepository
	updates map[string]any
}

func (r *miMoUsageRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates = updates
	return nil
}

func TestMiMoDesktopUsage_PersistsWeeklySnapshot(t *testing.T) {
	upstream := &miMoUsageUpstream{status: http.StatusOK, body: `{"code":0,"message":"success","data":{"percent":25.5,"resetDate":"2026-10-20","resetAt":1792475699}}`}
	repo := &miMoUsageRepo{}
	svc := NewCNProviderQuotaService(repo, nil, upstream, &config.Config{})
	account := miMoTestAccount(MiMoModeDesktop)
	account.Credentials["base_url"] = "https://mimo-server-cn.xiaomimimo.com/api/route/chat/completions"

	result, err := svc.QueryUsageForAccount(context.Background(), account)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, "https://mimo-server-cn.xiaomimimo.com/api/user/usage", upstream.req.URL.String())
	require.Equal(t, "serviceToken=desktop-test; userId=123", upstream.req.Header.Get("Cookie"))
	require.Empty(t, upstream.req.Header.Get("Authorization"))
	// percent is the remaining share in the Desktop client.
	require.Equal(t, []CNQuotaTier{{Window: "weekly", UsedPercent: 74.5, ResetAt: "2026-10-20T05:54:59Z"}}, result.Tiers)
	require.Equal(t, 74.5, repo.updates["mimo_weekly_used_percent"])
	require.Equal(t, "2026-10-20T05:54:59Z", repo.updates["mimo_weekly_reset_at"])
}

func TestMiMoDesktopUsage_FailuresDoNotPersist(t *testing.T) {
	cases := map[string]*miMoUsageUpstream{
		"expired session": {status: http.StatusUnauthorized, body: `{}`},
		"business error":  {status: http.StatusOK, body: `{"code":401,"message":"not login"}`},
		"no subscription": {status: http.StatusOK, body: `{"code":0,"data":{"percent":0,"resetDate":null}}`},
	}
	for name, upstream := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &miMoUsageRepo{}
			svc := NewCNProviderQuotaService(repo, nil, upstream, &config.Config{})
			result, err := svc.QueryUsageForAccount(context.Background(), miMoTestAccount(MiMoModeDesktop))
			require.NoError(t, err)
			require.False(t, result.Success)
			require.NotEmpty(t, result.Error)
			require.Nil(t, repo.updates)
		})
	}
}

func TestMiMoUsage_OnlyDesktopHasWindow(t *testing.T) {
	upstream := &recordingHTTPUpstream{}
	svc := NewCNProviderQuotaService(&miMoUsageRepo{}, nil, upstream, nil)
	for _, mode := range []string{AccountModePayG, MiMoModeTokenPlan} {
		_, err := svc.QueryUsageForAccount(context.Background(), miMoTestAccount(mode))
		requireReason(t, err, "CN_QUOTA_NOT_CODING_PLAN")
	}
	require.Zero(t, upstream.calls)
}

const miMoQuotaExhaustedBody = `{"error":{"message":"本周用量已满，请等待额度重置或升级套餐","type":"permission_error","code":"subscription_quota_exhausted","biz_code":30011}}`

func TestHandleUpstreamError_MiMoQuotaExhausted403CoolsDownToWeeklyReset(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := miMoTestAccount(MiMoModeDesktop)
	resetAt := time.Now().Add(3 * 24 * time.Hour).UTC().Truncate(time.Second)
	account.Extra = map[string]any{"mimo_weekly_used_percent": 100.0, "mimo_weekly_reset_at": resetAt.Format(time.RFC3339)}

	shouldDisable := service.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte(miMoQuotaExhaustedBody))

	require.True(t, shouldDisable, "failover to another account must still happen")
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.True(t, repo.lastRateLimitedAt.Equal(resetAt))
}

func TestHandleUpstreamError_MiMoQuotaExhausted403WithoutSnapshotNeverDisables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := miMoTestAccount(MiMoModeDesktop)

	for i := 0; i < openAI403DisableThreshold+1; i++ {
		service.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte(miMoQuotaExhaustedBody))
	}

	require.Equal(t, 0, repo.setErrorCalls, "quota exhaustion must not escalate to a permanent error")
	require.Equal(t, openAI403DisableThreshold+1, repo.tempCalls)
	require.True(t, strings.HasPrefix(repo.lastTempReason, cnQuotaExhaustedReasonPrefix))
}
