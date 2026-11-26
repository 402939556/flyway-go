package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/linsu/flywaygo/core"
)

func TestUserCLIOfficialGoldenBytes(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		probeDir   string
		stdoutFile string
		stderrFile string
		wantCode   int
	}{
		{name: "noargs", probeDir: "official-cli-probe4", stdoutFile: "noargs.out", stderrFile: "noargs.err", wantCode: 0},
		{name: "help", args: []string{"help"}, probeDir: "official-cli-probe4", stdoutFile: "help.out", stderrFile: "help.err", wantCode: 0},
		{name: "dash help is invalid", args: []string{"-help"}, probeDir: "official-cli-probe4", stdoutFile: "dashhelp.out", stderrFile: "dashhelp.err", wantCode: 1},
		{name: "h with bad flag", args: []string{"-h", "--bad"}, probeDir: "official-cli-probe4", stdoutFile: "hbad.out", stderrFile: "hbad.err", wantCode: 1},
		{name: "help with bad flag", args: []string{"help", "--bad"}, probeDir: "official-cli-probe4", stdoutFile: "helpbad.out", stderrFile: "helpbad.err", wantCode: 1},
		{name: "quiet version", args: []string{"version", "-q"}, probeDir: "official-cli-probe4", stdoutFile: "versionq.out", stderrFile: "versionq.err", wantCode: 0},
		{name: "version json", args: []string{"version", "-outputType=json"}, probeDir: "official-cli-probe4", stdoutFile: "versionjson.out", stderrFile: "versionjson.err", wantCode: 0},
		{name: "version output type case insensitive", args: []string{"version", "-outputType=JSON"}, probeDir: "official-cli-probe4", stdoutFile: "outputupper.out", stderrFile: "outputupper.err", wantCode: 0},
		{name: "list engines json", args: []string{"list-engines", "-outputType=json"}, probeDir: "official-cli-probe4", stdoutFile: "listjson.out", stderrFile: "listjson.err", wantCode: 0},
		{name: "multiple version operations", args: []string{"version", "list-engines"}, probeDir: "official-cli-probe4", stdoutFile: "multi.out", stderrFile: "multi.err", wantCode: 0},
		{name: "invalid output type", args: []string{"version", "-outputType=xml"}, probeDir: "official-cli-probe4", stdoutFile: "outputbad.out", stderrFile: "outputbad.err", wantCode: 1},
		{name: "invalid color", args: []string{"version", "-color=Always"}, probeDir: "official-cli-probe4", stdoutFile: "colorbad.out", stderrFile: "colorbad.err", wantCode: 1},
		{name: "invalid upper color", args: []string{"version", "-color=ALWAYS"}, probeDir: "official-cli-probe4", stdoutFile: "colorupper.out", stderrFile: "colorupper.err", wantCode: 1},
		{name: "auth commercial", args: []string{"auth"}, probeDir: "official-cli-probe4", stdoutFile: "auth.out", stderrFile: "auth.err", wantCode: 35},
		{name: "undo commercial", args: []string{"undo"}, probeDir: "official-cli-probe3", stdoutFile: "undo.stdout.txt", stderrFile: "undo.stderr.txt", wantCode: 35},
		{name: "deploy commercial", args: []string{"deploy"}, probeDir: "official-cli-probe4", stdoutFile: "deploy.out", stderrFile: "deploy.err", wantCode: 35},
		{name: "check commercial", args: []string{"check"}, probeDir: "official-cli-probe4", stdoutFile: "check.out", stderrFile: "check.err", wantCode: 35},
		{name: "diff commercial", args: []string{"diff"}, probeDir: "official-cli-probe4", stdoutFile: "diff.out", stderrFile: "diff.err", wantCode: 35},
		{name: "diffText commercial", args: []string{"diffText"}, probeDir: "official-cli-probe4", stdoutFile: "diffText.out", stderrFile: "diffText.err", wantCode: 35},
		{name: "generate commercial", args: []string{"generate"}, probeDir: "official-cli-probe4", stdoutFile: "generate.out", stderrFile: "generate.err", wantCode: 35},
		{name: "init commercial", args: []string{"init"}, probeDir: "official-cli-probe4", stdoutFile: "init.out", stderrFile: "init.err", wantCode: 35},
		{name: "model commercial", args: []string{"model"}, probeDir: "official-cli-probe4", stdoutFile: "model.out", stderrFile: "model.err", wantCode: 35},
		{name: "prepare commercial", args: []string{"prepare"}, probeDir: "official-cli-probe4", stdoutFile: "prepare.out", stderrFile: "prepare.err", wantCode: 35},
		{name: "migrate missing url", args: []string{"migrate"}, probeDir: "official-cli-probe4", stdoutFile: "migrate.out", stderrFile: "migrate.err", wantCode: 1},
		{name: "info missing url", args: []string{"info"}, probeDir: "official-cli-probe4", stdoutFile: "info.out", stderrFile: "info.err", wantCode: 1},
		{name: "validate missing url", args: []string{"validate"}, probeDir: "official-cli-probe4", stdoutFile: "validate.out", stderrFile: "validate.err", wantCode: 1},
		{name: "baseline missing url", args: []string{"baseline"}, probeDir: "official-cli-probe4", stdoutFile: "baseline.out", stderrFile: "baseline.err", wantCode: 1},
		{name: "repair missing url", args: []string{"repair"}, probeDir: "official-cli-probe4", stdoutFile: "repair.out", stderrFile: "repair.err", wantCode: 1},
		{name: "clean missing url", args: []string{"clean"}, probeDir: "official-cli-probe4", stdoutFile: "clean.out", stderrFile: "clean.err", wantCode: 1},
		{name: "unknown environment parameter", args: []string{"migrate", "-environment=prod", "-environments.prod.notARealSetting=value"}, probeDir: "official-cli-probe4", stdoutFile: "envunknown.out", stderrFile: "envunknown.err", wantCode: 1},
		{name: "spaced equals", args: []string{"migrate", "-url", "=", "postgres://u:p@localhost:5432/db"}, probeDir: "official-cli-probe4", stdoutFile: "equalssplit.out", stderrFile: "equalssplit.err", wantCode: 1},
		{name: "period hint", args: []string{"migrate", "-url", ".postgres://u:p@localhost:5432/db"}, probeDir: "official-cli-probe4", stdoutFile: "dotquote.out", stderrFile: "dotquote.err", wantCode: 1},
		{name: "environment name only", args: []string{"migrate", "-environments.prod=x"}, probeDir: "official-cli-probe4", stdoutFile: "envmissing.out", stderrFile: "envmissing.err", wantCode: 1},
		{name: "help json", args: []string{"help", "-outputType=json"}, probeDir: "official-cli-probe4", stdoutFile: "helpjson.out", stderrFile: "helpjson.err", wantCode: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expectedStdout := readProbeBytes(t, tc.probeDir, tc.stdoutFile)
			expectedStderr := readProbeBytes(t, tc.probeDir, tc.stderrFile)

			var stdout, stderr bytes.Buffer
			code := runUserCLI(tc.args, &stdout, &stderr, officialCLIGoldenDependencies(t))
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d", code, tc.wantCode)
			}
			assertGoldenBytes(t, "stdout", stdout.Bytes(), expectedStdout)
			assertGoldenBytes(t, "stderr", stderr.Bytes(), expectedStderr)
		})
	}
}

func TestUserCLIOfficialGoldenBytesExtended(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		probeDir     string
		stdoutFile   string
		stderrFile   string
		codeFile     string
		outputFile   string
		outputGolden string
	}{
		{name: "help check", args: []string{"help", "check"}, probeDir: "official-cli-probe5", stdoutFile: "help-check.stdout.txt", stderrFile: "help-check.stderr.txt", codeFile: "help-check.code.txt"},
		{name: "help list engines", args: []string{"help", "list-engines"}, probeDir: "official-cli-probe5", stdoutFile: "help-list-engines.stdout.txt", stderrFile: "help-list-engines.stderr.txt", codeFile: "help-list-engines.code.txt"},
		{name: "help mcp", args: []string{"help", "mcp"}, probeDir: "official-cli-probe5", stdoutFile: "help-mcp.stdout.txt", stderrFile: "help-mcp.stderr.txt", codeFile: "help-mcp.code.txt"},
		{name: "help migrate", args: []string{"help", "migrate"}, probeDir: "official-cli-probe5", stdoutFile: "help-migrate.stdout.txt", stderrFile: "help-migrate.stderr.txt", codeFile: "help-migrate.code.txt"},
		{name: "help version", args: []string{"help", "version"}, probeDir: "official-cli-probe5", stdoutFile: "help-version.stdout.txt", stderrFile: "help-version.stderr.txt", codeFile: "help-version.code.txt"},
		{name: "list engines help", args: []string{"list-engines", "-h"}, probeDir: "official-cli-probe5", stdoutFile: "list-engines-h.stdout.txt", stderrFile: "list-engines-h.stderr.txt", codeFile: "list-engines-h.code.txt"},
		{name: "migrate dash h", args: []string{"migrate", "-h"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-h.stdout.txt", stderrFile: "migrate-h.stderr.txt", codeFile: "migrate-h.code.txt"},
		{name: "migrate help", args: []string{"migrate", "--help"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-help.stdout.txt", stderrFile: "migrate-help.stderr.txt", codeFile: "migrate-help.code.txt"},
		{name: "migrate json logs", args: []string{"migrate", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-json-logs.stdout.txt", stderrFile: "migrate-json-logs.stderr.txt", codeFile: "migrate-json-logs.code.txt"},
		{name: "migrate json output file", args: []string{"migrate", "-outputType=json"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-json-outfile.stdout.txt", stderrFile: "migrate-json-outfile.stderr.txt", codeFile: "migrate-json-outfile.code.txt", outputFile: "migrate-json-outfile.log", outputGolden: "migrate-json-outfile.log"},
		{name: "migrate json", args: []string{"migrate", "-outputType=json"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-json.stdout.txt", stderrFile: "migrate-json.stderr.txt", codeFile: "migrate-json.code.txt"},
		{name: "migrate logs", args: []string{"migrate"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-logs.stdout.txt", stderrFile: "migrate-logs.stderr.txt", codeFile: "migrate-logs.code.txt"},
		{name: "migrate output file", args: []string{"migrate"}, probeDir: "official-cli-probe5", stdoutFile: "migrate-outfile.stdout.txt", stderrFile: "migrate-outfile.stderr.txt", codeFile: "migrate-outfile.code.txt", outputFile: "migrate-outfile.log", outputGolden: "migrate-outfile.log"},
		{name: "version h", args: []string{"version", "-h"}, probeDir: "official-cli-probe5", stdoutFile: "version-h.stdout.txt", stderrFile: "version-h.stderr.txt", codeFile: "version-h.code.txt"},
		{name: "version json output file", args: []string{"version", "-outputType=json"}, probeDir: "official-cli-probe5", stdoutFile: "version-json-outfile.stdout.txt", stderrFile: "version-json-outfile.stderr.txt", codeFile: "version-json-outfile.code.txt", outputFile: "version-json-outfile.log", outputGolden: "version-json-outfile.log"},
		{name: "version output file", args: []string{"version"}, probeDir: "official-cli-probe5", stdoutFile: "version-outfile.stdout.txt", stderrFile: "version-outfile.stderr.txt", codeFile: "version-outfile.code.txt", outputFile: "version-outfile.log", outputGolden: "version-outfile.log"},

		{name: "bad flag output file", args: []string{"--bad"}, probeDir: "official-cli-probe6", stdoutFile: "bad-outfile.stdout.txt", stderrFile: "bad-outfile.stderr.txt", codeFile: "bad-outfile.code.txt", outputFile: "bad.log", outputGolden: "bad.log"},
		{name: "bad color always", args: []string{"version", "-color=bad", "-color=always"}, probeDir: "official-cli-probe6", stdoutFile: "colorbad-always.stdout.txt", stderrFile: "colorbad-always.stderr.txt", codeFile: "colorbad-always.code.txt"},
		{name: "dash dash help", args: []string{"--help"}, probeDir: "official-cli-probe6", stdoutFile: "dash-help.stdout.txt", stderrFile: "dash-help.stderr.txt", codeFile: "dash-help.code.txt"},
		{name: "dash question", args: []string{"-?"}, probeDir: "official-cli-probe6", stdoutFile: "dash-question.stdout.txt", stderrFile: "dash-question.stderr.txt", codeFile: "dash-question.code.txt"},
		{name: "help color always", args: []string{"help", "-color=always"}, probeDir: "official-cli-probe6", stdoutFile: "help-color-always.stdout.txt", stderrFile: "help-color-always.stderr.txt", codeFile: "help-color-always.code.txt"},
		{name: "help output file", args: []string{"help"}, probeDir: "official-cli-probe6", stdoutFile: "help-outfile.stdout.txt", stderrFile: "help-outfile.stderr.txt", codeFile: "help-outfile.code.txt", outputFile: "help.log", outputGolden: "help.log"},
		{name: "migrate color always", args: []string{"migrate", "-color=always"}, probeDir: "official-cli-probe6", stdoutFile: "migrate-color-always.stdout.txt", stderrFile: "migrate-color-always.stderr.txt", codeFile: "migrate-color-always.code.txt"},
		{name: "noargs output file", args: nil, probeDir: "official-cli-probe6", stdoutFile: "noargs-outfile.stdout.txt", stderrFile: "noargs-outfile.stderr.txt", codeFile: "noargs-outfile.code.txt", outputFile: "noargs.log", outputGolden: "noargs.log"},
		{name: "version color always", args: []string{"version", "-color=always"}, probeDir: "official-cli-probe6", stdoutFile: "version-color-always.stdout.txt", stderrFile: "version-color-always.stderr.txt", codeFile: "version-color-always.code.txt"},
		{name: "version color auto", args: []string{"version", "-color=auto"}, probeDir: "official-cli-probe6", stdoutFile: "version-color-auto.stdout.txt", stderrFile: "version-color-auto.stderr.txt", codeFile: "version-color-auto.code.txt"},
		{name: "version color never", args: []string{"version", "-color=never"}, probeDir: "official-cli-probe6", stdoutFile: "version-color-never.stdout.txt", stderrFile: "version-color-never.stderr.txt", codeFile: "version-color-never.code.txt"},
		{name: "version logs json output file", args: []string{"version", "-outputLogsInJson=true"}, probeDir: "official-cli-probe6", stdoutFile: "version-logsjson-outfile.stdout.txt", stderrFile: "version-logsjson-outfile.stderr.txt", codeFile: "version-logsjson-outfile.code.txt", outputFile: "version-logsjson.log", outputGolden: "version-logsjson.log"},
		{name: "version logs json", args: []string{"version", "-outputLogsInJson=true"}, probeDir: "official-cli-probe6", stdoutFile: "version-logsjson.stdout.txt", stderrFile: "version-logsjson.stderr.txt", codeFile: "version-logsjson.code.txt", outputFile: "version-logsjson.log", outputGolden: "version-logsjson.log"},

		{name: "bad json", args: []string{"--bad", "-outputType=json"}, probeDir: "official-cli-probe7", stdoutFile: "bad-json.out", stderrFile: "bad-json.err", codeFile: "bad-json.code"},
		{name: "bad json logs", args: []string{"--bad", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "bad-json-logs.out", stderrFile: "bad-json-logs.err", codeFile: "bad-json-logs.code"},
		{name: "bad json output file", args: []string{"--bad", "-outputType=json"}, probeDir: "official-cli-probe7", stdoutFile: "bad-json-outfile.out", stderrFile: "bad-json-outfile.err", codeFile: "bad-json.code", outputFile: "bad-json.log", outputGolden: "bad-json.log"},
		{name: "help check json", args: []string{"help", "check", "-outputType=json"}, probeDir: "official-cli-probe7", stdoutFile: "help-check-json.out", stderrFile: "help-check-json.err", codeFile: "help-check-json.code"},
		{name: "help check quiet", args: []string{"help", "check", "-q"}, probeDir: "official-cli-probe7", stdoutFile: "help-check-quiet.out", stderrFile: "help-check-quiet.err", codeFile: "help-check-json.code"},
		{name: "help json logs", args: []string{"help", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "help-json-logs.out", stderrFile: "help-json-logs.err", codeFile: "help-json.code"},
		{name: "help json", args: []string{"help", "-outputType=json"}, probeDir: "official-cli-probe7", stdoutFile: "help-json.out", stderrFile: "help-json.err", codeFile: "help-json.code"},
		{name: "help quiet", args: []string{"help", "-q"}, probeDir: "official-cli-probe7", stdoutFile: "help-quiet.out", stderrFile: "help-quiet.err", codeFile: "help-json.code"},
		{name: "list engines json logs", args: []string{"list-engines", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "list-json-logs.out", stderrFile: "list-json-logs.err", codeFile: "list-json-logs.code"},
		{name: "migrate quiet json logs", args: []string{"migrate", "-q", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "migrate-quiet-json-logs.out", stderrFile: "migrate-quiet-json-logs.err", codeFile: "migrate-quiet-json-logs.code"},
		{name: "noargs json logs", args: []string{"-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "noargs-json-logs.out", stderrFile: "noargs-json-logs.err", codeFile: "noargs-json.code"},
		{name: "noargs json", args: []string{"-outputType=json"}, probeDir: "official-cli-probe7", stdoutFile: "noargs-json.out", stderrFile: "noargs-json.err", codeFile: "noargs-json.code"},
		{name: "noargs quiet", args: []string{"-q"}, probeDir: "official-cli-probe7", stdoutFile: "noargs-quiet.out", stderrFile: "noargs-quiet.err", codeFile: "noargs-json.code"},
		{name: "version json logs", args: []string{"version", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "version-json-logs.out", stderrFile: "version-json-logs.err", codeFile: "version-json-logs.code"},
		{name: "version output file quiet", args: []string{"version", "-q"}, probeDir: "official-cli-probe7", stdoutFile: "version-outfile-quiet.out", stderrFile: "version-outfile-quiet.err", codeFile: "version-json-logs.code", outputFile: "version-quiet.log", outputGolden: "version-quiet.log"},
		{name: "version quiet json logs", args: []string{"version", "-q", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe7", stdoutFile: "version-quiet-json-logs.out", stderrFile: "version-quiet-json-logs.err", codeFile: "version-quiet-json-logs.code"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{}, tc.args...)
			var outputPath string
			if tc.outputFile != "" {
				outputPath = filepath.Join(t.TempDir(), "output.log")
				args = append(args, "-outputFile="+outputPath)
			}
			expectedCode := readProbeCode(t, tc.probeDir, tc.codeFile)
			expectedStdout := readProbeBytes(t, tc.probeDir, tc.stdoutFile)
			expectedStderr := readProbeBytes(t, tc.probeDir, tc.stderrFile)

			var stdout, stderr bytes.Buffer
			code := runUserCLI(args, &stdout, &stderr, officialCLIGoldenDependencies(t))
			if code != expectedCode {
				t.Fatalf("exit code = %d, want %d", code, expectedCode)
			}
			assertGoldenBytes(t, "stdout", stdout.Bytes(), expectedStdout)
			assertGoldenBytes(t, "stderr", stderr.Bytes(), expectedStderr)
			if tc.outputGolden != "" {
				assertGoldenBytes(t, "outputFile", readFileBytes(t, outputPath), readProbeBytes(t, tc.probeDir, tc.outputGolden))
			}
		})
	}
}

func TestUserCLIOfficialGoldenBytesDebug(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		probeDir     string
		stdoutFile   string
		stderrFile   string
		codeFile     string
		tempOutput   bool
		outputGolden string
	}{
		{name: "help x", args: []string{"help", "-X"}, probeDir: "official-cli-probe8", stdoutFile: "help-x.stdout.txt", stderrFile: "help-x.stderr.txt", codeFile: "help-x.code.txt"},
		{name: "x help", args: []string{"-X", "help"}, probeDir: "official-cli-probe8", stdoutFile: "x-help.stdout.txt", stderrFile: "x-help.stderr.txt", codeFile: "x-help.code.txt"},
		{name: "migrate x", args: []string{"migrate", "-X"}, probeDir: "official-cli-probe8", stdoutFile: "migrate-x.stdout.txt", stderrFile: "migrate-x.stderr.txt", codeFile: "migrate-x.code.txt"},
		{name: "migrate quiet x", args: []string{"migrate", "-q", "-X"}, probeDir: "official-cli-probe8", stdoutFile: "migrate-q-x.stdout.txt", stderrFile: "migrate-q-x.stderr.txt", codeFile: "migrate-q-x.code.txt"},
		{name: "migrate x json", args: []string{"migrate", "-X", "-outputType=json"}, probeDir: "official-cli-probe8", stdoutFile: "migrate-x-json.stdout.txt", stderrFile: "migrate-x-json.stderr.txt", codeFile: "migrate-x-json.code.txt"},
		{name: "migrate x json logs", args: []string{"migrate", "-X", "-outputLogsInJson=true"}, probeDir: "official-cli-probe8", stdoutFile: "migrate-x-jsonlogs.stdout.txt", stderrFile: "migrate-x-jsonlogs.stderr.txt", codeFile: "migrate-x-jsonlogs.code.txt"},
		{name: "version x", args: []string{"version", "-X"}, probeDir: "official-cli-probe8", stdoutFile: "version-x.stdout.txt", stderrFile: "version-x.stderr.txt", codeFile: "version-x.code.txt"},
		{name: "version quiet x", args: []string{"version", "-q", "-X"}, probeDir: "official-cli-probe8", stdoutFile: "version-q-x.stdout.txt", stderrFile: "version-q-x.stderr.txt", codeFile: "version-q-x.code.txt"},
		{name: "version x json", args: []string{"version", "-X", "-outputType=json"}, probeDir: "official-cli-probe8", stdoutFile: "version-x-json.stdout.txt", stderrFile: "version-x-json.stderr.txt", codeFile: "version-x-json.code.txt"},
		{name: "version x json logs", args: []string{"version", "-X", "-outputLogsInJson=true"}, probeDir: "official-cli-probe8", stdoutFile: "version-x-jsonlogs.stdout.txt", stderrFile: "version-x-jsonlogs.stderr.txt", codeFile: "version-x-jsonlogs.code.txt"},
		{name: "version x json logs json", args: []string{"version", "-X", "-outputType=json", "-outputLogsInJson=true"}, probeDir: "official-cli-probe8", stdoutFile: "version-x-jsonlogs-json.stdout.txt", stderrFile: "version-x-jsonlogs-json.stderr.txt", codeFile: "version-x-jsonlogs-json.code.txt"},
		{name: "version quiet x json", args: []string{"version", "-q", "-X", "-outputType=json"}, probeDir: "official-cli-probe9", stdoutFile: "version-q-x-json.stdout.txt", stderrFile: "version-q-x-json.stderr.txt", codeFile: "version-q-x-json.code.txt"},
		{name: "bad x", args: []string{"--bad", "-X"}, probeDir: "official-cli-probe9", stdoutFile: "bad-x.stdout.txt", stderrFile: "bad-x.stderr.txt", codeFile: "bad-x.code.txt"},
		{name: "bad quiet x", args: []string{"--bad", "-q", "-X"}, probeDir: "official-cli-probe9", stdoutFile: "bad-q-x.stdout.txt", stderrFile: "bad-q-x.stderr.txt", codeFile: "bad-q-x.code.txt"},
		{name: "noargs x", args: []string{"-X"}, probeDir: "official-cli-probe9", stdoutFile: "noargs-x.stdout.txt", stderrFile: "noargs-x.stderr.txt", codeFile: "noargs-x.code.txt"},
		{name: "migrate x output file", args: []string{"migrate", "-X"}, probeDir: "official-cli-probe9", stdoutFile: "migrate-x-outfile.stdout.txt", stderrFile: "migrate-x-outfile.stderr.txt", codeFile: "migrate-x-outfile.code.txt", tempOutput: true, outputGolden: "migrate-x.log"},
		{name: "version x output file", args: []string{"version", "-X"}, probeDir: "official-cli-probe9", stdoutFile: "version-x-outfile.stdout.txt", stderrFile: "version-x-outfile.stderr.txt", codeFile: "version-x-outfile.code.txt", tempOutput: true, outputGolden: "version-x.log"},
		{name: "version bad output file", args: []string{"version", `-outputFile=E:\work\flyway-go\tmp`}, probeDir: "official-cli-probe8", stdoutFile: "version-bad-outfile.stdout.txt", stderrFile: "version-bad-outfile.stderr.txt", codeFile: "version-bad-outfile.code.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{}, tc.args...)
			var outputPath string
			if tc.tempOutput {
				outputPath = filepath.Join(t.TempDir(), "output.log")
				args = append(args, "-outputFile="+outputPath)
			}
			expectedCode := readProbeCode(t, tc.probeDir, tc.codeFile)
			expectedStdout := readProbeBytes(t, tc.probeDir, tc.stdoutFile)
			expectedStderr := readProbeBytes(t, tc.probeDir, tc.stderrFile)

			var stdout, stderr bytes.Buffer
			code := runUserCLI(args, &stdout, &stderr, officialCLIGoldenDependencies(t))
			if code != expectedCode {
				t.Fatalf("exit code = %d, want %d", code, expectedCode)
			}
			assertGoldenBytes(t, "stdout", stdout.Bytes(), expectedStdout)
			assertGoldenBytes(t, "stderr", stderr.Bytes(), expectedStderr)
			if tc.outputGolden != "" {
				assertGoldenBytes(t, "outputFile", readFileBytes(t, outputPath), readProbeBytes(t, tc.probeDir, tc.outputGolden))
			}
		})
	}
}

func readProbeCode(t *testing.T, probeDir, name string) int {
	t.Helper()
	value := strings.TrimSpace(string(readProbeBytes(t, probeDir, name)))
	code, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse golden exit code %q: %v", value, err)
	}
	return code
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file %s: %v", path, err)
	}
	return data
}
func officialCLIGoldenDependencies(t *testing.T) cliDependencies {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	installDirectory := filepath.Dir(repoRoot)
	return cliDependencies{
		installationDirectory: func() string { return installDirectory },
		fileExists:            func(string) bool { return false },
		environment:           func() map[string]string { return map[string]string{} },
		rawEnvironment:        func() map[string]string { return map[string]string{} },
		loadDefaultTOML: func(string, string) (core.ConfigurationModel, error) {
			return core.ConfigurationModel{}, nil
		},
		loadTOMLFiles: func([]string) (core.ConfigurationModel, error) {
			return core.ConfigurationModel{}, nil
		},
		openDB: func(string, string) (*sql.DB, error) {
			t.Fatal("openDB must not be called for golden CLI cases")
			return nil, nil
		},
	}
}

func readProbeBytes(t *testing.T, probeDir, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "tmp", probeDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file %s: %v", path, err)
	}
	return data
}

func assertGoldenBytes(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if bytes.Equal(got, want) {
		return
	}
	t.Fatalf("%s mismatch:\n got %d bytes: %s\nwant %d bytes: %s", label, len(got), formatGoldenBytes(got), len(want), formatGoldenBytes(want))
}

func formatGoldenBytes(value []byte) string {
	const max = 600
	if len(value) > max {
		return fmt.Sprintf("%q...", value[:max])
	}
	return fmt.Sprintf("%q", value)
}

func TestProxyDiagnosticMessage(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "none", env: map[string]string{}, want: "Proxy: no proxy configured"},
		{
			name: "http env is ignored",
			env:  map[string]string{"HTTP_PROXY": "http://127.0.0.1:8888", "HTTPS_PROXY": "http://127.0.0.1:8888", "NO_PROXY": "localhost"},
			want: "Proxy: no proxy configured",
		},
		{
			name: "http explicit",
			env:  map[string]string{"http.proxyHost": "127.0.0.1", "http.proxyPort": "9999"},
			want: "Proxy: explicit JVM properties in effect (http.proxyHost=127.0.0.1, http.proxyPort=9999, https.proxyHost=null, https.proxyPort=null, http.nonProxyHosts=null)",
		},
		{
			name: "https explicit",
			env:  map[string]string{"https.proxyHost": "127.0.0.1"},
			want: "Proxy: explicit JVM properties in effect (http.proxyHost=null, http.proxyPort=null, https.proxyHost=127.0.0.1, https.proxyPort=null, http.nonProxyHosts=null)",
		},
		{
			name: "blank host is not explicit",
			env:  map[string]string{"http.proxyHost": "   ", "http.proxyPort": "1"},
			want: "Proxy: no proxy configured",
		},
		{
			name: "system proxies",
			env:  map[string]string{"java.net.useSystemProxies": "true"},
			want: "Proxy: OS system proxies in effect (java.net.useSystemProxies=true)",
		},
		{
			name: "system proxies case",
			env:  map[string]string{"java.net.useSystemProxies": "TRUE"},
			want: "Proxy: OS system proxies in effect (java.net.useSystemProxies=true)",
		},
		{
			name: "system proxies false",
			env:  map[string]string{"java.net.useSystemProxies": "false"},
			want: "Proxy: no proxy configured",
		},
		{
			name: "explicit wins over system proxies",
			env:  map[string]string{"JAVA_ARGS": "-Dhttps.proxyHost=10.0.0.2 -Dhttp.nonProxyHosts=localhost|127.0.0.1", "java.net.useSystemProxies": "true"},
			want: "Proxy: explicit JVM properties in effect (http.proxyHost=null, http.proxyPort=null, https.proxyHost=10.0.0.2, https.proxyPort=null, http.nonProxyHosts=localhost|127.0.0.1)",
		},
		{
			name: "java tool options",
			env:  map[string]string{"JAVA_TOOL_OPTIONS": "-Djava.net.useSystemProxies=true"},
			want: "Proxy: OS system proxies in effect (java.net.useSystemProxies=true)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyDiagnosticMessage(tc.env); got != tc.want {
				t.Fatalf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUserCLIProxyDiagnosticOnlyWhenDebug(t *testing.T) {
	deps := officialCLIGoldenDependencies(t)
	deps.rawEnvironment = func() map[string]string {
		return map[string]string{"http.proxyHost": "127.0.0.1", "http.proxyPort": "9999"}
	}
	var stdout, stderr bytes.Buffer
	if code := runUserCLI([]string{"version"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Proxy:") {
		t.Fatalf("proxy diagnostic leaked without -X: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runUserCLI([]string{"version", "-X"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	want := "DEBUG: Proxy: explicit JVM properties in effect (http.proxyHost=127.0.0.1, http.proxyPort=9999, https.proxyHost=null, https.proxyPort=null, http.nonProxyHosts=null)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
	}
}
