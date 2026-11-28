package console

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
)

// parseAllowedHosts builds the set of hostnames the console answers to: the
// loopback names, CONSOLE_ADDR's host when it names one (a wildcard bind says
// nothing about what operators browse to), and CONSOLE_ALLOWED_HOSTS. A
// malformed entry is an error rather than skipped, so a typo can't quietly
// lock an operator out or leave them guessing why.
func parseAllowedHosts(consoleAddr, list string) (map[string]bool, error) {
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

	if host, _, err := net.SplitHostPort(consoleAddr); err == nil {
		ip := net.ParseIP(host)
		if h := normalizeHost(host); h != "" && (ip == nil || !ip.IsUnspecified()) {
			allowed[h] = true
		}
	}

	for entry := range strings.SplitSeq(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, err := hostEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("CONSOLE_ALLOWED_HOSTS entry %q: %w", entry, err)
		}
		allowed[host] = true
	}
	return allowed, nil
}

// hostEntry validates one CONSOLE_ALLOWED_HOSTS entry: a specified IP literal
// or a DNS name, optionally with a numeric port, which is dropped.
func hostEntry(entry string) (string, error) {
	if strings.ContainsAny(entry, "/@?# \t") {
		return "", errors.New("want a hostname, optionally with :port")
	}
	host, isIP, err := canonicalHost(entry)
	if err != nil {
		return "", err
	}
	if isIP {
		// Like a wildcard CONSOLE_ADDR, this is a bind address, never a Host.
		if netip.MustParseAddr(host).IsUnspecified() {
			return "", errors.New("unspecified address; list the address operators browse to")
		}
	} else if err := checkDNSName(host); err != nil {
		return "", err
	}
	return host, nil
}

// normalizeHost reduces a Host value to its comparable hostname, or "" when it
// is malformed, which no allowlist entry can equal.
func normalizeHost(h string) string {
	host, _, err := canonicalHost(h)
	if err != nil {
		return ""
	}
	return host
}

// canonicalHost drops an optional port and returns the hostname in the form
// both sides of the comparison share: IP literals in canonical form, so
// [0:0:0:0:0:0:0:1] equals ::1, and names in lower case without the root dot.
// Brackets are accepted only around an IPv6 address.
func canonicalHost(h string) (host string, isIP bool, err error) {
	host = h
	port, hasPort := "", false
	bracketed := strings.HasPrefix(h, "[")
	if bracketed {
		end := strings.IndexByte(h, ']')
		if end < 0 {
			return "", false, errors.New("unbalanced brackets")
		}
		host = h[1:end]
		if rest := h[end+1:]; rest != "" {
			port, hasPort = strings.CutPrefix(rest, ":")
			if !hasPort {
				return "", false, errors.New("unexpected text after ']'")
			}
		}
	} else if strings.Count(h, ":") == 1 {
		// More than one colon is a bare IPv6 address, which can't carry a port.
		host, port, hasPort = strings.Cut(h, ":")
	}
	if hasPort {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return "", false, fmt.Errorf("%q is not a valid port number", port)
		}
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		// A zone names a local interface, not something a browser sends as a
		// host, so refusing it fails closed.
		if addr.Zone() != "" {
			return "", false, errors.New("IPv6 zone ids are not supported")
		}
		if bracketed && !addr.Is6() {
			return "", false, errors.New("brackets are only for IPv6 addresses")
		}
		return addr.String(), true, nil
	}
	if bracketed {
		return "", false, errors.New("brackets are only for IPv6 addresses")
	}
	// Checked before lowering: Unicode case folding maps some non-ASCII runes
	// (the Kelvin sign) onto ASCII letters, which would let them match a name.
	for i := range len(host) {
		if host[i] > unicode.MaxASCII {
			return "", false, errors.New("hostname is not ASCII; use the punycode form")
		}
	}
	return strings.TrimSuffix(strings.ToLower(host), "."), false, nil
}

// checkDNSName accepts dot-separated labels of letters, digits and hyphens, so
// a wildcard or a typo fails startup instead of matching nothing.
func checkDNSName(name string) error {
	if name == "" {
		return errors.New("empty hostname")
	}
	if len(name) > 253 {
		return errors.New("hostname is longer than 253 characters")
	}
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" {
			return errors.New("hostname has an empty label")
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q is longer than 63 characters", label)
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return fmt.Errorf("hostname has invalid character %q", c)
			}
		}
	}
	// No top-level domain is all digits, so this is a mistyped IP address.
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return errors.New("not a valid IP address")
	}
	return nil
}

// requireAllowedHost refuses a request whose Host the console doesn't answer
// to. This defeats DNS rebinding: a page on attacker.example that re-points its
// name at 127.0.0.1 still sends Host: attacker.example, so it can't read the
// console even though the browser treats the response as same-origin. The port
// is not compared, since the name alone is what rebinding can't forge.
func (srv *Server) requireAllowedHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !srv.allowedHosts[normalizeHost(r.Host)] {
			slog.Warn("console host refused", "host", clipHeader(r.Host), "path", clipHeader(r.URL.Path))
			http.Error(w, "this console does not answer to that host name", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
