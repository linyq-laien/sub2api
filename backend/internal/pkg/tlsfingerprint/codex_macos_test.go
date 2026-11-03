package tlsfingerprint

import (
	"context"

	utls "github.com/refraction-networking/utls"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// parseClientHelloFields extracts the fingerprint-relevant fields of a
// ClientHello handshake body (after the 4-byte handshake header).
func parseClientHelloFields(t *testing.T, ch []byte) (ciphers []uint16, extensions []uint16, extData map[int][]byte, sessionIDLen int) {
	t.Helper()
	if len(ch) < 40 {
		t.Fatalf("client hello too short: %d", len(ch))
	}
	pos := 34
	sessionIDLen = int(ch[pos])
	pos += 1 + sessionIDLen
	csLen := int(binary.BigEndian.Uint16(ch[pos : pos+2]))
	pos += 2
	for i := 0; i < csLen; i += 2 {
		ciphers = append(ciphers, binary.BigEndian.Uint16(ch[pos+i:pos+i+2]))
	}
	pos += csLen
	compLen := int(ch[pos])
	pos += 1 + compLen
	extTotal := int(binary.BigEndian.Uint16(ch[pos : pos+2]))
	pos += 2
	extData = map[int][]byte{}
	end := pos + extTotal
	for pos+4 <= end {
		etype := binary.BigEndian.Uint16(ch[pos : pos+2])
		elen := int(binary.BigEndian.Uint16(ch[pos+2 : pos+4]))
		extensions = append(extensions, etype)
		extData[int(etype)] = ch[pos+4 : pos+4+elen]
		pos += 4 + elen
	}
	return ciphers, extensions, extData, sessionIDLen
}

// captureClientHello starts a raw TCP listener that records the first
// ClientHello bytes a client sends, then closes the connection. 失败时向
// doneCh 发送 nil（goroutine 内不得调用 t.Fatalf）。
func captureClientHello(addrCh chan<- string, doneCh chan<- []byte) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		addrCh <- ""
		doneCh <- nil
		return
	}
	addrCh <- ln.Addr().String()
	conn, err := ln.Accept()
	if err != nil {
		doneCh <- nil
		return
	}
	defer func() { _ = ln.Close() }()
	defer func() { _ = conn.Close() }()
	head := make([]byte, 5)
	if _, err := io.ReadFull(conn, head); err != nil {
		doneCh <- nil
		return
	}
	if head[0] != 0x16 {
		doneCh <- nil
		return
	}
	bodyLen := int(binary.BigEndian.Uint16(head[3:5]))
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		doneCh <- nil
		return
	}
	if body[0] != 0x01 {
		doneCh <- nil
		return
	}
	hsLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	ch := body[4 : 4+hsLen]
	doneCh <- append([]byte(nil), ch...)
	close(doneCh)
}

// codexMacOSCaptured* 是 codex 0.166.0 (aarch64-apple-darwin) 直连 chatgpt.com
// 实抓的 ClientHello 参数，作为模板回归基线。
var (
	codexMacOSCapturedCiphers = []uint16{
		0x00ff, 0xc02c, 0xc02b, 0xc024, 0xc023, 0xc00a, 0xc009, 0xc008,
		0xc030, 0xc02f, 0xc028, 0xc027, 0xc014, 0xc013, 0xc012,
		0x009d, 0x009c, 0x003d, 0x003c, 0x0035, 0x002f, 0x000a,
	}
	codexMacOSCapturedExtensions = []uint16{0, 10, 11, 13, 5, 18, 23}
)

func TestCodexMacOSProfileSpec(t *testing.T) {
	profile := CodexMacOSProfile()
	spec := buildClientHelloSpecFromProfile(profile)

	if len(spec.CipherSuites) != len(codexMacOSCapturedCiphers) {
		t.Fatalf("cipher count = %d, want %d", len(spec.CipherSuites), len(codexMacOSCapturedCiphers))
	}
	for i, c := range codexMacOSCapturedCiphers {
		if spec.CipherSuites[i] != c {
			t.Fatalf("cipher[%d] = 0x%04x, want 0x%04x", i, spec.CipherSuites[i], c)
		}
	}
	if spec.TLSVersMax != utls.VersionTLS12 {
		t.Fatalf("TLSVersMax = 0x%04x, want TLS1.2-only", spec.TLSVersMax)
	}
	forbidden := map[uint16]bool{16: true, 43: true, 51: true, 45: true, 35: true, 65281: true}
	for _, e := range profile.Extensions {
		if forbidden[e] {
			t.Fatalf("SecureTransport 模板不应包含扩展 %d", e)
		}
	}
	if ProfileOffersH2(profile) {
		t.Fatal("SecureTransport 模板不应提供 h2")
	}
	if !ProfileOffersH2(&Profile{ALPNProtocols: []string{"h2", "http/1.1"}}) {
		t.Fatal("h2 模板应被识别")
	}
	if ProfileOffersH2(nil) {
		t.Fatal("nil 模板不应提供 h2")
	}
}

func TestCodexMacOSProfileWireClientHello(t *testing.T) {
	addrCh := make(chan string, 1)
	doneCh := make(chan []byte, 1)
	go captureClientHello(addrCh, doneCh)
	addr := <-addrCh
	if addr == "" {
		t.Fatal("capture listener failed to start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 用 localhost（而非 127.0.0.1）拨号：utls 与 crypto/tls 一样对 IP 字面量
	// 省略 SNI，而 SecureTransport 对 IP 也发送 SNI；生产目标是域名，这里以
	// 域名路径验证 SNI 扩展存在。
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	dialer := NewDialer(CodexMacOSProfile(), nil)
	conn, err := dialer.DialTLSContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err == nil {
		_ = conn.Close()
		// 监听器主动断开，握手必然失败；拿到连接说明握手被错误放行。
		t.Fatal("handshake should fail against raw listener")
	}

	ch := <-doneCh
	if ch == nil {
		t.Fatal("failed to capture ClientHello")
	}
	ciphers, extensions, extData, sessionIDLen := parseClientHelloFields(t, ch)

	if len(ciphers) != len(codexMacOSCapturedCiphers) {
		t.Fatalf("wire cipher count = %d, want %d", len(ciphers), len(codexMacOSCapturedCiphers))
	}
	for i, c := range codexMacOSCapturedCiphers {
		if ciphers[i] != c {
			t.Fatalf("wire cipher[%d] = 0x%04x, want 0x%04x", i, ciphers[i], c)
		}
	}
	if len(extensions) != len(codexMacOSCapturedExtensions) {
		t.Fatalf("wire extensions = %v, want %v", extensions, codexMacOSCapturedExtensions)
	}
	for i, e := range codexMacOSCapturedExtensions {
		if extensions[i] != e {
			t.Fatalf("wire extension[%d] = %d, want %d", i, extensions[i], e)
		}
	}
	if sessionIDLen != 0 {
		t.Fatalf("wire legacy_session_id length = %d, want 0 (SecureTransport)", sessionIDLen)
	}
	// 扩展 10 的曲线：23/24/25
	if len(extData[10]) < 4 {
		t.Fatalf("supported_groups payload too short: %v", extData[10])
	}
	groupsLen := int(binary.BigEndian.Uint16(extData[10][:2]))
	if groupsLen != 6 {
		t.Fatalf("supported_groups list length = %d, want 6 (3 curves)", groupsLen)
	}
}
