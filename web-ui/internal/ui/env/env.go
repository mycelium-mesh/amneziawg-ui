// Package env is what the page hands every component below it. A card, a
// row or a dialog deep in the tree needs the same handful of things - the
// window to open dialogs on, the backend to talk to, a way to report back -
// and passing them once, as one value, keeps the constructors down the tree
// from repeating the list.
package env

import (
	"fyne.io/fyne/v2"

	"amneziawg-web-ui/web-ui/api"
	"amneziawg-web-ui/web-ui/internal/ui/backend"
)

// Env is the page seen from a component: dependencies only, no state of the
// component's own and no widgets.
type Env struct {
	Win     fyne.Window
	Backend *backend.Client
	Notify  Notifier

	// Reload asks the page to fetch the server list again and re-render what
	// changed. Every action that alters what the backend holds calls it once
	// the call has come back. Safe from any goroutine.
	Reload func()

	// DefaultI is the I1-I5 set the backend gives a new client, as last
	// reported by it. The client editor shows it as placeholders.
	DefaultI func() api.ISettings

	// RefreshDefaultI re-reads that set from the backend, for the actions
	// that change it (saving or restoring the operator's standard), so the
	// next dialog opens with the new placeholders. Safe from any goroutine.
	RefreshDefaultI func()
}

// Notifier is the page's feedback channel: a transient note in the footer
// for outcomes, a dialog for failures. All three are safe from any goroutine.
//
// The message is shown as given: callers localise it with lang.L, which does
// the substitution as well, so there is no format to fill in here.
type Notifier interface {
	OK(message string)
	Warn(message string)
	Fail(err error)
}
