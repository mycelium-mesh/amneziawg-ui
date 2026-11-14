// Package httpapi is the REST surface over the manager: routing, request
// decoding, and turning the manager's typed errors into status codes. It
// never touches the disk or the host itself.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v3"

	"amneziawg-web-ui/internal/manager"
	"amneziawg-web-ui/web-ui/api"
)

// Handlers holds dependencies for HTTP request handlers.
type Handlers struct {
	mgr *manager.Manager
}

// New creates a Handlers instance.
func New(mgr *manager.Manager) *Handlers {
	return &Handlers{mgr: mgr}
}

// FiberConfig is the configuration the app is built with. It lives here
// rather than in main.go so the handler tests run against the same one:
// Immutable is a correctness setting, not a tuning knob, and a test app
// without it would not exercise what production does.
func FiberConfig() fiber.Config {
	return fiber.Config{
		// c.Params() hands back a string pointing into the request buffer,
		// which fiber recycles as soon as the handler returns. Anything the
		// manager keeps past that - a client's ServerID is taken straight
		// from the route - would then read as whatever the next request put
		// in that buffer ("i-sett", a slice of some later .../i-settings
		// path). This app answers a handful of requests per minute, so the
		// allocation the immutable mode costs is worth removing the whole
		// class of bug rather than copying at each call site.
		Immutable: true,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
			}
			return c.Status(code).JSON(api.ErrorResponse{Error: err.Error()})
		},
	}
}

// RegisterRoutes attaches all API routes to the Fiber app.
func (h *Handlers) RegisterRoutes(app *fiber.App) {
	app.Get("/status", h.containerUptime)

	r := app.Group("/api")

	r.Get("/traffic", h.getTraffic)

	// Servers
	r.Get("/servers", h.getServers)
	r.Post("/servers", h.createServer)
	r.Put("/servers/:id/endpoint", h.updateServerEndpoint)
	r.Delete("/servers/:id", h.deleteServer)
	r.Post("/servers/:id/start", h.startServer)
	r.Post("/servers/:id/stop", h.stopServer)
	r.Get("/servers/:id/config", h.getServerConfig)
	r.Get("/servers/:id/config/download", h.downloadServerConfig)
	r.Get("/servers/:id/info", h.getServerInfo)

	// Clients
	r.Get("/servers/:id/clients", h.getServerClients)
	r.Post("/servers/:id/clients", h.addClient)
	r.Get("/servers/:id/clients/configs", h.downloadClientConfigs)
	r.Delete("/servers/:id/clients/:clientId", h.deleteClient)
	r.Put("/servers/:id/clients/:clientId/allowed-ips", h.updateClientAllowedIPs)
	r.Put("/servers/:id/clients/:clientId/i-settings", h.updateClientISettings)
	r.Get("/servers/:id/clients/:clientId/config", h.downloadClientConfig)
	r.Get("/servers/:id/clients/:clientId/config-both", h.getClientConfigBoth)
	r.Get("/servers/:id/clients/:clientId/link", h.getClientAmneziaLink)
	r.Post("/servers/:id/clients/:clientId/suspend", h.suspendClient)
	r.Post("/servers/:id/clients/:clientId/activate", h.activateClient)
	r.Put("/servers/:id/clients/:clientId/suspend-time", h.updateClientSuspendTime)

	// Misc
	r.Get("/clients", h.getAllClients)
	r.Get("/default-i-settings", h.getDefaultISettings)
	r.Get("/system/status", h.systemStatus)
	r.Get("/system/refresh-ip", h.refreshIP)
	r.Get("/system/iptables-test", h.iptablesTest)
}

// fail turns a manager error into a response. The status code comes from the
// kind of error, so "no such server" and "the interface refused to come up"
// stop sharing one code the way they did when the manager answered in
// booleans.
func fail(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, manager.ErrNotFound):
		return respond(c, fiber.StatusNotFound, api.ErrorResponse{Error: err.Error()})
	case errors.Is(err, manager.ErrInvalid):
		return respond(c, fiber.StatusBadRequest, api.ErrorResponse{Error: err.Error()})
	case errors.Is(err, manager.ErrConflict):
		return respond(c, fiber.StatusConflict, api.ErrorResponse{Error: err.Error()})
	default:
		return respond(c, fiber.StatusInternalServerError, api.ErrorResponse{Error: err.Error()})
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
		return fmt.Errorf("%w: %s", manager.ErrInvalid, err)
	}
	return nil
}
