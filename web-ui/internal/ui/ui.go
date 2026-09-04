package ui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	sio "github.com/zishang520/socket.io/clients/socket/v3"

	"amneziawg-web-ui/web-ui/api"
)

const (
	// The Socket.IO feed drives the counters; these loops only cover the
	// case where the socket is down, and keep the list honest if an event
	// is ever missed.
	trafficInterval = 10 * time.Second
	serversInterval = 60 * time.Second
)

// UI owns every widget that outlives a single render pass, plus the cached
// backend state the render passes read from.
type UI struct {
	app     fyne.App
	win     fyne.Window
	backend *Backend

	publicIP   *canvas.Text
	statusDot  *canvas.Circle
	transport  *canvas.Text
	statusText *canvas.Text
	toast      *canvas.Text

	serversBox *fyne.Container
	emptyHint  *widget.Label
	form       *serverForm
	scroll     *container.Scroll

	socket     *sio.Socket
	socketLive atomic.Bool

	mu          sync.Mutex
	servers     []api.Server
	serversHash string
	cards       map[string]*serverCard
	defaultI    api.ISettings

	toastSeq int
}

func New(a fyne.App, w fyne.Window) *UI {
	return &UI{
		app:      a,
		win:      w,
		backend:  newBackend(),
		cards:    map[string]*serverCard{},
		defaultI: api.ISettings{},
	}
}

// Build assembles the whole page: a fixed header, a scrolling body holding
// the create-server form and the server cards, and a status footer.
func (u *UI) Build() fyne.CanvasObject {
	u.serversBox = container.NewVBox()
	u.emptyHint = widget.NewLabel("No servers created yet. Create your first server above.")
	u.emptyHint.Alignment = fyne.TextAlignCenter
	u.form = newServerForm(u)

	body := container.NewVBox(
		u.form.canvasObject(),
		u.serversBox,
		u.emptyHint,
	)

	u.scroll = container.NewVScroll(container.NewPadded(body))

	return container.NewBorder(u.header(), u.footer(), nil, nil, u.scroll)
}

func (u *UI) header() fyne.CanvasObject {
	title := canvas.NewText("AmneziaWG Web UI", colText)
	title.TextSize = 22
	title.TextStyle = fyne.TextStyle{Bold: true}

	ipCaption := smallText("Public IP", colMuted)
	u.publicIP = smallText("detecting…", colPrimary)
	u.publicIP.TextStyle = fyne.TextStyle{Monospace: true}

	u.statusDot = canvas.NewCircle(colMuted)
	u.statusDot.Resize(fyne.NewSize(10, 10))
	dot := container.NewGridWrap(fyne.NewSize(10, 10), u.statusDot)
	u.transport = smallText("connecting…", colMuted)
	u.statusText = smallText("", colMuted)

	refresh := button("Refresh IP", theme.ViewRefreshIcon(), u.refreshPublicIP)
	refresh.Importance = widget.LowImportance

	// canvas.Text draws at the top of whatever box it gets, so every item in
	// this single-line row is centred explicitly.
	info := container.NewHBox(
		container.NewCenter(ipCaption), container.NewCenter(u.publicIP),
		verticalRule(),
		container.NewCenter(dot), container.NewCenter(u.transport),
		verticalRule(),
		container.NewCenter(u.statusText),
	)

	bar := container.NewBorder(nil, nil,
		container.NewCenter(title),
		container.NewHBox(info, refresh),
	)

	bg := canvas.NewRectangle(colSurface)
	line := canvas.NewRectangle(colBorder)
	line.SetMinSize(fyne.NewSize(0, 1))

	return container.NewStack(bg, container.NewBorder(nil, line, nil, nil, container.NewPadded(bar)))
}

func (u *UI) footer() fyne.CanvasObject {
	u.toast = smallText("", colMuted)
	link := smallText("© AmneziaWG Go Web UI", colMuted)

	bar := container.NewBorder(nil, nil, container.NewCenter(u.toast), container.NewCenter(link))

	bg := canvas.NewRectangle(colSurface)
	line := canvas.NewRectangle(colBorder)
	line.SetMinSize(fyne.NewSize(0, 1))

	return container.NewStack(bg, container.NewBorder(line, nil, nil, nil, container.NewPadded(bar)))
}

// ── Feedback helpers ─────────────────────────────────────────────────────────

// notify shows a transient message in the footer. Successive messages replace
// each other, and each one clears itself after a few seconds.
func (u *UI) notify(message string, c color.Color) {
	fyne.Do(func() {
		u.toastSeq++
		seq := u.toastSeq
		u.toast.Text = message
		u.toast.Color = c
		u.toast.Refresh()

		time.AfterFunc(4*time.Second, func() {
			fyne.Do(func() {
				if u.toastSeq != seq {
					return
				}
				u.toast.Text = ""
				u.toast.Refresh()
			})
		})
	})
}

func (u *UI) ok(format string, args ...any) {
	u.notify(fmt.Sprintf(format, args...), colSuccess)
}

func (u *UI) warn(format string, args ...any) {
	u.notify(fmt.Sprintf(format, args...), colWarning)
}

func (u *UI) fail(err error) {
	fyne.Do(func() { dialog.ShowError(err, u.win) })
}

// setTransport drives the status light: green while the Socket.IO feed is
// carrying updates, amber while the UI is falling back to REST polling, red
// when the backend cannot be reached at all.
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
	u.setTransport("offline", colError)
	u.setSummary("backend unreachable")
}

func (u *UI) setPublicIP(ip string) {
	if ip == "" {
		ip = "unknown"
	}
	fyne.Do(func() {
		u.publicIP.Text = ip
		u.publicIP.Refresh()
	})
}

// ── Data loading ─────────────────────────────────────────────────────────────

func (u *UI) Start() {
	go u.loadSystemStatus()
	go u.loadDefaultISettings()
	go u.reloadServers()
	go u.connectSocket()

	// Fallbacks for when the Socket.IO feed is not up: poll the same data
	// over REST, but stay quiet while events are flowing.
	go func() {
		for {
			time.Sleep(trafficInterval)
			if u.socketLive.Load() {
				continue
			}
			u.refreshTraffic()
		}
	}()
	go func() {
		for {
			time.Sleep(serversInterval)
			u.reloadServers()
		}
	}()
}

func (u *UI) loadSystemStatus() {
	status, err := u.backend.SystemStatus()
	if err != nil {
		u.unreachable()
		return
	}
	if !u.socketLive.Load() {
		u.setTransport("polling", colWarning)
	}
	u.setSummary(fmt.Sprintf("%d/%d servers running · %d clients",
		status.ActiveServers, status.TotalServers, status.TotalClients))
	u.setPublicIP(status.PublicIP)
}

func (u *UI) loadDefaultISettings() {
	settings, err := u.backend.DefaultISettings()
	if err != nil {
		return
	}
	u.mu.Lock()
	u.defaultI = settings
	u.mu.Unlock()
}

func (u *UI) refreshPublicIP() {
	go func() {
		ip, err := u.backend.RefreshIP()
		if err != nil {
			u.fail(err)
			return
		}
		u.setPublicIP(ip)
		u.ok("Public IP refreshed")
		u.reloadServers()
	}()
}

// reloadServers pulls the server list and rebuilds the cards when something
// actually changed. Every action that alters structure (create, delete,
// start/stop, client changes) calls it.
func (u *UI) reloadServers() {
	servers, err := u.backend.Servers()
	if err != nil {
		u.unreachable()
		return
	}
	u.loadSystemStatus()

	hash := fingerprint(servers)

	u.mu.Lock()
	unchanged := hash == u.serversHash
	u.servers = servers
	u.serversHash = hash
	u.mu.Unlock()

	if unchanged {
		return
	}

	fyne.Do(func() {
		u.renderServers(servers)
	})

	if !u.socketLive.Load() {
		u.refreshTraffic()
	}
}

// refreshTraffic fetches the counters over REST. Only used while the
// Socket.IO feed is down.
func (u *UI) refreshTraffic() {
	iface, err := u.backend.InterfaceTraffic()
	if err != nil {
		u.unreachable()
		return
	}

	u.mu.Lock()
	servers := u.servers
	u.mu.Unlock()

	peers := map[string]map[string]api.ClientTraffic{}
	for _, srv := range servers {
		peers[srv.ID] = u.backend.PeerTraffic(srv.ID)
	}

	u.applyTraffic(iface, peers)
}

// applyTraffic pushes counters into the existing labels, so the page updates
// without rebuilding (and without losing scroll position or focus).
func (u *UI) applyTraffic(iface map[string]api.InterfaceTraffic, peers map[string]map[string]api.ClientTraffic) {
	fyne.Do(func() {
		for id, card := range u.cards {
			card.applyInterfaceTraffic(iface[id])
			card.applyPeerTraffic(peers[id])
		}
	})
}

// fingerprint is a cheap "did anything change" marker for the server list.
func fingerprint(servers []api.Server) string {
	data, err := json.Marshal(servers)
	if err != nil {
		return ""
	}
	return string(data)
}

// clampScroll re-checks the scroll offset against the content. Collapsing the
// form or replacing the server list can leave the viewport parked past the
// end of the (now shorter) page, showing nothing but background.
func (u *UI) clampScroll() {
	if u.scroll != nil {
		u.scroll.Refresh()
	}
}

// ── Small shared widgets ─────────────────────────────────────────────────────

// pointerButton is widget.Button with the hand cursor a browser user expects
// over anything clickable. Fyne's button deliberately reports the default
// arrow - correct for a desktop window, wrong for a page - and the wasm driver
// maps whatever a widget asks for onto the CSS cursor, so this is all it takes.
type pointerButton struct {
	widget.Button
}

// button is the constructor every button in this UI goes through, so none of
// them can be built without the cursor.
func button(label string, icon fyne.Resource, tapped func()) *pointerButton {
	b := &pointerButton{}
	b.ExtendBaseWidget(b)
	b.Text = label
	b.Icon = icon
	b.OnTapped = tapped
	return b
}

func (b *pointerButton) Cursor() desktop.Cursor {
	if b.Disabled() {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func smallText(text string, c color.Color) *canvas.Text {
	t := canvas.NewText(text, c)
	t.TextSize = 12
	return t
}

func badge(text string, c color.NRGBA) fyne.CanvasObject {
	bg := canvas.NewRectangle(withAlpha(c, 0x2e))
	bg.CornerRadius = 9
	bg.StrokeColor = withAlpha(c, 0x99)
	bg.StrokeWidth = 1

	label := canvas.NewText(text, c)
	label.TextSize = 11
	label.TextStyle = fyne.TextStyle{Bold: true}

	padded := container.New(&insetLayout{h: 8, v: 3}, label)
	return container.NewStack(bg, padded)
}

// card wraps content in the rounded surface panel used throughout the page.
func card(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colSurface)
	bg.CornerRadius = 10
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.NewPadded(content))
}

func withAlpha(c color.NRGBA, alpha uint8) color.NRGBA {
	c.A = alpha
	return c
}

func separator() fyne.CanvasObject {
	line := canvas.NewRectangle(colBorder)
	line.SetMinSize(fyne.NewSize(0, 1))
	return line
}

// verticalRule is the thin divider used inside single-line header rows.
func verticalRule() fyne.CanvasObject {
	line := canvas.NewRectangle(colBorder)
	line.SetMinSize(fyne.NewSize(1, 16))
	return container.NewCenter(line)
}

// insetLayout pads its children by an explicit number of pixels, which the
// theme padding cannot express per-side (used by the small badges).
type insetLayout struct {
	h, v float32
}

func (i *insetLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(i.h, i.v))
		o.Resize(size.Subtract(fyne.NewSize(i.h*2, i.v*2)))
	}
}

func (i *insetLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var min fyne.Size
	for _, o := range objects {
		min = min.Max(o.MinSize())
	}
	return min.Add(fyne.NewSize(i.h*2, i.v*2))
}
