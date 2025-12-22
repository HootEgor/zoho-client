package util

import (
	"net"
	"strings"
)

// ExtractIPAddress extracts the client IP address from the request.
// It handles X-Forwarded-For header (taking the first IP if multiple are present)
// and RemoteAddr (removing port if present).
func ExtractIPAddress(remoteAddr string, xForwardedFor string) string {
	// If X-Forwarded-For is present, use the first IP address
	if xForwardedFor != "" {
		// X-Forwarded-For can contain multiple IPs separated by commas
		ips := strings.Split(xForwardedFor, ",")
		if len(ips) > 0 {
			// Take the first IP and trim whitespace
			ip := strings.TrimSpace(ips[0])
			// Remove port if present (e.g., "192.168.1.1:12345" -> "192.168.1.1")
			if host, _, err := net.SplitHostPort(ip); err == nil {
				return host
			}
			return ip
		}
	}

	// Fall back to RemoteAddr, removing port if present
	if remoteAddr != "" {
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			return host
		}
		return remoteAddr
	}

	return ""
}
