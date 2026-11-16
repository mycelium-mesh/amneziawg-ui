// Package clientdialogs holds the dialogs a client row opens: the add/edit
// form and the configuration viewer with its QR code.
package clientdialogs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	datepicker "github.com/sdassow/fyne-datepicker"

	"amneziawg-web-ui/web-ui/api"
	"amneziawg-web-ui/web-ui/internal/ui/dialogs"
	"amneziawg-web-ui/web-ui/internal/ui/env"
	"amneziawg-web-ui/web-ui/internal/ui/widgets"
)

// SuspendLayout is how the auto-suspend time is typed and shown: local time,
// to the minute.
const SuspendLayout = "2006-01-02 15:04"

// ShowEditor opens the add/edit form. A nil client means "add new".
func ShowEditor(e *env.Env, server api.Server, client *api.Client) {
	editing := client != nil

	name := widgets.NewEntry()
	allowedIPs := widgets.EntryWithText("0.0.0.0/0, ::/0")
	suspendAt := widgets.EntryWithPlaceholder(SuspendLayout)

	applyI := widget.NewCheck(lang.L("Apply I-settings (custom signature packets I1-I5)"), nil)
	iEntries := make([]*widget.Entry, 5)
	for i := range iEntries {
		iEntries[i] = widget.NewEntry()
	}

	defaults := e.DefaultI()
	for i, entry := range iEntries {
		key := fmt.Sprintf("i%d", i+1)
		if value := defaults[key]; value != "" {
			entry.SetPlaceHolder(lang.L("server default: {{.Value}}", map[string]any{"Value": widgets.Truncate(value, 40)}))
		} else {
			entry.SetPlaceHolder(lang.L("leave empty to skip"))
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
			suspendAt.SetText(time.Unix(int64(*client.SuspendAt), 0).Local().Format(SuspendLayout))
		}
	} else {
		name.SetPlaceHolder(lang.L("New Client"))
	}

	iBox := container.NewVBox()
	for i, entry := range iEntries {
		iBox.Add(widgets.Labeled(fmt.Sprintf("I%d", i+1), entry))
	}
	// The operator's own standard: the same five values this dialog writes
	// per client, kept once as what every new client starts from. Both
	// buttons live inside iBox, so they appear and vanish with the checkbox.
	saveStandard := widgets.NewButton(lang.L("Save as my standard"), theme.DocumentSaveIcon(), func() {
		saveAsStandard(e, iEntries)
	})
	restoreStandard := widgets.NewButton(lang.L("Restore shipped defaults"), theme.ContentUndoIcon(), func() {
		dialogs.Confirm(e.Win, lang.L("Restore shipped defaults"),
			lang.L("Your saved standard will be replaced with the shipped defaults."),
			func() {
				putDefaultI(e, api.ISettings{}, lang.L("Shipped defaults restored"))
			})
	})
	iBox.Add(container.NewHBox(saveStandard, restoreStandard))
	iNote := widget.NewLabel(lang.L("Each value describes one packet sent before the handshake, as a sequence of tags: " +
		"<b 0x...> static bytes, <t> a 4-byte timestamp, <r N> random bytes, <rc N> random letters, <rd N> random digits " +
		"(N up to 1000). Example: <b 0xd100000001><rc 8><t><r 50>. These are client-only parameters, they are never " +
		"written into the server config; empty values are skipped, and an empty I1 turns the whole set off. Keep each " +
		"packet under about 1472 bytes or it is fragmented on the way out, which is the signature this is meant to avoid. " +
		"A config that grows past the QR code limit can still be downloaded as a file."))
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

	nameItem := &widget.FormItem{Text: lang.L("Client name"), Widget: name}
	if !editing {
		// The name is checked as it is typed, so the rule is stated here
		// rather than only once it is broken. An existing client keeps the
		// name it was given: the field is closed, and the name is not part
		// of what an edit saves.
		nameItem.HintText = lang.L("The characters {{.Chars}} are not accepted, and a name must not end with a dot or a space",
			map[string]any{"Chars": api.NameForbiddenList})
	}

	items := []*widget.FormItem{
		nameItem,
		{Text: lang.L("Allowed IPs"), Widget: allowedIPs,
			HintText: lang.L("comma-separated ranges routed through the VPN; default 0.0.0.0/0, ::/0")},
	}

	if editing {
		pick := widgets.NewButton("", theme.CalendarIcon(), func() { pickSuspendTime(e, suspendAt) })
		clear := widgets.NewButton("", theme.CancelIcon(), func() { suspendAt.SetText("") })
		items = append(items, &widget.FormItem{
			Text:     lang.L("Auto-suspend at"),
			Widget:   container.NewBorder(nil, nil, nil, container.NewHBox(pick, clear), suspendAt),
			HintText: lang.L("local time, format {{.Layout}}; empty disables auto-suspension", map[string]any{"Layout": SuspendLayout}),
		})
		created := time.Unix(int64(client.CreatedAt), 0).Local().Format("2006-01-02 15:04:05")
		items = append(items, &widget.FormItem{Text: lang.L("Created"), Widget: widget.NewLabel(created)})
	}

	content := container.NewVBox(widget.NewForm(items...), widgets.Separator(), applyI, iBox)

	title := lang.L("Add client to {{.Server}}", map[string]any{"Server": server.Name})
	confirm := lang.L("Add client")
	if editing {
		title = lang.L("Edit client {{.Name}}", map[string]any{"Name": client.Name})
		confirm = lang.L("Save")
	}

	dialogs.ShowForm(e.Win, title, confirm, lang.L("Cancel"), dialogs.Scrolled(content), dialogs.Size(e.Win, 760, 620), func() bool {
		var problems []string

		// The name lands in a file name, so it is checked as it was typed:
		// a name a desktop would refuse is turned down here rather than
		// reshaped on the way out. An edit does not carry a name, so only
		// a new client is checked here.
		if !editing {
			if problem := api.ValidateName(name.Text); problem != "" {
				problems = append(problems, lang.L(problem))
			}
		}

		settings, iProblems := collectISettings(iEntries)
		// Only worth checking what will actually be written: with the box
		// unticked the values are kept but never reach a config.
		if applyI.Checked {
			problems = append(problems, iProblems...)
		}
		if len(problems) > 0 {
			e.Notify.Fail(errors.New(strings.Join(problems, "\n")))
			return false
		}

		routes := strings.TrimSpace(allowedIPs.Text)
		if routes == "" {
			routes = "0.0.0.0/0, ::/0"
		}

		if editing {
			save(e, server.ID, *client, routes, applyI.Checked, settings, strings.TrimSpace(suspendAt.Text))
			return true
		}
		add(e, server, strings.TrimSpace(name.Text), routes, applyI.Checked, settings)
		return true
	})
}

// collectISettings reads the five I-fields the way every action in this
// dialog treats them: trimmed, an empty value skipped as "no opinion", and
// the rest checked with the shared key loop. It returns the values that
// would be written and the problems found in the others.
func collectISettings(iEntries []*widget.Entry) (api.ISettings, []string) {
	settings := api.ISettings{}
	var problems []string
	for i, entry := range iEntries {
		value := strings.TrimSpace(entry.Text)
		if value == "" {
			continue
		}
		key := fmt.Sprintf("I%d", i+1)
		if problem := api.ValidateCPS(key, value); problem != "" {
			problems = append(problems, problem)
			continue
		}
		settings[strings.ToLower(key)] = value
	}
	return settings, problems
}

// saveAsStandard makes the five fields the operator's own standard: the
// whole saved set is replaced by what they hold, and an empty field leaves
// the shipped default in place. Nothing is sent until the values pass the
// same rules the dialog's confirm applies, and a row where nothing is typed
// is a warning rather than a PUT - blank fields must not silently wipe what
// was saved.
func saveAsStandard(e *env.Env, iEntries []*widget.Entry) {
	settings, problems := collectISettings(iEntries)
	if len(problems) > 0 {
		e.Notify.Fail(errors.New(strings.Join(problems, "\n")))
		return
	}
	if len(settings) == 0 {
		e.Notify.Warn(lang.L("All five I1-I5 fields are empty - type the values to save, or use \"Restore shipped defaults\" to clear the saved standard"))
		return
	}
	putDefaultI(e, settings, lang.L("Standard I-settings saved"))
}

// putDefaultI replaces the saved standard and, once the backend confirms,
// refreshes the cached effective set so the next dialog opens with the new
// placeholders. The request runs off the UI goroutine; the notifier and the
// refresh are safe from any.
func putDefaultI(e *env.Env, settings api.ISettings, done string) {
	go func() {
		if err := e.Backend.PutDefaultISettings(settings); err != nil {
			e.Notify.Fail(err)
			return
		}
		e.RefreshDefaultI()
		e.Notify.OK(done)
	}()
}

// pickSuspendTime opens a calendar over the editor and writes the chosen
// moment back into the entry, which stays the one source of truth: typing a
// time by hand keeps working, and the picker starts from whatever the entry
// holds, or from now when it holds nothing parseable.
//
// Only the calendar comes from fyne-datepicker. Its NewDateTimePicker loses
// the hour and minute: they are written into a variable of their own, while
// the confirm callback reads the date picker's, so the time the user set never
// reaches it. The time row is built here instead and joined with the date on
// confirm.
//
// The calendar stays in English in both languages: its "datepicker.*" keys
// sit in the translation files with the English text only because lang.X
// logs a "Translation failure" for every key a language lacks. Translated
// month names would also break it - on the arrows it selects
// time.Month.String(), which is English and matches none of the options.
func pickSuspendTime(e *env.Env, suspendAt *widget.Entry) {
	when := time.Now().Truncate(time.Minute)
	if parsed, err := time.ParseInLocation(SuspendLayout, strings.TrimSpace(suspendAt.Text), time.Local); err == nil {
		when = parsed
	}

	hour := widget.NewSelectEntry(twoDigits(24))
	hour.SetText(when.Format("15"))
	minute := widget.NewSelectEntry(twoDigits(60))
	minute.SetText(when.Format("04"))
	// A select entry asks for the width of its text alone, which clips two
	// digits against the dropdown arrow.
	field := fyne.NewSize(80, hour.MinSize().Height)
	timeRow := container.NewHBox(widget.NewLabel(lang.L("Time")),
		container.NewGridWrap(field, hour), widget.NewLabel(":"), container.NewGridWrap(field, minute))

	var date time.Time
	calendar := datepicker.NewDatePicker(when, time.Monday, func(chosen time.Time, _ bool) { date = chosen })

	content := container.NewBorder(nil, timeRow, nil, nil, calendar)
	dialogs.ShowForm(e.Win, lang.L("Auto-suspend at"), lang.L("OK"), lang.L("Cancel"), content, dialogs.Size(e.Win, 460, 520), func() bool {
		h, errH := strconv.Atoi(strings.TrimSpace(hour.Text))
		m, errM := strconv.Atoi(strings.TrimSpace(minute.Text))
		if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			e.Notify.Fail(errors.New(lang.L("time must be hours 00-23 and minutes 00-59")))
			return false
		}
		calendar.OnActioned(true)
		chosen := time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, time.Local)
		suspendAt.SetText(chosen.Format(SuspendLayout))
		return true
	})
}

// twoDigits lists "00" up to n-1, the options of an hour or minute field.
func twoDigits(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%02d", i)
	}
	return out
}

func add(e *env.Env, server api.Server, name, allowedIPs string, applyI bool, settings api.ISettings) {
	if name == "" {
		name = lang.L("New Client")
	}

	go func() {
		err := e.Backend.AddClient(server.ID, api.AddClientRequest{
			Name:           name,
			ApplyISettings: applyI,
			ISettings:      settings,
			AllowedIPs:     allowedIPs,
		})
		if err != nil {
			e.Notify.Fail(err)
			return
		}
		e.Notify.OK(lang.L("Client \"{{.Name}}\" added", map[string]any{"Name": name}))
		e.Reload()
	}()
}

// save applies the three independent updates the backend exposes for an
// existing peer: routing, I-settings and the auto-suspend timestamp.
func save(e *env.Env, serverID string, client api.Client, allowedIPs string, applyI bool, settings api.ISettings, suspendAt string) {
	var when *time.Time
	if suspendAt != "" {
		parsed, err := time.ParseInLocation(SuspendLayout, suspendAt, time.Local)
		if err != nil {
			e.Notify.Fail(errors.New(lang.L("auto-suspend time must look like {{.Layout}}", map[string]any{"Layout": SuspendLayout})))
			return
		}
		when = &parsed
	}

	go func() {
		if err := e.Backend.UpdateAllowedIPs(serverID, client.ID, allowedIPs); err != nil {
			e.Notify.Fail(err)
			return
		}
		if err := e.Backend.UpdateISettings(serverID, client.ID, applyI, settings); err != nil {
			e.Notify.Fail(err)
			return
		}
		if err := e.Backend.UpdateSuspendTime(serverID, client.ID, when); err != nil {
			e.Notify.Fail(err)
			return
		}
		e.Notify.OK(lang.L("Client \"{{.Name}}\" updated", map[string]any{"Name": client.Name}))
		e.Reload()
	}()
}
