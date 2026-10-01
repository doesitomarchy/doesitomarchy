package web

//go:generate go run ../../tools/themegen

// themeChoice is one entry of the theme picker.
type themeChoice struct {
	Key  string
	Name string
	Dark bool
}
