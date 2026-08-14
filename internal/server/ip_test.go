package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func newRequest(remote string, headers map[string]string) *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/p", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestClientIPUntrustedIgnoresHeaders(t *testing.T) {
	// Cliente directo (no confiable): las cabeceras XFF deben IGNORARSE.
	req := newRequest("198.51.100.7:5000", map[string]string{
		"X-Forwarded-For":  "203.0.113.1",
		"CF-Connecting-IP": "203.0.113.2",
	})
	ip, ok := clientIP(req, nil)
	if !ok || ip != netip.MustParseAddr("198.51.100.7") {
		t.Errorf("ip = %v ok=%v, want 198.51.100.7", ip, ok)
	}
}

func TestClientIPTrustedProxyCF(t *testing.T) {
	trusted := parseTestCIDRs(t, "127.0.0.1/32")
	req := newRequest("127.0.0.1:1234", map[string]string{
		"CF-Connecting-IP": "203.0.113.9",
	})
	ip, ok := clientIP(req, trusted)
	if !ok || ip != netip.MustParseAddr("203.0.113.9") {
		t.Errorf("ip = %v ok=%v, want 203.0.113.9", ip, ok)
	}
}

func TestClientIPTrustedProxyXFF(t *testing.T) {
	// Cadena: cliente -> proxy confiable -> proxy confiable -> servidor.
	// XFF = "203.0.113.5, 10.0.0.2". Debe tomar 203.0.113.5 (la no confiable
	// más a la derecha).
	trusted := parseTestCIDRs(t, "127.0.0.1/32,10.0.0.0/8")
	req := newRequest("127.0.0.1:1234", map[string]string{
		"X-Forwarded-For": "203.0.113.5, 10.0.0.2",
	})
	ip, ok := clientIP(req, trusted)
	if !ok || ip != netip.MustParseAddr("203.0.113.5") {
		t.Errorf("ip = %v ok=%v, want 203.0.113.5", ip, ok)
	}
}

func TestClientIPTrustedProxyAllTrusted(t *testing.T) {
	// Todos los saltos confiables -> se usa el último (el proxy edge).
	trusted := parseTestCIDRs(t, "127.0.0.1/32,10.0.0.0/8")
	req := newRequest("127.0.0.1:1234", map[string]string{
		"X-Forwarded-For": "10.0.0.5, 10.0.0.2",
	})
	ip, ok := clientIP(req, trusted)
	if !ok || ip != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("ip = %v ok=%v, want 127.0.0.1", ip, ok)
	}
}

func TestClientIPDockerBridgeGateway(t *testing.T) {
	// Escenario producción: Cloudflare -> Caddy (host) -> contenedor Docker.
	// El contenedor ve como peer el gateway del bridge (172.26.0.1, dentro de
	// 172.16.0.0/12). Con el gateway confiado, debe usarse CF-Connecting-IP.
	trusted := parseTestCIDRs(t, "127.0.0.1/32,::1/128,172.16.0.0/12")
	req := newRequest("172.26.0.1:5432", map[string]string{
		"CF-Connecting-IP": "190.66.238.186",
		"X-Forwarded-For":  "190.66.238.186, 172.68.12.3",
	})
	ip, ok := clientIP(req, trusted)
	if !ok || ip != netip.MustParseAddr("190.66.238.186") {
		t.Errorf("ip = %v ok=%v, want 190.66.238.186", ip, ok)
	}
}

func TestClientIPBadRemote(t *testing.T) {
	req := newRequest("not-an-ip", nil)
	if _, ok := clientIP(req, nil); ok {
		t.Error("IP inválida debería fallar")
	}
}

func parseTestCIDRs(t *testing.T, list string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, part := range splitClean(list) {
		_, ipnet, err := net.ParseCIDR(part)
		if err != nil {
			t.Fatalf("CIDR inválido %q: %v", part, err)
		}
		out = append(out, ipnet)
	}
	return out
}

func splitClean(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
