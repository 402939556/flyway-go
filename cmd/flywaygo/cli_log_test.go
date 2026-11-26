package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestColorAlwaysOnBufferIsStrippedLikeJansi(t *testing.T) {
	logger, err := newUserCLILogger(userCLIOutputOptions{color: "always"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	logger.stdout = &stdout
	logger.stderr = &stderr
	logger.Warn("%s", "plain")
	logger.Error("%s", "boom")
	logger.Info("%s", "Successfully applied")
	logger.Notice("%s", "note")
	logger.debug = true
	logger.Debug("%s", "detail")
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "\x1b") {
		t.Fatalf("redirected color=always should be stripped, got %q", combined)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "WARNING: plain\r\n") || !strings.Contains(stdout.String(), "ERROR: boom\r\n") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestColorSequencesWhenConsoleForced(t *testing.T) {
	forced := true
	cliColorForced = &forced
	t.Cleanup(func() { cliColorForced = nil })

	var stdout, stderr bytes.Buffer
	logger, err := newUserCLILogger(userCLIOutputOptions{color: "always", debug: true}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("%s", "detail")
	logger.Info("%s", "Successfully applied")
	logger.Info("%s", "Flyway OSS")
	logger.Notice("%s", "note")
	logger.Warn("%s", "careful")
	logger.Error("%s", "boom")
	logger.ErrorWithStack("boom", "stack line\r\n")

	wantStdout := "\x1b[90mDEBUG: detail\r\n\x1b[m" +
		"\x1b[32mSuccessfully applied\r\n\x1b[m" +
		"Flyway OSS\r\n" +
		"\x1b[34mnote\r\n\x1b[m" +
		"\x1b[33mWARNING: careful\r\n\x1b[m"
	if stdout.String() != wantStdout {
		t.Fatalf("stdout = %q", stdout.String())
	}
	wantStderr := "\x1b[31mERROR: boom\r\n\x1b[m" +
		"\x1b[31mERROR: boom\r\nstack line\r\n\x1b[m"
	if stderr.String() != wantStderr {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestColorAutoDoesNotPaintStderrOrBuffers(t *testing.T) {
	forced := true
	cliColorForced = &forced
	t.Cleanup(func() { cliColorForced = nil })

	var stdout, stderr bytes.Buffer
	logger, err := newUserCLILogger(userCLIOutputOptions{color: "auto"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("%s", "careful")
	logger.Error("%s", "boom")
	if stdout.String() != "\x1b[33mWARNING: careful\r\n\x1b[m" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "ERROR: boom\r\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}

	never, err := newUserCLILogger(userCLIOutputOptions{color: "never"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	never.stdout = &out
	never.Warn("%s", "careful")
	if out.String() != "WARNING: careful\r\n" {
		t.Fatalf("never = %q", out.String())
	}
}

func TestColorJSONAndFileStayPlain(t *testing.T) {
	forced := true
	cliColorForced = &forced
	t.Cleanup(func() { cliColorForced = nil })

	var stderr bytes.Buffer
	logger, err := newUserCLILogger(userCLIOutputOptions{color: "always", outputJSON: true, outputLogsInJSON: true}, &bytes.Buffer{}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("%s", "careful")
	if strings.Contains(stderr.String(), "\x1b") || !strings.Contains(stderr.String(), `"WARN"`) {
		t.Fatalf("json = %q", stderr.String())
	}
}
