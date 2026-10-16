package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const mimoDesktopMeURL = "https://mimo-server-cn.xiaomimimo.com/api/user/xiaomi/me"
const MiMoLoginSessionCredential = "mimo_login_session_id"

// MiMo Desktop uses Xiaomi Passport SSO, not the API platform's key-issuing
// authorization flow. Only business cookies survive the short-lived login jar.
type MiMoLoginService struct {
	mu        sync.Mutex
	sessions  map[string]*miMoLoginSession
	transport http.RoundTripper
	ttl       time.Duration
}
type miMoLoginSession struct {
	owner                    int64
	expires                  time.Time
	status, loginURL, cookie string
	cancel                   context.CancelFunc
}
type MiMoLoginStatus struct {
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	LoginURL  string    `json:"login_url,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewMiMoLoginService() *MiMoLoginService {
	return &MiMoLoginService{sessions: make(map[string]*miMoLoginSession), transport: http.DefaultTransport, ttl: 5 * time.Minute}
}
func miMoLoginError() error {
	return apperrors.BadRequest("MIMO_LOGIN_UNAVAILABLE", "MiMo login is unavailable or expired; start a new Xiaomi login")
}
func allowedMiMoLoginURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "account.xiaomi.com" || strings.HasSuffix(h, ".account.xiaomi.com") || h == "mimo-server-cn.xiaomimimo.com"
}
func (s *MiMoLoginService) Start(ctx context.Context, owner int64) (*MiMoLoginStatus, error) {
	if owner <= 0 {
		return nil, miMoLoginError()
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, miMoLoginError()
	}
	id := hex.EncodeToString(b)
	authCtx, cancel := context.WithTimeout(context.Background(), s.ttl)
	session := &miMoLoginSession{owner: owner, expires: time.Now().Add(s.ttl), status: "pending", cancel: cancel}
	s.mu.Lock()
	for key, old := range s.sessions {
		if old.owner == owner || time.Now().After(old.expires) {
			old.cancel()
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 32 {
		s.mu.Unlock()
		cancel()
		return nil, miMoLoginError()
	}
	s.sessions[id] = session
	s.mu.Unlock()
	time.AfterFunc(s.ttl, func() { s.Cancel(owner, id) })
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	client := &http.Client{Jar: jar, Transport: s.transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// Xiaomi's STS followup can use http; keep business cookies on HTTPS.
		if req.URL.Scheme == "http" && req.URL.Host == "mimo-server-cn.xiaomimimo.com" {
			req.URL.Scheme = "https"
		}
		if len(via) > 10 || !allowedMiMoLoginURL(req.URL.String()) {
			return errors.New("invalid login redirect")
		}
		return nil
	}}
	startCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	// Cancel network setup too when another login replaces this attempt.
	undo := context.AfterFunc(authCtx, stop)
	defer undo()
	_, finalURL, err := miMoLoginGet(startCtx, client, mimoDesktopMeURL)
	if err != nil || finalURL.Hostname() != "account.xiaomi.com" {
		s.Cancel(owner, id)
		return nil, miMoLoginError()
	}
	q := finalURL.Query()
	q.Set("_qrsize", "240")
	raw, _, err := miMoLoginGet(startCtx, client, "https://account.xiaomi.com/longPolling/loginUrl?"+q.Encode())
	var qr struct {
		Code     int    `json:"code"`
		LoginURL string `json:"loginUrl"`
		LP       string `json:"lp"`
	}
	if err != nil || decodeMiMoLogin(raw, &qr) != nil || qr.Code != 0 || !allowedMiMoLoginURL(qr.LoginURL) || !allowedMiMoLoginURL(qr.LP) {
		s.Cancel(owner, id)
		return nil, miMoLoginError()
	}
	s.mu.Lock()
	if s.sessions[id] != session {
		s.mu.Unlock()
		return nil, miMoLoginError()
	}
	session.loginURL = qr.LoginURL
	s.mu.Unlock()
	go s.poll(authCtx, client, id, session, qr.LP)
	return s.Status(owner, id)
}
func miMoLoginGet(ctx context.Context, client *http.Client, raw string) ([]byte, *url.URL, error) {
	if !allowedMiMoLoginURL(raw) {
		return nil, nil, errors.New("invalid login URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, errors.New("login HTTP failure")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if len(b) > 1<<20 {
		return nil, nil, errors.New("login response too large")
	}
	return b, resp.Request.URL, err
}
func decodeMiMoLogin(b []byte, v any) error {
	return json.Unmarshal([]byte(strings.TrimPrefix(string(b), "&&&START&&&")), v)
}
func (s *MiMoLoginService) poll(ctx context.Context, client *http.Client, id string, session *miMoLoginSession, lp string) {
	cookie, err := completeMiMoLogin(ctx, client, lp)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[id] != session || ctx.Err() != nil {
		return
	}
	if err != nil {
		session.status = "failed"
		return
	}
	session.cookie = cookie
	session.status = "authenticated"
	session.loginURL = ""
}
func completeMiMoLogin(ctx context.Context, client *http.Client, lp string) (string, error) {
	b, _, err := miMoLoginGet(ctx, client, lp)
	if err != nil {
		return "", err
	}
	var result struct {
		Code     int    `json:"code"`
		Location string `json:"location"`
	}
	if decodeMiMoLogin(b, &result) != nil || result.Code != 0 || result.Location == "" {
		return "", miMoLoginError()
	}
	if _, _, err = miMoLoginGet(ctx, client, result.Location); err != nil {
		return "", err
	}
	b, _, err = miMoLoginGet(ctx, client, mimoDesktopMeURL)
	if err != nil {
		return "", err
	}
	var me struct {
		Code int `json:"code"`
		Data struct {
			UserID json.RawMessage `json:"userId"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &me) != nil || me.Code != 0 || len(me.Data.UserID) == 0 || string(me.Data.UserID) == "null" || string(me.Data.UserID) == `""` {
		return "", miMoLoginError()
	}
	u, _ := url.Parse(mimoDesktopMeURL)
	parts := []string{}
	hasToken := false
	for _, c := range client.Jar.Cookies(u) {
		switch c.Name {
		case "serviceToken", "userId", "mimopc_slh", "mimopc_ph":
			parts = append(parts, c.Name+"="+c.Value)
			if c.Name == "serviceToken" && c.Value != "" {
				hasToken = true
			}
		}
	}
	if !hasToken {
		return "", miMoLoginError()
	}
	return strings.Join(parts, "; "), nil
}
func (s *MiMoLoginService) Status(owner int64, id string) (*MiMoLoginStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.sessions[id]
	if v == nil || v.owner != owner || time.Now().After(v.expires) {
		return nil, miMoLoginError()
	}
	return &MiMoLoginStatus{SessionID: id, Status: v.status, LoginURL: v.loginURL, ExpiresAt: v.expires}, nil
}
func (s *MiMoLoginService) Cookie(owner int64, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.sessions[id]
	if v == nil || v.owner != owner || time.Now().After(v.expires) || v.status != "authenticated" {
		return "", miMoLoginError()
	}
	return v.cookie, nil
}
func (s *MiMoLoginService) Cancel(owner int64, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.sessions[id]; v != nil && v.owner == owner {
		v.cancel()
		v.cookie = ""
		delete(s.sessions, id)
	}
}
