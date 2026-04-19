package dispatch

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrForbiddenPeerAddress is returned when the forwarder is asked to send
// a request to an address that resolves to a loopback, link-local,
// unspecified, or multicast range. Sentinel so callers can distinguish
// SSRF-guard rejections from network errors.
var ErrForbiddenPeerAddress = errors.New("peer address is forbidden")

// validatePeerAddress parses a cluster registry address and rejects
// addresses pointing at addresses the cluster forwarder must never dial:
// loopback, link-local (169.254.0.0/16, fe80::/10), IPv4/IPv6 unspecified,
// multicast. Literal IPs are checked directly; hostnames resolve at
// validation time and every A/AAAA answer must pass.
//
// This guards against the SSRF vector where an attacker who can write
// to the cluster registry pivots HMAC-authenticated dispatch requests to
// an internal service (Postgres on 127.0.0.1, cloud metadata at
// 169.254.169.254, etc.).
//
// DNS resolution at validation time is a best-effort defence; a full
// defence against DNS rebinding would also pin the resolved IP on the
// dialer. That is out of scope here — the loopback/link-local guard is
// the first-order fix.
func validatePeerAddress(raw string, allowLoopback bool) error {
	hostPort := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("%w: parse %q: %v", ErrForbiddenPeerAddress, raw, err)
		}
		hostPort = u.Host
	}
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return fmt.Errorf("%w: empty host in %q", ErrForbiddenPeerAddress, raw)
	}

	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip, allowLoopback); err != nil {
			return fmt.Errorf("%w: %v (%s)", ErrForbiddenPeerAddress, err, raw)
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%w: resolve %q: %v", ErrForbiddenPeerAddress, host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %q resolved to no addresses", ErrForbiddenPeerAddress, host)
	}
	for _, ip := range ips {
		if err := checkIP(ip, allowLoopback); err != nil {
			return fmt.Errorf("%w: %q resolved to %s (%v)", ErrForbiddenPeerAddress, host, ip, err)
		}
	}
	return nil
}

// checkIP returns a descriptive error if ip is in any forbidden range.
// When allowLoopback is true, 127.0.0.0/8 and ::1 are permitted (test
// harnesses only — never set in production). Link-local, unspecified,
// and multicast remain rejected even with the flag set.
func checkIP(ip net.IP, allowLoopback bool) error {
	switch {
	case ip.IsLoopback():
		if allowLoopback {
			return nil
		}
		return fmt.Errorf("loopback %s", ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf("link-local %s", ip)
	case ip.IsUnspecified():
		return fmt.Errorf("unspecified %s", ip)
	case ip.IsMulticast():
		return fmt.Errorf("multicast %s", ip)
	}
	return nil
}
