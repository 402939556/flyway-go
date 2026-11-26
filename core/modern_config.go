package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IsEnvironmentField reports whether a Flyway property belongs to
// EnvironmentModel in Modern configuration.
func IsEnvironmentField(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "url", "user", "password", "driver", "displayname", "schemas",
		"connectretries", "connectretriesinterval", "initsql", "jdbcproperties",
		"resolvers", "provisioner", "environmentgroups":
		return true
	default:
		return false
	}
}

// MergeTemporaryEnvironment overlays tempConfigEnvironment on the active
// environment and removes the temporary environment, mirroring
// ModernConfigurationManager.getConfiguration().
func (m ConfigurationModel) MergeTemporaryEnvironment() ConfigurationModel {
	result := m.Clone()
	temp, ok := result.Environments["tempConfigEnvironment"]
	if !ok {
		return result
	}
	environmentName := "default"
	if value, ok := result.Flyway["environment"]; ok && value != nil {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
			environmentName = text
		}
	}
	existing := result.Environments[environmentName]
	if existing == nil {
		existing = map[string]any{}
	}
	result.Environments[environmentName] = mergeStringMaps(existing, temp)
	delete(result.Environments, "tempConfigEnvironment")
	return result
}

// ResolveRelativePaths applies ConfigUtils.makeRelativePathsBasedOnWorkingDirectory
// to the root Flyway model and every configured environment.
func (m ConfigurationModel) ResolveRelativePaths(workingDirectory string) ConfigurationModel {
	result := m.Clone()
	if strings.TrimSpace(workingDirectory) == "" {
		return result
	}
	expandFlywayModelPaths(result.Flyway, workingDirectory)
	for _, environment := range result.Environments {
		raw, ok := environment["flyway"]
		if !ok {
			continue
		}
		expandFlywayModelPaths(asStringMap(raw), workingDirectory)
	}
	return result
}

func expandFlywayModelPaths(flyway map[string]any, workingDirectory string) {
	expandModelLocationList(flyway, "locations", workingDirectory)
	expandModelLocationList(flyway, "callbackLocations", workingDirectory)
	expandModelJarDirList(flyway, "jarDirs", workingDirectory)
}

func expandModelLocationList(model map[string]any, key, workingDirectory string) {
	value, ok := model[key]
	if !ok {
		return
	}
	locations := stringListValue(value)
	for i, location := range locations {
		if !strings.HasPrefix(location, "filesystem:") {
			continue
		}
		path := strings.TrimPrefix(location, "filesystem:")
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDirectory, path)
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		locations[i] = "filesystem:" + filepath.Clean(path)
	}
	model[key] = locations
}

func expandModelJarDirList(model map[string]any, key, workingDirectory string) {
	value, ok := model[key]
	if !ok {
		return
	}
	jarDirs := stringListValue(value)
	for i, dir := range jarDirs {
		if dir == "" {
			continue
		}
		path := dir
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDirectory, path)
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		jarDirs[i] = filepath.Clean(path)
	}
	model[key] = jarDirs
}

// EnvironmentNotFoundError returns the official active-environment error,
// including a case-insensitive suggestion when one exists.
func (m ConfigurationModel) EnvironmentNotFoundError(name string) error {
	matches := make([]string, 0, 1)
	for candidate := range m.Environments {
		if candidate != name && strings.EqualFold(candidate, name) {
			matches = append(matches, candidate)
		}
	}
	message := fmt.Sprintf("Environment '%s' not found. Check that this environment exists in your configuration.", name)
	if len(matches) > 0 {
		sort.Strings(matches)
		message += fmt.Sprintf(" Did you mean '%s'?", matches[0])
	}
	return fmt.Errorf("%s", message)
}

// ApplyDefaultSQLocation mirrors ConfigUtils.shouldUseDefaultCliSqlLocation for
// the CLI's derived installation directory.
func ApplyDefaultSQLocation(cfg *Config, installationDirectory string, warn func(format string, args ...interface{})) {
	if cfg == nil || strings.TrimSpace(installationDirectory) == "" {
		return
	}
	if !usesDefaultMigrationLocation(cfg.Locations) {
		return
	}

	sqlFolder := filepath.Join(installationDirectory, "sql")
	if _, err := os.Stat(sqlFolder); err == nil {
		absolute, absErr := filepath.Abs(sqlFolder)
		if absErr == nil {
			sqlFolder = absolute
		}
		cfg.Locations = []string{"filesystem:" + filepath.Clean(sqlFolder)}
		warnIfUsingDeprecatedMigrationsFolder(sqlFolder, ".sql", warn)
		return
	}
	if warn != nil {
		warn("No locations configured and default location 'sql' not found.")
	}
}

func usesDefaultMigrationLocation(locations []string) bool {
	return len(locations) == 1 && normalizeLocationDescriptor(locations[0]) == "classpath:db/migration"
}

func normalizeLocationDescriptor(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	if !strings.Contains(location, ":") {
		return "classpath:" + location
	}
	return location
}

// warnIfUsingDeprecatedMigrationsFolder mirrors
// ConfigUtils.warnIfUsingDeprecatedMigrationsFolder. Directories count too:
// the official implementation only checks each child's name suffix.
func warnIfUsingDeprecatedMigrationsFolder(folder, extension string, warn func(format string, args ...interface{})) {
	if warn == nil {
		return
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), extension) {
			warn(fmt.Sprintf("Storing migrations in '%s' is not recommended and default scanning of this location may be deprecated in a future release", filepath.Base(folder)))
			return
		}
	}
}
