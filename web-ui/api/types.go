// Package api holds the transport structures shared by the Go/Fiber backend
// and the Fyne web frontend. Both sides live in one repository, so the JSON
// contract is declared exactly once here instead of being mirrored (and
// slowly drifting) in two places.
package api

// ObfuscationParams holds AmneziaWG obfuscation parameters.
type ObfuscationParams struct {
	Jc   int `json:"Jc"`
	Jmin int `json:"Jmin"`
	Jmax int `json:"Jmax"`
	S1   int `json:"S1"`
	S2   int `json:"S2"`
	S3   int `json:"S3"`
	S4   int `json:"S4"`
	H1   int `json:"H1"`
	H2   int `json:"H2"`
	H3   int `json:"H3"`
	H4   int `json:"H4"`
	MTU  int `json:"MTU"`
	// HeaderProtectionKey is a 32-byte base64-encoded key used by the
	// AmneziaWG 3.0 header protection mechanism. Generated automatically
	// whenever obfuscation is enabled, and must match byte-for-byte between
	// server and client.
	HeaderProtectionKey string `json:"HeaderProtectionKey,omitempty"`

	// The following are optional AmneziaWG 3.0 "client-side" tuning knobs:
	// each side of the tunnel applies them to its own behavior, so they do
	// NOT need to match between server and client. All accept either a
	// plain integer or an "a-b" range (e.g. "5-10"); empty means "use the
	// engine default".
	ContentPaddingAddition string `json:"ContentPaddingAddition,omitempty"`
	RekeyAfterTime         string `json:"RekeyAfterTime,omitempty"`
	RekeyTimeout           string `json:"RekeyTimeout,omitempty"`
	RejectAfterTime        string `json:"RejectAfterTime,omitempty"`
	KeepaliveTimeout       string `json:"KeepaliveTimeout,omitempty"`
	MaxHandshakeAttempts   string `json:"MaxHandshakeAttempts,omitempty"`
	// PersistentKeepalive overrides the hardcoded "25" written to client
	// [Peer] sections; accepts a plain integer or "a-b" range. Empty means
	// keep the default of 25.
	PersistentKeepalive string `json:"PersistentKeepalive,omitempty"`

	// The following two are AmneziaWG 3.1 additions. Both are written into
	// the server .conf and every client config from the same stored values,
	// which keeps RandomTrailers - the one of the two that has to agree on
	// both ends - in sync without any manual copying.

	// RandomTrailers appends a random number of bytes to every packet, so a
	// handshake no longer has a fixed on-the-wire length. The receiver only
	// tolerates the extra bytes when it has the same flag on - a 3.0-era
	// client (amnezia-client < 5.0.1.5, older amneziawg-android/apple) will
	// silently drop the oversized handshake.
	RandomTrailers bool `json:"RandomTrailers"`

	// DisableCookies stops the interface from ever answering with a cookie
	// reply, and disables under-load MAC2 verification altogether, removing
	// the cookie exchange as a fingerprint. Purely local behavior, but it is
	// written to both sides for consistency.
	DisableCookies bool `json:"DisableCookies"`
}

// Server holds a WireGuard server configuration.
//
// The private key never leaves the backend: it is stored in the on-disk
// config, but the frontend simply ignores the field.
type Server struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Protocol           string             `json:"protocol"`
	Port               int                `json:"port"`
	Status             string             `json:"status"`
	Interface          string             `json:"interface"`
	ConfigPath         string             `json:"config_path"`
	ServerPublicKey    string             `json:"server_public_key"`
	ServerPrivateKey   string             `json:"server_private_key"`
	Subnet             string             `json:"subnet"`
	ServerIP           string             `json:"server_ip"`
	MTU                int                `json:"mtu"`
	PublicIP           string             `json:"public_ip"`
	Endpoint           string             `json:"endpoint"`
	ObfuscationEnabled bool               `json:"obfuscation_enabled"`
	ObfuscationParams  *ObfuscationParams `json:"obfuscation_params"`
	AutoStart          bool               `json:"auto_start"`
	DNS                []string           `json:"dns"`
	Clients            []Client           `json:"clients"`
	UnboundNATIPs      []string           `json:"unbound_nat_ips"`
	CreatedAt          float64            `json:"created_at"`
}

// Client holds a WireGuard client configuration.
type Client struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	ServerID           string             `json:"server_id"`
	ServerName         string             `json:"server_name"`
	Status             string             `json:"status"`
	CreatedAt          float64            `json:"created_at"`
	ClientPrivateKey   string             `json:"client_private_key"`
	ClientPublicKey    string             `json:"client_public_key"`
	PresharedKey       string             `json:"preshared_key"`
	ClientIP           string             `json:"client_ip"`
	ObfuscationEnabled bool               `json:"obfuscation_enabled"`
	ObfuscationParams  *ObfuscationParams `json:"obfuscation_params"`
	ApplyISettings     bool               `json:"apply_i_settings"`
	ISettings          map[string]string  `json:"i_settings"`
	AllowedIPs         string             `json:"allowed_ips"`
	SuspendAt          *float64           `json:"suspend_at,omitempty"`
}

// CreateServerRequest is the payload to create a new server.
type CreateServerRequest struct {
	Name              string             `json:"name"`
	Port              int                `json:"port"`
	Subnet            string             `json:"subnet"`
	MTU               int                `json:"mtu"`
	Endpoint          string             `json:"endpoint"`
	DNS               interface{}        `json:"dns"`
	Obfuscation       *bool              `json:"obfuscation"`
	ObfuscationParams *ObfuscationParams `json:"obfuscation_params"`
	AutoStart         *bool              `json:"auto_start"`
}

// AddClientRequest is the payload to add a new client.
type AddClientRequest struct {
	Name           string            `json:"name"`
	ApplyISettings bool              `json:"apply_i_settings"`
	ISettings      map[string]string `json:"i_settings"`
	AllowedIPs     string            `json:"allowed_ips"`
}

// UpdateAllowedIPsRequest is the payload to update client AllowedIPs.
type UpdateAllowedIPsRequest struct {
	AllowedIPs string `json:"allowed_ips"`
}

// UpdateISettingsRequest is the payload to update client I-settings.
type UpdateISettingsRequest struct {
	ApplyISettings *bool             `json:"apply_i_settings"`
	ISettings      map[string]string `json:"i_settings"`
}

// UpdateSuspendTimeRequest is the payload to set auto-suspend time.
type UpdateSuspendTimeRequest struct {
	SuspendAt *string `json:"suspend_at"` // ISO 8601 or null
}

// ClientTraffic holds traffic statistics for a single client.
type ClientTraffic struct {
	Received      string `json:"received"`
	Sent          string `json:"sent"`
	LastHandshake string `json:"last_handshake"`
	Endpoint      string `json:"endpoint"`
}

// InterfaceTraffic holds the RX/TX counters of one WireGuard interface,
// keyed "rx" and "tx".
type InterfaceTraffic = map[string]string

// ISettings are the optional I1-I5 signature packets, keyed "i1".."i5".
type ISettings = map[string]string

// ── Response payloads ────────────────────────────────────────────────────────

// ServerInfo is the payload of GET /api/servers/:id/info.
type ServerInfo struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Protocol           string             `json:"protocol"`
	Port               int                `json:"port"`
	Status             string             `json:"status"`
	Interface          string             `json:"interface"`
	ConfigPath         string             `json:"config_path"`
	PublicIP           string             `json:"public_ip"`
	ServerIP           string             `json:"server_ip"`
	Subnet             string             `json:"subnet"`
	MTU                int                `json:"mtu"`
	ObfuscationEnabled bool               `json:"obfuscation_enabled"`
	ObfuscationParams  *ObfuscationParams `json:"obfuscation_params"`
	ClientsCount       int                `json:"clients_count"`
	Clients            []Client           `json:"clients"`
	CreatedAt          float64            `json:"created_at"`
	ConfigPreview      string             `json:"config_preview"`
	PublicKey          string             `json:"public_key"`
	DNS                []string           `json:"dns"`
	DefaultISettings   ISettings          `json:"default_i_settings"`
}

// ServerConfig is the payload of GET /api/servers/:id/config.
type ServerConfig struct {
	ServerID      string `json:"server_id"`
	ServerName    string `json:"server_name"`
	ConfigPath    string `json:"config_path"`
	ConfigContent string `json:"config_content"`
	Interface     string `json:"interface"`
	PublicKey     string `json:"public_key"`
}

// ClientConfigs is the payload of GET
// /api/servers/:id/clients/:clientId/config-both.
type ClientConfigs struct {
	ServerID          string   `json:"server_id"`
	ClientID          string   `json:"client_id"`
	ClientName        string   `json:"client_name"`
	CleanConfig       string   `json:"clean_config"`
	FullConfig        string   `json:"full_config"`
	CleanLength       int      `json:"clean_length"`
	FullLength        int      `json:"full_length"`
	CreatedAt         float64  `json:"created_at"`
	CreatedAtReadable string   `json:"created_at_readable"`
	SuspendAt         *float64 `json:"suspend_at"`
	SuspendAtReadable string   `json:"suspend_at_readable"`
}

// ErrorResponse is what every failing endpoint returns.
type ErrorResponse struct {
	Error string `json:"error"`
}

// ClientResult is the payload of the endpoints that create or change one
// client: the client as it now stands, plus - where the change affects it -
// the regenerated .conf.
type ClientResult struct {
	Client  *Client `json:"client"`
	Config  string  `json:"config,omitempty"`
	Message string  `json:"message,omitempty"`
}

// ActionResult is the payload of the endpoints that just do something to a
// server or client and have nothing to return but the outcome.
type ActionResult struct {
	Status   string `json:"status"`
	ServerID string `json:"server_id,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Message  string `json:"message,omitempty"`
}

// IptablesTest is the payload of GET /api/system/iptables-test.
type IptablesTest struct {
	ServerID      string            `json:"server_id"`
	ServerName    string            `json:"server_name"`
	Interface     string            `json:"interface"`
	Subnet        string            `json:"subnet"`
	IptablesCheck map[string]string `json:"iptables_check"`
}

// AmneziaLink is the payload of GET /api/servers/:id/clients/:clientId/link.
type AmneziaLink struct {
	VPNURL string `json:"vpn_url"`
}

// PublicIP is the payload of GET /api/system/refresh-ip.
type PublicIP struct {
	Address string `json:"public_ip"`
}

// SystemStatus is the payload of GET /api/system/status.
type SystemStatus struct {
	AWGAvailable  bool              `json:"awg_available"`
	PublicIP      string            `json:"public_ip"`
	TotalServers  int               `json:"total_servers"`
	TotalClients  int               `json:"total_clients"`
	ActiveServers int               `json:"active_servers"`
	Timestamp     float64           `json:"timestamp"`
	Environment   SystemEnvironment `json:"environment"`
}

// SystemEnvironment reports the defaults the backend was started with.
type SystemEnvironment struct {
	WebUIPort        string `json:"web_ui_port"`
	AutoStartServers bool   `json:"auto_start_servers"`
	DefaultMTU       int    `json:"default_mtu"`
	DefaultSubnet    string `json:"default_subnet"`
	DefaultPort      int    `json:"default_port"`
	DefaultDNS       string `json:"default_dns"`
}

// ── Socket.IO events ─────────────────────────────────────────────────────────

// StatusEvent is the "status" event sent to every socket on connect. Port is
// the WEB_UI_PORT value, which the backend keeps as the raw string it read
// from the environment.
type StatusEvent struct {
	Message  string `json:"message"`
	PublicIP string `json:"public_ip"`
	Port     string `json:"port"`
}

// ServerStatusEvent is the "server_status" event, broadcast whenever an
// interface goes up or down.
type ServerStatusEvent struct {
	ServerID string `json:"server_id"`
	Status   string `json:"status"`
}

// TrafficEvent is the "traffic_update" event: interface counters per server,
// and peer counters per server and client.
type TrafficEvent struct {
	Timestamp     float64                             `json:"timestamp"`
	ClientTraffic map[string]map[string]ClientTraffic `json:"client_traffic"`
	ServerTraffic map[string]InterfaceTraffic         `json:"server_traffic"`
}
