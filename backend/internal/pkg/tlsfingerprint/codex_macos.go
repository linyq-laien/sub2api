package tlsfingerprint

import utls "github.com/refraction-networking/utls"

// CodexMacOSProfileName 内置模板名：官方 Codex CLI（codex-rs）macOS 版。
const CodexMacOSProfileName = "Built-in Codex CLI macOS (SecureTransport)"

// CodexMacOSProfile 返回官方 Codex CLI macOS 版出站 ClientHello 的指纹模板。
//
// 参数为 codex 0.166.0（aarch64-apple-darwin）直连 chatgpt.com 的实抓值：
// 记录层 0x0301、TLS1.2-only、无 ALPN（SecureTransport 不支持，出站恒
// HTTP/1.1）、空 legacy_session_id、0x00ff SCSV 位于套件列表首位、
// 22 个套件、secp256r1/384r1/521r1、8 个签名算法、7 个扩展按序。
//
// codex 二进制同时内嵌 rustls，但默认信任路径（系统根证书）走 reqwest
// native-tls → Apple SecureTransport；仅配置自定义 CA（SSL_CERT_FILE /
// CODEX_CA_CERTIFICATE）时才切换 rustls。上游视角下伪装成默认形态的
// macOS CLI 应使用本模板，而不是 rustls 模板。
func CodexMacOSProfile() *Profile {
	return &Profile{
		Name:  CodexMacOSProfileName,
		CipherSuites: []uint16{
			0x00ff, // TLS_EMPTY_RENEGOTIATION_INFO_SCSV（SecureTransport 以套件位 signaling）
			0xc02c, // ECDHE-ECDSA-AES256-GCM-SHA384
			0xc02b, // ECDHE-ECDSA-AES128-GCM-SHA256
			0xc024, // ECDHE-ECDSA-AES256-CBC-SHA384
			0xc023, // ECDHE-ECDSA-AES128-CBC-SHA256
			0xc00a, // ECDHE-ECDSA-AES256-CBC-SHA
			0xc009, // ECDHE-ECDSA-AES128-CBC-SHA
			0xc008, // ECDHE-ECDSA-DES-CBC3-SHA
			0xc030, // ECDHE-RSA-AES256-GCM-SHA384
			0xc02f, // ECDHE-RSA-AES128-GCM-SHA256
			0xc028, // ECDHE-RSA-AES256-CBC-SHA384
			0xc027, // ECDHE-RSA-AES128-CBC-SHA256
			0xc014, // ECDHE-RSA-AES256-CBC-SHA
			0xc013, // ECDHE-RSA-AES128-CBC-SHA
			0xc012, // ECDHE-RSA-DES-CBC3-SHA
			0x009d, // RSA-AES256-GCM-SHA384
			0x009c, // RSA-AES128-GCM-SHA256
			0x003d, // RSA-AES256-SHA256
			0x003c, // RSA-AES128-SHA256
			0x0035, // RSA-AES256-SHA
			0x002f, // RSA-AES128-SHA
			0x000a, // RSA-3DES-EDE-CBC-SHA
		},
		Curves:              []uint16{23, 24, 25}, // secp256r1, secp384r1, secp521r1
		PointFormats:        []uint16{0},
		SignatureAlgorithms: []uint16{0x0401, 0x0201, 0x0501, 0x0601, 0x0403, 0x0203, 0x0503, 0x0603},
		// SecureTransport 只支持到 TLS1.2，且不发送 supported_versions / ALPN /
		// key_share / psk_key_exchange_modes / session_ticket / GREASE 等扩展。
		SupportedVersions: []uint16{utls.VersionTLS12},
		ALPNProtocols:     nil,
		KeyShareGroups:    nil,
		PSKModes:          nil,
		Extensions:        []uint16{0 /*server_name*/, 10 /*supported_groups*/, 11 /*ec_point_formats*/, 13 /*signature_algorithms*/, 5 /*status_request*/, 18 /*SCT*/, 23 /*extended_master_secret*/},
	}
}

// ProfileOffersH2 reports whether the profile's resolved ALPN list offers h2.
// 空 ALPN 与默认模板一样只有 http/1.1。
func ProfileOffersH2(profile *Profile) bool {
	alpn := []string{"http/1.1"}
	if profile != nil && len(profile.ALPNProtocols) > 0 {
		alpn = profile.ALPNProtocols
	}
	for _, p := range alpn {
		if p == "h2" {
			return true
		}
	}
	return false
}
