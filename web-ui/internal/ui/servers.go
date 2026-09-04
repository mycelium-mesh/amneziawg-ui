package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"amneziawg-web-ui/web-ui/api"
)

// serverCard is one panel in the server list. It keeps references to the
// labels that the traffic poller updates in place.
type serverCard struct {
	ui     *UI
	server api.Server

	ifaceText *canvas.Text
	rows      map[string]*clientRow

	object fyne.CanvasObject
}

// clientRow holds the mutable labels of a single peer line.
type clientRow struct {
	traffic   *canvas.Text
	handshake *canvas.Text
	endpoint  *canvas.Text
}

// renderServers rebuilds the whole list. Must run on the UI goroutine.
func (u *UI) renderServers(servers []api.Server) {
	u.cards = map[string]*serverCard{}
	u.serversBox.Objects = nil

	for _, srv := range servers {
		card := u.newServerCard(srv)
		u.cards[srv.ID] = card
		u.serversBox.Add(card.object)
	}

	if len(servers) > 0 {
		u.emptyHint.Hide()
	} else {
		u.emptyHint.Show()
	}
	u.serversBox.Refresh()
	u.clampScroll()
}

func (u *UI) newServerCard(srv api.Server) *serverCard {
	c := &serverCard{ui: u, server: srv, rows: map[string]*clientRow{}}

	title := canvas.NewText(srv.Name, colText)
	title.TextSize = 17
	title.TextStyle = fyne.TextStyle{Bold: true}

	running := srv.Status == "running"
	statusColor := colError
	if running {
		statusColor = colSuccess
	}

	meta := []string{
		"ID " + srv.ID,
		fmt.Sprintf("Port %d", srv.Port),
		"Subnet " + srv.Subnet,
		fmt.Sprintf("MTU %d", srv.MTU),
	}
	if srv.ObfuscationEnabled {
		meta = append(meta, "Obfuscated (AWG 3.1)")
	}

	c.ifaceText = smallText("Interface: RX — · TX —", colMuted)

	remove := button("", theme.DeleteIcon(), c.confirmDelete)
	remove.Importance = widget.DangerImportance

	head := container.NewBorder(nil, nil,
		container.NewVBox(
			title,
			smallText(strings.Join(meta, "  ·  "), colMuted),
			c.ifaceText,
		),
		container.NewHBox(
			container.NewCenter(badge(strings.ToUpper(srv.Status), statusColor)),
			container.NewCenter(remove),
		),
	)

	start := button("Start", theme.MediaPlayIcon(), func() { c.setRunning(true) })
	start.Importance = widget.SuccessImportance
	stop := button("Stop", theme.MediaStopIcon(), func() { c.setRunning(false) })
	stop.Importance = widget.DangerImportance
	add := button("Add client", theme.ContentAddIcon(), func() {
		u.showClientDialog(srv, nil)
	})
	add.Importance = widget.HighImportance
	config := button("Show config", theme.DocumentIcon(), func() {
		u.showServerConfig(srv.ID)
	})

	if running {
		start.Disable()
	} else {
		stop.Disable()
	}

	actions := container.NewHBox(start, stop, add, config)

	content := container.NewVBox(
		head,
		actions,
		separator(),
		c.buildClients(srv.Clients),
	)

	c.object = container.NewPadded(card(content))
	return c
}

func (c *serverCard) buildClients(clients []api.Client) fyne.CanvasObject {
	if len(clients) == 0 {
		return smallText("No clients yet.", colMuted)
	}

	heading := canvas.NewText(fmt.Sprintf("Clients (%d)", len(clients)), colText)
	heading.TextSize = 13
	heading.TextStyle = fyne.TextStyle{Bold: true}

	list := container.NewVBox(heading)
	for _, client := range clients {
		list.Add(c.buildClientRow(client))
	}
	return list
}

func (c *serverCard) applyInterfaceTraffic(traffic api.InterfaceTraffic) {
	rx, tx := "—", "—"
	if traffic != nil {
		rx, tx = traffic["rx"], traffic["tx"]
	}
	c.ifaceText.Text = fmt.Sprintf("Interface: RX %s · TX %s", rx, tx)
	c.ifaceText.Refresh()
}

func (c *serverCard) applyPeerTraffic(traffic map[string]api.ClientTraffic) {
	for id, row := range c.rows {
		data, ok := traffic[id]
		if !ok {
			data = api.ClientTraffic{Received: "0 B", Sent: "0 B", LastHandshake: "Never"}
		}

		row.traffic.Text = fmt.Sprintf("RX %s · TX %s", data.Received, data.Sent)
		row.traffic.Refresh()

		row.handshake.Text = "handshake: " + data.LastHandshake
		row.handshake.Refresh()

		endpoint := data.Endpoint
		if endpoint == "" {
			endpoint = "endpoint: —"
		} else {
			endpoint = "endpoint: " + endpoint
		}
		row.endpoint.Text = endpoint
		row.endpoint.Refresh()
	}
}

// ── Server actions ───────────────────────────────────────────────────────────

func (c *serverCard) setRunning(start bool) {
	go func() {
		var err error
		if start {
			err = c.ui.backend.StartServer(c.server.ID)
		} else {
			err = c.ui.backend.StopServer(c.server.ID)
		}
		if err != nil {
			c.ui.fail(err)
			return
		}
		if start {
			c.ui.ok("Server %q started", c.server.Name)
		} else {
			c.ui.ok("Server %q stopped", c.server.Name)
		}
		c.ui.reloadServers()
	}()
}

func (c *serverCard) confirmDelete() {
	dialog.ShowConfirm("Delete server",
		fmt.Sprintf("Delete %q and all of its clients?", c.server.Name),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			go func() {
				if err := c.ui.backend.DeleteServer(c.server.ID); err != nil {
					c.ui.fail(err)
					return
				}
				c.ui.ok("Server %q deleted", c.server.Name)
				c.ui.reloadServers()
			}()
		}, c.ui.win)
}
