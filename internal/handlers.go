package internal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"encoding/json"

	"github.com/gofiber/fiber/v3"
)

// Handlers holds dependencies for HTTP request handlers.
type Handlers struct {
	mgr *Manager
	hub Hub
}

// Hub interface for handler usage (broader than HubBroadcaster).
type Hub interface {
	HubBroadcaster
}

// NewHandlers creates a Handlers instance.
func NewHandlers(mgr *Manager, hub Hub) *Handlers {
	return &Handlers{mgr: mgr, hub: hub}
}

// fail turns a manager error into a response. The status code comes from the
// kind of error, so "no such server" and "the interface refused to come up"
// stop sharing one code the way they did when the manager answered in
// booleans.
func fail(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return respond(c, fiber.StatusNotFound, ErrorResponse{Error: err.Error()})
	case errors.Is(err, ErrInvalid):
		return respond(c, fiber.StatusBadRequest, ErrorResponse{Error: err.Error()})
	case errors.Is(err, ErrConflict):
		return respond(c, fiber.StatusConflict, ErrorResponse{Error: err.Error()})
	default:
		return respond(c, fiber.StatusInternalServerError, ErrorResponse{Error: err.Error()})
	}
}

// respond writes a typed payload with an explicit status.
func respond(c fiber.Ctx, status int, payload any) error {
	return c.Status(status).JSON(payload)
}

// decode reads a JSON request body. An unparsable body is a bad request, not
// a silent fallback to the zero value of every field.
func decode(c fiber.Ctx, out any) error {
	body := c.Body()
	if len(body) == 0 {
		return nil // every payload here is optional in full
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	return nil
}

// RegisterRoutes attaches all API routes to the Fiber app.
func (h *Handlers) RegisterRoutes(app *fiber.App) {
	app.Get("/status", h.containerUptime)

	api := app.Group("/api")

	// Servers – static routes first
	api.Get("/servers/traffic", h.getAllServersTraffic)
	api.Get("/servers", h.getServers)
	api.Post("/servers", h.createServer)
	api.Delete("/servers/:id", h.deleteServer)
	api.Post("/servers/:id/start", h.startServer)
	api.Post("/servers/:id/stop", h.stopServer)
	api.Get("/servers/:id/config", h.getServerConfig)
	api.Get("/servers/:id/config/download", h.downloadServerConfig)
	api.Get("/servers/:id/info", h.getServerInfo)
	api.Get("/servers/:id/traffic", h.getServerTraffic)

	// Clients
	api.Get("/servers/:id/clients", h.getServerClients)
	api.Post("/servers/:id/clients", h.addClient)
	api.Delete("/servers/:id/clients/:clientId", h.deleteClient)
	api.Put("/servers/:id/clients/:clientId/allowed-ips", h.updateClientAllowedIPs)
	api.Put("/servers/:id/clients/:clientId/i-settings", h.updateClientISettings)
	api.Get("/servers/:id/clients/:clientId/config", h.downloadClientConfig)
	api.Get("/servers/:id/clients/:clientId/config-both", h.getClientConfigBoth)
	api.Get("/servers/:id/clients/:clientId/link", h.getClientAmneziaLink)
	api.Post("/servers/:id/clients/:clientId/suspend", h.suspendClient)
	api.Post("/servers/:id/clients/:clientId/activate", h.activateClient)
	api.Put("/servers/:id/clients/:clientId/suspend-time", h.updateClientSuspendTime)

	// Misc
	api.Get("/clients", h.getAllClients)
	api.Get("/default-i-settings", h.getDefaultISettings)
	api.Get("/system/status", h.systemStatus)
	api.Get("/system/refresh-ip", h.refreshIP)
	api.Get("/system/iptables-test", h.iptablesTest)
}

func (h *Handlers) containerUptime(c fiber.Ctx) error {
	out, err := exec.Command("stat", "-c", "%Y", "/proc/1/cmdline").Output()
	if err != nil {
		return c.SendString("Container Uptime: unknown")
	}
	epoch, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	uptime := time.Now().Unix() - epoch
	d := uptime / 86400
	h2 := (uptime % 86400) / 3600
	m2 := (uptime % 3600) / 60
	s2 := uptime % 60
	return c.SendString(fmt.Sprintf("Container Uptime: %dd %dh %dm %ds", d, h2, m2, s2))
}

// ── Servers ──────────────────────────────────────────────────────────────────

func (h *Handlers) getServers(c fiber.Ctx) error {
	return c.JSON(h.mgr.GetServersWithStatus())
}

func (h *Handlers) createServer(c fiber.Ctx) error {
	var req CreateServerRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}
	srv, err := h.mgr.CreateServer(req)
	if err != nil {
		// Everything CreateServer rejects is a problem with the request.
		return respond(c, fiber.StatusBadRequest, ErrorResponse{Error: err.Error()})
	}
	return c.JSON(srv)
}

func (h *Handlers) deleteServer(c fiber.Ctx) error {
	id := c.Params("id")
	if err := h.mgr.DeleteServer(id); err != nil {
		return fail(c, err)
	}
	return c.JSON(ActionResult{Status: "deleted", ServerID: id})
}

func (h *Handlers) startServer(c fiber.Ctx) error {
	id := c.Params("id")
	if err := h.mgr.StartServer(id); err != nil {
		return fail(c, err)
	}
	return c.JSON(ActionResult{Status: "started", ServerID: id})
}

func (h *Handlers) stopServer(c fiber.Ctx) error {
	id := c.Params("id")
	if err := h.mgr.StopServer(id); err != nil {
		return fail(c, err)
	}
	return c.JSON(ActionResult{Status: "stopped", ServerID: id})
}

func (h *Handlers) getServerConfig(c fiber.Ctx) error {
	id := c.Params("id")
	srv, ok := h.mgr.getServer(id)
	if !ok {
		return fail(c, serverNotFound(id))
	}
	data, err := os.ReadFile(srv.ConfigPath)
	if err != nil {
		return respond(c, fiber.StatusNotFound, ErrorResponse{Error: "config file not found"})
	}
	return c.JSON(ServerConfig{
		ServerID:      id,
		ServerName:    srv.Name,
		ConfigPath:    srv.ConfigPath,
		ConfigContent: string(data),
		Interface:     srv.Interface,
		PublicKey:     srv.ServerPublicKey,
	})
}

func (h *Handlers) downloadServerConfig(c fiber.Ctx) error {
	id := c.Params("id")
	srv, ok := h.mgr.getServer(id)
	if !ok {
		return fail(c, serverNotFound(id))
	}
	if _, err := os.Stat(srv.ConfigPath); err != nil {
		return respond(c, fiber.StatusNotFound, ErrorResponse{Error: "config file not found"})
	}
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"`, srv.Interface))
	return c.SendFile(srv.ConfigPath)
}

func (h *Handlers) getServerInfo(c fiber.Ctx) error {
	id := c.Params("id")
	srv, ok := h.mgr.getServer(id)
	if !ok {
		return fail(c, serverNotFound(id))
	}

	status := h.mgr.GetServerStatus(id)
	// A detached copy - srv.Clients is the live slice, and serialising it
	// while another request adds a peer is a race.
	clients := h.mgr.GetClientConfigs(id)

	preview := ""
	if data, err := os.ReadFile(srv.ConfigPath); err == nil {
		lines := strings.Split(string(data), "\n")
		if len(lines) > 10 {
			lines = lines[:10]
		}
		preview = strings.Join(lines, "\n")
	}

	return c.JSON(ServerInfo{
		ID:                 srv.ID,
		Name:               srv.Name,
		Protocol:           srv.Protocol,
		Port:               srv.Port,
		Status:             status,
		Interface:          srv.Interface,
		ConfigPath:         srv.ConfigPath,
		PublicIP:           srv.PublicIP,
		ServerIP:           srv.ServerIP,
		Subnet:             srv.Subnet,
		MTU:                srv.MTU,
		ObfuscationEnabled: srv.ObfuscationEnabled,
		ObfuscationParams:  srv.ObfuscationParams,
		ClientsCount:       len(clients),
		Clients:            clients,
		CreatedAt:          srv.CreatedAt,
		ConfigPreview:      preview,
		PublicKey:          srv.ServerPublicKey,
		DNS:                srv.DNS,
		DefaultISettings:   defaultISettings(),
	})
}

func (h *Handlers) getServerTraffic(c fiber.Ctx) error {
	id := c.Params("id")
	traffic := h.mgr.GetPeerTrafficForServer(id)
	if traffic == nil {
		return respond(c, fiber.StatusNotFound,
			ErrorResponse{Error: "server not found or no traffic data"})
	}
	return c.JSON(traffic)
}

func (h *Handlers) getAllServersTraffic(c fiber.Ctx) error {
	return c.JSON(h.mgr.GetAllServersTraffic())
}

// ── Clients ──────────────────────────────────────────────────────────────────

func (h *Handlers) getServerClients(c fiber.Ctx) error {
	id := c.Params("id")
	return c.JSON(h.mgr.GetClientConfigs(id))
}

func (h *Handlers) getAllClients(c fiber.Ctx) error {
	return c.JSON(h.mgr.GetClientConfigs(""))
}

func (h *Handlers) addClient(c fiber.Ctx) error {
	id := c.Params("id")
	var req AddClientRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}
	if req.Name == "" {
		req.Name = "New Client"
	}
	if req.AllowedIPs == "" {
		req.AllowedIPs = "0.0.0.0/0, ::/0"
	}

	client, configContent, err := h.mgr.AddClient(id, req.Name, req.ApplyISettings, req.ISettings, req.AllowedIPs)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(ClientResult{Client: client, Config: configContent})
}

func (h *Handlers) deleteClient(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	if err := h.mgr.DeleteClient(id, clientID); err != nil {
		return fail(c, err)
	}
	return c.JSON(ActionResult{Status: "deleted", ServerID: id, ClientID: clientID})
}

func (h *Handlers) updateClientAllowedIPs(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	var req UpdateAllowedIPsRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}
	if req.AllowedIPs == "" {
		req.AllowedIPs = "0.0.0.0/0, ::/0"
	}
	client, cfg, err := h.mgr.UpdateClientAllowedIPs(id, clientID, req.AllowedIPs)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(ClientResult{Client: client, Config: cfg})
}

func (h *Handlers) updateClientISettings(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	var req UpdateISettingsRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}
	client, cfg, err := h.mgr.UpdateClientISettings(id, clientID, req.ApplyISettings, req.ISettings)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(ClientResult{Client: client, Config: cfg})
}

func (h *Handlers) downloadClientConfig(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	gc, ok := h.mgr.getClientInServer(id, clientID)
	if !ok {
		return fail(c, clientNotFound(clientID))
	}

	configContent := h.mgr.GenerateClientConfig(id, &gc, true)
	// Both names are sanitised on the way in, but this one goes into a
	// quoted header value, so it is not left to that alone.
	filename := fmt.Sprintf("%s_%s.conf", sanitizeName(gc.Name, "client"), sanitizeName(gc.ServerName, "server"))
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.SendString(configContent)
}

func (h *Handlers) getClientAmneziaLink(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	gc, ok := h.mgr.getClientInServer(id, clientID)
	if !ok {
		return fail(c, clientNotFound(clientID))
	}

	vpnURL, err := h.mgr.GenerateAmneziaVpnURL(id, &gc)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(AmneziaLink{VPNURL: vpnURL})
}

func (h *Handlers) getClientConfigBoth(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	gc, ok := h.mgr.getClientInServer(id, clientID)
	if !ok {
		return fail(c, clientNotFound(clientID))
	}

	clean := h.mgr.GenerateClientConfig(id, &gc, false)
	full := h.mgr.GenerateClientConfig(id, &gc, true)

	suspendAtReadable := ""
	if gc.SuspendAt != nil {
		suspendAtReadable = time.Unix(int64(*gc.SuspendAt), 0).UTC().String()
	}

	return c.JSON(ClientConfigs{
		ServerID:          id,
		ClientID:          clientID,
		ClientName:        gc.Name,
		CleanConfig:       clean,
		FullConfig:        full,
		CleanLength:       len(clean),
		FullLength:        len(full),
		CreatedAt:         gc.CreatedAt,
		CreatedAtReadable: time.Unix(int64(gc.CreatedAt), 0).UTC().String(),
		SuspendAt:         gc.SuspendAt,
		SuspendAtReadable: suspendAtReadable,
	})
}

func (h *Handlers) suspendClient(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	msg, err := h.mgr.SuspendClient(id, clientID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(ActionResult{Status: "suspended", ServerID: id, ClientID: clientID, Message: msg})
}

func (h *Handlers) activateClient(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	msg, err := h.mgr.ActivateClient(id, clientID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(ActionResult{Status: "activated", ServerID: id, ClientID: clientID, Message: msg})
}

func (h *Handlers) updateClientSuspendTime(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	var req UpdateSuspendTimeRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}

	var ts *float64
	if req.SuspendAt != nil && *req.SuspendAt != "" {
		t, err := time.Parse(time.RFC3339, *req.SuspendAt)
		if err != nil {
			// Try without timezone
			t, err = time.Parse("2006-01-02T15:04:05", *req.SuspendAt)
			if err != nil {
				return respond(c, fiber.StatusBadRequest,
					ErrorResponse{Error: "invalid datetime format"})
			}
		}
		v := float64(t.Unix())
		ts = &v
	}

	client, msg, err := h.mgr.UpdateClientSuspendTime(id, clientID, ts)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(ClientResult{Client: client, Message: msg})
}

// ── Misc ─────────────────────────────────────────────────────────────────────

func (h *Handlers) getDefaultISettings(c fiber.Ctx) error {
	return c.JSON(defaultISettings())
}

// defaultISettings is the I1-I5 set a new client starts from. One definition,
// because it is served on its own and again inside every ServerInfo.
func defaultISettings() ISettings {
	return ISettings{
		"i1": DefaultI1, "i2": DefaultI2,
		"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
	}
}

func (h *Handlers) systemStatus(c fiber.Ctx) error {
	servers := h.mgr.copyServers()
	total := len(servers)
	totalClients := h.mgr.clientCount()

	active := 0
	for _, s := range servers {
		if h.mgr.GetServerStatus(s.ID) == "running" {
			active++
		}
	}

	_, awgErr := os.Stat("/usr/bin/awg")
	_, awgQuickErr := os.Stat("/usr/bin/awg-quick")

	return c.JSON(SystemStatus{
		AWGAvailable:  awgErr == nil && awgQuickErr == nil,
		PublicIP:      h.mgr.PublicIP(),
		TotalServers:  total,
		TotalClients:  totalClients,
		ActiveServers: active,
		Timestamp:     float64(time.Now().Unix()),
		Environment: SystemEnvironment{
			WebUIPort:        h.mgr.WebUIPort,
			AutoStartServers: h.mgr.AutoStart,
			DefaultMTU:       h.mgr.DefaultMTU,
			DefaultSubnet:    h.mgr.DefaultSubnet,
			DefaultPort:      h.mgr.DefaultPort,
			DefaultDNS:       strings.Join(h.mgr.DNSServers, ","),
		},
	})
}

func (h *Handlers) refreshIP(c fiber.Ctx) error {
	return c.JSON(PublicIP{Address: h.mgr.RefreshPublicIP()})
}

func (h *Handlers) iptablesTest(c fiber.Ctx) error {
	serverID := c.Query("server_id")
	if serverID == "" {
		return respond(c, fiber.StatusBadRequest,
			ErrorResponse{Error: "server_id parameter required"})
	}
	srv, ok := h.mgr.getServer(serverID)
	if !ok {
		return fail(c, serverNotFound(serverID))
	}

	checks := []string{
		fmt.Sprintf("iptables -L INPUT -n | grep %s", srv.Interface),
		fmt.Sprintf("iptables -L FORWARD -n | grep %s", srv.Interface),
		fmt.Sprintf("iptables -t nat -L POSTROUTING -n | grep %s", srv.Subnet),
	}
	results := map[string]string{}
	for _, cmd := range checks {
		out, err := execCommand(cmd)
		if err == nil && out != "" {
			results[cmd] = "Found"
		} else {
			results[cmd] = "Not found"
		}
	}

	return c.JSON(IptablesTest{
		ServerID:      serverID,
		ServerName:    srv.Name,
		Interface:     srv.Interface,
		Subnet:        srv.Subnet,
		IptablesCheck: results,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────
