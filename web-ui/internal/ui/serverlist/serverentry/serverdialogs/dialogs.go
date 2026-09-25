// Package serverdialogs holds the dialogs a server card opens: the overview
// with the parameters, and the raw .conf viewer.
package serverdialogs

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"amneziawg-web-ui/web-ui/api"
	"amneziawg-web-ui/web-ui/internal/ui/browser"
	"amneziawg-web-ui/web-ui/internal/ui/dialogs"
	"amneziawg-web-ui/web-ui/internal/ui/env"
	"amneziawg-web-ui/web-ui/internal/ui/style"
	"amneziawg-web-ui/web-ui/internal/ui/widgets"
)

// ShowConfig fetches the server's details and opens the overview.
func ShowConfig(e *env.Env, serverID string) {
	go func() {
		info, err := e.Backend.ServerInfo(serverID)
		if err != nil {
			e.Notify.Fail(err)
			return
		}
		fyne.Do(func() { presentConfig(e, info) })
	}()
}

func presentConfig(e *env.Env, info api.ServerInfo) {
	statusColor := style.Error
	if info.Status == "running" {
		statusColor = style.Success
	}

	basics := widgets.InfoGrid([][2]string{
		{lang.L("Interface"), info.Interface},
		{lang.L("Port"), fmt.Sprintf("%d", info.Port)},
		{lang.L("Subnet"), info.Subnet},
		{lang.L("Server IP"), info.ServerIP},
		{lang.L("Public IP"), info.PublicIP},
		{"MTU", fmt.Sprintf("%d", info.MTU)},
		{lang.L("Protocol"), info.Protocol},
		{lang.L("Clients"), fmt.Sprintf("%d", info.ClientsCount)},
		{"DNS", strings.Join(info.DNS, ", ")},
		{lang.L("Public key"), info.PublicKey},
	})

	head := container.NewHBox(
		widgets.SectionTitle(info.Name),
		container.NewCenter(widgets.Badge(strings.ToUpper(lang.L(info.Status)), statusColor)),
	)

	body := container.NewVBox(head, basics)

	if info.ObfuscationEnabled && info.ObfuscationParams != nil {
		body.Add(widgets.Separator())
		body.Add(widgets.SectionTitle(lang.L("Obfuscation parameters (AmneziaWG 3.1)")))
		body.Add(obfuscationGrid(info.ObfuscationParams))
	}

	if len(info.DefaultISettings) > 0 {
		body.Add(widgets.Separator())
		body.Add(widgets.SectionTitle(lang.L("Default I-settings")))
		rows := make([][2]string, 0, len(info.DefaultISettings))
		for i := 1; i <= 5; i++ {
			key := fmt.Sprintf("i%d", i)
			value := info.DefaultISettings[key]
			if value == "" {
				value = lang.L("empty")
			} else {
				value = widgets.Truncate(value, 60)
			}
			rows = append(rows, [2]string{strings.ToUpper(key), value})
		}
		body.Add(widgets.InfoGrid(rows))
		body.Add(widgets.MutedNote(lang.L("These defaults are used for new clients when \"Apply I-settings\" is enabled.")))
	}

	full := widgets.NewButton(lang.L("View full config"), theme.DocumentIcon(), func() {
		showRaw(e, info.ID)
	})
	download := widgets.NewButton(lang.L("Download config"), theme.DownloadIcon(), func() {
		browser.OpenURL(e.Backend.ServerConfigURL(info.ID))
	})
	download.Importance = widget.HighImportance

	// The actions stay outside the scroll area so they are always reachable.
	content := container.NewBorder(nil, container.NewHBox(full, download), nil, nil, dialogs.Scrolled(body))

	dialogs.Show(e.Win, lang.L("Server configuration"), lang.L("Close"), content, dialogs.Size(e.Win, 880, 720))
}

func showRaw(e *env.Env, serverID string) {
	go func() {
		config, err := e.Backend.ServerConfig(serverID)
		if err != nil {
			e.Notify.Fail(err)
			return
		}

		fyne.Do(func() {
			view, _ := widgets.MonospaceView(config.ConfigContent)
			view.SetMinRowsVisible(20)

			copyButton := widgets.NewButton(lang.L("Copy"), theme.ContentCopyIcon(), func() {
				dialogs.Copy(e.Notify, config.ConfigContent)
			})
			download := widgets.NewButton(lang.L("Download"), theme.DownloadIcon(), func() {
				browser.OpenURL(e.Backend.ServerConfigURL(config.ServerID))
			})
			download.Importance = widget.HighImportance

			body := container.NewBorder(
				container.NewVBox(
					widgets.SmallText(config.ConfigPath, style.Muted),
					container.NewHBox(copyButton, download),
				), nil, nil, nil, view)

			dialogs.Show(e.Win, lang.L("Raw configuration: {{.Name}}", map[string]any{"Name": config.ServerName}),
				lang.L("Close"), body, dialogs.Size(e.Win, 900, 760))
		})
	}()
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
		{"HeaderProtectionKey", widgets.Truncate(p.HeaderProtectionKey, 24)},
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

	return widgets.InfoGrid(rows)
}

func onOff(value bool) string {
	if value {
		return lang.L("on")
	}
	return lang.L("off")
}
