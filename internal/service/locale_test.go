// SPDX-License-Identifier: AGPL-3.0-or-later
package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestLocaleSelection(t *testing.T) {
	cases := []struct {
		name, override, all, messages, lang, goos, preferred string
		want                                                 language
		calls                                                int
	}{
		{"all wins", "auto", "ja_JP.UTF-8", "en_US", "en_US", "darwin", "en-US", japanese, 0},
		{"messages wins", "auto", "", "ja-JP", "en_US", "darwin", "en-US", japanese, 0},
		{"lang", "auto", "", "", "JA_JP.UTF-8@modifier", "linux", "", japanese, 0},
		{"explicit Japanese", "ja", "C", "", "", "darwin", "en-US", japanese, 0},
		{"explicit English", "en", "ja_JP", "", "", "darwin", "ja-JP", english, 0},
		{"C authoritative", "auto", "C.UTF-8", "ja_JP", "ja_JP", "darwin", "ja-JP", english, 0},
		{"POSIX authoritative", "auto", "", "POSIX", "ja_JP", "darwin", "ja-JP", english, 0},
		{"unknown authoritative", "auto", "fr_FR", "ja_JP", "ja_JP", "darwin", "ja-JP", english, 0},
		{"unknown not prefix", "auto", "japanese", "", "", "darwin", "ja-JP", english, 0},
		{"Mac fallback", "auto", "", "", "", "darwin", "ja-JP", japanese, 1},
		{"Mac unknown fallback", "auto", "", "", "", "darwin", "fr-FR", english, 1},
		{"Mac failed fallback", "auto", "", "", "", "darwin", "", english, 1},
		{"non-Mac fallback", "auto", "", "", "", "linux", "ja-JP", english, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{"LC_ALL": c.all, "LC_MESSAGES": c.messages, "LANG": c.lang}
			calls := 0
			got := resolveLanguage(c.override, func(k string) string { return env[k] }, c.goos, func() string { calls++; return c.preferred })
			if got != c.want || calls != c.calls {
				t.Fatalf("got %q (%d fallback calls), want %q (%d)", got, calls, c.want, c.calls)
			}
		})
	}
}

func TestAppleLanguagesParsingAndOutputBound(t *testing.T) {
	for input, want := range map[string]string{"(\n    \"ja-JP\",\n    \"en-US\"\n)": "ja-JP", "(en, ja)": "en", "()": "", "(\"fr-FR\",ja)": "fr-FR", "defaults: failed": "", "(ja; echo bad)": "", "(\"\",ja)": ""} {
		if got := parseAppleLanguages(input); got != want {
			t.Errorf("%q: %q, want %q", input, got, want)
		}
	}
	var b localeBuffer
	if _, err := b.Write(bytes.Repeat([]byte("a"), 4096)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("b")); err == nil || b.Len() != 4096 {
		t.Fatal("unbounded locale output")
	}
}

func TestLanguageArgumentsPreserveValues(t *testing.T) {
	cases := []struct {
		args, rest []string
		lang       string
	}{
		{[]string{"--lang", "ja", "setup", "--address", "host.example"}, []string{"setup", "--address", "host.example"}, "ja"},
		{[]string{"setup", "--lang=en", "--data-dir", "/a 日本 space"}, []string{"setup", "--data-dir", "/a 日本 space"}, "en"},
		{[]string{"setup", "--address", "--lang", "--lang", "ja"}, []string{"setup", "--address", "--lang"}, "ja"},
		{[]string{"setup", "--data-dir=--lang=ja", "--lang=auto"}, []string{"setup", "--data-dir=--lang=ja"}, "auto"},
		{[]string{"setup", "--", "--lang", "ja"}, []string{"setup", "--", "--lang", "ja"}, "auto"},
	}
	for _, c := range cases {
		got, lang, err := languageArgs(c.args)
		if err != nil || lang != c.lang || !reflect.DeepEqual(got, c.rest) {
			t.Fatalf("%v => %v %s %v", c.args, got, lang, err)
		}
	}
	for _, args := range [][]string{{"--lang"}, {"--lang="}, {"--lang=fr"}, {"--lang", "JA"}} {
		if _, _, err := languageArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestLocalizedHelpAndErrors(t *testing.T) {
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	for _, args := range [][]string{{"help"}, {"--help"}, {"setup", "--help"}, {"help", "setup"}, {"start", "--help"}, {"status", "--help"}} {
		var out bytes.Buffer
		if err := Run(args, strings.NewReader(""), &out, "test"); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out.String(), "rustdesk-server") || out.String() == usage || out.String() == setupUsage {
			t.Fatalf("not localized: %v: %s", args, out.String())
		}
	}
	for _, args := range [][]string{{"bad-command"}, {"setup", "--wat"}, {"setup", "--address"}, {"setup", "--yes=maybe"}, {"setup", "--yes"}, {"status", "unexpected"}, {"--lang"}, {"--lang=fr"}} {
		err := Run(args, strings.NewReader(""), &bytes.Buffer{}, "test")
		if err == nil || !strings.HasPrefix(ErrorMessage(err), "エラー") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if runtime.GOOS != "darwin" {
		err := Run([]string{"setup", "--address", "192.168.1.20", "--yes"}, strings.NewReader(""), &bytes.Buffer{}, "test")
		if err == nil || strings.Contains(err.Error(), "requires macOS") || !strings.Contains(err.Error(), "macOS") {
			t.Fatalf("unlocalized platform failure: %v", err)
		}
	}
	var en, ja bytes.Buffer
	if err := Run([]string{"--lang", "en", "help"}, nil, &en, "test"); err != nil {
		t.Fatal(err)
	}
	if en.String() != usage {
		t.Fatal("explicit English did not win")
	}
	if err := Run([]string{"--lang", "ja", "version"}, nil, &ja, "literal-dev"); err != nil {
		t.Fatal(err)
	}
	if ja.String() != "rustdesk-server literal-dev (upstream 1.1.16)\n" {
		t.Fatalf("version modified: %q", ja.String())
	}
}

func TestConcurrentInvocationsKeepLocale(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lang := english
			if i%2 == 1 {
				lang = japanese
			}
			var out bytes.Buffer
			if err := Run([]string{"--lang", string(lang), "help"}, nil, &out, "test"); err != nil || out.String() != tr(lang, usage) {
				t.Errorf("locale leaked: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestLocalizedErrorsPreserveValuesAndCauses(t *testing.T) {
	raw := errors.New("raw stopped /tmp/running/日本語 100.100.1.1 %s")
	err := problem("%w; rollback also failed: %v; inspect status before retrying", problem("runtime missing; rerun setup: %w", raw), problem("private key missing; restore a backup before start"))
	got := renderError(japanese, err)
	if !strings.Contains(got, raw.Error()) || strings.Contains(got, "rollback also failed") || strings.Contains(got, "%!") || !errors.Is(err, raw) {
		t.Fatal(got)
	}
	wrapped := &localizedFailure{japanese, errors.Join(err, errSetupCancelled)}
	if !errors.Is(wrapped, errSetupCancelled) || !strings.Contains(wrapped.Error(), "\n") {
		t.Fatal("error chain lost")
	}
	for _, message := range []string{"refusing non-regular file: %s", "not executable: %s", "services did not become ready; inspect logs under %s"} {
		value := "/tmp/Started; will start automatically at login./日本語"
		if got := renderError(japanese, problem(message, value)); !strings.Contains(got, value) {
			t.Fatal("literal value translated: " + got)
		}
	}
}

func TestLocalizedSetupCancellationAndRetry(t *testing.T) {
	for _, lang := range []language{english, japanese} {
		for _, input := range []string{"cancel\n", "取消\n", "キャンセル\n", "manual\ncancel\n", "manual\nvalid.example\ncancel\n", "manual\nvalid.example\n\ncancel\n", "manual\n127.0.0.1\nvalid.example\n\nn\n"} {
			t.Run(string(lang)+strings.ReplaceAll(input, "\n", "_"), func(t *testing.T) {
				m, _, _, _ := fixture(t)
				m.Language = lang
				var out bytes.Buffer
				if err := setupCLIWithDiscovery(m, nil, strings.NewReader(input), &out, "test", testAddressDiscovery(true)); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(m.Root); !os.IsNotExist(err) {
					t.Fatal("cancel changed service files")
				}
				if !strings.Contains(out.String(), tr(lang, "Cancelled; no changes made.")) {
					t.Fatal(out.String())
				}
				if strings.Contains(input, "127.0.0.1") && !strings.Contains(out.String(), tr(lang, "choose a client-reachable non-loopback address")) {
					t.Fatal("missing retry explanation")
				}
			})
		}
	}
}

func TestLocalizedStatusStartStopAndStableConfig(t *testing.T) {
	for _, lang := range []language{english, japanese} {
		t.Run(string(lang), func(t *testing.T) {
			m, _, c, src := fixture(t)
			m.Language = lang
			var out bytes.Buffer
			if err := showStatus(m, &out); err != nil || !strings.Contains(out.String(), tr(lang, "Not configured. Run rustdesk-server setup.")) {
				t.Fatalf("empty status: %v %s", err, &out)
			}
			if err := m.Setup(c, src); err != nil {
				t.Fatal(err)
			}
			configBefore := mustRead(t, m.configPath())
			keyBefore := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
			for _, cmd := range []string{"status", "stop", "start", "start"} {
				out.Reset()
				if err := runManaged(m, []string{cmd}, nil, &out, "test"); err != nil {
					t.Fatal(err)
				}
				if cmd != "stop" {
					for _, value := range []string{c.Address + ":21116", c.Address + ":21117", c.DataDir, c.PackageVersion, string(mustRead(t, filepath.Join(c.DataDir, "id_ed25519.pub"))), tr(lang, "running")} {
						if !strings.Contains(out.String(), value) {
							t.Fatalf("%s missing %q: %s", cmd, value, &out)
						}
					}
				}
				if strings.Contains(out.String(), string(keyBefore)) {
					t.Fatal("private key output")
				}
			}
			if !bytes.Equal(configBefore, mustRead(t, m.configPath())) || !bytes.Equal(keyBefore, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
				t.Fatal("locale changed config or identity")
			}
			var config map[string]any
			if err := json.Unmarshal(configBefore, &config); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"schema", "address", "data_dir", "package_version"} {
				if _, ok := config[key]; !ok {
					t.Fatalf("JSON field changed: %s", key)
				}
			}
			if err := m.Stop(); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			err := showStatus(m, &out)
			if err == nil || !strings.Contains(out.String(), tr(lang, "stopped")) {
				t.Fatalf("stopped status: %v %s", err, &out)
			}
		})
	}
}

func TestLocalizedEOFAndPlanSafetyWarnings(t *testing.T) {
	for _, lang := range []language{english, japanese} {
		t.Run(string(lang), func(t *testing.T) {
			m, _, c, _ := fixture(t)
			m.Language = lang
			var out bytes.Buffer
			err := setupCLIWithDiscovery(m, []string{"--address", c.Address, "--data-dir", c.DataDir}, strings.NewReader(""), &out, "test", noAddressDiscovery(t))
			if err == nil || !strings.Contains(renderError(lang, err), tr(lang, "input ended before confirmation; no service changes made (use explicit flags and --yes for automation)")) {
				t.Fatalf("EOF: %v", err)
			}
			for _, required := range []string{c.Address + ":21116", c.DataDir, "TCP 21115–21119", "UDP 21116", "Ed25519", "hbbs", "Tailscale"} {
				if !strings.Contains(out.String(), required) {
					t.Fatalf("plan omitted %s", required)
				}
			}
			if _, err := os.Stat(m.Root); !os.IsNotExist(err) {
				t.Fatal("EOF changed files")
			}
		})
	}
}

// Prevent future English-only service messages and accidental format drift.
func TestJapaneseCatalogCoverage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "messages_ja.go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			index := -1
			if id.Name == "problem" {
				index = 0
			}
			if id.Name == "tr" {
				index = 1
			}
			if index < 0 || len(call.Args) <= index {
				return true
			}
			lit, ok := call.Args[index].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			message, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := japaneseMessages[message]; !ok {
				t.Errorf("%s missing translation: %q", name, message)
			}
			return true
		})
	}
	for _, message := range []string{usage, setupUsage, clientReachabilityNotice, "stopped", "loaded, not running", "running", "Tailscale IPv4", "Tailscale IPv6", "MagicDNS hostname; requires client DNS"} {
		if _, ok := japaneseMessages[message]; !ok {
			t.Errorf("missing dynamic template: %q", message)
		}
	}
	placeholders := regexp.MustCompile(`%(?:\[[0-9]+\])?[+# .0-9-]*[a-zA-Z%]`)
	for original, translated := range japaneseMessages {
		if translated == "" {
			t.Errorf("empty translation: %s", original)
		}
		if !reflect.DeepEqual(placeholders.FindAllString(original, -1), placeholders.FindAllString(translated, -1)) {
			t.Errorf("format mismatch: %q => %q", original, translated)
		}
	}
}

func TestPromptCancelSentinel(t *testing.T) {
	_, err := prompt(bufio.NewReader(strings.NewReader("cancel\n")), &bytes.Buffer{}, "label", "")
	if !errors.Is(err, errSetupCancelled) {
		t.Fatal(err)
	}
}

func TestRawFailureHasLocalizedRemediation(t *testing.T) {
	raw := &os.PathError{Op: "open", Path: "/tmp/running 日本語/config.json", Err: os.ErrPermission}
	for _, lang := range []language{english, japanese} {
		err := &localizedFailure{lang, raw}
		got := ErrorMessage(err)
		if !strings.Contains(got, raw.Error()) || !strings.Contains(got, "rustdesk-server status") || !errors.Is(err, os.ErrPermission) {
			t.Fatal(got)
		}
		if lang == japanese && strings.Contains(got, "Operation failed") {
			t.Fatal(got)
		}
	}
}

func TestLocalizedSetupSuccessRepeatAndRollback(t *testing.T) {
	for _, lang := range []language{english, japanese} {
		t.Run(string(lang), func(t *testing.T) {
			m, fake, c, src := fixture(t)
			m.Language = lang
			m.executable = func() (string, error) { return filepath.Join(src, "rustdesk-server"), nil }
			args := []string{"--address", c.Address, "--data-dir", c.DataDir}
			var out bytes.Buffer
			if err := setupCLIWithDiscovery(m, args, strings.NewReader("はい\n"), &out, "localized-test", noAddressDiscovery(t)); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tr(lang, "\nSetup complete. Existing data and server identity are kept on later setup runs.")) {
				t.Fatal(out.String())
			}
			before := mustRead(t, m.configPath())
			key := mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))
			out.Reset()
			if err := setupCLIWithDiscovery(m, nil, strings.NewReader("y\n"), &out, "localized-test", noAddressDiscovery(t)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, mustRead(t, m.configPath())) || !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
				t.Fatal("repeated setup changed config/identity")
			}
			fake.failName = "hbbr"
			out.Reset()
			err := setupCLIWithDiscovery(m, append(args, "--yes"), nil, &out, "next-test", noAddressDiscovery(t))
			if err == nil || !strings.Contains(renderError(lang, err), strings.Split(tr(lang, "%w; previous service configuration restored; data and keys preserved"), "%w")[1]) {
				t.Fatalf("rollback not localized: %v", err)
			}
			if !bytes.Equal(before, mustRead(t, m.configPath())) || !bytes.Equal(key, mustRead(t, filepath.Join(c.DataDir, "id_ed25519"))) {
				t.Fatal("failed setup lost config/identity")
			}
		})
	}
}
