// Package publicip finds the address clients should connect to, which a
// container behind NAT cannot read off its own interfaces.
package publicip

import (
	"net/http"
	"strings"
	"time"

	"amneziawg-web-ui/internal/netutil"
	"amneziawg-web-ui/web-ui/api"
)

// Unknown is what every generated config shows when nothing could tell us
// the address; the operator sees it and sets an endpoint by hand. It is the
// shared placeholder, so the config and the API cannot disagree about it.
const Unknown = api.EndpointUnknown

// Services answer a plain GET with the caller's address.
var Services = []string{
	"http://ifconfig.me",
	"https://api.ipify.org",
	"https://ident.me",
}

// Detect asks each service in turn and returns the first IPv4 answer. When
// none answers, fallback - typically the host's own routing table - gets a
// say; failing that, Unknown.
func Detect(fallback func() (string, error)) string {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, svc := range Services {
		resp, err := client.Get(svc)
		if err != nil {
			continue
		}
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		ip := strings.TrimSpace(string(buf[:n]))
		if netutil.IsIPv4(ip) {
			return ip
		}
	}

	if fallback != nil {
		if ip, err := fallback(); err == nil && netutil.IsIPv4(ip) {
			return ip
		}
	}
	return Unknown
}
