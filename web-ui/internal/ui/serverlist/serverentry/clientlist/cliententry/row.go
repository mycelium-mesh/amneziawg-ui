// Package cliententry is one peer line inside a server card: the name, the
// address, the state badges, the live counters and the row of actions.
package cliententry

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"amneziawg-web-ui/web-ui/api"
	"amneziawg-web-ui/web-ui/internal/ui/browser"
	"amneziawg-web-ui/web-ui/internal/ui/dialogs"
	"amneziawg-web-ui/web-ui/internal/ui/env"
	"amneziawg-web-ui/web-ui/internal/ui/serverlist/serverentry/clientlist/cliententry/clientdialogs"
	"amneziawg-web-ui/web-ui/internal/ui/style"
	"amneziawg-web-ui/web-ui/internal/ui/widgets"
)

// Row is one peer line. It keeps the labels the traffic feed updates in
// place, so a counter tick never rebuilds the row.
type Row struct {
	env    *env.Env
	server api.Server
	client api.Client

	online    *canvas.Circle
	rx        *canvas.Text
	tx        *canvas.Text
	handshake *canvas.Text
	endpoint  *canvas.Text

	object fyne.CanvasObject
}

// New renders the row for client, a peer of server.
func New(e *env.Env, server api.Server, client api.Client) *Row {
	r := &Row{
		env:       e,
		server:    server,
		client:    client,
		online:    canvas.NewCircle(style.Muted),
		rx:        widgets.SmallText("—", style.Muted),
		tx:        widgets.SmallText("—", style.Muted),
		handshake: widgets.SmallText(handshakeText("—"), style.Muted),
		endpoint:  widgets.SmallText(endpointText("—"), style.Muted),
	}

	name := canvas.NewText(client.Name, style.Text)
	name.TextSize = 14
	name.TextStyle = fyne.TextStyle{Bold: true}

	address := widgets.SmallText(client.ClientIP, style.Primary)
	address.TextStyle = fyne.TextStyle{Monospace: true}

	labels := container.NewHBox(name, container.NewCenter(address))
	if client.ApplyISettings {
		labels.Add(container.NewCenter(widgets.Badge("I1-5", style.Accent)))
	}
	if client.Status == "suspended" {
		labels.Add(container.NewCenter(widgets.Badge(lang.L("SUSPENDED"), style.Warning)))
	} else {
		labels.Add(container.NewCenter(widgets.Badge(lang.L("ACTIVE"), style.Success)))
	}
	if client.SuspendAt != nil {
		when := time.Unix(int64(*client.SuspendAt), 0).Local().Format(clientdialogs.SuspendLayout)
		labels.Add(container.NewCenter(widgets.Badge(lang.L("auto-suspend {{.When}}", map[string]any{"When": when}), style.Error)))
	}

	// The presence light is the header's status dot: green while the peer
	// holds a live session, muted otherwise. The counters carry the same
	// glyphs as the dashboard tiles: download for received, upload for sent.
	counters := container.NewHBox(
		container.NewCenter(container.NewGridWrap(fyne.NewSize(10, 10), r.online)),
		container.NewCenter(widgets.SmallIcon(theme.DownloadIcon())), r.rx,
		container.NewCenter(widgets.SmallIcon(theme.UploadIcon())), r.tx,
		widgets.SmallText("·", style.Border),
		r.handshake, widgets.SmallText("·", style.Border),
		r.endpoint,
	)

	edit := widgets.NewButton(lang.L("Edit"), theme.DocumentCreateIcon(), func() {
		clientdialogs.ShowEditor(e, server, &client)
	})
	qr := widgets.NewButton(lang.L("QR / config"), theme.VisibilityIcon(), func() {
		clientdialogs.ShowConfig(e, server, client)
	})
	download := widgets.NewButton("", theme.DownloadIcon(), func() {
		browser.OpenURL(e.Backend.ClientConfigURL(server.ID, client.ID))
	})

	var toggle *widgets.Button
	if client.Status == "suspended" {
		toggle = widgets.NewButton(lang.L("Activate"), theme.MediaPlayIcon(), func() { r.setSuspended(false) })
		toggle.Importance = widget.SuccessImportance
	} else {
		toggle = widgets.NewButton(lang.L("Suspend"), theme.MediaPauseIcon(), func() { r.setSuspended(true) })
		toggle.Importance = widget.WarningImportance
	}

	remove := widgets.NewButton("", theme.DeleteIcon(), r.confirmDelete)
	remove.Importance = widget.DangerImportance

	actions := container.NewHBox(edit, qr, download, toggle, remove)

	bg := canvas.NewRectangle(style.SurfaceAlt)
	bg.CornerRadius = 8

	body := container.NewBorder(nil, nil, nil, container.NewCenter(actions),
		container.NewVBox(labels, counters))

	r.object = container.NewStack(bg, container.NewPadded(body))
	return r
}

// CanvasObject is the row as the list places it.
func (r *Row) CanvasObject() fyne.CanvasObject {
	return r.object
}

// Apply pushes one traffic snapshot into the labels. Must run on the UI
// goroutine.
func (r *Row) Apply(data api.ClientTraffic) {
	r.online.FillColor = style.Muted
	if data.Online {
		r.online.FillColor = style.Success
	}
	r.online.Refresh()

	r.rx.Text = data.Received
	r.rx.Refresh()
	r.tx.Text = data.Sent
	r.tx.Refresh()

	r.handshake.Text = handshakeText(data.LastHandshake)
	r.handshake.Refresh()

	endpoint := data.Endpoint
	if endpoint == "" {
		endpoint = "—"
	}
	r.endpoint.Text = endpointText(endpoint)
	r.endpoint.Refresh()
}

func handshakeText(when string) string {
	return lang.L("handshake: {{.When}}", map[string]any{"When": when})
}

func endpointText(endpoint string) string {
	return lang.L("endpoint: {{.Endpoint}}", map[string]any{"Endpoint": endpoint})
}

// ── Actions ──────────────────────────────────────────────────────────────────

func (r *Row) setSuspended(suspend bool) {
	e, client := r.env, r.client

	name := map[string]any{"Name": client.Name}
	question := lang.L("Activate \"{{.Name}}\" again?", name)
	if suspend {
		question = lang.L("Suspend \"{{.Name}}\"? The client loses its connection until reactivated.", name)
	}

	dialogs.Confirm(e.Win, lang.L("Change client state"), question, func() {
		go func() {
			var err error
			if suspend {
				err = e.Backend.SuspendClient(r.server.ID, client.ID)
			} else {
				err = e.Backend.ActivateClient(r.server.ID, client.ID)
			}
			if err != nil {
				e.Notify.Fail(err)
				return
			}
			if suspend {
				e.Notify.OK(lang.L("Client \"{{.Name}}\" suspended", name))
			} else {
				e.Notify.OK(lang.L("Client \"{{.Name}}\" activated", name))
			}
			e.Reload()
		}()
	})
}

func (r *Row) confirmDelete() {
	e, client := r.env, r.client

	name := map[string]any{"Name": client.Name}
	dialogs.Confirm(e.Win, lang.L("Delete client"), lang.L("Delete \"{{.Name}}\"?", name), func() {
		go func() {
			if err := e.Backend.DeleteClient(r.server.ID, client.ID); err != nil {
				e.Notify.Fail(err)
				return
			}
			e.Notify.OK(lang.L("Client \"{{.Name}}\" deleted", name))
			e.Reload()
		}()
	})
}
