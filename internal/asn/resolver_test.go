package asn

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func writeCSV(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCSVResolverRange(t *testing.T) {
	path := writeCSV(t, "asn.csv", "start_ip,end_ip,asn,name,isp\n"+
		"10.0.0.0,10.0.255.255,65001,Example Net,Example ISP\n"+
		"2001:db8::,2001:db8::ffff,65002,Example6,Example6 ISP\n")
	r, err := NewCSVResolver(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	info, ok := r.Lookup(netip.MustParseAddr("10.0.1.5"))
	if !ok || info.ASN != 65001 {
		t.Errorf("Lookup 10.0.1.5 = %+v ok=%v", info, ok)
	}
	if _, ok := r.Lookup(netip.MustParseAddr("10.1.0.0")); ok {
		t.Error("Lookup fuera de rango devolvió ok")
	}
	info6, ok := r.Lookup(netip.MustParseAddr("2001:db8::1234"))
	if !ok || info6.ASN != 65002 {
		t.Errorf("Lookup v6 = %+v ok=%v", info6, ok)
	}
}

func TestCSVResolverGeoLite2(t *testing.T) {
	path := writeCSV(t, "geolite.csv", "network,autonomous_system_number,autonomous_system_organization\n"+
		"186.96.0.0/13,13490,Comcel S.A.\n"+
		"8.8.8.0/24,15169,Google LLC\n")
	r, err := NewCSVResolver(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	info, ok := r.Lookup(netip.MustParseAddr("186.100.1.1"))
	if !ok || info.ASN != 13490 {
		t.Errorf("Lookup = %+v ok=%v", info, ok)
	}
	if _, ok := r.Lookup(netip.MustParseAddr("186.112.0.1")); ok {
		t.Error("Lookup fuera de rango devolvió ok")
	}
	info8, ok := r.Lookup(netip.MustParseAddr("8.8.8.8"))
	if !ok || info8.ASN != 15169 {
		t.Errorf("Lookup 8.8.8.8 = %+v ok=%v", info8, ok)
	}
}

func TestEmptyResolver(t *testing.T) {
	var r EmptyResolver
	if _, ok := r.Lookup(netip.MustParseAddr("8.8.8.8")); ok {
		t.Error("EmptyResolver devolvió ok")
	}
}

func TestLastAddr(t *testing.T) {
	pfx := netip.MustParsePrefix("186.96.0.0/13")
	last := lastAddr(pfx)
	if last.String() != "186.103.255.255" {
		t.Errorf("lastAddr = %s", last)
	}
}
