package tlsfingerprint

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

// TestH2ProfileWireALPN 验证 ALPN 含 h2 的模板在 wire 上按序提供 h2 + http/1.1，
// 且 H2Transport 可构建。transport 与 http.Transport 的分派由 repository 包的
// buildUpstreamTransportWithTLSFingerprint 测试覆盖。
func TestH2ProfileWireALPN(t *testing.T) {
	h2Profile := &Profile{
		Name:          "h2 test",
		ALPNProtocols: []string{"h2", "http/1.1"},
		Extensions:    []uint16{0, 16, 43, 51, 45, 10, 13, 23, 65281},
	}
	if !ProfileOffersH2(h2Profile) {
		t.Fatal("h2 profile not detected")
	}
	rt := NewH2Transport(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("unreachable in this test")
	}, 15*time.Second, 15*time.Second)
	if rt == nil {
		t.Fatal("nil H2Transport")
	}

	addrCh := make(chan string, 1)
	doneCh := make(chan []byte, 1)
	go captureClientHello(addrCh, doneCh)
	addr := <-addrCh
	if addr == "" {
		t.Fatal("capture listener failed to start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialer := NewDialer(h2Profile, nil)
	if _, err := dialer.DialTLSContext(ctx, "tcp", addr); err == nil {
		t.Fatal("handshake should fail against raw listener")
	}
	ch := <-doneCh
	if ch == nil {
		t.Fatal("failed to capture ClientHello")
	}
	_, _, extData, _ := parseClientHelloFields(t, ch)
	alpnExt, ok := extData[16]
	if !ok || len(alpnExt) < 14 {
		t.Fatalf("ALPN extension missing or malformed: %v", alpnExt)
	}
	listLen := int(binary.BigEndian.Uint16(alpnExt[0:2]))
	if listLen != 12 {
		t.Fatalf("ALPN list length = %d, want 12 (3 for h2 + 9 for http/1.1)", listLen)
	}
	if alpnExt[2] != 2 || string(alpnExt[3:5]) != "h2" {
		t.Fatalf("first ALPN protocol = %q, want h2", alpnExt[2:5])
	}
	if alpnExt[5] != 8 || string(alpnExt[6:14]) != "http/1.1" {
		t.Fatalf("second ALPN protocol = %q, want http/1.1", alpnExt[5:14])
	}
}
