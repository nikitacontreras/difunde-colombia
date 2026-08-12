// Package asn resuelve IP -> ASN de forma LOCAL.
// Nunca se consultan servicios externos en cada observación.
// La IP se usa solo en memoria; no se persiste ni se loguea.
package asn

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Info struct {
	ASN  int
	Name string
	ISP  string
}

// Resolver es la abstracción para fuentes locales de IP -> ASN
// (GeoLite2 ASN, IP2ASN, archivos de rangos propios).
type Resolver interface {
	Lookup(ip netip.Addr) (Info, bool)
}

// CSVResolver implementa Resolver sobre un CSV local.
// Formatos soportados (detección por cabecera):
//   - GeoLite2-ASN:  network,autonomous_system_number,autonomous_system_organization
//   - Rangos:        start_ip,end_ip,asn,name,isp
type CSVResolver struct {
	ranges []ipRange
}

type ipRange struct {
	start netip.Addr
	end   netip.Addr
	info  Info
}

// NewCSVResolver carga el archivo de base ASN.
func NewCSVResolver(path string) (*CSVResolver, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("abrir base ASN %s: %w", path, err)
	}
	defer f.Close()

	rd := csv.NewReader(f)
	rd.FieldsPerRecord = -1
	header, err := rd.Read()
	if err != nil {
		return nil, fmt.Errorf("leer header ASN: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	_, hasNetwork := idx["network"]
	_, hasASN := idx["autonomous_system_number"]
	_, hasStart := idx["start_ip"]
	_, hasEnd := idx["end_ip"]
	geo := hasNetwork && hasASN
	if !geo && !(hasStart && hasEnd) {
		return nil, fmt.Errorf("formato de base ASN no reconocido en %s (espera GeoLite2-ASN o start_ip,end_ip,...)", path)
	}

	var out []ipRange
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var start, end netip.Addr
		var info Info
		if geo {
			if len(rec) < 3 {
				continue
			}
			pfx, err := netip.ParsePrefix(rec[idx["network"]])
			if err != nil {
				continue
			}
			start = pfx.Addr()
			end = lastAddr(pfx)
			info.ASN, _ = strconv.Atoi(strings.TrimSpace(rec[idx["autonomous_system_number"]]))
			info.Name = strings.TrimSpace(rec[idx["autonomous_system_organization"]])
		} else {
			var err error
			start, err = netip.ParseAddr(rec[idx["start_ip"]])
			if err != nil {
				continue
			}
			end, err = netip.ParseAddr(rec[idx["end_ip"]])
			if err != nil {
				continue
			}
			if i, ok := idx["asn"]; ok && i < len(rec) {
				info.ASN, _ = strconv.Atoi(rec[i])
			}
			if i, ok := idx["name"]; ok && i < len(rec) {
				info.Name = rec[i]
			}
			if i, ok := idx["isp"]; ok && i < len(rec) {
				info.ISP = rec[i]
			}
		}
		if start.IsValid() && end.IsValid() && compareAddr(start, end) <= 0 {
			out = append(out, ipRange{start: start, end: end, info: info})
		}
	}
	sort.Slice(out, func(i, j int) bool { return compareAddr(out[i].start, out[j].start) < 0 })
	return &CSVResolver{ranges: out}, nil
}

// Lookup devuelve la información ASN para una IP.
func (r *CSVResolver) Lookup(ip netip.Addr) (Info, bool) {
	// Búsqueda binaria: último rango con start <= ip.
	i := sort.Search(len(r.ranges), func(i int) bool { return compareAddr(r.ranges[i].start, ip) > 0 })
	if i == 0 {
		return Info{}, false
	}
	cand := r.ranges[i-1]
	if compareAddr(ip, cand.end) <= 0 {
		return cand.info, true
	}
	return Info{}, false
}

// EmptyResolver devuelve siempre no encontrado. Usado cuando no hay base ASN:
// la API funciona con asn=null / isp=null / operator=unknown.
type EmptyResolver struct{}

func (EmptyResolver) Lookup(netip.Addr) (Info, bool) { return Info{}, false }

func lastAddr(pfx netip.Prefix) netip.Addr {
	addr := pfx.Addr()
	bits := pfx.Bits()
	if addr.Is4() {
		var b [4]byte
		b = addr.As4()
		for i := bits; i < 32; i++ {
			b[i/8] |= 1 << (7 - uint(i%8))
		}
		return netip.AddrFrom4(b)
	}
	var b [16]byte
	b = addr.As16()
	for i := bits; i < 128; i++ {
		b[i/8] |= 1 << (7 - uint(i%8))
	}
	return netip.AddrFrom16(b)
}

func compareAddr(a, b netip.Addr) int {
	if a.Is4() && b.Is4() {
		return compareU32(a.As4(), b.As4())
	}
	if a.Is6() && b.Is6() {
		return compareU64(a.As16(), b.As16())
	}
	return a.Compare(b)
}

func compareU32(a, b [4]byte) int {
	av := binary.BigEndian.Uint32(a[:])
	bv := binary.BigEndian.Uint32(b[:])
	switch {
	case av < bv:
		return -1
	case av > bv:
		return 1
	default:
		return 0
	}
}

func compareU64(a, b [16]byte) int {
	ah := binary.BigEndian.Uint64(a[:8])
	bh := binary.BigEndian.Uint64(b[:8])
	if ah != bh {
		if ah < bh {
			return -1
		}
		return 1
	}
	al := binary.BigEndian.Uint64(a[8:])
	bl := binary.BigEndian.Uint64(b[8:])
	switch {
	case al < bl:
		return -1
	case al > bl:
		return 1
	default:
		return 0
	}
}
