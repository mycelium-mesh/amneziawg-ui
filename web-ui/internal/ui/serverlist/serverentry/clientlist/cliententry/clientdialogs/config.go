package clientdialogs

import (
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	qrcode "github.com/skip2/go-qrcode"

	"amneziawg-web-ui/web-ui/api"
	"amneziawg-web-ui/web-ui/internal/ui/browser"
	"amneziawg-web-ui/web-ui/internal/ui/dialogs"
	"amneziawg-web-ui/web-ui/internal/ui/env"
	"amneziawg-web-ui/web-ui/internal/ui/style"
	"amneziawg-web-ui/web-ui/internal/ui/widgets"
)

// qrCapacity is the largest payload still worth rendering as a QR code; past
// that the symbol gets too dense for a phone camera, so the UI points at the
// download instead. It matches the limit the previous web UI used.
const qrCapacity = 2000

// amneziaAppMTU is what the AmneziaVPN app writes over the MTU of every
// vpn:// link it imports (ImportController::processAmneziaConfig), whatever
// the link carries. A server on any other MTU needs the user to set it back
// by hand in the app's connection settings.
const amneziaAppMTU = 1376

// configView is one of the representations a client config has.
type configView struct {
	label string
	icon  fyne.Resource
	text  string
	note  string
	// warning is shown above the text in the warning colour, for what the
	// user has to do by hand after importing.
	warning string
	// qr marks the views worth rendering as a QR code. The AmneziaVPN link
	// is not one of them: it is far too long to scan comfortably, and the
	// app takes it from the clipboard anyway.
	qr bool
}

// ShowConfig fetches the client's configs and opens the viewer.
func ShowConfig(e *env.Env, server api.Server, client api.Client) {
	go func() {
		configs, err := e.Backend.ClientConfigs(server.ID, client.ID)
		if err != nil {
			e.Notify.Fail(err)
			return
		}
		link := e.Backend.AmneziaLink(server.ID, client.ID)

		fyne.Do(func() { presentConfig(e, server, client, configs, link) })
	}()
}

func presentConfig(e *env.Env, server api.Server, client api.Client, configs api.ClientConfigs, link string) {
	views := []configView{{
		label: ".conf",
		icon:  theme.DocumentIcon(),
		text:  configs.CleanConfig,
		note:  lang.L("Scan with the AmneziaWG / AmneziaVPN app"),
		qr:    true,
	}}
	if link != "" {
		// Importing a plain .conf makes the official Amnezia app tag the
		// server as the legacy "amnezia-awg" container (AmneziaWG 2.0, no
		// header protection). The native link carries the container and
		// protocol_version fields that make it recognise AWG 3.x, so it is
		// the view to point Amnezia users at.
		view := configView{
			label: lang.L("AmneziaVPN link"),
			icon:  theme.MailForwardIcon(),
			text:  link,
			note:  lang.L("Copy this link into the AmneziaVPN app - it is recognised as AmneziaWG 3.x"),
		}
		if server.MTU != amneziaAppMTU {
			view.warning = lang.L("The AmneziaVPN app replaces the MTU with {{.AppMTU}} on import. "+
				"After importing, set MTU to {{.MTU}} in the app's AmneziaWG connection settings, "+
				"or import the .conf instead - it keeps the MTU", map[string]any{"AppMTU": amneziaAppMTU, "MTU": server.MTU})
		}
		views = append(views, view)
	}

	tabs := container.NewAppTabs()
	for _, view := range views {
		tabs.Append(container.NewTabItemWithIcon(view.label, view.icon, viewPage(e, client, view)))
	}

	download := widgets.NewButton(lang.L("Download .conf"), theme.DownloadIcon(), func() {
		browser.OpenURL(e.Backend.ClientConfigURL(server.ID, client.ID))
	})
	download.Importance = widget.HighImportance

	created := lang.L("unknown")
	if configs.CreatedAt > 0 {
		created = configs.CreatedAtReadable
	}
	suspend := lang.L("not set")
	if configs.SuspendAt != nil {
		suspend = configs.SuspendAtReadable
	}

	meta := container.NewHBox(
		widgets.SmallText(lang.L("Created: {{.When}}", map[string]any{"When": created}), style.Muted),
		widgets.SmallText("·", style.Border),
		widgets.SmallText(lang.L("Auto-suspend: {{.When}}", map[string]any{"When": suspend}), style.Muted),
	)

	body := container.NewBorder(meta, container.NewHBox(download), nil, nil, tabs)

	dialogs.Show(e.Win, lang.L("Client configuration: {{.Name}}", map[string]any{"Name": client.Name}),
		lang.L("Close"), body, dialogs.Size(e.Win, 960, 720))
}

// viewPage is one tab: the text with its hint, length and copy button, and
// for the views that have one, the QR code alongside. A view without a QR
// code has no left column at all, so the text takes the full width instead
// of leaving a hole.
func viewPage(e *env.Env, client api.Client, view configView) fyne.CanvasObject {
	text, _ := widgets.MonospaceView(view.text)
	text.SetMinRowsVisible(12)

	length := widgets.SmallText(lang.N("{{.Count}} characters", len(view.text), map[string]any{"Count": len(view.text)}), style.Muted)
	copyButton := widgets.NewButton(lang.L("Copy"), theme.ContentCopyIcon(), func() {
		dialogs.Copy(e.Notify, view.text)
	})

	note := widgets.SmallText(view.note, style.Muted)
	head := container.NewVBox(note)
	if view.warning != "" {
		head.Add(warningLabel(view.warning))
	}

	page := container.NewBorder(head, container.NewBorder(nil, nil, length, copyButton), nil, nil, text)
	if !view.qr {
		return page
	}

	png, err := encodeQR(view.text)
	if err != nil {
		note.Text = ""
		head.Add(warningLabel(err.Error()))
		return page
	}

	qrImage := canvas.NewImageFromResource(fyne.NewStaticResource("qr.png", png))
	qrImage.FillMode = canvas.ImageFillContain
	qrImage.SetMinSize(fyne.NewSize(280, 280))
	saveQR := widgets.NewButton(lang.L("Save QR image"), theme.DownloadIcon(), func() {
		browser.SaveBytes(safeFileName(client.Name)+"_qr.png", "image/png", png)
	})
	left := container.NewVBox(container.NewCenter(qrImage), container.NewCenter(saveQR))

	return container.NewBorder(nil, nil, left, nil, page)
}

// warningLabel is a wrapped label in the warning colour.
func warningLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.Importance = widget.WarningImportance
	return label
}

// encodeQR renders the payload as a PNG, refusing anything too dense to scan.
func encodeQR(text string) ([]byte, error) {
	if text == "" {
		return nil, errors.New(lang.L("this view is unavailable"))
	}
	if len(text) > qrCapacity {
		return nil, errors.New(lang.L("config is too large for a QR code: {{.Length}} characters (max {{.Max}}). "+
			"Use \"Download .conf\" instead", map[string]any{"Length": len(text), "Max": qrCapacity}))
	}

	png, err := qrcode.Encode(text, qrcode.Medium, 640)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", lang.L("could not generate QR code"), err)
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
