package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const userCLINewline = "\r\n"

const (
	ansiRed         = "\x1b[31m"
	ansiYellow      = "\x1b[33m"
	ansiGreen       = "\x1b[32m"
	ansiBlue        = "\x1b[34m"
	ansiBrightBlack = "\x1b[90m"
	ansiReset       = "\x1b[m"
)

// cliColorForced overrides console detection. Tests set it; production leaves it nil.
var cliColorForced *bool

type userCLIOutputOptions struct {
	outputJSON       bool
	outputLogsInJSON bool
	quiet            bool
	debug            bool
	debugRequested   bool
	outputFile       string
	color            string
}

type userCLILogger struct {
	stdout     io.Writer
	stderr     io.Writer
	file       *os.File
	quiet      bool
	debug      bool
	outputJSON bool
	jsonLogs   bool
	color      string
	silent     bool
}

func scanUserCLIOutputOptions(args []string) userCLIOutputOptions {
	opts := userCLIOutputOptions{}
	for _, arg := range args {
		if arg == "-q" {
			opts.quiet = true
			continue
		}
		if arg == "-X" {
			opts.debugRequested = true
			continue
		}
		if !isConfigurationArg(arg) {
			continue
		}
		name, value := splitConfigurationArg(arg)
		switch name {
		case "outputType":
			opts.outputJSON = strings.EqualFold(value, "json")
		case "outputLogsInJson":
			opts.outputLogsInJSON = strings.EqualFold(value, "true")
		case "outputFile":
			opts.outputFile = value
		case "color":
			opts.color = value
		}
	}
	opts.debug = opts.debugRequested && !opts.quiet
	return opts
}

func newUserCLILogger(opts userCLIOutputOptions, stdout, stderr io.Writer) (*userCLILogger, error) {
	logger := &userCLILogger{
		stdout:     stdout,
		stderr:     stderr,
		quiet:      opts.quiet,
		debug:      opts.debug,
		outputJSON: opts.outputJSON,
		jsonLogs:   opts.outputLogsInJSON,
		color:      opts.color,
	}
	if logger.outputJSON || opts.outputFile == "" {
		return logger, nil
	}
	file, err := os.OpenFile(opts.outputFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("Could not initialize log file at %s.", opts.outputFile)
	}
	logger.file = file
	return logger, nil
}

func (l *userCLILogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *userCLILogger) Debug(format string, args ...interface{}) {
	if l.silent || !l.debug {
		return
	}
	message := fmt.Sprintf(format, args...)
	if l.outputJSON {
		l.writeJSON(message, "DEBUG")
		return
	}
	l.writeConsole(l.stdout, ansiBrightBlack, "DEBUG: ", message)
	l.writeFile("DEBUG", message)
}

func (l *userCLILogger) Info(format string, args ...interface{}) {
	if l.silent {
		return
	}
	message := fmt.Sprintf(format, args...)
	if l.quiet && !l.jsonLogs {
		return
	}
	if l.outputJSON {
		l.writeJSON(message, "INFO")
		return
	}
	l.writeConsole(l.stdout, infoColor(message), "", message)
	l.writeFile("", message)
}

func (l *userCLILogger) Notice(format string, args ...interface{}) {
	if l.silent {
		return
	}
	message := fmt.Sprintf(format, args...)
	if l.outputJSON {
		l.writeJSON(message, "NOTICE")
		return
	}
	if l.quiet {
		return
	}
	l.writeConsole(l.stdout, ansiBlue, "", message)
}

func (l *userCLILogger) Warn(format string, args ...interface{}) {
	if l.silent {
		return
	}
	message := fmt.Sprintf(format, args...)
	if l.outputJSON {
		l.writeJSON(message, "WARN")
		return
	}
	l.writeConsole(l.stdout, ansiYellow, "WARNING: ", message)
	l.writeFile("WARNING", message)
}

func (l *userCLILogger) Error(format string, args ...interface{}) {
	if l.silent {
		return
	}
	message := fmt.Sprintf(format, args...)
	if l.outputJSON {
		l.writeJSON(message, "ERROR")
		return
	}
	stream := l.errorStream()
	l.writeConsole(stream, ansiRed, "ERROR: ", message)
	l.writeFile("ERROR", message)
}

// ErrorWithStack mirrors the Java CLI's DEBUG error path: the logger prefixes
// the top-level message, then the exception stack trace is emitted verbatim.
func (l *userCLILogger) ErrorWithStack(message, stack string) {
	if l.silent {
		return
	}
	if l.outputJSON {
		l.Error("%s", message)
		return
	}
	stream := l.errorStream()
	colored := l.shouldColor(stream)
	if colored {
		_, _ = io.WriteString(stream, ansiRed)
	}
	l.writePlain(stream, "ERROR: ", message)
	_, _ = io.WriteString(stream, stack)
	if colored {
		_, _ = io.WriteString(stream, ansiReset)
	}
	if l.file != nil {
		l.writeFile("ERROR", message)
		_, _ = io.WriteString(l.file, stack)
		_, _ = io.WriteString(l.file, "\n")
	}
}

// errorStream matches AnsiConsole.systemInstall for color=always.
// jansi wraps both standard streams. When stdout is not a console, the
// wrapped stderr follows that redirected stdout, so console errors show up
// on stdout with ANSI stripped. A real console keeps errors on stderr.
func (l *userCLILogger) errorStream() io.Writer {
	if l.color != "always" {
		return l.stderr
	}
	if cliColorForced != nil {
		if *cliColorForced {
			return l.stderr
		}
		return l.stdout
	}
	if file, ok := l.stdout.(*os.File); ok && fdIsConsole(file) {
		return l.stderr
	}
	return l.stdout
}
func (l *userCLILogger) writeConsole(out io.Writer, color, prefix, message string) {
	if color != "" && l.shouldColor(out) {
		_, _ = io.WriteString(out, color)
		l.writePlain(out, prefix, message)
		_, _ = io.WriteString(out, ansiReset)
		return
	}
	l.writePlain(out, prefix, message)
}

func (l *userCLILogger) writePlain(out io.Writer, prefix, message string) {
	_, _ = io.WriteString(out, prefix+message+userCLINewline)
}

func infoColor(message string) string {
	if strings.HasPrefix(message, "Successfully") {
		return ansiGreen
	}
	return ""
}

// shouldColor matches Flyway OSS with jansi 3.30.4.
// ColorizedConsoleLog always prints ANSI, then AnsiConsole strips those codes
// unless the destination is a real console. Flyway sets the legacy property
// jansi.force=true for color=always; jansi 3.30.4 ignores it and still strips
// redirected streams. auto additionally requires System.console()!=null, which
// is true only when both stdin and stdout are consoles.
func (l *userCLILogger) shouldColor(out io.Writer) bool {
	if l == nil || l.outputJSON || out == nil {
		return false
	}
	mode := l.color
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "never":
		return false
	case "always":
		if cliColorForced != nil {
			return *cliColorForced
		}
		file, ok := out.(*os.File)
		return ok && fdIsConsole(file)
	default:
		if cliColorForced != nil {
			return *cliColorForced && (out == os.Stdout || out == l.stdout)
		}
		if out != os.Stdout && out != os.Stderr {
			return false
		}
		return fdIsConsole(os.Stdin) && fdIsConsole(os.Stdout)
	}
}

func (l *userCLILogger) writeFile(prefix, message string) {
	if l.file == nil {
		return
	}
	if prefix != "" {
		message = prefix + ": " + message
	}
	_, _ = io.WriteString(l.file, message+"\n")
}

func (l *userCLILogger) writeJSON(message, level string) {
	if !l.jsonLogs {
		return
	}
	for _, line := range javaSplitLines(message) {
		payload, err := marshalCompactJSON(struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}{Level: level, Message: line})
		if err != nil {
			continue
		}
		_, _ = io.WriteString(l.stderr, payload+userCLINewline)
	}
}

func javaSplitLines(message string) []string {
	lines := strings.Split(message, "\n")
	for len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func marshalCompactJSON(value interface{}) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func prettyUserCLIErrorJSON(errorCode, message string) string {
	encodedMessage, err := marshalCompactJSON(message)
	if err != nil {
		encodedMessage = `""`
	}
	return "{\r\n" +
		`  "error" : {` + "\r\n" +
		`    "errorCode" : ` + quoteJSONString(errorCode) + "," + "\r\n" +
		`    "message" : ` + encodedMessage + "," + "\r\n" +
		`    "cause" : null` + "\r\n" +
		"  }\r\n" +
		"}"
}

func quoteJSONString(value string) string {
	encoded, err := marshalCompactJSON(value)
	if err != nil {
		return `""`
	}
	return encoded
}

func userCLIErrorCode(message string) string {
	if strings.Contains(message, "Unable to connect to the database") {
		return "CONFIGURATION"
	}
	return "ERROR"
}

func stripFinalCRLF(value []byte) []byte {
	if bytes.HasSuffix(value, []byte(userCLINewline)) {
		return value[:len(value)-len(userCLINewline)]
	}
	return value
}
