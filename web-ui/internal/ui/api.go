package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"amneziawg-web-ui/web-ui/api"
)

// apiBase prefixes every backend endpoint path.
const apiBase = "/api"

// Backend is a thin REST client for the Go/Fiber backend. Requests are
// relative to the page origin, so the browser replays the HTTP basic-auth
// credentials it already holds for this realm.
type Backend struct {
	base string
	http *http.Client
}

func newBackend() *Backend {
	return &Backend{
		base: strings.TrimSuffix(baseURL(), "/"),
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// URL turns an API path into an absolute URL, for the places where the
// browser has to fetch something itself (downloads, new tabs).
func (b *Backend) URL(path string) string {
	return b.base + path
}

func (b *Backend) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, b.URL(path), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s", apiError(data, resp.StatusCode))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unexpected response from %s: %w", path, err)
	}
	return nil
}

// apiError digs the "error" field out of a failed JSON response, falling back
// to the raw body (trimmed) or the status code.
func apiError(body []byte, status int) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		return text
	}
	return fmt.Sprintf("HTTP %d", status)
}

func (b *Backend) get(path string, out any) error { return b.do(http.MethodGet, path, nil, out) }
func (b *Backend) del(path string) error          { return b.do(http.MethodDelete, path, nil, nil) }

func (b *Backend) post(path string, body, out any) error {
	return b.do(http.MethodPost, path, body, out)
}

func (b *Backend) put(path string, body, out any) error {
	return b.do(http.MethodPut, path, body, out)
}

// ── Endpoints ────────────────────────────────────────────────────────────────

func (b *Backend) SystemStatus() (api.SystemStatus, error) {
	var out api.SystemStatus
	return out, b.get(apiBase+"/system/status", &out)
}

func (b *Backend) RefreshIP() (string, error) {
	var out api.PublicIP
	err := b.get(apiBase+"/system/refresh-ip", &out)
	return out.Address, err
}

func (b *Backend) Servers() ([]api.Server, error) {
	var out []api.Server
	return out, b.get(apiBase+"/servers", &out)
}

func (b *Backend) CreateServer(req api.CreateServerRequest) (api.Server, error) {
	var out api.Server
	return out, b.post(apiBase+"/servers", req, &out)
}

func (b *Backend) DeleteServer(id string) error { return b.del(apiBase + "/servers/" + id) }

func (b *Backend) StartServer(id string) error {
	return b.post(apiBase+"/servers/"+id+"/start", nil, nil)
}

func (b *Backend) StopServer(id string) error {
	return b.post(apiBase+"/servers/"+id+"/stop", nil, nil)
}

func (b *Backend) ServerInfo(id string) (api.ServerInfo, error) {
	var out api.ServerInfo
	return out, b.get(apiBase+"/servers/"+id+"/info", &out)
}

func (b *Backend) ServerConfig(id string) (api.ServerConfig, error) {
	var out api.ServerConfig
	return out, b.get(apiBase+"/servers/"+id+"/config", &out)
}

func (b *Backend) InterfaceTraffic() (map[string]api.InterfaceTraffic, error) {
	out := map[string]api.InterfaceTraffic{}
	return out, b.get(apiBase+"/servers/traffic", &out)
}

// PeerTraffic returns the per-client counters of one server. A stopped
// interface simply has no counters, which is not an error worth surfacing.
func (b *Backend) PeerTraffic(serverID string) map[string]api.ClientTraffic {
	out := map[string]api.ClientTraffic{}
	if err := b.get(apiBase+"/servers/"+serverID+"/traffic", &out); err != nil {
		return map[string]api.ClientTraffic{}
	}
	return out
}

func (b *Backend) Clients(serverID string) ([]api.Client, error) {
	var out []api.Client
	return out, b.get(apiBase+"/servers/"+serverID+"/clients", &out)
}

func (b *Backend) AddClient(serverID string, req api.AddClientRequest) error {
	return b.post(apiBase+"/servers/"+serverID+"/clients", req, nil)
}

func (b *Backend) DeleteClient(serverID, clientID string) error {
	return b.del(apiBase + "/servers/" + serverID + "/clients/" + clientID)
}

func (b *Backend) UpdateAllowedIPs(serverID, clientID, allowedIPs string) error {
	return b.put(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/allowed-ips",
		api.UpdateAllowedIPsRequest{AllowedIPs: allowedIPs}, nil)
}

func (b *Backend) UpdateISettings(serverID, clientID string, apply bool, settings api.ISettings) error {
	if settings == nil {
		settings = api.ISettings{}
	}
	return b.put(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/i-settings",
		api.UpdateISettingsRequest{ApplyISettings: &apply, ISettings: settings}, nil)
}

// UpdateSuspendTime sends an RFC 3339 timestamp, or null to clear it.
func (b *Backend) UpdateSuspendTime(serverID, clientID string, at *time.Time) error {
	var stamp *string
	if at != nil {
		value := at.Format(time.RFC3339)
		stamp = &value
	}
	return b.put(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/suspend-time",
		api.UpdateSuspendTimeRequest{SuspendAt: stamp}, nil)
}

func (b *Backend) SuspendClient(serverID, clientID string) error {
	return b.post(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/suspend", nil, nil)
}

func (b *Backend) ActivateClient(serverID, clientID string) error {
	return b.post(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/activate", nil, nil)
}

func (b *Backend) ClientConfigs(serverID, clientID string) (api.ClientConfigs, error) {
	var out api.ClientConfigs
	return out, b.get(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/config-both", &out)
}

// AmneziaLink returns the native vpn:// link, or an empty string when the
// backend cannot build one (the UI then just disables that view).
func (b *Backend) AmneziaLink(serverID, clientID string) string {
	var out api.AmneziaLink
	if err := b.get(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/link", &out); err != nil {
		return ""
	}
	return out.VPNURL
}

func (b *Backend) DefaultISettings() (api.ISettings, error) {
	out := api.ISettings{}
	return out, b.get(apiBase+"/default-i-settings", &out)
}

// ClientConfigURL is the download endpoint for a client .conf file.
func (b *Backend) ClientConfigURL(serverID, clientID string) string {
	return b.URL(apiBase + "/servers/" + url.PathEscape(serverID) + "/clients/" + url.PathEscape(clientID) + "/config")
}

// ServerConfigURL is the download endpoint for a server .conf file.
func (b *Backend) ServerConfigURL(serverID string) string {
	return b.URL(apiBase + "/servers/" + url.PathEscape(serverID) + "/config/download")
}
