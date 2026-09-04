// Command web-ui is the AmneziaWG Web UI frontend: a Fyne application
// compiled to WebAssembly and served, together with its loader page, by the
// Go/Fiber backend it talks to.
package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"amneziawg-web-ui/web-ui/internal/ui"
)

func main() {
	application := app.NewWithID("io.amnezia.webui")

	// One theme, always dark - the page is served with a matching dark
	// loader, so the app never flashes a light background.
	application.Settings().SetTheme(ui.NewDarkTheme())

	window := application.NewWindow("AmneziaWG Web UI")
	frontend := ui.New(application, window)
	window.SetContent(frontend.Build())
	window.Resize(fyne.NewSize(1280, 900))

	// Loading starts once the toolkit is running, so the first fyne.Do calls
	// always find a live event loop.
	application.Lifecycle().SetOnStarted(frontend.Start)

	window.ShowAndRun()
}
