package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/crashreport"
	"reasonix/internal/i18n"
	"reasonix/internal/netclient"
)

func isolateCLIReports(t *testing.T) string {
	t.Helper()
	i18n.DetectLanguage("en")
	t.Cleanup(func() { i18n.DetectLanguage("en") })
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("REASONIX_SAFE_MODE", "")
	t.Chdir(t.TempDir())
	return home
}

func captureCLIReport(t *testing.T, home string) crashreport.Pending {
	t.Helper()
	if err := crashreport.CapturePanic(home, "v1.20.0", "private panic value", []byte("goroutine 1 [running]:\nreasonix.run()\n\t/Users/alice/reasonix/main.go:12")); err != nil {
		t.Fatal(err)
	}
	pending, err := crashreport.Load(home, "")
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestReportCommandPreviewDoesNotSendNonInteractive(t *testing.T) {
	home := isolateCLIReports(t)
	pending := captureCLIReport(t, home)
	previous := sendCLIReport
	t.Cleanup(func() { sendCLIReport = previous })
	sendCLIReport = func(context.Context, crashreport.Report, netclient.ProxySpec) error {
		t.Fatal("non-interactive preview attempted upload")
		return nil
	}

	var out, errOut bytes.Buffer
	if rc := reportCommandWithIO(nil, false, strings.NewReader("y\n"), &out, &errOut); rc != 0 {
		t.Fatalf("report rc=%d stderr=%q", rc, errOut.String())
	}
	if !strings.Contains(out.String(), pending.ID) || !strings.Contains(out.String(), "Preview only") || !strings.Contains(out.String(), "<path>/main.go:12") {
		t.Fatalf("preview output:\n%s", out.String())
	}
	if _, err := crashreport.Load(home, pending.ID); err != nil {
		t.Fatalf("preview removed report: %v", err)
	}
}

func TestReportCommandInteractiveConfirmationPrivacyOptOut(t *testing.T) {
	home := isolateCLIReports(t)
	pending := captureCLIReport(t, home)
	previous := sendCLIReport
	t.Cleanup(func() { sendCLIReport = previous })
	calls := 0
	sendCLIReport = func(_ context.Context, report crashreport.Report, _ netclient.ProxySpec) error {
		calls++
		return nil
	}

	var out, errOut bytes.Buffer
	if rc := reportCommandWithIO(nil, true, strings.NewReader("y\n"), &out, &errOut); rc != 0 {
		t.Fatalf("report rc=%d stderr=%q", rc, errOut.String())
	}
	if calls != 0 {
		t.Fatalf("privacy opt-out must not upload (calls=%d)", calls)
	}
	if _, err := crashreport.Load(home, pending.ID); !errors.Is(err, crashreport.ErrNoReports) {
		t.Fatalf("privacy opt-out must clear the local report: %v", err)
	}
}

func TestReportCommandPrivacyOptOutClearsReport(t *testing.T) {
	home := isolateCLIReports(t)
	pending := captureCLIReport(t, home)

	var out, errOut bytes.Buffer
	if rc := reportCommandWithIO([]string{"send", pending.ID}, false, strings.NewReader(""), &out, &errOut); rc != 0 {
		t.Fatalf("send rc=%d", rc)
	}
	if !strings.Contains(out.String(), "Deleted CLI crash report") {
		t.Fatalf("output=%q", out.String())
	}
	if _, err := crashreport.Load(home, pending.ID); !errors.Is(err, crashreport.ErrNoReports) {
		t.Fatalf("privacy opt-out must clear the local report: %v", err)
	}
}

func TestLegacySafeModeEnvPrivacyOptOutDoesNotUpload(t *testing.T) {
	home := isolateCLIReports(t)
	pending := captureCLIReport(t, home)
	t.Setenv("REASONIX_SAFE_MODE", "1")
	previous := sendCLIReport
	t.Cleanup(func() { sendCLIReport = previous })
	calls := 0
	sendCLIReport = func(context.Context, crashreport.Report, netclient.ProxySpec) error {
		calls++
		return nil
	}

	var out, errOut bytes.Buffer
	if rc := reportCommandWithIO([]string{"send", pending.ID}, false, strings.NewReader(""), &out, &errOut); rc != 0 {
		t.Fatalf("legacy-env send rc=%d stderr=%q", rc, errOut.String())
	}
	if calls != 0 {
		t.Fatalf("privacy opt-out upload calls=%d, want 0", calls)
	}
}

func TestReportCommandListShowAndNoReports(t *testing.T) {
	home := isolateCLIReports(t)
	pending := captureCLIReport(t, home)
	var out, errOut bytes.Buffer
	if rc := reportCommandWithIO([]string{"list"}, false, strings.NewReader(""), &out, &errOut); rc != 0 || !strings.Contains(out.String(), pending.ID) {
		t.Fatalf("list rc=%d out=%q err=%q", rc, out.String(), errOut.String())
	}
	out.Reset()
	if rc := reportCommandWithIO([]string{"show", pending.ID}, false, strings.NewReader(""), &out, &errOut); rc != 0 || !strings.Contains(out.String(), `"source": "cli.go"`) {
		t.Fatalf("show rc=%d out=%q err=%q", rc, out.String(), errOut.String())
	}
	if err := crashreport.Remove(home, pending.ID); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := reportCommandWithIO(nil, false, strings.NewReader(""), &out, &errOut); rc != 0 || !strings.Contains(out.String(), "No pending") {
		t.Fatalf("empty rc=%d out=%q err=%q", rc, out.String(), errOut.String())
	}
}
