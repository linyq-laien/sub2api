//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mimoLoginTransport func(*http.Request) (*http.Response, error)

func (f mimoLoginTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mimoLoginResponse(r *http.Request, code int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: headers, Request: r}
}
func TestMiMoLoginBrowserConfirmation(t *testing.T) {
	s := NewMiMoLoginService()
	confirmed := make(chan struct{})
	s.transport = mimoLoginTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/user/xiaomi/me":
			if _, err := r.Cookie("serviceToken"); err == nil {
				return mimoLoginResponse(r, 200, `{"code":0,"data":{"userId":"123"}}`, nil), nil
			}
			return mimoLoginResponse(r, 302, "", http.Header{"Location": []string{"https://account.xiaomi.com/fe/service/login?sid=mimopc&callback=test"}}), nil
		case "/fe/service/login":
			return mimoLoginResponse(r, 200, "<html>Sign in</html>", nil), nil
		case "/longPolling/loginUrl":
			require.Equal(t, "mimopc", r.URL.Query().Get("sid"))
			return mimoLoginResponse(r, 200, `&&&START&&&{"code":0,"loginUrl":"https://ak.account.xiaomi.com/longPolling/login?ticket=test","lp":"https://ak.lp.account.xiaomi.com/lp/s?ticket=test"}`, nil), nil
		case "/lp/s":
			select {
			case <-confirmed:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return mimoLoginResponse(r, 200, `{"code":0,"location":"https://mimo-server-cn.xiaomimimo.com/api/sts?ticket=test"}`, nil), nil
		case "/api/sts":
			return mimoLoginResponse(r, 200, "OK", http.Header{"Set-Cookie": []string{"serviceToken=business-only; Secure; Path=/", "userId=123; Secure; Path=/", "passToken=never-persist; Secure; Path=/"}}), nil
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			return mimoLoginResponse(r, 404, "", nil), nil
		}
	})
	status, err := s.Start(context.Background(), 42)
	require.NoError(t, err)
	defer s.Cancel(42, status.SessionID)
	require.Equal(t, "pending", status.Status)
	_, err = s.Cookie(42, status.SessionID)
	require.Error(t, err)
	_, err = s.Status(43, status.SessionID)
	require.Error(t, err)
	close(confirmed)
	require.Eventually(t, func() bool { v, e := s.Status(42, status.SessionID); return e == nil && v.Status == "authenticated" }, time.Second, time.Millisecond)
	cookie, err := s.Cookie(42, status.SessionID)
	require.NoError(t, err)
	require.Contains(t, cookie, "serviceToken=business-only")
	require.NotContains(t, cookie, "passToken")
	status, err = s.Status(42, status.SessionID)
	require.NoError(t, err)
	data, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(data), "business-only")
	require.Empty(t, status.LoginURL)
	_, err = s.Cookie(43, status.SessionID)
	require.Error(t, err)
	s.Cancel(43, status.SessionID)
	_, err = s.Cookie(42, status.SessionID)
	require.NoError(t, err)
	s.Cancel(42, status.SessionID)
	_, err = s.Cookie(42, status.SessionID)
	require.Error(t, err)
}
func TestMiMoLoginRejectsRedirectsAndExpiredHandles(t *testing.T) {
	for _, u := range []string{"http://account.xiaomi.com/login", "https://account.xiaomi.com.evil.test/login", "https://evil.test", "https://127.0.0.1", "https://user@account.xiaomi.com/login", "https://account.xiaomi.com:444/login"} {
		require.False(t, allowedMiMoLoginURL(u), u)
	}
	s := NewMiMoLoginService()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.sessions["expired"] = &miMoLoginSession{owner: 1, status: "authenticated", cookie: "secret", expires: time.Now().Add(-time.Second), cancel: cancel}
	_, err := s.Cookie(1, "expired")
	require.Error(t, err)
	s.transport = mimoLoginTransport(func(r *http.Request) (*http.Response, error) {
		return mimoLoginResponse(r, 302, "", http.Header{"Location": []string{"https://127.0.0.1/private"}}), nil
	})
	_, err = s.Start(ctx, 1)
	require.Error(t, err)
	require.Empty(t, s.sessions)
}
