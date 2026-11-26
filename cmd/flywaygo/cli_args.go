package main

import (
	"fmt"
	"strings"
)

type userCLIArgs struct {
	operations        []string
	flags             map[string]bool
	configurationArgs []string
	configuration     map[string]string
	help              bool
	outputJSON        bool
	outputLogsInJSON  bool
	quiet             bool
	debug             bool
	migrationIDs      bool
	outputFile        string
	color             string
	colorInvalid      bool
	invalidColorValue string
}

var userCLIOperations = map[string]bool{
	"help": true, "migrate": true, "clean": true, "info": true,
	"validate": true, "undo": true, "baseline": true, "repair": true,
	"version": true, "list-engines": true,
	"deploy": true, "check": true, "diff": true, "diffText": true,
	"generate": true, "init": true, "model": true, "prepare": true,
	"auth": true,
}

var userCLIFlags = map[string]bool{
	"-X": true, "-q": true, "-communityFallback": true,
	"-skipCheckForUpdate": true, "-migrationIds": true,
	"outputLogsInJson": true, "-?": true, "-h": true, "--help": true,
	"-v": true, "--version": true,
}

func parseUserCLIArgs(args []string) (userCLIArgs, error) {
	parsed := userCLIArgs{
		flags:         map[string]bool{},
		configuration: map[string]string{},
	}
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) != "" {
			filtered = append(filtered, arg)
		}
	}
	for i, arg := range filtered {
		if isConfigurationArg(arg) {
			name, value := splitConfigurationArg(arg)
			parsed.configurationArgs = append(parsed.configurationArgs, arg)
			if _, exists := parsed.configuration[name]; !exists {
				parsed.configuration[name] = value
			}
			continue
		}
		if userCLIOperations[arg] || userCLIFlags[arg] {
			continue
		}
		if i+1 < len(filtered) && filtered[i+1] == "=" {
			return parsed, fmt.Errorf("Invalid configuration argument: %s. Please check you have not included any spaces in your configuration argument.", arg)
		}
		hint := ""
		if i+1 < len(filtered) && strings.HasPrefix(filtered[i+1], ".") {
			hint = "Shell may cause input parameters containing periods (.) to be misinterpreted - do you need to wrap your parameter in double quotes?"
		}
		if hint == "" {
			return parsed, fmt.Errorf("Invalid flag: %s\n", arg)
		}
		return parsed, fmt.Errorf("Invalid flag: %s\n%s", arg, hint)
	}

	for _, arg := range filtered {
		if isConfigurationArg(arg) {
			name, _ := splitConfigurationArg(arg)
			if strings.HasPrefix(name, "environments") {
				if strings.HasSuffix(name, ".") || len(strings.Split(name, ".")) == 2 {
					return parsed, fmt.Errorf("Invalid configuration argument: -%s. Both the environment name and configuration option are required.", name)
				}
			}
			continue
		}
		if userCLIOperations[arg] {
			parsed.operations = append(parsed.operations, arg)
		}
		if arg == "-v" || arg == "--version" {
			parsed.operations = append(parsed.operations, "version")
		}
		if userCLIFlags[arg] {
			parsed.flags[arg] = true
		}
	}

	outputType := strings.ToLower(parsed.configuration["outputType"])
	if outputType != "" && outputType != "json" {
		return parsed, fmt.Errorf("'%s' is an invalid value for the -outputType option. Use 'json'.", outputType)
	}
	parsed.outputJSON = outputType == "json"
	parsed.outputLogsInJSON = strings.EqualFold(parsed.configuration["outputLogsInJson"], "true")
	parsed.quiet = parsed.flags["-q"]
	parsed.debug = parsed.flags["-X"] && !parsed.quiet
	parsed.migrationIDs = parsed.flags["-migrationIds"]
	parsed.outputFile = parsed.configuration["outputFile"]
	parsed.color = parsed.configuration["color"]
	if !validUserCLIColor(parsed.color) {
		parsed.colorInvalid = true
		parsed.invalidColorValue = parsed.color
		return parsed, fmt.Errorf("'%s' is an invalid value for the -color option. Use 'always', 'never', or 'auto'.", parsed.color)
	}

	for _, arg := range filtered {
		if arg == "help" || arg == "-?" || arg == "-h" || arg == "--help" {
			parsed.help = true
			break
		}
	}
	return parsed, nil
}

func validUserCLIColor(value string) bool {
	return value == "" || value == "always" || value == "never" || value == "auto"
}

func isConfigurationArg(arg string) bool {
	return strings.HasPrefix(arg, "-") && strings.Contains(arg, "=")
}

func splitConfigurationArg(arg string) (string, string) {
	i := strings.IndexByte(arg, '=')
	if i < 0 {
		return "", ""
	}
	return arg[1:i], arg[i+1:]
}
