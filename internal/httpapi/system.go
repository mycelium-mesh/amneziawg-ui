package httpapi

import (
	"fmt"
	"os"
	"time"

	"github.com/gofiber/fiber/v3"

	"amneziawg-web-ui/web-ui/api"
)

// containerUptime is the open health check. PID 1's /proc entry was created
// when the container started, so its modification time is the boot time.
func (h *Handlers) containerUptime(c fiber.Ctx) error {
	info, err := os.Stat("/proc/1/cmdline")
	if err != nil {
		return c.SendString("Container Uptime: unknown")
	}
	uptime := int64(time.Since(info.ModTime()).Seconds())
	d := uptime / 86400
	h2 := (uptime % 86400) / 3600
	m2 := (uptime % 3600) / 60
	s2 := uptime % 60
	return c.SendString(fmt.Sprintf("Container Uptime: %dd %dh %dm %ds", d, h2, m2, s2))
}

// getDefaultISettings answers with the effective standard set: the shipped
// defaults under the operator's saved values, from the manager's one
// definition of it.
func (h *Handlers) getDefaultISettings(c fiber.Ctx) error {
	return c.JSON(h.mgr.DefaultISettings())
}

// putDefaultISettings replaces the operator's saved standard I1-I5 set.
// The body is the set itself; an empty or absent one clears the override,
// and a value the shared rules reject comes back as 400 through fail, like
// every other manager validation error. The answer is the effective set as
// it now stands - the same shape GET hands out.
func (h *Handlers) putDefaultISettings(c fiber.Ctx) error {
	var settings api.ISettings
	if err := decode(c, &settings); err != nil {
		return fail(c, err)
	}
	effective, err := h.mgr.UpdateDefaultISettings(settings)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(effective)
}

func (h *Handlers) systemStatus(c fiber.Ctx) error {
	return c.JSON(h.mgr.SystemStatus())
}

func (h *Handlers) refreshIP(c fiber.Ctx) error {
	return c.JSON(api.PublicIP{Address: h.mgr.RefreshPublicIP()})
}

func (h *Handlers) iptablesTest(c fiber.Ctx) error {
	serverID := c.Query("server_id")
	if serverID == "" {
		return respond(c, fiber.StatusBadRequest,
			api.ErrorResponse{Error: "server_id parameter required"})
	}
	result, err := h.mgr.IPTablesCheck(serverID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(result)
}
