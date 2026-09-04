package ui

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"amneziawg-web-ui/web-ui/api"
)

var (
	subnetPattern = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}/\d{1,2}$`)
	ipPattern     = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	rangePattern  = regexp.MustCompile(`^\d+(-\d+)?$`)
)

// serverForm is the collapsible "Create New VPN Server" panel.
type serverForm struct {
	ui *UI

	name     *widget.Entry
	port     *widget.Entry
	subnet   *widget.Entry
	mtu      *widget.Entry
	dns      *widget.Entry
	endpoint *widget.Entry

	autoStart   *widget.Check
	obfuscation *widget.Check

	jc, jmin, jmax *widget.Entry
	s1, s2, s3, s4 *widget.Entry
	h1, h2, h3, h4 *widget.Entry

	randomTrailers *widget.Check
	disableCookies *widget.Check
	headerKey      *widget.Entry

	advanced []*advancedField

	obfBox *fyne.Container
	errors *widget.Label
	create *pointerButton

	toggle *pointerButton
	body   *fyne.Container
	panel  *fyne.Container
}

// advancedField is one of the optional AWG 3.x per-side timing knobs.
type advancedField struct {
	key   string
	hint  string
	entry *widget.Entry
}

func newServerForm(u *UI) *serverForm {
	f := &serverForm{ui: u}

	f.name = entryWithPlaceholder("My VPN Server")
	f.port = entryWithText("54844")
	f.subnet = entryWithText("10.0.0.0/24")
	f.mtu = entryWithText("1420")
	f.dns = entryWithText("8.8.8.8,1.1.1.1")
	f.endpoint = entryWithPlaceholder("leave empty to use the auto-detected public IP")

	f.autoStart = widget.NewCheck("Auto-start server on creation", nil)
	f.autoStart.SetChecked(true)

	f.obfuscation = widget.NewCheck("Enable traffic obfuscation (AmneziaWG 3.1)", func(on bool) {
		if f.obfBox == nil {
			return
		}
		if on {
			f.obfBox.Show()
		} else {
			f.obfBox.Hide()
		}
	})
	f.obfuscation.SetChecked(true)

	basics := widget.NewForm(
		&widget.FormItem{Text: "Server name", Widget: f.name},
		&widget.FormItem{Text: "Port", Widget: f.port},
		&widget.FormItem{Text: "Subnet", Widget: f.subnet, HintText: "e.g. 10.0.0.0/24"},
		&widget.FormItem{Text: "MTU", Widget: f.mtu, HintText: "1280-1440; 1420-1440 performs best on most links"},
		&widget.FormItem{Text: "DNS servers", Widget: f.dns, HintText: "comma-separated IPs, e.g. 8.8.8.8,1.1.1.1"},
		&widget.FormItem{Text: "Endpoint", Widget: f.endpoint, HintText: "custom IP or hostname clients connect to"},
	)

	f.errors = widget.NewLabel("")
	f.errors.Wrapping = fyne.TextWrapWord
	f.errors.Importance = widget.DangerImportance
	f.errors.Hide()

	f.create = button("Create server", theme.ConfirmIcon(), f.submit)
	f.create.Importance = widget.HighImportance

	content := container.NewVBox(
		basics,
		f.autoStart,
		f.obfuscation,
		f.buildObfuscation(),
		f.errors,
		container.NewHBox(f.create),
	)

	// A hand-rolled disclosure rather than widget.Accordion: collapsing has
	// to tell the page to re-clamp its scroll offset, otherwise the viewport
	// can stay parked below the end of the shortened content.
	f.body = container.NewPadded(content)
	f.body.Hide()

	f.toggle = button("Create New VPN Server", theme.MenuDropDownIcon(), f.toggleOpen)
	f.toggle.Alignment = widget.ButtonAlignLeading
	f.toggle.Importance = widget.LowImportance

	f.panel = container.NewVBox(f.toggle, f.body)

	return f
}

func (f *serverForm) canvasObject() fyne.CanvasObject {
	return container.NewPadded(card(f.panel))
}

func (f *serverForm) toggleOpen() {
	if f.body.Visible() {
		f.collapse()
		return
	}

	f.body.Show()
	f.toggle.SetIcon(theme.MenuDropUpIcon())
	f.ui.clampScroll()
}

func (f *serverForm) collapse() {
	f.body.Hide()
	f.toggle.SetIcon(theme.MenuDropDownIcon())
	f.ui.clampScroll()
}

// buildObfuscation lays out the AmneziaWG parameter block: the packet-shaping
// numbers first, then the 3.1 switches, the header protection key and the
// optional per-side timing knobs.
func (f *serverForm) buildObfuscation() fyne.CanvasObject {
	f.jc = entryWithText("4")
	f.jmin = entryWithText("8")
	f.jmax = entryWithText("80")
	f.s1 = entryWithText("50")
	f.s2 = entryWithText("60")
	f.s3 = entryWithText("20")
	f.s4 = entryWithText("16")
	f.h1 = entryWithText("1000")
	f.h2 = entryWithText("2000")
	f.h3 = entryWithText("3000")
	f.h4 = entryWithText("4000")

	junk := container.NewGridWithColumns(3,
		labeled("Jc (4-12)", f.jc),
		labeled("Jmin (recommended 8)", f.jmin),
		labeled("Jmax (recommended 80)", f.jmax),
	)
	sizes := container.NewGridWithColumns(4,
		labeled("S1 (15-150)", f.s1),
		labeled("S2 (15-150)", f.s2),
		labeled("S3 (12-256)", f.s3),
		labeled("S4 (12-32)", f.s4),
	)
	headers := container.NewGridWithColumns(4,
		labeled("H1", f.h1),
		labeled("H2", f.h2),
		labeled("H3", f.h3),
		labeled("H4", f.h4),
	)

	random := button("Generate random parameters", theme.ViewRefreshIcon(), f.randomise)

	f.randomTrailers = widget.NewCheck("RandomTrailers - append a random number of bytes to every packet", nil)
	f.randomTrailers.SetChecked(true)
	f.disableCookies = widget.NewCheck("DisableCookies - never answer with cookie replies, skip under-load MAC2 checks", nil)
	f.disableCookies.SetChecked(true)

	togglesNote := widget.NewLabel("AmneziaWG 3.1 packet shaping. Unlike the timing knobs below, both switches are written " +
		"identically into the server config and every client config - the two sides must agree, so turn them off if any of " +
		"your clients is older than AmneziaWG 3.1 (AmneziaVPN < 5.0.1.5).")
	togglesNote.Wrapping = fyne.TextWrapWord
	togglesNote.TextStyle = fyne.TextStyle{Italic: true}

	f.headerKey = entryWithPlaceholder("auto-generated if left empty")
	keyNote := widget.NewLabel("AmneziaWG 3.x requires S1-S4 >= 12 and this key to match byte-for-byte between the server " +
		"and every client config.")
	keyNote.Wrapping = fyne.TextWrapWord
	keyNote.TextStyle = fyne.TextStyle{Italic: true}

	f.advanced = []*advancedField{
		{key: "ContentPaddingAddition", hint: "e.g. 0-64"},
		{key: "RekeyAfterTime", hint: "default 120, e.g. 100-140"},
		{key: "RekeyTimeout", hint: "default 5, e.g. 4-7"},
		{key: "RejectAfterTime", hint: "default 180, e.g. 160-200"},
		{key: "KeepaliveTimeout", hint: "default 10, e.g. 8-12"},
		{key: "MaxHandshakeAttempts", hint: "default 18, e.g. 14-20"},
		{key: "PersistentKeepalive", hint: "default 25, e.g. 22-30"},
	}
	advancedGrid := container.NewGridWithColumns(2)
	for _, field := range f.advanced {
		field.entry = entryWithPlaceholder(field.hint)
		advancedGrid.Add(labeled(field.key, field.entry))
	}

	advancedNote := widget.NewLabel("Optional AWG 3.x timing knobs (an integer or an \"a-b\" range). They are applied " +
		"per side and do not need to match between server and client; leave empty to use the engine defaults.")
	advancedNote.Wrapping = fyne.TextWrapWord
	advancedNote.TextStyle = fyne.TextStyle{Italic: true}

	f.obfBox = container.NewVBox(
		sectionTitle("Obfuscation parameters"),
		junk, sizes, headers,
		container.NewHBox(random),
		separator(),
		togglesNote, f.randomTrailers, f.disableCookies,
		separator(),
		labeled("HeaderProtectionKey (base64)", f.headerKey), keyNote,
		separator(),
		advancedNote, advancedGrid,
	)

	return container.NewPadded(card(f.obfBox))
}

func (f *serverForm) randomise() {
	f.jc.SetText(strconv.Itoa(rand.Intn(9) + 4))    // 4-12
	f.s1.SetText(strconv.Itoa(rand.Intn(136) + 15)) // 15-150
	f.s2.SetText(strconv.Itoa(rand.Intn(136) + 15)) // 15-150
	f.s3.SetText(strconv.Itoa(rand.Intn(245) + 12)) // 12-256, header protection needs >= 12
	f.s4.SetText(strconv.Itoa(rand.Intn(21) + 12))  // 12-32, header protection needs >= 12

	// H1-H4 have to be four distinct values.
	seen := map[int]bool{}
	values := make([]int, 0, 4)
	for len(values) < 4 {
		candidate := rand.Intn(1000000) + 1000
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		values = append(values, candidate)
	}
	f.h1.SetText(strconv.Itoa(values[0]))
	f.h2.SetText(strconv.Itoa(values[1]))
	f.h3.SetText(strconv.Itoa(values[2]))
	f.h4.SetText(strconv.Itoa(values[3]))
}

func (f *serverForm) showErrors(messages []string) {
	if len(messages) == 0 {
		f.errors.Hide()
		return
	}
	f.errors.SetText(strings.Join(messages, "\n"))
	f.errors.Show()
}

// build validates every field and returns the payload for POST /api/servers.
func (f *serverForm) build() (api.CreateServerRequest, []string) {
	var problems []string
	autoStart := f.autoStart.Checked
	obfuscation := f.obfuscation.Checked
	req := api.CreateServerRequest{
		Name:        strings.TrimSpace(f.name.Text),
		Subnet:      strings.TrimSpace(f.subnet.Text),
		Endpoint:    strings.TrimSpace(f.endpoint.Text),
		DNS:         strings.TrimSpace(f.dns.Text),
		AutoStart:   &autoStart,
		Obfuscation: &obfuscation,
	}

	if req.Name == "" {
		problems = append(problems, "Server name is required")
	}

	port, err := strconv.Atoi(strings.TrimSpace(f.port.Text))
	if err != nil || port < 1 || port > 65535 {
		problems = append(problems, "Port must be between 1 and 65535")
	}
	req.Port = port

	if !subnetPattern.MatchString(req.Subnet) {
		problems = append(problems, "Valid subnet is required (e.g. 10.0.0.0/24)")
	}

	mtu, err := strconv.Atoi(strings.TrimSpace(f.mtu.Text))
	if err != nil || mtu < 1280 || mtu > 1440 {
		problems = append(problems, "MTU must be between 1280 and 1440")
	}
	req.MTU = mtu

	servers := splitList(strings.TrimSpace(f.dns.Text))
	if len(servers) == 0 {
		problems = append(problems, "At least one DNS server is required")
	}
	for _, dns := range servers {
		if !ipPattern.MatchString(dns) {
			problems = append(problems, "Invalid DNS server IP: "+dns)
			break
		}
	}

	if obfuscation {
		params, issues := f.obfuscationParams(mtu)
		problems = append(problems, issues...)
		req.ObfuscationParams = params
	}

	return req, problems
}

func (f *serverForm) obfuscationParams(mtu int) (*api.ObfuscationParams, []string) {
	var problems []string

	number := func(entry *widget.Entry, label string) int {
		value, err := strconv.Atoi(strings.TrimSpace(entry.Text))
		if err != nil {
			problems = append(problems, label+" must be a number")
			return 0
		}
		return value
	}

	params := &api.ObfuscationParams{
		Jc:                  number(f.jc, "Jc"),
		Jmin:                number(f.jmin, "Jmin"),
		Jmax:                number(f.jmax, "Jmax"),
		S1:                  number(f.s1, "S1"),
		S2:                  number(f.s2, "S2"),
		S3:                  number(f.s3, "S3"),
		S4:                  number(f.s4, "S4"),
		H1:                  number(f.h1, "H1"),
		H2:                  number(f.h2, "H2"),
		H3:                  number(f.h3, "H3"),
		H4:                  number(f.h4, "H4"),
		RandomTrailers:      f.randomTrailers.Checked,
		DisableCookies:      f.disableCookies.Checked,
		HeaderProtectionKey: strings.TrimSpace(f.headerKey.Text),
	}

	for _, field := range f.advanced {
		value := strings.TrimSpace(field.entry.Text)
		if value == "" {
			continue
		}
		if !rangePattern.MatchString(value) {
			problems = append(problems, fmt.Sprintf("%s (%s) must be an integer or an \"a-b\" range", field.key, value))
			continue
		}
		switch field.key {
		case "ContentPaddingAddition":
			params.ContentPaddingAddition = value
		case "RekeyAfterTime":
			params.RekeyAfterTime = value
		case "RekeyTimeout":
			params.RekeyTimeout = value
		case "RejectAfterTime":
			params.RejectAfterTime = value
		case "KeepaliveTimeout":
			params.KeepaliveTimeout = value
		case "MaxHandshakeAttempts":
			params.MaxHandshakeAttempts = value
		case "PersistentKeepalive":
			params.PersistentKeepalive = value
		}
	}

	problems = append(problems, validateObfuscation(params, mtu)...)
	return params, problems
}

// validateObfuscation mirrors the checks the backend performs, so an invalid
// combination is caught before the request goes out.
func validateObfuscation(p *api.ObfuscationParams, mtu int) []string {
	var problems []string

	if !(p.Jmin < p.Jmax && p.Jmax <= mtu) {
		problems = append(problems, fmt.Sprintf("Jmin (%d) must be less than Jmax (%d), and Jmax <= MTU (%d)", p.Jmin, p.Jmax, mtu))
	}
	if p.Jmin >= mtu {
		problems = append(problems, fmt.Sprintf("Jmin (%d) must be less than MTU (%d)", p.Jmin, mtu))
	}
	if !(p.S1 <= mtu-148 && p.S1 >= 15 && p.S1 <= 150) {
		problems = append(problems, fmt.Sprintf("S1 (%d) must be in [15, 150] and <= MTU-148 (%d)", p.S1, mtu-148))
	}
	if !(p.S2 <= mtu-92 && p.S2 >= 15 && p.S2 <= 150) {
		problems = append(problems, fmt.Sprintf("S2 (%d) must be in [15, 150] and <= MTU-92 (%d)", p.S2, mtu-92))
	}
	if p.S1+56 == p.S2 {
		problems = append(problems, fmt.Sprintf("S1 + 56 (%d) must not equal S2 (%d)", p.S1+56, p.S2))
	}
	if p.S4 > 32 {
		problems = append(problems, fmt.Sprintf("S4 (%d) must be in range [0, 32]", p.S4))
	}

	// Obfuscation is always AmneziaWG 3.x header protection, whose cipher
	// takes its 12-byte nonce from the start of the padding.
	for label, value := range map[string]int{"S1": p.S1, "S2": p.S2, "S3": p.S3, "S4": p.S4} {
		if value < 12 {
			problems = append(problems, fmt.Sprintf("%s (%d) must be at least 12 for AmneziaWG 3.x header protection", label, value))
		}
	}

	return problems
}

func (f *serverForm) submit() {
	req, problems := f.build()
	if len(problems) > 0 {
		f.showErrors(problems)
		return
	}
	f.showErrors(nil)

	f.create.Disable()
	f.create.SetText("Creating…")

	go func() {
		server, err := f.ui.backend.CreateServer(req)

		fyne.Do(func() {
			f.create.Enable()
			f.create.SetText("Create server")
		})

		if err != nil {
			fyne.Do(func() { f.showErrors([]string{err.Error()}) })
			f.ui.fail(err)
			return
		}

		f.ui.ok("Server %q created", server.Name)
		fyne.Do(func() {
			f.name.SetText("")
			f.endpoint.SetText("")
			f.collapse()
		})
		f.ui.reloadServers()
	}()
}

// ── Shared form helpers ──────────────────────────────────────────────────────

func entryWithText(text string) *widget.Entry {
	entry := widget.NewEntry()
	entry.SetText(text)
	return entry
}

func entryWithPlaceholder(placeholder string) *widget.Entry {
	entry := widget.NewEntry()
	entry.SetPlaceHolder(placeholder)
	return entry
}

// labeled stacks a small caption above a field, for the dense parameter grids
// where a full widget.Form row would waste horizontal space.
func labeled(caption string, field fyne.CanvasObject) fyne.CanvasObject {
	label := canvas.NewText(caption, colMuted)
	label.TextSize = 11
	return container.NewVBox(label, field)
}

func sectionTitle(text string) fyne.CanvasObject {
	title := canvas.NewText(text, colText)
	title.TextSize = 14
	title.TextStyle = fyne.TextStyle{Bold: true}
	return title
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
