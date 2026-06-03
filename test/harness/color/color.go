// Package color provides minimal ANSI color helpers for harness log output.
// Colors are disabled automatically when NO_COLOR is set or TERM=dumb so that
// CI pipelines and piped output stay clean plain text.
package color

import (
	"fmt"
	"os"
)

var enabled = os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"

func Cyan(s string) string   { return wrap(s, "36") }
func Green(s string) string  { return wrap(s, "32") }
func Yellow(s string) string { return wrap(s, "33") }
func Red(s string) string    { return wrap(s, "31") }
func Blue(s string) string   { return wrap(s, "34") }
func Bold(s string) string   { return wrap(s, "1") }

// Status returns code as a colored string: green=2xx, yellow=4xx, red=5xx+.
func Status(code int) string {
	s := fmt.Sprintf("%d", code)
	switch {
	case code >= 200 && code < 300:
		return Green(s)
	case code >= 400 && code < 500:
		return Yellow(s)
	default:
		return Red(s)
	}
}

func wrap(s, code string) string {
	if !enabled {
		return s
	}
	return fmt.Sprintf("\033[%sm%s\033[0m", code, s)
}
