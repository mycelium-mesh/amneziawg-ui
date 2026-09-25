package e2e

import "testing"

func TestSmoke(t *testing.T) {
	a := startApp(t)

	visible, err := a.page.Locator("canvas").IsVisible()
	must(t, err)
	if !visible {
		t.Error("the canvas is not visible")
	}
	a.noPageErrors()

	status := getJSON[map[string]any](t, "/api/system/status")
	if _, ok := status["public_ip"]; !ok {
		t.Errorf("/api/system/status has no public_ip: %v", status)
	}

	// Fyne sets the document title from the window title once it is running.
	title, err := a.page.Title()
	must(t, err)
	if title != "AmneziaWG Web UI" {
		t.Errorf("title = %q, want %q", title, "AmneziaWG Web UI")
	}

	a.shot("01-loaded")
}
