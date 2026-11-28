package console

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAllowedHosts(t *testing.T) {
	loopback := []string{"localhost", "127.0.0.1", "::1"}
	label63 := strings.Repeat("a", 63)
	for _, tc := range []struct {
		name    string
		addr    string
		list    string
		want    []string // beyond the loopback names
		wantErr bool
		errHas  string // pins which check refused it, where another would also fail
	}{
		{name: "defaults"},
		{name: "addr literal", addr: "10.0.0.5:8090", want: []string{"10.0.0.5"}},
		{name: "addr hostname, case and dot", addr: "Console.Internal.:8090", want: []string{"console.internal"}},
		{name: "addr ipv4 wildcard", addr: "0.0.0.0:8090"},
		{name: "addr ipv6 wildcard", addr: "[::]:8090"},
		{name: "addr port only", addr: ":8090"},
		{name: "entries", list: " a.example , B.Example.:443,, [fd00::1]:8090 ,fd00::2",
			want: []string{"a.example", "b.example", "fd00::1", "fd00::2"}},
		{name: "addr and entries", addr: "10.0.0.5:8090", list: "a.example", want: []string{"10.0.0.5", "a.example"}},
		{name: "addr ipv6 canonical", addr: "[2001:DB8::0001]:8090", want: []string{"2001:db8::1"}},
		{name: "ipv6 canonical", list: "2001:DB8::0001", want: []string{"2001:db8::1"}},
		{name: "bare ipv6 has no port", list: "fd00::1:8090", want: []string{"fd00::1:8090"}},
		{name: "unclosed bracket", list: "[fd00::1", wantErr: true},
		{name: "unopened bracket", list: "fd00::1]", wantErr: true},
		{name: "bracketed name", list: "[localhost]", wantErr: true},
		{name: "zone id", list: "[fe80::1%eth0]:8090", wantErr: true},
		{name: "wildcard", list: "*.example.com", wantErr: true},
		{name: "empty label", list: "a..b", wantErr: true},
		{name: "non-ascii", list: "bücher.example", wantErr: true},
		{name: "numeric, not an ip", list: "256.1.1.1", wantErr: true},
		{name: "named port", list: "a.example:http", wantErr: true},
		{name: "port out of range", list: "a.example:99999", wantErr: true},
		{name: "scheme", list: "https://a.example", wantErr: true, errHas: "want a hostname"},
		{name: "path", list: "a.example/console", wantErr: true},
		{name: "userinfo", list: "op@a.example", wantErr: true},
		{name: "inner space", list: "a .example", wantErr: true},
		{name: "scheme without slashes", list: "http:a.example", wantErr: true},
		{name: "port only", list: ":8090", wantErr: true},
		{name: "port zero", list: "a.example:0", wantErr: true},
		{name: "bracketed ipv4", list: "[127.0.0.1]", wantErr: true},
		{name: "text after bracket", list: "[::1]x", wantErr: true},
		{name: "64-character label", list: strings.Repeat("a", 64) + ".example", wantErr: true},
		{name: "254-character name", list: label63 + "." + label63 + "." + label63 + "." + strings.Repeat("b", 62), wantErr: true},
		{name: "ipv4 unspecified", list: "0.0.0.0", wantErr: true},
		{name: "ipv6 unspecified", list: "[::]", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAllowedHosts(tc.addr, tc.list)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("parseAllowedHosts = %v, %v; want an error containing %q", got, err, tc.errHas)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAllowedHosts: %v", err)
			}
			want := map[string]bool{}
			for _, h := range append(loopback, tc.want...) {
				want[h] = true
			}
			if !maps.Equal(got, want) {
				t.Errorf("parseAllowedHosts = %v, want %v", got, want)
			}
		})
	}
}

// A Host the console doesn't answer to is refused before routing and auth, so
// a DNS-rebinding page can't reach even the static assets, and the refusal
// still carries the security headers.
func TestConsoleRefusesUnknownHost(t *testing.T) {
	t.Setenv("CONSOLE_ADDR", "0.0.0.0:8090")
	t.Setenv("CONSOLE_ALLOWED_HOSTS", "console.example.com,2001:DB8::0001,kiosk.example")
	srv, err := NewServer(nil, "test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	withHost := func(host, target string) *http.Request {
		req := httptest.NewRequest("GET", target, nil)
		req.Host = host
		return req
	}
	const js = "/console/static/console.js"
	checkHeaders(t, srv, []headerCase{
		{"rebinding name", withHost("attacker.example:8090", "/console/tokens"), http.StatusMisdirectedRequest, noStore},
		{"rebinding name, static", withHost("attacker.example", js), http.StatusMisdirectedRequest, noStore},
		{"wildcard bind address", withHost("0.0.0.0:8090", js), http.StatusMisdirectedRequest, noStore},
		{"no host", withHost("", js), http.StatusMisdirectedRequest, noStore},
		{"absolute-form target", httptest.NewRequest("GET", "http://attacker.example"+js, nil), http.StatusMisdirectedRequest, noStore},
		{"bracketed name", withHost("[localhost]", js), http.StatusMisdirectedRequest, noStore},
		{"named port", withHost("localhost:abc", js), http.StatusMisdirectedRequest, noStore},
		{"text after bracket", withHost("[::1]x", js), http.StatusMisdirectedRequest, noStore},
		{"kelvin sign folds to k", withHost("\u212Aiosk.example", js), http.StatusMisdirectedRequest, noStore},
		{"ipv6 loopback, long form", withHost("[0:0:0:0:0:0:0:1]:8090", js), http.StatusOK, staticCache},
		{"configured ipv6, canonical", withHost("[2001:db8::1]", js), http.StatusOK, staticCache},
		{"localhost, case and dot", withHost("LocalHost.:8090", js), http.StatusOK, staticCache},
		{"ipv4 loopback", withHost("127.0.0.1:8090", js), http.StatusOK, staticCache},
		{"ipv6 loopback", withHost("[::1]:8090", js), http.StatusOK, staticCache},
		{"configured name, any port", withHost("console.example.com:9443", js), http.StatusOK, staticCache},
		{"allowed host reaches auth", withHost("localhost:8090", "/console/tokens"), http.StatusUnauthorized, noStore},
	})
}

func TestNewServerRejectsInvalidAllowedHost(t *testing.T) {
	t.Setenv("CONSOLE_ALLOWED_HOSTS", "https://console.example.com")
	if _, err := NewServer(nil, "test"); err == nil {
		t.Fatal("NewServer accepted a CONSOLE_ALLOWED_HOSTS entry with a scheme")
	}
}
