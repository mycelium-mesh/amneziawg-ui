// Package ui is the page: a fixed header, a scrolling body holding the
// dashboard, the create-server form and the server list, and a status
// footer. It owns the data the body is rendered from and the REST polling
// that keeps it current, and hands everything the components need down to
// them through env.Env.
//
// The tree below this package follows the page top to bottom:
//
//	dashboard                      the collapsible traffic and host charts
//	newserver                      the collapsible create form
//	serverlist                     the cards
//	  serverentry                  one server
//	    serverdialogs              its overview / raw config dialogs
//	    clientlist                 its peers
//	      cliententry              one peer
//	        clientdialogs          the add/edit form, the config + QR viewer
//
// alongside the leaves every level draws on: style, widgets, dialogs,
// backend, browser and env.
package ui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"amneziawg-web-ui/web-ui/internal/ui/backend"
	"amneziawg-web-ui/web-ui/internal/ui/dashboard"
	"amneziawg-web-ui/web-ui/internal/ui/env"
	"amneziawg-web-ui/web-ui/internal/ui/newserver"
	"amneziawg-web-ui/web-ui/internal/ui/serverlist"
	"amneziawg-web-ui/web-ui/internal/ui/style"
	"amneziawg-web-ui/web-ui/internal/ui/widgets"
)

// NewDarkTheme is the page's theme, for main to install before the window
// opens.
func NewDarkTheme() fyne.Theme {
	return style.NewTheme()
}

// UI is the page. It holds the header widgets it updates itself, the two
// body components, and the state behind them.
type UI struct {
	app fyne.App
	win fyne.Window
	env *env.Env

	*state
	feedback *feedback

	publicIP   *canvas.Text
	statusDot  *canvas.Circle
	transport  *canvas.Text
	statusText *canvas.Text

	charts *dashboard.Panel
	form   *newserver.Form
	list   *serverlist.List
	scroll *container.Scroll
}

// New wires the page up; Build lays it out and Start begins loading.
func New(a fyne.App, w fyne.Window) *UI {
	u := &UI{
		app:      a,
		win:      w,
		state:    newState(),
		feedback: &feedback{win: w},
	}
	u.env = &env.Env{
		Win:      w,
		Backend:  backend.New(),
		Notify:   u.feedback,
		Reload:   u.reloadServers,
		DefaultI: u.defaultISettings,

		RefreshDefaultI: u.loadDefaultISettings,
	}
	return u
}

// Build assembles the whole page: a fixed header, a scrolling body holding
// the dashboard, the create-server form and the server cards, and a status
// footer.
func (u *UI) Build() fyne.CanvasObject {
	u.charts = dashboard.New(trafficInterval, u.clampScroll)
	u.form = newserver.New(u.env, u.state, u.clampScroll)
	u.list = serverlist.New(u.env)

	body := container.NewVBox(
		u.charts.CanvasObject(),
		u.form.CanvasObject(),
		u.list.CanvasObject(),
	)

	u.scroll = container.NewVScroll(container.NewPadded(body))

	return container.NewBorder(u.header(), u.footer(), nil, nil, u.scroll)
}

func (u *UI) header() fyne.CanvasObject {
	title := canvas.NewText("AmneziaWG Web UI", style.Text)
	title.TextSize = 22
	title.TextStyle = fyne.TextStyle{Bold: true}

	version := widgets.SmallText(u.version(), style.Muted)
	version.TextStyle = fyne.TextStyle{Monospace: true}

	ipCaption := widgets.SmallText(lang.L("Public IP"), style.Muted)
	u.publicIP = widgets.SmallText(lang.L("detecting…"), style.Primary)
	u.publicIP.TextStyle = fyne.TextStyle{Monospace: true}

	u.statusDot = canvas.NewCircle(style.Muted)
	u.statusDot.Resize(fyne.NewSize(10, 10))
	dot := container.NewGridWrap(fyne.NewSize(10, 10), u.statusDot)
	u.transport = widgets.SmallText(lang.L("connecting…"), style.Muted)
	u.statusText = widgets.SmallText("", style.Muted)

	refresh := widgets.NewButton(lang.L("Refresh IP"), theme.ViewRefreshIcon(), u.refreshPublicIP)
	refresh.Importance = widget.LowImportance

	// canvas.Text draws at the top of whatever box it gets, so every item in
	// this single-line row is centred explicitly.
	info := container.NewHBox(
		container.NewCenter(ipCaption), container.NewCenter(u.publicIP),
		widgets.VerticalRule(),
		container.NewCenter(dot), container.NewCenter(u.transport),
		widgets.VerticalRule(),
		container.NewCenter(u.statusText),
	)

	bar := container.NewBorder(nil, nil,
		container.NewHBox(container.NewCenter(title), container.NewCenter(version)),
		container.NewHBox(info, refresh),
	)

	bg := canvas.NewRectangle(style.Surface)
	line := canvas.NewRectangle(style.Border)
	line.SetMinSize(fyne.NewSize(0, 1))

	return container.NewStack(bg, container.NewBorder(nil, line, nil, nil, container.NewPadded(bar)))
}

// version is the build stamp "fyne package" bakes into the bundle: the
// release tag it reads from FyneApp.toml - written there from the newest git
// tag by the web-ui make target, and by the image's frontend stage - and the
// build number it bumps on every package run. A bundle built by a plain "go build" carries no metadata at
// all, so it reports itself as a development build rather than claiming a
// version it was never given.
func (u *UI) version() string {
	meta := u.app.Metadata()
	if meta.Version == "" {
		return "dev"
	}

	return fmt.Sprintf("v%s · build %d", meta.Version, meta.Build)
}

func (u *UI) footer() fyne.CanvasObject {
	u.feedback.toast = widgets.SmallText("", style.Muted)
	link := widgets.SmallText("© AmneziaWG Go Web UI", style.Muted)

	bar := container.NewBorder(nil, nil, container.NewCenter(u.feedback.toast), container.NewCenter(link))

	bg := canvas.NewRectangle(style.Surface)
	line := canvas.NewRectangle(style.Border)
	line.SetMinSize(fyne.NewSize(0, 1))

	return container.NewStack(bg, container.NewBorder(line, nil, nil, nil, container.NewPadded(bar)))
}

// ── Header status ────────────────────────────────────────────────────────────

// setTransport drives the status light: green while the backend answers,
// red once a request has failed and until the next one succeeds.
func (u *UI) setTransport(label string, c color.NRGBA) {
	fyne.Do(func() {
		u.statusDot.FillColor = c
		u.statusDot.Refresh()
		u.transport.Text = label
		u.transport.Color = c
		u.transport.Refresh()
	})
}

// setSummary shows the server/client counts next to the status light.
func (u *UI) setSummary(text string) {
	fyne.Do(func() {
		u.statusText.Text = text
		u.statusText.Refresh()
	})
}

// unreachable is the shared reaction to a failed REST call.
func (u *UI) unreachable() {
	u.setTransport(lang.L("offline"), style.Error)
	u.setSummary(lang.L("backend unreachable"))
}

func (u *UI) setPublicIP(ip string) {
	if ip == "" {
		ip = lang.L("unknown")
	}
	fyne.Do(func() {
		u.publicIP.Text = ip
		u.publicIP.Refresh()
	})
}

// clampScroll re-checks the scroll offset against the content. Collapsing the
// form or replacing the server list can leave the viewport parked past the
// end of the (now shorter) page, showing nothing but background.
func (u *UI) clampScroll() {
	if u.scroll != nil {
		u.scroll.Refresh()
	}
}
