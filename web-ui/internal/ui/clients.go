package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"amneziawg-web-ui/web-ui/api"
)

const suspendLayout = "2006-01-02 15:04"

// buildClientRow renders one peer line and registers its mutable labels so
// the traffic feed can update them in place.
func (c *serverCard) buildClientRow(client api.Client) fyne.CanvasObject {
	row := &clientRow{
		traffic:   smallText("RX — · TX —", colMuted),
		handshake: smallText("handshake: —", colMuted),
		endpoint:  smallText("endpoint: —", colMuted),
	}
	c.rows[client.ID] = row

	name := canvas.NewText(client.Name, colText)
	name.TextSize = 14
	name.TextStyle = fyne.TextStyle{Bold: true}

	address := smallText(client.ClientIP, colPrimary)
	address.TextStyle = fyne.TextStyle{Monospace: true}

	labels := container.NewHBox(name, container.NewCenter(address))
	if client.ApplyISettings {
		labels.Add(container.NewCenter(badge("I1-5", colAccent)))
	}
	if client.Status == "suspended" {
		labels.Add(container.NewCenter(badge("SUSPENDED", colWarning)))
	} else {
		labels.Add(container.NewCenter(badge("ACTIVE", colSuccess)))
	}
	if client.SuspendAt != nil {
		when := time.Unix(int64(*client.SuspendAt), 0).Local().Format(suspendLayout)
		labels.Add(container.NewCenter(badge("auto-suspend "+when, colError)))
	}

	counters := container.NewHBox(
		row.traffic, smallText("·", colBorder),
		row.handshake, smallText("·", colBorder),
		row.endpoint,
	)

	edit := button("Edit", theme.DocumentCreateIcon(), func() {
		c.ui.showClientDialog(c.server, &client)
	})
	qr := button("QR / config", theme.VisibilityIcon(), func() {
		c.ui.showClientConfig(c.server, client)
	})
	download := button("", theme.DownloadIcon(), func() {
		openURL(c.ui.backend.ClientConfigURL(c.server.ID, client.ID))
	})

	var toggle *pointerButton
	if client.Status == "suspended" {
		toggle = button("Activate", theme.MediaPlayIcon(), func() {
			c.ui.setClientSuspended(c.server.ID, client, false)
		})
		toggle.Importance = widget.SuccessImportance
	} else {
		toggle = button("Suspend", theme.MediaPauseIcon(), func() {
			c.ui.setClientSuspended(c.server.ID, client, true)
		})
		toggle.Importance = widget.WarningImportance
	}

	remove := button("", theme.DeleteIcon(), func() {
		c.ui.confirmDeleteClient(c.server.ID, client)
	})
	remove.Importance = widget.DangerImportance

	actions := container.NewHBox(edit, qr, download, toggle, remove)

	bg := canvas.NewRectangle(colSurfaceAlt)
	bg.CornerRadius = 8

	body := container.NewBorder(nil, nil, nil, container.NewCenter(actions),
		container.NewVBox(labels, counters))

	return container.NewStack(bg, container.NewPadded(body))
}

// ── Client actions ───────────────────────────────────────────────────────────

func (u *UI) setClientSuspended(serverID string, client api.Client, suspend bool) {
	question := fmt.Sprintf("Activate %q again?", client.Name)
	if suspend {
		question = fmt.Sprintf("Suspend %q? The client loses its connection until reactivated.", client.Name)
	}

	dialog.ShowConfirm("Change client state", question, func(confirmed bool) {
		if !confirmed {
			return
		}
		go func() {
			var err error
			if suspend {
				err = u.backend.SuspendClient(serverID, client.ID)
			} else {
				err = u.backend.ActivateClient(serverID, client.ID)
			}
			if err != nil {
				u.fail(err)
				return
			}
			if suspend {
				u.ok("Client %q suspended", client.Name)
			} else {
				u.ok("Client %q activated", client.Name)
			}
			u.reloadServers()
		}()
	}, u.win)
}

func (u *UI) confirmDeleteClient(serverID string, client api.Client) {
	dialog.ShowConfirm("Delete client", fmt.Sprintf("Delete %q?", client.Name), func(confirmed bool) {
		if !confirmed {
			return
		}
		go func() {
			if err := u.backend.DeleteClient(serverID, client.ID); err != nil {
				u.fail(err)
				return
			}
			u.ok("Client %q deleted", client.Name)
			u.reloadServers()
		}()
	}, u.win)
}

// showClientDialog opens the add/edit form. A nil client means "add new".
func (u *UI) showClientDialog(server api.Server, client *api.Client) {
	editing := client != nil

	name := widget.NewEntry()
	allowedIPs := entryWithText("0.0.0.0/0, ::/0")
	suspendAt := entryWithPlaceholder(suspendLayout)

	applyI := widget.NewCheck("Apply I-settings (custom signature packets I1-I5)", nil)
	iEntries := make([]*widget.Entry, 5)
	for i := range iEntries {
		iEntries[i] = widget.NewMultiLineEntry()
		iEntries[i].Wrapping = fyne.TextWrapBreak
		iEntries[i].SetMinRowsVisible(2)
	}

	u.mu.Lock()
	defaults := u.defaultI
	u.mu.Unlock()
	for i, entry := range iEntries {
		key := fmt.Sprintf("i%d", i+1)
		if value := defaults[key]; value != "" {
			entry.SetPlaceHolder("server default: " + truncate(value, 40))
		} else {
			entry.SetPlaceHolder("leave empty to skip")
		}
	}

	if editing {
		name.SetText(client.Name)
		name.Disable()
		if client.AllowedIPs != "" {
			allowedIPs.SetText(client.AllowedIPs)
		}
		applyI.SetChecked(client.ApplyISettings)
		for i, entry := range iEntries {
			entry.SetText(client.ISettings[fmt.Sprintf("i%d", i+1)])
		}
		if client.SuspendAt != nil {
			suspendAt.SetText(time.Unix(int64(*client.SuspendAt), 0).Local().Format(suspendLayout))
		}
	} else {
		name.SetPlaceHolder("New Client")
	}

	iBox := container.NewVBox()
	for i, entry := range iEntries {
		iBox.Add(labeled(fmt.Sprintf("I%d", i+1), entry))
	}
	iNote := widget.NewLabel("I-settings are client-only parameters; empty values are omitted from the generated config. " +
		"If I1 is empty, all I-settings are ignored. A config that grows past the QR code limit can still be downloaded as a file.")
	iNote.Wrapping = fyne.TextWrapWord
	iNote.TextStyle = fyne.TextStyle{Italic: true}
	iBox.Add(iNote)
	if !applyI.Checked {
		iBox.Hide()
	}

	applyI.OnChanged = func(on bool) {
		if on {
			iBox.Show()
		} else {
			iBox.Hide()
		}
	}

	items := []*widget.FormItem{
		{Text: "Client name", Widget: name},
		{Text: "Allowed IPs", Widget: allowedIPs,
			HintText: "comma-separated ranges routed through the VPN; default 0.0.0.0/0, ::/0"},
	}

	if editing {
		clear := button("", theme.CancelIcon(), func() { suspendAt.SetText("") })
		items = append(items, &widget.FormItem{
			Text:     "Auto-suspend at",
			Widget:   container.NewBorder(nil, nil, nil, clear, suspendAt),
			HintText: "local time, format " + suspendLayout + "; empty disables auto-suspension",
		})
		created := time.Unix(int64(client.CreatedAt), 0).Local().Format("2006-01-02 15:04:05")
		items = append(items, &widget.FormItem{Text: "Created", Widget: widget.NewLabel(created)})
	}

	content := container.NewVBox(widget.NewForm(items...), separator(), applyI, iBox)

	title := "Add client to " + server.Name
	confirm := "Add client"
	if editing {
		title = "Edit client " + client.Name
		confirm = "Save"
	}

	form := dialog.NewCustomConfirm(title, confirm, "Cancel", u.scrolled(content), func(save bool) {
		if !save {
			return
		}

		settings := api.ISettings{}
		for i, entry := range iEntries {
			if value := strings.TrimSpace(entry.Text); value != "" {
				settings[fmt.Sprintf("i%d", i+1)] = value
			}
		}

		routes := strings.TrimSpace(allowedIPs.Text)
		if routes == "" {
			routes = "0.0.0.0/0, ::/0"
		}

		if editing {
			u.saveClient(server.ID, *client, routes, applyI.Checked, settings, strings.TrimSpace(suspendAt.Text))
			return
		}
		u.addClient(server, strings.TrimSpace(name.Text), routes, applyI.Checked, settings)
	}, u.win)

	form.Resize(u.dialogSize(760, 620))
	form.Show()
}

func (u *UI) addClient(server api.Server, name, allowedIPs string, applyI bool, settings api.ISettings) {
	if name == "" {
		name = "New Client"
	}

	go func() {
		err := u.backend.AddClient(server.ID, api.AddClientRequest{
			Name:           name,
			ApplyISettings: applyI,
			ISettings:      settings,
			AllowedIPs:     allowedIPs,
		})
		if err != nil {
			u.fail(err)
			return
		}
		u.ok("Client %q added", name)
		u.reloadServers()
	}()
}

// saveClient applies the three independent updates the backend exposes for an
// existing peer: routing, I-settings and the auto-suspend timestamp.
func (u *UI) saveClient(serverID string, client api.Client, allowedIPs string, applyI bool, settings api.ISettings, suspendAt string) {
	var when *time.Time
	if suspendAt != "" {
		parsed, err := time.ParseInLocation(suspendLayout, suspendAt, time.Local)
		if err != nil {
			u.fail(fmt.Errorf("auto-suspend time must look like %s", suspendLayout))
			return
		}
		when = &parsed
	}

	go func() {
		if err := u.backend.UpdateAllowedIPs(serverID, client.ID, allowedIPs); err != nil {
			u.fail(err)
			return
		}
		if err := u.backend.UpdateISettings(serverID, client.ID, applyI, settings); err != nil {
			u.fail(err)
			return
		}
		if err := u.backend.UpdateSuspendTime(serverID, client.ID, when); err != nil {
			u.fail(err)
			return
		}
		u.ok("Client %q updated", client.Name)
		u.reloadServers()
	}()
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
