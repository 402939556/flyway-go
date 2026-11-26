package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	flywayUpdateCheck = func() (string, error) { return "", nil }
	m.Run()
}

func TestFlywayUpdateMessageAndVersion(t *testing.T) {
	latest, err := mavenReleaseVersion("<metadata><versioning><release>13.20.1</release></versioning></metadata>")
	if err != nil {
		t.Fatal(err)
	}
	if latest != "13.20.1" {
		t.Fatalf("release = %s", latest)
	}
	newer, err := flywayVersionLess("13.13.0", latest)
	if err != nil || !newer {
		t.Fatalf("newer = %v %v", newer, err)
	}
	same, err := flywayVersionLess("13.13.0", "13.13")
	if err != nil || same {
		t.Fatalf("same = %v %v", same, err)
	}
	message := flywayUpdateMessage(latest)
	if !strings.Contains(message, "Find out more about Flyway 13.20.1 at "+flywayStayingUpToDateURL) {
		t.Fatalf("message = %q", message)
	}
	trimmed := strings.TrimSpace(strings.Split(message, "\n")[2])
	border := strings.Repeat("-", len(trimmed))
	if !strings.HasPrefix(message, "\n"+border+"\n") || !strings.HasSuffix(message, "\n"+border) {
		t.Fatalf("border mismatch: %q", message)
	}
}

func TestUserCLIPrintsFinishedUpdateCheck(t *testing.T) {
	previous := flywayUpdateCheck
	previousSync := flywayUpdateCheckSync
	t.Cleanup(func() {
		flywayUpdateCheck = previous
		flywayUpdateCheckSync = previousSync
	})
	flywayUpdateCheckSync = true
	flywayUpdateCheck = func() (string, error) { return flywayUpdateMessage("99.0.0"), nil }

	deps := officialCLIGoldenDependencies(t)
	var stdout, stderr bytes.Buffer
	if code := runUserCLI([]string{"version"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "A more recent version of Flyway is available") || !strings.Contains(stdout.String(), "Flyway 99.0.0") {
		t.Fatalf("stdout = %s", stdout.String())
	}

	stdout.Reset()
	if code := runUserCLI([]string{"version", "-skipCheckForUpdate"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("skip code = %d", code)
	}
	if strings.Contains(stdout.String(), "more recent version") {
		t.Fatalf("skip still printed update:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := runUserCLI([]string{"version", "-outputType=json"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("json code = %d", code)
	}
	if strings.Contains(stdout.String(), "more recent version") {
		t.Fatalf("json printed update:\n%s", stdout.String())
	}
}

func TestUserCLIUpdateFailureIsDebug(t *testing.T) {
	previous := flywayUpdateCheck
	previousSync := flywayUpdateCheckSync
	t.Cleanup(func() {
		flywayUpdateCheck = previous
		flywayUpdateCheckSync = previousSync
	})
	flywayUpdateCheckSync = true
	flywayUpdateCheck = func() (string, error) { return "", errUpdateCheck }

	deps := officialCLIGoldenDependencies(t)
	var stdout, stderr bytes.Buffer
	if code := runUserCLI([]string{"version", "-X"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stdout.String(), "DEBUG: Unable to check for updates: maven metadata unavailable") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

type updateError string

func (e updateError) Error() string { return string(e) }

const errUpdateCheck updateError = "maven metadata unavailable"
