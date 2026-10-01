// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Language belongs to one invocation; no process-global mutable locale is used.
type language string

const (
	english  language = "en"
	japanese language = "ja"
)

func localeLanguage(value string) language {
	value = strings.ToLower(strings.TrimSpace(value))
	if i := strings.IndexAny(value, "_.-@"); i >= 0 {
		value = value[:i]
	}
	if value == "ja" {
		return japanese
	}
	return english
}

func resolveLanguage(override string, getenv func(string) string, goos string, preferred func() string) language {
	if override == "ja" {
		return japanese
	}
	if override == "en" {
		return english
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return localeLanguage(value)
		}
	}
	if goos == "darwin" {
		return localeLanguage(preferred())
	}
	return english
}

// Read only the first OS language when the shell has not selected a locale.
// C/POSIX and unknown shell locales deliberately remain English.
func macPreferredLanguage() string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", "AppleLanguages")
	var output localeBuffer
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if cmd.Run() != nil {
		return ""
	}
	return parseAppleLanguages(output.String())
}

type localeBuffer struct{ bytes.Buffer }

func (b *localeBuffer) Write(p []byte) (int, error) {
	if len(p) > 4096-b.Len() {
		return 0, errors.New("locale output limit")
	}
	return b.Buffer.Write(p)
}
func parseAppleLanguages(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return ""
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "("), ")"))
	first, _, _ := strings.Cut(s, ",")
	first = strings.Trim(strings.TrimSpace(first), `"`)
	if first == "" {
		return ""
	}
	for _, r := range first {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return first
}

// Global flags can precede or follow the command. Never interpret an address or
// path value as a flag, and preserve everything after a literal -- separator.
func languageArgs(args []string) ([]string, string, error) {
	var rest []string
	selected := "auto"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if arg == "--address" || arg == "-address" || arg == "--data-dir" || arg == "-data-dir" {
			rest = append(rest, arg)
			if i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
			continue
		}
		if arg == "--lang" || arg == "-lang" {
			if i+1 == len(args) {
				return rest, selected, problem("--lang requires ja, en, or auto; run rustdesk-server help")
			}
			i++
			selected = args[i]
		} else if strings.HasPrefix(arg, "--lang=") || strings.HasPrefix(arg, "-lang=") {
			_, selected, _ = strings.Cut(arg, "=")
		} else {
			rest = append(rest, arg)
			continue
		}
		if selected != "auto" && selected != "ja" && selected != "en" {
			return rest, "auto", problem("invalid --lang value %q; choose ja, en, or auto", selected)
		}
	}
	return rest, selected, nil
}

func tr(lang language, message string) string {
	if lang == japanese {
		if translated, ok := japaneseMessages[message]; ok {
			return translated
		}
	}
	return message
}

// A typed message separates human text from paths, addresses, keys and other
// literal values. Translate the template, never search/replace rendered output.
type messageError struct {
	message string
	args    []any
}

func problem(message string, args ...any) error { return &messageError{message, args} }
func (e *messageError) Error() string           { return renderMessage(english, e) }
func (e *messageError) Unwrap() []error {
	var causes []error
	for _, arg := range e.args {
		if err, ok := arg.(error); ok {
			causes = append(causes, err)
		}
	}
	return causes
}
func renderMessage(lang language, e *messageError) string {
	args := append([]any(nil), e.args...)
	for i, arg := range args {
		if err, ok := arg.(error); ok {
			args[i] = renderError(lang, err)
		}
	}
	return fmt.Sprintf(strings.ReplaceAll(tr(lang, e.message), "%w", "%v"), args...)
}
func renderError(lang language, err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*messageError); ok {
		return renderMessage(lang, e)
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		var parts []string
		for _, cause := range e.Unwrap() {
			parts = append(parts, renderError(lang, cause))
		}
		return strings.Join(parts, "\n")
	}
	if lang != japanese {
		return err.Error()
	}
	// Preserve raw OS/upstream diagnostics verbatim and label them clearly. This
	// also avoids translating user-controlled text embedded in a native error.
	return fmt.Sprintf(tr(lang, "System detail: %s"), err.Error())
}

type localizedFailure struct {
	language language
	cause    error
}

func (e *localizedFailure) Error() string {
	var explained *messageError
	if errors.As(e.cause, &explained) {
		return renderError(e.language, e.cause)
	}
	return fmt.Sprintf(tr(e.language, "Operation failed. Review the diagnostic below; check rustdesk-server status and the logs before retrying.\nSystem detail: %s"), e.cause.Error())
}
func (e *localizedFailure) Unwrap() error { return e.cause }

// ErrorMessage adds the label in the same locale chosen for the invocation.
func ErrorMessage(err error) string {
	lang := english
	if e, ok := err.(*localizedFailure); ok {
		lang = e.language
	}
	return tr(lang, "Error: ") + err.Error()
}

func invocationLanguage(selected string) language {
	return resolveLanguage(selected, os.Getenv, runtime.GOOS, macPreferredLanguage)
}
