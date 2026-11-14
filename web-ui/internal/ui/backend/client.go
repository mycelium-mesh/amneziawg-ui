// Package backend is the REST client for the Go/Fiber server the page was
// served by. It knows the endpoints and the wire types, nothing about the
// widgets that call it.
package backend

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
	"amneziawg-web-ui/web-ui/internal/ui/browser"
)

// apiBase prefixes every backend endpoint path.
const apiBase = "/api"

// Client is a thin REST client for the Go/Fiber backend. Requests are
// relative to the page origin, so the browser replays the HTTP basic-auth
// credentials it already holds for this realm.
type Client struct {
	base string
	http *http.Client
}

func New() *Client {
	return &Client{
		base: strings.TrimSuffix(browser.Origin(), "/"),
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// URL turns an API path into an absolute URL, for the places where the
// browser has to fetch something itself (downloads, new tabs).
func (b *Client) URL(path string) string {
	return b.base + path
}

func (b *Client) do(method, path string, body, out any) error {
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

func (b *Client) get(path string, out any) error { return b.do(http.MethodGet, path, nil, out) }
func (b *Client) del(path string) error          { return b.do(http.MethodDelete, path, nil, nil) }

func (b *Client) post(path string, body, out any) error {
	return b.do(http.MethodPost, path, body, out)
}

func (b *Client) put(path string, body, out any) error {
	return b.do(http.MethodPut, path, body, out)
}

// ── Endpoints ────────────────────────────────────────────────────────────────

func (b *Client) SystemStatus() (api.SystemStatus, error) {
	var out api.SystemStatus
	return out, b.get(apiBase+"/system/status", &out)
}

func (b *Client) RefreshIP() (string, error) {
	var out api.PublicIP
	err := b.get(apiBase+"/system/refresh-ip", &out)
	return out.Address, err
}

func (b *Client) Servers() ([]api.Server, error) {
	var out []api.Server
	return out, b.get(apiBase+"/servers", &out)
}

func (b *Client) CreateServer(req api.CreateServerRequest) (api.Server, error) {
	var out api.Server
	return out, b.post(apiBase+"/servers", req, &out)
}

func (b *Client) DeleteServer(id string) error { return b.del(apiBase + "/servers/" + id) }

func (b *Client) StartServer(id string) error {
	return b.post(apiBase+"/servers/"+id+"/start", nil, nil)
}

func (b *Client) StopServer(id string) error {
	return b.post(apiBase+"/servers/"+id+"/stop", nil, nil)
}

// UpdateServerEndpoint sets the endpoint a client config carries; an empty
// value clears it back to the auto-detected public IP.
func (b *Client) UpdateServerEndpoint(serverID, endpoint string) error {
	return b.put(apiBase+"/servers/"+serverID+"/endpoint",
		api.UpdateEndpointRequest{Endpoint: &endpoint}, nil)
}

func (b *Client) ServerInfo(id string) (api.ServerInfo, error) {
	var out api.ServerInfo
	return out, b.get(apiBase+"/servers/"+id+"/info", &out)
}

func (b *Client) ServerConfig(id string) (api.ServerConfig, error) {
	var out api.ServerConfig
	return out, b.get(apiBase+"/servers/"+id+"/config", &out)
}

// Traffic returns every counter the page shows in one call.
func (b *Client) Traffic() (api.TrafficSnapshot, error) {
	var out api.TrafficSnapshot
	return out, b.get(apiBase+"/traffic", &out)
}

func (b *Client) Clients(serverID string) ([]api.Client, error) {
	var out []api.Client
	return out, b.get(apiBase+"/servers/"+serverID+"/clients", &out)
}

func (b *Client) AddClient(serverID string, req api.AddClientRequest) error {
	return b.post(apiBase+"/servers/"+serverID+"/clients", req, nil)
}

func (b *Client) DeleteClient(serverID, clientID string) error {
	return b.del(apiBase + "/servers/" + serverID + "/clients/" + clientID)
}

func (b *Client) UpdateAllowedIPs(serverID, clientID, allowedIPs string) error {
	return b.put(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/allowed-ips",
		api.UpdateAllowedIPsRequest{AllowedIPs: allowedIPs}, nil)
}

func (b *Client) UpdateISettings(serverID, clientID string, apply bool, settings api.ISettings) error {
	if settings == nil {
		settings = api.ISettings{}
	}
	return b.put(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/i-settings",
		api.UpdateISettingsRequest{ApplyISettings: &apply, ISettings: settings}, nil)
}

// UpdateSuspendTime sends an RFC 3339 timestamp, or null to clear it.
func (b *Client) UpdateSuspendTime(serverID, clientID string, at *time.Time) error {
	var stamp *string
	if at != nil {
		value := at.Format(time.RFC3339)
		stamp = &value
	}
	return b.put(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/suspend-time",
		api.UpdateSuspendTimeRequest{SuspendAt: stamp}, nil)
}

func (b *Client) SuspendClient(serverID, clientID string) error {
	return b.post(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/suspend", nil, nil)
}

func (b *Client) ActivateClient(serverID, clientID string) error {
	return b.post(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/activate", nil, nil)
}

func (b *Client) ClientConfigs(serverID, clientID string) (api.ClientConfigs, error) {
	var out api.ClientConfigs
	return out, b.get(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/config-both", &out)
}

// AmneziaLink returns the native vpn:// link, or an empty string when the
// backend cannot build one (the UI then just disables that view).
func (b *Client) AmneziaLink(serverID, clientID string) string {
	var out api.AmneziaLink
	if err := b.get(apiBase+"/servers/"+serverID+"/clients/"+clientID+"/link", &out); err != nil {
		return ""
	}
	return out.VPNURL
}

func (b *Client) DefaultISettings() (api.ISettings, error) {
	out := api.ISettings{}
	return out, b.get(apiBase+"/default-i-settings", &out)
}

// ClientConfigURL is the download endpoint for a client .conf file.
func (b *Client) ClientConfigURL(serverID, clientID string) string {
	return b.URL(apiBase + "/servers/" + url.PathEscape(serverID) + "/clients/" + url.PathEscape(clientID) + "/config")
}

// ClientConfigsURL is the download endpoint for the configs of every client
// of a server, as one zip archive.
func (b *Client) ClientConfigsURL(serverID string) string {
	return b.URL(apiBase + "/servers/" + url.PathEscape(serverID) + "/clients/configs")
}

// ServerConfigURL is the download endpoint for a server .conf file.
func (b *Client) ServerConfigURL(serverID string) string {
	return b.URL(apiBase + "/servers/" + url.PathEscape(serverID) + "/config/download")
}
