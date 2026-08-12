package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP determina la IP pública del cliente.
//   - Fuera de trusted proxies: RemoteAddr (la IP nunca se confía de cabeceras).
//   - Dentro de trusted proxies: se acepta CF-Connecting-IP o X-Forwarded-For
//     (tomando la IP no confiable más a la derecha).
//
// La IP se usa SOLO en memoria para ASN y rate limit; no se persiste ni se loguea.
func clientIP(r *http.Request, trusted []*net.IPNet) (netip.Addr, bool) {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	remote, err := netip.ParseAddr(host)
	if err != nil || !remote.IsValid() {
		return netip.Addr{}, false
	}
	if !isTrusted(remote, trusted) {
		return remote, true
	}

	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		if a, err := netip.ParseAddr(v); err == nil && a.IsValid() {
			return a, true
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil || !a.IsValid() {
				continue
			}
			if !isTrusted(a, trusted) {
				return a, true
			}
		}
	}
	return remote, true
}

func isTrusted(ip netip.Addr, trusted []*net.IPNet) bool {
	for _, n := range trusted {
		if n.IP != nil && n.Contains(net.IP(ip.AsSlice())) {
			return true
		}
	}
	return false
}
