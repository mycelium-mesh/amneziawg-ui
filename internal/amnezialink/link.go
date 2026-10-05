// Package amnezialink builds "vpn://" links in AmneziaVPN's own native config
// format: base64 of the same JSON the app exports and imports.
package amnezialink

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"amneziawg-web-ui/internal/netutil"
	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

// protocolVersion is the "protocol_version" value the AmneziaVPN app uses
// for the AmneziaWG 3 generation (protocols::awg::awgV3 in amnezia-client).
const protocolVersion = "3.1"

// container is the AmneziaVPN container name for AmneziaWG 3.
const container = "amnezia-awg2"

// Build renders the link for one client of a server. endpoint is the host
// the client connects to, already resolved by the caller.
//
// The link sits alongside the plain .conf export rather than replacing it:
// it is a single string, so it survives being pasted into a chat and the app
// opens it directly, and the JSON has room for what a .conf cannot express -
// the container name, the protocol version the app matches against, a
// readable server description and the DNS servers as their own keys.
//
// The shape follows the app's own per-client export
// (ExportController::generateConnectionConfig): no "isThirdPartyConfig", and
// the .conf text under "config" in last_config. The flag would send the app
// to its read-only raw view instead of the connection settings page, and
// that page is the only place a user can fix the MTU - the app overwrites
// last_config's "mtu" with its own 1376 on every vpn:// import
// (ImportController::processAmneziaConfig), whatever the link says.
func Build(srv *api.Server, client *api.Client, endpoint string) (string, error) {
	_, subnetCidr := netutil.SplitCIDR(srv.Subnet)

	var allowedIPList []string
	for _, ip := range strings.Split(wgconf.AllowedIPsOrDefault(client.AllowedIPs), ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			allowedIPList = append(allowedIPList, ip)
		}
	}

	awgObj := map[string]interface{}{
		"port":            fmt.Sprintf("%d", srv.Port),
		"transport_proto": "udp",
		// The app compares this against its own protocols::awg::awgV3
		// constant, which is the literal string "3.1" in every release that
		// knows about AmneziaWG 3 (5.0.1.5 onwards; no shipped client ever
		// used a bare "3"). A mismatch makes the app render the server with
		// the pre-3.0 settings page and flag it as an outdated container.
		"protocol_version": protocolVersion,
		"subnet_address":   srv.ServerIP,
		"subnet_cidr":      subnetCidr,
	}

	clientObj := map[string]interface{}{
		"hostName":        endpoint,
		"port":            srv.Port,
		"client_ip":       client.ClientIP,
		"client_priv_key": client.ClientPrivateKey,
		"client_pub_key":  client.ClientPublicKey,
		"server_pub_key":  srv.ServerPublicKey,
		"psk_key":         client.PresharedKey,
		"clientId":        client.ClientPublicKey,
		"allowed_ips":     allowedIPList,
		"mtu":             fmt.Sprintf("%d", srv.MTU),
		"config":          wgconf.ClientConf(srv, client, endpoint, false),
	}

	if client.ObfuscationEnabled && client.ObfuscationParams != nil {
		p := client.ObfuscationParams
		for k, v := range map[string]string{
			"Jc": fmt.Sprintf("%d", p.Jc), "Jmin": fmt.Sprintf("%d", p.Jmin), "Jmax": fmt.Sprintf("%d", p.Jmax),
			"S1": fmt.Sprintf("%d", p.S1), "S2": fmt.Sprintf("%d", p.S2),
			"S3": fmt.Sprintf("%d", p.S3), "S4": fmt.Sprintf("%d", p.S4),
			"H1": fmt.Sprintf("%d", p.H1), "H2": fmt.Sprintf("%d", p.H2),
			"H3": fmt.Sprintf("%d", p.H3), "H4": fmt.Sprintf("%d", p.H4),
		} {
			awgObj[k] = v
			clientObj[k] = v
		}
		for k, v := range map[string]string{
			"HeaderProtectionKey":    p.HeaderProtectionKey,
			"ContentPaddingAddition": p.ContentPaddingAddition,
			"RekeyAfterTime":         p.RekeyAfterTime,
			"RekeyTimeout":           p.RekeyTimeout,
			"RejectAfterTime":        p.RejectAfterTime,
			"KeepaliveTimeout":       p.KeepaliveTimeout,
			"MaxHandshakeAttempts":   p.MaxHandshakeAttempts,
			// The app stores these as the strings "on"/"off" (awgBoolOn /
			// awgBoolOff) and treats an absent key as off, so only the "on"
			// case needs to travel.
			"RandomTrailers": boolOrEmpty(p.RandomTrailers),
			"DisableCookies": boolOrEmpty(p.DisableCookies),
		} {
			if v != "" {
				awgObj[k] = v
				clientObj[k] = v
			}
		}
	}
	clientObj["persistent_keep_alive"] = wgconf.PersistentKeepalive(client.ObfuscationParams)

	if client.ApplyISettings {
		for n := 1; n <= 5; n++ {
			if v := client.ISettings[fmt.Sprintf("i%d", n)]; v != "" {
				iKey := fmt.Sprintf("I%d", n)
				awgObj[iKey] = v
				clientObj[iKey] = v
			}
		}
	}

	clientJSON, err := json.Marshal(clientObj)
	if err != nil {
		return "", err
	}
	awgObj["last_config"] = string(clientJSON)

	root := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"container": container,
				"awg":       awgObj,
			},
		},
		"defaultContainer": container,
		"description":      fmt.Sprintf("%s - %s", srv.Name, client.Name),
		"hostName":         endpoint,
	}
	if len(srv.DNS) > 0 {
		root["dns1"] = srv.DNS[0]
		if len(srv.DNS) > 1 {
			root["dns2"] = srv.DNS[1]
		}
	}

	rootJSON, err := json.Marshal(root)
	if err != nil {
		return "", err
	}

	return "vpn://" + base64.RawURLEncoding.EncodeToString(rootJSON), nil
}

// boolOrEmpty renders an AmneziaWG 3.1 toggle the way the AmneziaVPN app's
// JSON expects it, or "" for off so the key can be dropped entirely.
func boolOrEmpty(enabled bool) string {
	if enabled {
		return "on"
	}
	return ""
}
