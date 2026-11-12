package httpapi

import (
	"fmt"

	"github.com/gofiber/fiber/v3"

	"amneziawg-web-ui/web-ui/api"
)

func (h *Handlers) getServers(c fiber.Ctx) error {
	return c.JSON(h.mgr.Servers())
}

func (h *Handlers) createServer(c fiber.Ctx) error {
	var req api.CreateServerRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}
	srv, err := h.mgr.CreateServer(req)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(srv)
}

// updateServerEndpoint sets the endpoint a client config carries; an empty
// value clears it back to the auto-detected public IP.
func (h *Handlers) updateServerEndpoint(c fiber.Ctx) error {
	id := c.Params("id")
	var req api.UpdateEndpointRequest
	if err := decode(c, &req); err != nil {
		return fail(c, err)
	}
	srv, err := h.mgr.UpdateServerEndpoint(id, req.Endpoint)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(srv)
}

func (h *Handlers) deleteServer(c fiber.Ctx) error {
	id := c.Params("id")
	if err := h.mgr.DeleteServer(id); err != nil {
		return fail(c, err)
	}
	return c.JSON(api.ActionResult{Status: "deleted", ServerID: id})
}

func (h *Handlers) startServer(c fiber.Ctx) error {
	id := c.Params("id")
	if err := h.mgr.StartServer(id); err != nil {
		return fail(c, err)
	}
	return c.JSON(api.ActionResult{Status: "started", ServerID: id})
}

func (h *Handlers) stopServer(c fiber.Ctx) error {
	id := c.Params("id")
	if err := h.mgr.StopServer(id); err != nil {
		return fail(c, err)
	}
	return c.JSON(api.ActionResult{Status: "stopped", ServerID: id})
}

func (h *Handlers) getServerConfig(c fiber.Ctx) error {
	cfg, err := h.mgr.ServerConfig(c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(cfg)
}

func (h *Handlers) downloadServerConfig(c fiber.Ctx) error {
	cfg, err := h.mgr.ServerConfig(c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"`, cfg.Interface))
	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.SendString(cfg.ConfigContent)
}

func (h *Handlers) getServerInfo(c fiber.Ctx) error {
	info, err := h.mgr.ServerInfo(c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(info)
}

// getTraffic is what the page polls: every counter it shows in one
// response, so a refresh costs one request rather than one per server.
func (h *Handlers) getTraffic(c fiber.Ctx) error {
	return c.JSON(h.mgr.TrafficSnapshot())
}
