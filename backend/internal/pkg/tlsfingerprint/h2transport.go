package tlsfingerprint

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// h2DialTimeout bounds a single TCP+proxy+TLS establishment driven by http2.Transport.
const h2DialTimeout = 30 * time.Second

// H2Transport 在 utls 指纹握手上运行 HTTP/2。
//
// 背景：http.Transport 的自定义 DialTLSContext 返回 *utls.UConn（非
// *crypto/tls.Conn），net/http 无法读取其 ALPN 协商结果并把 h2 分派给
// 内置 http2 处理器——模板 ALPN 含 h2 而走普通 http.Transport 时，连接
// 协商出 h2 却被按 HTTP/1.1 写入，直接产生协议错乱。因此 ALPN 含 h2 的
// 指纹模板必须由本 transport 自行管理 HTTP/2 连接池。
type H2Transport struct {
	t2 http2.Transport
}

// NewH2Transport 创建基于 utls 指纹的 HTTP/2 RoundTripper。
// dialTLSContext 由调用方注入（直连 / SOCKS5 / HTTP CONNECT 已在 dialer
// 内部处理），必须返回完成 utls 握手的连接；仅接受协商出 h2 的连接，
// 其余协议直接报错，避免静默降级破坏指纹语义。
func NewH2Transport(
	dialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error),
	readIdleTimeout, pingTimeout time.Duration,
) *H2Transport {
	t := &H2Transport{}
	t.t2 = http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, h2DialTimeout)
			defer cancel()
			conn, err := dialTLSContext(dialCtx, network, addr)
			if err != nil {
				return nil, err
			}
			stateConn, ok := conn.(interface{ ConnectionState() tls.ConnectionState })
			if !ok {
				_ = conn.Close()
				return nil, fmt.Errorf("tls fingerprint h2: dialer returned non-TLS connection %T", conn)
			}
			if proto := stateConn.ConnectionState().NegotiatedProtocol; proto != "h2" {
				_ = conn.Close()
				return nil, fmt.Errorf("tls fingerprint h2: server negotiated ALPN %q, want h2", proto)
			}
			return conn, nil
		},
		ReadIdleTimeout: readIdleTimeout,
		PingTimeout:     pingTimeout,
	}
	return t
}

func (t *H2Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.t2.RoundTrip(req)
}

func (t *H2Transport) CloseIdleConnections() {
	t.t2.CloseIdleConnections()
}
