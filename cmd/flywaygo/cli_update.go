package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const flywayMavenMetadataURL = "https://repo1.maven.org/maven2/org/flywaydb/flyway-core/maven-metadata.xml"
const flywayStayingUpToDateURL = "https://help.red-gate.com/help/flyway-cli13/help_13.aspx?topic=release-notes-and-older-versions/release-notes-for-flyway-engine"

// flywayUpdateCheck is replaced in tests so golden CLI output does not depend
// on Maven Central latency. The default matches MavenVersionChecker.
var flywayUpdateCheck = fetchMavenFlywayUpdate

// flywayUpdateCheckSync makes the check complete before the command continues.
// Production stays asynchronous, matching MavenVersionChecker.
var flywayUpdateCheckSync bool

type flywayUpdateOutcome struct {
	message string
	err     error
}

type flywayUpdateHandle struct {
	result  <-chan flywayUpdateOutcome
	settled bool
}

func shouldCheckForFlywayUpdate(parsed userCLIArgs) bool {
	if parsed.outputJSON || parsed.flags["-skipCheckForUpdate"] {
		return false
	}
	return !userCLIHasOperation(parsed.operations, "mcp")
}

func beginFlywayUpdateCheck(parsed userCLIArgs) *flywayUpdateHandle {
	if !shouldCheckForFlywayUpdate(parsed) {
		return nil
	}
	result := make(chan flywayUpdateOutcome, 1)
	if flywayUpdateCheckSync {
		message, err := flywayUpdateCheck()
		result <- flywayUpdateOutcome{message: message, err: err}
		return &flywayUpdateHandle{result: result}
	}
	go func() {
		message, err := flywayUpdateCheck()
		result <- flywayUpdateOutcome{message: message, err: err}
	}()
	return &flywayUpdateHandle{result: result}
}

// printIfReady matches Main.printUpdateMessage: the update line is emitted
// only when the check has already finished. Failures are DEBUG, like the
// checker logging them before returning null.
func (h *flywayUpdateHandle) printIfReady(logger *userCLILogger) {
	if h == nil || h.settled || logger == nil {
		return
	}
	h.settled = true
	select {
	case outcome := <-h.result:
		if outcome.err != nil {
			logger.Debug("Unable to check for updates: %s", outcome.err.Error())
			return
		}
		if outcome.message != "" {
			logger.Info("%s", outcome.message)
		}
	default:
	}
}

func fetchMavenFlywayUpdate() (string, error) {
	body, err := getFlywayMavenMetadata()
	if err != nil {
		return "", err
	}
	latest, err := mavenReleaseVersion(body)
	if err != nil {
		return "", err
	}
	current := flywayOSSVersion
	if dash := strings.IndexByte(current, '-'); dash >= 0 {
		current = current[:dash]
	}
	newer, err := flywayVersionLess(current, latest)
	if err != nil {
		return "", err
	}
	if !newer {
		return "", nil
	}
	return flywayUpdateMessage(latest), nil
}

func getFlywayMavenMetadata() (string, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, flywayMavenMetadataURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Flyway")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("maven metadata status %s", response.Status)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

var mavenReleasePattern = regexp.MustCompile(`<release>\s*([^<\s]+)\s*</release>`)

func mavenReleaseVersion(metadata string) (string, error) {
	match := mavenReleasePattern.FindStringSubmatch(metadata)
	if match == nil {
		return "", fmt.Errorf("maven metadata did not contain a release version")
	}
	return match[1], nil
}

func flywayUpdateMessage(latest string) string {
	message := "\nA more recent version of Flyway is available. Find out more about Flyway " + latest + " at " + flywayStayingUpToDateURL + "\n"
	border := strings.Repeat("-", len(strings.TrimSpace(message)))
	return "\n" + border + message + border
}

func flywayVersionLess(current, latest string) (bool, error) {
	left, err := flywayVersionParts(current)
	if err != nil {
		return false, err
	}
	right, err := flywayVersionParts(latest)
	if err != nil {
		return false, err
	}
	width := len(left)
	if len(right) > width {
		width = len(right)
	}
	for len(left) < width {
		left = append(left, 0)
	}
	for len(right) < width {
		right = append(right, 0)
	}
	for i := 0; i < width; i++ {
		if left[i] < right[i] {
			return true, nil
		}
		if left[i] > right[i] {
			return false, nil
		}
	}
	return false, nil
}

func flywayVersionParts(version string) ([]int, error) {
	if strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("empty flyway version")
	}
	raw := strings.Split(version, ".")
	parts := make([]int, 0, len(raw))
	for _, item := range raw {
		value, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("invalid flyway version %q", version)
		}
		parts = append(parts, value)
	}
	return parts, nil
}
