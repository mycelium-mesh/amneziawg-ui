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
	qrcode "github.com/skip2/go-qrcode"

	"amneziawg-web-ui/web-ui/api"
)

// qrCapacity is the largest payload still worth rendering as a QR code; past
// that the symbol gets too dense for a phone camera, so the UI points at the
// download instead. It matches the limit the previous web UI used.
const qrCapacity = 2000

// scrolled wraps dialog content so a long form never grows past the viewport.
func (u *UI) scrolled(content fyne.CanvasObject) fyne.CanvasObject {
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(520, 320))
	return scroll
}

// dialogSize clamps a preferred size to the browser window.
func (u *UI) dialogSize(width, height float32) fyne.Size {
	canvasSize := u.win.Canvas().Size()
	if canvasSize.Width > 0 && width > canvasSize.Width-40 {
		width = canvasSize.Width - 40
	}
	if canvasSize.Height > 0 && height > canvasSize.Height-40 {
		height = canvasSize.Height - 40
	}
	return fyne.NewSize(width, height)
}

// copyToClipboard reports the outcome instead of assuming one: the browser is
// entitled to refuse a clipboard write, and the text stays selectable in the
// dialog for exactly that case.
func (u *UI) copyToClipboard(text string) {
	copyText(text, func(ok bool) {
		if ok {
			u.ok("Copied to clipboard")
			return
		}
		u.warn("Could not copy - select the text and press Ctrl+C")
	})
}

// monospaceView is a scrollable text box for configs, plus the setter that
// replaces its contents. Entry has no read-only mode that still allows
// selecting and copying, so edits are reverted - which means the widget needs
// to know which text is the legitimate one at any moment, and callers must go
// through the setter rather than SetText.
func monospaceView(text string) (*widget.Entry, func(string)) {
	view := widget.NewMultiLineEntry()
	view.TextStyle = fyne.TextStyle{Monospace: true}
	view.Wrapping = fyne.TextWrapBreak

	shown := text
	set := func(next string) {
		shown = next
		view.SetText(next)
	}

	view.OnChanged = func(typed string) {
		if typed != shown {
			view.SetText(shown)
		}
	}

	set(text)
	return view, set
}

// ── Server configuration ─────────────────────────────────────────────────────

func (u *UI) showServerConfig(serverID string) {
	go func() {
		info, err := u.backend.ServerInfo(serverID)
		if err != nil {
			u.fail(err)
			return
		}
		fyne.Do(func() { u.presentServerConfig(info) })
	}()
}

func (u *UI) presentServerConfig(info api.ServerInfo) {
	statusColor := colError
	if info.Status == "running" {
		statusColor = colSuccess
	}

	basics := infoGrid([][2]string{
		{"Interface", info.Interface},
		{"Port", fmt.Sprintf("%d", info.Port)},
		{"Subnet", info.Subnet},
		{"Server IP", info.ServerIP},
		{"Public IP", info.PublicIP},
		{"MTU", fmt.Sprintf("%d", info.MTU)},
		{"Protocol", info.Protocol},
		{"Clients", fmt.Sprintf("%d", info.ClientsCount)},
		{"DNS", strings.Join(info.DNS, ", ")},
		{"Public key", info.PublicKey},
	})

	head := container.NewHBox(
		sectionTitle(info.Name),
		container.NewCenter(badge(strings.ToUpper(info.Status), statusColor)),
	)

	body := container.NewVBox(head, basics)

	if info.ObfuscationEnabled && info.ObfuscationParams != nil {
		body.Add(separator())
		body.Add(sectionTitle("Obfuscation parameters (AmneziaWG 3.1)"))
		body.Add(obfuscationGrid(info.ObfuscationParams))
	}

	if len(info.DefaultISettings) > 0 {
		body.Add(separator())
		body.Add(sectionTitle("Default I-settings"))
		rows := make([][2]string, 0, len(info.DefaultISettings))
		for i := 1; i <= 5; i++ {
			key := fmt.Sprintf("i%d", i)
			value := info.DefaultISettings[key]
			if value == "" {
				value = "empty"
			} else {
				value = truncate(value, 60)
			}
			rows = append(rows, [2]string{strings.ToUpper(key), value})
		}
		body.Add(infoGrid(rows))
		body.Add(mutedNote("These defaults are used for new clients when \"Apply I-settings\" is enabled."))
	}

	body.Add(separator())
	body.Add(sectionTitle("Configuration preview"))
	preview, _ := monospaceView(info.ConfigPreview)
	preview.SetMinRowsVisible(10)
	body.Add(preview)

	full := button("View full config", theme.DocumentIcon(), func() {
		u.showRawServerConfig(info.ID)
	})
	download := button("Download config", theme.DownloadIcon(), func() {
		openURL(u.backend.ServerConfigURL(info.ID))
	})
	download.Importance = widget.HighImportance

	// The actions stay outside the scroll area so they are always reachable.
	content := container.NewBorder(nil, container.NewHBox(full, download), nil, nil, u.scrolled(body))

	view := dialog.NewCustom("Server configuration", "Close", content, u.win)
	view.Resize(u.dialogSize(880, 720))
	view.Show()
}

func (u *UI) showRawServerConfig(serverID string) {
	go func() {
		config, err := u.backend.ServerConfig(serverID)
		if err != nil {
			u.fail(err)
			return
		}

		fyne.Do(func() {
			view, _ := monospaceView(config.ConfigContent)
			view.SetMinRowsVisible(20)

			copyButton := button("Copy", theme.ContentCopyIcon(), func() {
				u.copyToClipboard(config.ConfigContent)
			})
			download := button("Download", theme.DownloadIcon(), func() {
				openURL(u.backend.ServerConfigURL(config.ServerID))
			})
			download.Importance = widget.HighImportance

			body := container.NewBorder(
				container.NewVBox(
					smallText(config.ConfigPath, colMuted),
					container.NewHBox(copyButton, download),
				), nil, nil, nil, view)

			raw := dialog.NewCustom("Raw configuration: "+config.ServerName, "Close", body, u.win)
			raw.Resize(u.dialogSize(900, 760))
			raw.Show()
		})
	}()
}

func infoGrid(rows [][2]string) fyne.CanvasObject {
	grid := container.NewGridWithColumns(2)
	for _, row := range rows {
		key := smallText(row[0], colMuted)
		value := smallText(row[1], colText)
		value.TextStyle = fyne.TextStyle{Monospace: true}
		grid.Add(container.NewHBox(key, value))
	}
	return grid
}

func obfuscationGrid(p *api.ObfuscationParams) fyne.CanvasObject {
	rows := [][2]string{
		{"Jc", fmt.Sprintf("%d", p.Jc)},
		{"Jmin", fmt.Sprintf("%d", p.Jmin)},
		{"Jmax", fmt.Sprintf("%d", p.Jmax)},
		{"S1", fmt.Sprintf("%d", p.S1)},
		{"S2", fmt.Sprintf("%d", p.S2)},
		{"S3", fmt.Sprintf("%d", p.S3)},
		{"S4", fmt.Sprintf("%d", p.S4)},
		{"H1", fmt.Sprintf("%d", p.H1)},
		{"H2", fmt.Sprintf("%d", p.H2)},
		{"H3", fmt.Sprintf("%d", p.H3)},
		{"H4", fmt.Sprintf("%d", p.H4)},
		{"RandomTrailers", onOff(p.RandomTrailers)},
		{"DisableCookies", onOff(p.DisableCookies)},
		{"HeaderProtectionKey", truncate(p.HeaderProtectionKey, 24)},
	}

	// The MTU carried inside the parameters is only set when it overrides the
	// interface MTU shown above, so an unset one is left out entirely.
	if p.MTU > 0 {
		rows = append(rows, [2]string{"MTU", fmt.Sprintf("%d", p.MTU)})
	}

	optional := [][2]string{
		{"ContentPaddingAddition", p.ContentPaddingAddition},
		{"RekeyAfterTime", p.RekeyAfterTime},
		{"RekeyTimeout", p.RekeyTimeout},
		{"RejectAfterTime", p.RejectAfterTime},
		{"KeepaliveTimeout", p.KeepaliveTimeout},
		{"MaxHandshakeAttempts", p.MaxHandshakeAttempts},
		{"PersistentKeepalive", p.PersistentKeepalive},
	}
	for _, row := range optional {
		if row[1] != "" {
			rows = append(rows, row)
		}
	}

	return infoGrid(rows)
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func mutedNote(text string) fyne.CanvasObject {
	note := widget.NewLabel(text)
	note.Wrapping = fyne.TextWrapWord
	note.TextStyle = fyne.TextStyle{Italic: true}
	return note
}

// ── Client configuration and QR code ─────────────────────────────────────────

// configView is one of the representations a client config has.
type configView struct {
	label string
	text  string
	note  string
	// qr marks the views worth rendering as a QR code. The AmneziaVPN link
	// is not one of them: it is far too long to scan comfortably, and the
	// app takes it from the clipboard anyway.
	qr bool
}

func (u *UI) showClientConfig(server api.Server, client api.Client) {
	go func() {
		configs, err := u.backend.ClientConfigs(server.ID, client.ID)
		if err != nil {
			u.fail(err)
			return
		}
		link := u.backend.AmneziaLink(server.ID, client.ID)

		fyne.Do(func() { u.presentClientConfig(server, client, configs, link) })
	}()
}

func (u *UI) presentClientConfig(server api.Server, client api.Client, configs api.ClientConfigs, link string) {
	views := []configView{}
	if link != "" {
		// Importing a plain .conf makes the official Amnezia app tag the
		// server as the legacy "amnezia-awg" container (AmneziaWG 2.0, no
		// header protection). The native link carries the container and
		// protocol_version fields that make it recognise AWG 3.x, so it is
		// the recommended - and default - view.
		views = append(views, configView{
			label: "AmneziaVPN link",
			text:  link,
			note:  "Copy this link into the AmneziaVPN app - it is recognised as AmneziaWG 3.x",
		})
	}
	views = append(views, configView{
		label: ".conf",
		text:  configs.CleanConfig,
		note:  "Scan with the AmneziaWG / AmneziaVPN app",
		qr:    true,
	})

	qrImage := canvas.NewImageFromResource(nil)
	qrImage.FillMode = canvas.ImageFillContain
	qrImage.SetMinSize(fyne.NewSize(280, 280))

	qrNote := smallText("", colMuted)
	warning := widget.NewLabel("")
	warning.Wrapping = fyne.TextWrapWord
	warning.Importance = widget.WarningImportance
	warning.Hide()

	text, setText := monospaceView("")
	text.SetMinRowsVisible(12)
	length := smallText("", colMuted)

	current := views[0]
	var qrPNG []byte

	copyButton := button("Copy", theme.ContentCopyIcon(), func() {
		u.copyToClipboard(current.text)
	})
	saveQR := button("Save QR image", theme.DownloadIcon(), func() {
		if qrPNG == nil {
			return
		}
		saveBytes(safeFileName(client.Name)+"_qr.png", "image/png", qrPNG)
	})

	// The QR column disappears for views that have no QR code, so the text
	// takes the full width instead of leaving a hole. The hint stays with
	// the text, which is the part that is always on screen.
	left := container.NewVBox(
		container.NewCenter(qrImage),
		container.NewCenter(saveQR),
	)

	render := func(view configView) {
		current = view
		setText(view.text)
		length.Text = fmt.Sprintf("%d characters", len(view.text))
		length.Refresh()

		qrPNG = nil
		qrNote.Text = view.note

		if !view.qr {
			left.Hide()
			warning.Hide()
			qrNote.Refresh()
			return
		}

		left.Show()
		qrImage.Show()
		saveQR.Show()
		png, err := encodeQR(view.text)
		switch {
		case err != nil:
			qrImage.Hide()
			saveQR.Hide()
			qrNote.Text = ""
			warning.SetText(err.Error())
			warning.Show()
		default:
			qrPNG = png
			qrImage.Resource = fyne.NewStaticResource("qr.png", png)
			qrImage.Show()
			qrImage.Refresh()
			warning.Hide()
		}
		qrNote.Refresh()
	}

	labels := make([]string, 0, len(views))
	byLabel := map[string]configView{}
	for _, view := range views {
		labels = append(labels, view.label)
		byLabel[view.label] = view
	}

	tabs := widget.NewRadioGroup(labels, func(selected string) {
		if view, ok := byLabel[selected]; ok {
			render(view)
		}
	})
	tabs.Horizontal = true
	tabs.SetSelected(views[0].label)

	download := button("Download .conf", theme.DownloadIcon(), func() {
		openURL(u.backend.ClientConfigURL(server.ID, client.ID))
	})
	download.Importance = widget.HighImportance

	created := "unknown"
	if configs.CreatedAt > 0 {
		created = configs.CreatedAtReadable
	}
	suspend := "not set"
	if configs.SuspendAt != nil {
		suspend = configs.SuspendAtReadable
	}

	right := container.NewBorder(
		container.NewVBox(tabs, qrNote, warning),
		container.NewVBox(
			container.NewBorder(nil, nil, length, copyButton),
		), nil, nil, text)

	meta := container.NewHBox(
		smallText("Created: "+created, colMuted),
		smallText("·", colBorder),
		smallText("Auto-suspend: "+suspend, colMuted),
	)

	body := container.NewBorder(meta, container.NewHBox(download), left, nil, right)

	view := dialog.NewCustom("Client configuration: "+client.Name, "Close", body, u.win)
	view.Resize(u.dialogSize(960, 720))
	view.Show()

	render(current)
}

// encodeQR renders the payload as a PNG, refusing anything too dense to scan.
func encodeQR(text string) ([]byte, error) {
	if text == "" {
		return nil, fmt.Errorf("this view is unavailable")
	}
	if len(text) > qrCapacity {
		return nil, fmt.Errorf("config is too large for a QR code: %d characters (max %d). "+
			"Use \"Download .conf\" instead", len(text), qrCapacity)
	}

	png, err := qrcode.Encode(text, qrcode.Medium, 640)
	if err != nil {
		return nil, fmt.Errorf("could not generate QR code: %w", err)
	}
	return png, nil
}

func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "client"
	}
	return b.String()
}
