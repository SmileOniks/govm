package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SmileOniks/govm/internal/doctor"
	"github.com/charmbracelet/colorprofile"
)

// newDoctorApp wires a fake Doctor operation. The output goes through
// a colorprofile.Writer the way main does; a bytes.Buffer is not a
// terminal, so the verdict colours are stripped and the expectations
// below stay plain text. Pass an explicit profile to keep them.
func newDoctorApp(report doctor.Report, calls *[]bool, profile ...colorprofile.Profile) (*App, *bytes.Buffer) {
	var out bytes.Buffer
	w := colorprofile.NewWriter(&out, nil)
	if len(profile) > 0 {
		w.Profile = profile[0]
	}
	app := NewApp(Operations{
		Doctor: func(_ context.Context, offline bool) doctor.Report {
			*calls = append(*calls, offline)
			return report
		},
	}, nil, w, w)
	return app, &out
}

func TestDoctorUnknownFlagIsUsageErrorWithoutRunningChecks(t *testing.T) {
	var calls []bool
	app, out := newDoctorApp(doctor.Report{}, &calls)

	if app.Doctor("--bogus") {
		t.Fatal("expected false for unknown flag")
	}
	if len(calls) != 0 {
		t.Fatalf("checks ran %d time(s), want 0", len(calls))
	}
	got := out.String()
	if got != "Error: unknown doctor option \"--bogus\"\nusage: govm doctor [--offline]\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestDoctorPassesOfflineFlag(t *testing.T) {
	var calls []bool
	app, _ := newDoctorApp(doctor.Report{}, &calls)

	if !app.Doctor("--offline") {
		t.Fatal("expected true for an empty report")
	}
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("calls = %v, want [true]", calls)
	}
}

func TestDoctorNotConfigured(t *testing.T) {
	var out bytes.Buffer
	app := NewApp(Operations{}, nil, &out, &out)
	if app.Doctor() {
		t.Fatal("expected false when doctor is not configured")
	}
	if !strings.Contains(out.String(), "doctor is not configured") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDoctorRendersSpecLayout(t *testing.T) {
	report := doctor.Report{
		GovmVersion: "1.4.0",
		OS:          "darwin",
		Arch:        "arm64",
		Root:        "/home/u/.govm",
		Checks: []doctor.Check{
			{Name: doctor.CheckShimInPath, Verdict: doctor.VerdictOK, Detail: "shim directory is in PATH"},
			{
				Name:    doctor.CheckGoResolvesToShim,
				Verdict: doctor.VerdictFail,
				Detail:  "`go` resolves to /usr/local/go/bin/go, not the govm shim",
				Hint:    `move "$HOME/.govm/shim" before /usr/local/go/bin in PATH`,
			},
			{Name: doctor.CheckActiveVersion, Verdict: doctor.VerdictOK, Detail: "active version 1.27.1 is installed"},
			{
				Name:    doctor.CheckSource,
				Verdict: doctor.VerdictWarn,
				Detail:  "source https://go.dev/dl/ unreachable: timeout after 5s",
				Hint:    "check network or run with --offline",
			},
			{
				Name:    doctor.CheckDisk,
				Verdict: doctor.VerdictWarn,
				Detail:  "disk: 3 versions (1.2 GB), downloads 250 MB",
				Hint:    "run `govm prune` to remove inactive versions and downloads",
			},
		},
	}
	var calls []bool
	app, out := newDoctorApp(report, &calls)

	if app.Doctor() {
		t.Fatal("expected false when a check failed")
	}
	if len(calls) != 1 || calls[0] {
		t.Fatalf("calls = %v, want [false]", calls)
	}

	want := strings.Join([]string{
		"govm doctor  (govm 1.4.0, darwin/arm64, root /home/u/.govm)",
		"",
		"[ok]   shim directory is in PATH",
		"[fail] `go` resolves to /usr/local/go/bin/go, not the govm shim",
		`       hint: move "$HOME/.govm/shim" before /usr/local/go/bin in PATH`,
		"[ok]   active version 1.27.1 is installed",
		"[warn] source https://go.dev/dl/ unreachable: timeout after 5s",
		"       hint: check network or run with --offline",
		"[warn] disk: 3 versions (1.2 GB), downloads 250 MB",
		"       hint: run `govm prune` to remove inactive versions and downloads",
		"",
		"1 fail, 2 warn",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDoctorAllOKEndsWithAllChecksPassed(t *testing.T) {
	report := doctor.Report{
		GovmVersion: "dev",
		OS:          "linux",
		Arch:        "amd64",
		Root:        "/home/u/.govm",
		Checks: []doctor.Check{
			{Name: doctor.CheckShimInPath, Verdict: doctor.VerdictOK, Detail: "shim directory is in PATH"},
			{Name: doctor.CheckSettings, Verdict: doctor.VerdictOK, Detail: "settings.json is valid"},
		},
	}
	var calls []bool
	app, out := newDoctorApp(report, &calls)

	if !app.Doctor() {
		t.Fatal("expected true when every check is ok")
	}
	want := strings.Join([]string{
		"govm doctor  (govm dev, linux/amd64, root /home/u/.govm)",
		"",
		"[ok]   shim directory is in PATH",
		"[ok]   settings.json is valid",
		"",
		"all checks passed",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDoctorWarnOnlyExitsZero(t *testing.T) {
	report := doctor.Report{
		GovmVersion: "dev", OS: "linux", Arch: "amd64", Root: "/r",
		Checks: []doctor.Check{
			{Name: doctor.CheckSource, Verdict: doctor.VerdictWarn, Detail: "source x unreachable: boom", Hint: "h"},
		},
	}
	var calls []bool
	app, out := newDoctorApp(report, &calls)

	if !app.Doctor() {
		t.Fatal("warn must not change the exit code")
	}
	if !strings.HasSuffix(out.String(), "\n0 fail, 1 warn\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDoctorHeaderWithoutRoot(t *testing.T) {
	report := doctor.Report{GovmVersion: "dev", OS: "linux", Arch: "amd64"}
	var calls []bool
	app, out := newDoctorApp(report, &calls)
	app.Doctor()
	if !strings.HasPrefix(out.String(), "govm doctor  (govm dev, linux/amd64, root unknown)\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDoctorColoursVerdictsOnTerminal(t *testing.T) {
	report := doctor.Report{
		GovmVersion: "dev", OS: "linux", Arch: "amd64", Root: "/r",
		Checks: []doctor.Check{
			{Name: doctor.CheckShimInPath, Verdict: doctor.VerdictOK, Detail: "ok detail"},
			{Name: doctor.CheckSource, Verdict: doctor.VerdictWarn, Detail: "warn detail", Hint: "h"},
			{Name: doctor.CheckGoResolvesToShim, Verdict: doctor.VerdictFail, Detail: "fail detail", Hint: "h"},
		},
	}
	var calls []bool
	app, out := newDoctorApp(report, &calls, colorprofile.ANSI)
	app.Doctor()

	got := out.String()
	for _, want := range []string{
		"\x1b[32m[ok]\x1b[m   ok detail\n",
		"\x1b[33m[warn]\x1b[m warn detail\n",
		"\x1b[31m[fail]\x1b[m fail detail\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") && !strings.Contains(got, "       hint: h\n") {
		t.Errorf("hint lines must stay uncoloured and aligned:\n%q", got)
	}
}

func TestDoctorStripsColoursWhenNotATerminal(t *testing.T) {
	report := doctor.Report{
		GovmVersion: "dev", OS: "linux", Arch: "amd64", Root: "/r",
		Checks: []doctor.Check{
			{Name: doctor.CheckGoResolvesToShim, Verdict: doctor.VerdictFail, Detail: "fail detail", Hint: "h"},
		},
	}
	var calls []bool
	app, out := newDoctorApp(report, &calls)
	app.Doctor()
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("expected no escape sequences on a non-terminal writer:\n%q", out.String())
	}
}
