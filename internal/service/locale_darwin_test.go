//go:build darwin

// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bytes"
	"os/exec"
	"testing"
)

// Read existing macOS preferences; never change the machine's language settings.
// Parser fixtures separately cover Japanese, English, unknown and failed values.
func TestNativeLocaleFallback(t *testing.T) {
	raw, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	preferred := ""
	if err == nil {
		preferred = parseAppleLanguages(string(raw))
	}
	if got := macPreferredLanguage(); got != preferred {
		t.Fatalf("native preferred-language query returned %q, want %q", got, preferred)
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(name, "")
	}
	var out bytes.Buffer
	if err := Run([]string{"help"}, nil, &out, "test"); err != nil {
		t.Fatal(err)
	}
	if out.String() != tr(localeLanguage(preferred), usage) {
		t.Fatal("native locale fallback did not use the preferred language")
	}
}
