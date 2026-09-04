package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// The UI ships with a single dark palette, so every colour below is returned
// regardless of the variant the toolkit asks for.
var (
	colBackground  = color.NRGBA{R: 0x0f, G: 0x13, B: 0x1a, A: 0xff}
	colSurface     = color.NRGBA{R: 0x17, G: 0x1d, B: 0x27, A: 0xff}
	colSurfaceAlt  = color.NRGBA{R: 0x1e, G: 0x26, B: 0x33, A: 0xff}
	colBorder      = color.NRGBA{R: 0x2a, G: 0x34, B: 0x44, A: 0xff}
	colText        = color.NRGBA{R: 0xe6, G: 0xeb, B: 0xf2, A: 0xff}
	colMuted       = color.NRGBA{R: 0x8b, G: 0x97, B: 0xa8, A: 0xff}
	colPrimary     = color.NRGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0xff}
	colSuccess     = color.NRGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff}
	colWarning     = color.NRGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff}
	colError       = color.NRGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff}
	colAccent      = color.NRGBA{R: 0xa7, G: 0x8b, B: 0xfa, A: 0xff}
	colHover       = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}
	colPressed     = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1e}
	colSelection   = color.NRGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0x55}
	colScrollBar   = color.NRGBA{R: 0x3a, G: 0x46, B: 0x5a, A: 0xcc}
	colShadow      = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x66}
	colDisabled    = color.NRGBA{R: 0x5c, G: 0x67, B: 0x77, A: 0xff}
	colDisabledBtn = color.NRGBA{R: 0x1a, G: 0x21, B: 0x2c, A: 0xff}
)

// darkTheme wraps the default theme and forces the dark variant everywhere,
// so the app looks the same no matter what the browser reports for
// prefers-color-scheme.
type darkTheme struct {
	fyne.Theme
}

func NewDarkTheme() fyne.Theme {
	return &darkTheme{Theme: theme.DefaultTheme()}
}

func (t *darkTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameButton:
		return colSurfaceAlt
	case theme.ColorNameDisabledButton:
		return colDisabledBtn
	case theme.ColorNameDisabled:
		return colDisabled
	case theme.ColorNameForeground, theme.ColorNameForegroundOnPrimary,
		theme.ColorNameForegroundOnError, theme.ColorNameForegroundOnSuccess,
		theme.ColorNameForegroundOnWarning:
		return colText
	case theme.ColorNameHeaderBackground:
		return colSurface
	case theme.ColorNameHover:
		return colHover
	case theme.ColorNameHyperlink:
		return colPrimary
	case theme.ColorNameInputBackground:
		return colSurfaceAlt
	case theme.ColorNameInputBorder:
		return colBorder
	case theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return colSurface
	case theme.ColorNamePlaceHolder:
		return colMuted
	case theme.ColorNamePressed:
		return colPressed
	case theme.ColorNamePrimary:
		return colPrimary
	case theme.ColorNameScrollBar:
		return colScrollBar
	case theme.ColorNameScrollBarBackground:
		return colBackground
	case theme.ColorNameSelection:
		return colSelection
	case theme.ColorNameSeparator:
		return colBorder
	case theme.ColorNameShadow:
		return colShadow
	case theme.ColorNameSuccess:
		return colSuccess
	case theme.ColorNameWarning:
		return colWarning
	case theme.ColorNameError:
		return colError
	}

	return t.Theme.Color(name, theme.VariantDark)
}

func (t *darkTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 6
	}

	return t.Theme.Size(name)
}
