package core

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MigrationState 对应 Flyway 的 org.flywaydb.core.api.MigrationState。
// CLI/Info 输出使用枚举名（如 PENDING、SUCCESS），而不是 display name。
type MigrationState string

const (
	StatePending         MigrationState = "PENDING"
	StateAboveTarget     MigrationState = "ABOVE_TARGET"
	StateBelowBaseline   MigrationState = "BELOW_BASELINE"
	StateBaselineIgnored MigrationState = "BASELINE_IGNORED"
	StateBaseline        MigrationState = "BASELINE"
	StateIgnored         MigrationState = "IGNORED"
	StateMissingSuccess  MigrationState = "MISSING_SUCCESS"
	StateMissingFailed   MigrationState = "MISSING_FAILED"
	StateSuccess         MigrationState = "SUCCESS"
	StateUndone          MigrationState = "UNDONE"
	StateAvailable       MigrationState = "AVAILABLE"
	StateFailed          MigrationState = "FAILED"
	StateOutOfOrder      MigrationState = "OUT_OF_ORDER"
	StateFutureSuccess   MigrationState = "FUTURE_SUCCESS"
	StateFutureFailed    MigrationState = "FUTURE_FAILED"
	StateOutdated        MigrationState = "OUTDATED"
	StateSuperseded      MigrationState = "SUPERSEDED"
	StateDeleted         MigrationState = "DELETED"
)

func (s MigrationState) DisplayName() string {
	switch s {
	case StatePending:
		return "Pending"
	case StateAboveTarget:
		return "Above Target"
	case StateBelowBaseline:
		return "Below Baseline"
	case StateBaselineIgnored:
		return "Ignored (Baseline)"
	case StateBaseline:
		return "Baseline"
	case StateIgnored:
		return "Ignored"
	case StateMissingSuccess:
		return "Missing"
	case StateMissingFailed:
		return "Failed (Missing)"
	case StateSuccess:
		return "Success"
	case StateUndone:
		return "Undone"
	case StateAvailable:
		return "Available"
	case StateFailed:
		return "Failed"
	case StateOutOfOrder:
		return "Out of Order"
	case StateFutureSuccess:
		return "Future"
	case StateFutureFailed:
		return "Failed (Future)"
	case StateOutdated:
		return "Outdated"
	case StateSuperseded:
		return "Superseded"
	case StateDeleted:
		return "Deleted"
	default:
		return string(s)
	}
}

func (s MigrationState) PatternName() string {
	switch s {
	case StatePending:
		return "pending"
	case StateAboveTarget:
		return "Above Target"
	case StateBelowBaseline:
		return "Below Baseline"
	case StateBaselineIgnored:
		return "Ignored (Baseline)"
	case StateBaseline:
		return "Baseline"
	case StateIgnored:
		return "ignored"
	case StateMissingSuccess:
		return "missing"
	case StateMissingFailed:
		return "Failed (Missing)"
	case StateSuccess:
		return "Success"
	case StateUndone:
		return "Undone"
	case StateAvailable:
		return "Available"
	case StateFailed:
		return "Failed"
	case StateOutOfOrder:
		return "Out of Order"
	case StateFutureSuccess:
		return "future"
	case StateFutureFailed:
		return "Failed (Future)"
	case StateOutdated:
		return "Outdated"
	case StateSuperseded:
		return "Superseded"
	case StateDeleted:
		return "Deleted"
	default:
		return string(s)
	}
}

func (s MigrationState) IsResolved() bool {
	switch s {
	case StatePending, StateAboveTarget, StateBelowBaseline, StateBaselineIgnored,
		StateBaseline, StateIgnored, StateSuccess, StateUndone, StateAvailable,
		StateFailed, StateOutOfOrder, StateOutdated, StateSuperseded:
		return true
	default:
		return false
	}
}

func (s MigrationState) IsApplied() bool {
	switch s {
	case StateBaseline, StateMissingSuccess, StateMissingFailed, StateSuccess,
		StateUndone, StateFailed, StateOutOfOrder, StateFutureSuccess,
		StateFutureFailed, StateOutdated, StateSuperseded, StateDeleted:
		return true
	default:
		return false
	}
}

func (s MigrationState) IsFailed() bool {
	switch s {
	case StateMissingFailed, StateFailed, StateFutureFailed:
		return true
	default:
		return false
	}
}

// ValidatePattern 对应 Flyway 的 <migration_type>:<migration_state> 忽略规则。
type ValidatePattern struct {
	MigrationType  string
	MigrationState string
	Raw            string
}

var validValidateTypes = []string{"*", "repeatable", "versioned"}
var validValidateStates = []string{"*", "missing", "pending", "ignored", "future"}

func ParseValidatePattern(pattern string) (ValidatePattern, error) {
	parts := strings.Split(pattern, ":")
	if len(parts) != 2 {
		return ValidatePattern{}, fmt.Errorf("Invalid pattern '%s'. Pattern must be of the form <migration_type>:<migration_state> See https://help.red-gate.com/help/flyway-cli13/help_13.aspx?topic=ignore_migration_patterns for full details", pattern)
	}
	typ := strings.ToLower(strings.TrimSpace(parts[0]))
	state := strings.ToLower(strings.TrimSpace(parts[1]))
	if !containsString(validValidateTypes, typ) {
		return ValidatePattern{}, fmt.Errorf("Invalid migration type '%s'. Valid types are: [*, repeatable, versioned]", parts[0])
	}
	if !containsString(validValidateStates, state) {
		return ValidatePattern{}, fmt.Errorf("Invalid migration state '%s'. Valid states are: [*, missing, pending, ignored, future]", parts[1])
	}
	return ValidatePattern{MigrationType: typ, MigrationState: state, Raw: pattern}, nil
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (p ValidatePattern) MatchesMigration(isVersioned bool, state MigrationState) bool {
	if !strings.EqualFold(state.DisplayName(), p.MigrationState) && p.MigrationState != "*" {
		return false
	}
	if p.MigrationType == "*" {
		return true
	}
	if isVersioned && p.MigrationType == "versioned" {
		return true
	}
	return !isVersioned && p.MigrationType == "repeatable"
}

func IgnoreAllPatterns() []ValidatePattern {
	p, _ := ParseValidatePattern("*:*")
	return []ValidatePattern{p}
}

func ParseIgnorePatterns(patterns []string) ([]ValidatePattern, error) {
	out := make([]ValidatePattern, 0, len(patterns))
	for _, pattern := range patterns {
		p, err := ParseValidatePattern(pattern)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// MigrationInfoContext 对应 MigrationInfoContext。
type MigrationInfoContext struct {
	OutOfOrder           bool
	IgnorePatterns       []ValidatePattern
	Target               *Version
	Schema               *Version
	PendingBaseline      *Version
	AppliedBaseline      *Version
	LastResolved         Version
	LastApplied          Version
	LatestRepeatableRuns map[string]int
}

func newMigrationInfoContext() *MigrationInfoContext {
	return &MigrationInfoContext{
		LastResolved:         EmptyVersion(),
		LastApplied:          EmptyVersion(),
		LatestRepeatableRuns: map[string]int{},
	}
}

func (c *MigrationInfoContext) IsPendingIgnored() bool {
	for _, p := range c.IgnorePatterns {
		if p.MigrationType == "*" && p.MigrationState == "pending" {
			return true
		}
	}
	if !hasPatternFor(c.IgnorePatterns, true, StatePending) || !hasPatternFor(c.IgnorePatterns, false, StatePending) {
		return false
	}
	return true
}

func (c *MigrationInfoContext) IsIgnoredIgnored() bool {
	for _, p := range c.IgnorePatterns {
		if p.MigrationType == "*" && p.MigrationState == "ignored" {
			return true
		}
	}
	return hasPatternFor(c.IgnorePatterns, true, StateIgnored) && hasPatternFor(c.IgnorePatterns, false, StateIgnored)
}

func (c *MigrationInfoContext) IsMissingIgnored() bool {
	for _, p := range c.IgnorePatterns {
		if p.MigrationType == "*" && p.MigrationState == "missing" {
			return true
		}
	}
	return hasPatternFor(c.IgnorePatterns, true, StateMissingSuccess) && hasPatternFor(c.IgnorePatterns, false, StateMissingSuccess)
}

func (c *MigrationInfoContext) IsFutureIgnored() bool {
	for _, p := range c.IgnorePatterns {
		if p.MigrationType == "*" && p.MigrationState == "future" {
			return true
		}
	}
	return hasPatternFor(c.IgnorePatterns, true, StateFutureSuccess) && hasPatternFor(c.IgnorePatterns, false, StateFutureSuccess)
}

func hasPatternFor(patterns []ValidatePattern, versioned bool, state MigrationState) bool {
	for _, p := range patterns {
		if p.MatchesMigration(versioned, state) {
			return true
		}
	}
	return false
}

// AppliedMigrationAttributes 与官方 refresh 阶段的附加状态一致。
type AppliedMigrationAttributes struct {
	OutOfOrder bool
	Deleted    bool
	Undone     bool
}

// MigrationInfo 是 resolved 与 applied 合并后的视图，对应 MigrationInfoImpl。
type MigrationInfo struct {
	Resolved   *Migration
	Applied    *SchemaHistoryRow
	Context    *MigrationInfoContext
	OutOfOrder bool
	Deleted    bool
	Undone     bool

	// stateOverride is reserved for internal tests and synthetic status paths
	// that cannot be represented by resolved/applied rows alone.
	stateOverride *MigrationState
}

// InfoFilter 对应 org.flywaydb.core.api.MigrationFilter / MigrationFilterImpl。
// 日期过滤只作用于已应用迁移的 installedOn 时间。
type InfoFilter struct {
	SinceDate    *time.Time
	UntilDate    *time.Time
	SinceVersion *Version
	UntilVersion *Version
	States       []MigrationState
}

// Matches 与 MigrationFilterImpl.matches() 逐项一致。AVAILABLE 的特例由
// InfoWithFilter 在调用此方法后处理，不放进这里。
func (f *InfoFilter) Matches(info *MigrationInfo) bool {
	if f == nil || info == nil {
		return true
	}

	var installedOn *time.Time
	if info.Applied != nil {
		installed := info.Applied.InstalledOn
		installedOn = &installed
	}
	version := info.Version()
	state := info.State()

	afterFromDate := f.SinceDate == nil || installedOn == nil || installedOn.After(*f.SinceDate)
	beforeToDate := f.UntilDate == nil || installedOn != nil && installedOn.Before(*f.UntilDate)
	afterFromVersion := f.SinceVersion == nil || version == nil || version.Compare(*f.SinceVersion) >= 0
	beforeToVersion := f.UntilVersion == nil || version != nil && version.Compare(*f.UntilVersion) <= 0
	matchesState := len(f.States) == 0
	if !matchesState {
		for _, expected := range f.States {
			if state == expected {
				matchesState = true
				break
			}
		}
	}
	return afterFromDate && beforeToDate && afterFromVersion && beforeToVersion && matchesState
}

func shouldIncludeInfo(info *MigrationInfo, filter *InfoFilter) bool {
	return filter == nil || filter.Matches(info) || info.State() == StateAvailable
}

// ExecutionTime mirrors MigrationInfoImpl.getExecutionTime().
func (i *MigrationInfo) ExecutionTime() int {
	if i.Applied == nil {
		return 0
	}
	return i.Applied.ExecutionTime
}

// PhysicalLocation mirrors MigrationInfoImpl.getPhysicalLocation().
func (i *MigrationInfo) PhysicalLocation() string {
	if i.Resolved == nil {
		return ""
	}
	return i.Resolved.Path
}

// ShouldExecuteExpression mirrors the resolved script metadata accessor.
func (i *MigrationInfo) ShouldExecuteExpression() string {
	if i.Resolved == nil {
		return ""
	}
	return i.Resolved.ShouldExecuteExpression
}

// RawVersion prefers the resolved raw filename version. Rows read from the
// history table only retain the normalized version string, matching Java.
func (i *MigrationInfo) RawVersion() string {
	if i.Resolved != nil {
		if i.Resolved.RawVersion != "" || i.Resolved.Kind == KindVersioned || i.Resolved.Kind == KindUndo || i.Resolved.Kind == KindBaseline {
			if i.Resolved.RawVersion != "" {
				return i.Resolved.RawVersion
			}
			return i.Resolved.Version.RawVersion()
		}
		return ""
	}
	if v := i.Version(); v != nil {
		return v.RawVersion()
	}
	return ""
}
func (i *MigrationInfo) Version() *Version {
	if i.Applied != nil {
		if !i.Applied.VersionValid {
			return nil
		}
		v, err := ParseVersion(i.Applied.Version)
		if err != nil {
			return nil
		}
		return &v
	}
	if i.Resolved != nil && i.Resolved.Kind != KindRepeatable {
		v := i.Resolved.Version
		return &v
	}
	return nil
}

func (i *MigrationInfo) Type() string {
	if i.Applied != nil {
		return strings.ToUpper(i.Applied.Type)
	}
	if i.Resolved != nil {
		return i.Resolved.Type()
	}
	return ""
}

func (i *MigrationInfo) Description() string {
	if i.Applied != nil {
		return i.Applied.Description
	}
	if i.Resolved != nil {
		return i.Resolved.Description
	}
	return ""
}

func (i *MigrationInfo) Script() string {
	if i.Applied != nil {
		return i.Applied.Script
	}
	if i.Resolved != nil {
		return i.Resolved.Script
	}
	return ""
}

func (i *MigrationInfo) Checksum() sql.NullInt64 {
	if i.Applied != nil {
		return i.Applied.Checksum
	}
	if i.Resolved != nil {
		return sql.NullInt64{Int64: i.Resolved.Checksum, Valid: true}
	}
	return sql.NullInt64{}
}

func (i *MigrationInfo) InstalledRank() *int {
	if i.Applied == nil {
		return nil
	}
	rank := i.Applied.InstalledRank
	return &rank
}

func (i *MigrationInfo) VersionText() string {
	v := i.Version()
	if v == nil {
		return "null"
	}
	return v.String()
}
func (i *MigrationInfo) IsVersioned() bool {
	return i.Version() != nil
}

func (i *MigrationInfo) State() MigrationState {
	if i.stateOverride != nil {
		return *i.stateOverride
	}
	if i.Undone {
		return StateUndone
	}
	if i.Deleted {
		return StateDeleted
	}
	if i.Applied == nil {
		if i.Resolved != nil {
			return i.Resolved.State(i.Context)
		}
		return StatePending
	}
	return i.appliedState()
}

func (i *MigrationInfo) appliedState() MigrationState {
	row := i.Applied
	typ := strings.ToUpper(row.Type)
	if typ == TypeDelete {
		return StateSuccess
	}
	if isBaselineType(typ) {
		// BaselineAppliedMigration only overrides the synthetic BASELINE state
		// for SQL_BASELINE/JDBC_BASELINE. Failed baselines are FAILED, and a
		// locally missing baseline keeps the usual MISSING_* state.
		if strings.EqualFold(typ, TypeBaseline) {
			return StateBaseline
		}
		if !row.Success {
			return StateFailed
		}
		if missing := i.missingState(); missing != "" {
			return missing
		}
		return StateBaseline
	}
	missing := i.missingState()
	if missing != "" {
		return missing
	}
	if !row.Success {
		return StateFailed
	}
	if typ == TypeSchema {
		return StateSuccess
	}
	if !row.VersionValid {
		latestRank, ok := i.Context.LatestRepeatableRuns[row.Description]
		if !ok || row.InstalledRank == latestRank {
			if i.Resolved != nil && i.checksumMatches(row.Checksum) {
				return StateSuccess
			}
			return StateOutdated
		}
		return StateSuperseded
	}
	if i.OutOfOrder {
		return StateOutOfOrder
	}
	return StateSuccess
}

func (i *MigrationInfo) missingState() MigrationState {
	if i.Resolved != nil || !i.isRepeatableLatest() {
		return ""
	}
	row := i.Applied
	if strings.EqualFold(row.Type, TypeSchema) {
		return StateSuccess
	}
	if !row.VersionValid || versionLessThanString(i.Version(), i.Context.LastResolved) {
		if row.Success {
			return StateMissingSuccess
		}
		return StateMissingFailed
	}
	if row.Success {
		return StateFutureSuccess
	}
	return StateFutureFailed
}

func (i *MigrationInfo) isRepeatableLatest() bool {
	if i.Applied.VersionValid {
		return true
	}
	latestRank, ok := i.Context.LatestRepeatableRuns[i.Applied.Description]
	return !ok || i.Applied.InstalledRank == latestRank
}

func (i *MigrationInfo) checksumMatches(applied sql.NullInt64) bool {
	if i.Resolved == nil || !applied.Valid {
		return false
	}
	if applied.Int64 == i.Resolved.Checksum {
		return true
	}
	return i.Resolved.EquivalentChecksum != nil && applied.Int64 == *i.Resolved.EquivalentChecksum
}

// isChecksumMatching 对应官方 isChecksumMatching：任一侧 checksum 为 null 即视为匹配。
func (i *MigrationInfo) isChecksumMatching() bool {
	if i.Resolved == nil || !i.Applied.Checksum.Valid {
		return true
	}
	if i.Applied.Checksum.Int64 == i.Resolved.Checksum {
		return true
	}
	return i.Resolved.EquivalentChecksum != nil && i.Applied.Checksum.Int64 == *i.Resolved.EquivalentChecksum
}

// State 对应 ResolvedMigration.getState(context)。
func (m *Migration) State(context *MigrationInfoContext) MigrationState {
	state := m.resolvedBaseState(context)
	if m.Kind != KindBaseline {
		return state
	}
	// BaselineResolvedMigration.getState() override. A pending baseline script
	// is ignored once any migration or baseline exists. A script whose version
	// equals pendingBaseline is special: without applied/baseline history it is
	// eligible to run, otherwise it remains BASELINE_IGNORED.
	if state == StatePending && migrationsAppliedOrBaselineExists(context) {
		return StateIgnored
	}
	if state == StateBaselineIgnored && context.PendingBaseline != nil &&
		m.Version.Compare(*context.PendingBaseline) == 0 {
		if migrationsAppliedOrBaselineExists(context) {
			return StateBaselineIgnored
		}
		return StatePending
	}
	return state
}

func (m *Migration) resolvedBaseState(context *MigrationInfoContext) MigrationState {
	if m.Kind != KindRepeatable {
		baseline := context.AppliedBaseline
		if context.LastApplied.IsEmpty() {
			baseline = context.PendingBaseline
		}
		if baseline != nil {
			cmp := m.Version.Compare(*baseline)
			if cmp < 0 {
				return StateBelowBaseline
			}
			if cmp == 0 {
				return StateBaselineIgnored
			}
		}
		if context.Target != nil && context.Target.predef != predefNext && m.Version.Compare(*context.Target) > 0 {
			return StateAboveTarget
		}
		if !context.LastApplied.IsEmpty() && m.Version.Compare(context.LastApplied) < 0 && !context.OutOfOrder {
			return StateIgnored
		}
	}
	return StatePending
}

func migrationsAppliedOrBaselineExists(context *MigrationInfoContext) bool {
	return context.AppliedBaseline != nil || !context.LastApplied.IsEmpty()
}

func versionLessThanString(v *Version, other Version) bool {
	if v == nil {
		return true
	}
	return v.Compare(other) < 0
}

// BuildMigrationInfos 复刻 MigrationInfoServiceImpl.refresh() 的顺序。
func BuildMigrationInfos(resolved []*Migration, applied []SchemaHistoryRow, target *Version, outOfOrder bool, ignorePatterns []ValidatePattern, failOnMissingTarget bool, baselineOnMigrateVersion *Version) ([]*MigrationInfo, *MigrationInfoContext, error) {
	context := newMigrationInfoContext()
	context.Target = target
	context.OutOfOrder = outOfOrder
	context.IgnorePatterns = ignorePatterns

	resolvedVersioned := map[versionTypeKey]*Migration{}
	resolvedRepeatable := map[string]*Migration{}
	for _, m := range resolved {
		if m.Kind == KindRepeatable {
			resolvedRepeatable[m.Description] = m
			continue
		}
		resolvedVersioned[versionTypeKey{m.Version.Canonical(), m.Type()}] = m
		if m.Version.Compare(context.LastResolved) > 0 {
			context.LastResolved = m.Version
		}
	}

	appliedVersioned, appliedRepeatable, err := classifyApplied(applied, context)
	if err != nil {
		return nil, nil, err
	}
	updateContextFromAppliedVersioned(appliedVersioned, context)
	if baselineOnMigrateVersion != nil && len(applied) == 0 {
		b := *baselineOnMigrateVersion
		context.AppliedBaseline = &b
		context.LastApplied = b
	}
	if context.Target != nil && context.Target.predef == predefCurrent {
		last := context.LastApplied
		context.Target = &last
	}

	infos := make([]*MigrationInfo, 0, len(resolved)+len(applied))
	for _, pair := range appliedVersioned {
		row := pair.Row
		version, _ := ParseVersion(row.Version)
		resolvedMigration := resolvedVersioned[versionTypeKey{version.Canonical(), strings.ToUpper(row.Type)}]
		infos = append(infos, &MigrationInfo{Resolved: resolvedMigration, Applied: row, Context: context, OutOfOrder: pair.Attrs.OutOfOrder, Deleted: pair.Attrs.Deleted, Undone: pair.Attrs.Undone})
	}
	for _, m := range pendingResolvedVersioned(appliedVersioned, resolvedVersioned, context) {
		infos = append(infos, &MigrationInfo{Resolved: m, Context: context})
	}
	if failOnMissingTarget && target != nil && target.predef != predefCurrent && target.predef != predefLatest && target.predef != predefNext {
		if err := validateTarget(*target, infos); err != nil {
			return nil, nil, err
		}
	}

	context.LatestRepeatableRuns = latestRepeatableRuns(appliedRepeatable)
	for _, pair := range appliedRepeatable {
		row := pair.Row
		infos = append(infos, &MigrationInfo{Resolved: resolvedRepeatable[row.Description], Applied: row, Context: context, Deleted: pair.Attrs.Deleted})
	}
	for _, m := range pendingResolvedRepeatable(appliedRepeatable, resolvedRepeatable, context) {
		infos = append(infos, &MigrationInfo{Resolved: m, Context: context})
	}

	sort.SliceStable(infos, func(a, b int) bool {
		return compareMigrationInfo(infos[a], infos[b]) < 0
	})
	if context.Target != nil && context.Target.predef == predefNext {
		var next *Version
		for _, info := range infos {
			if info.State() == StatePending {
				next = info.Version()
				break
			}
		}
		context.Target = next
	}
	return infos, context, nil
}

type versionTypeKey struct {
	Version string
	Type    string
}

type appliedPair struct {
	Row   *SchemaHistoryRow
	Attrs AppliedMigrationAttributes
}

func classifyApplied(applied []SchemaHistoryRow, context *MigrationInfoContext) ([]*appliedPair, []*appliedPair, error) {
	appliedVersioned := make([]*appliedPair, 0, len(applied))
	appliedRepeatable := make([]*appliedPair, 0, len(applied))
	for idx := range applied {
		row := &applied[idx]
		if !row.VersionValid {
			appliedRepeatable = append(appliedRepeatable, &appliedPair{Row: row})
			if strings.EqualFold(row.Type, TypeDelete) && row.Success {
				markRepeatableAsDeleted(row.Description, appliedRepeatable)
			}
			continue
		}
		version, err := ParseVersion(row.Version)
		if err != nil {
			return nil, nil, fmt.Errorf("历史表中的版本号非法: %s", row.Version)
		}
		typ := strings.ToUpper(row.Type)
		if typ == TypeSchema {
			v := version
			context.Schema = &v
		}
		if isBaselineType(typ) && (context.AppliedBaseline == nil || version.Compare(*context.AppliedBaseline) > 0) {
			v := version
			context.AppliedBaseline = &v
		}
		if typ == TypeDelete && row.Success {
			if err := markAsDeleted(version, appliedVersioned); err != nil {
				return nil, nil, err
			}
			continue
		}
		if isUndoType(typ) && row.Success {
			if err := markAsUndone(version, appliedVersioned); err != nil {
				return nil, nil, err
			}
		}
		appliedVersioned = append(appliedVersioned, &appliedPair{Row: row})
	}
	return appliedVersioned, appliedRepeatable, nil
}

func markAsDeleted(version Version, pairs []*appliedPair) error {
	for i := len(pairs) - 1; i >= 0; i-- {
		row := pairs[i].Row
		if isSyntheticType(row.Type) {
			continue
		}
		pv, err := ParseVersion(row.Version)
		if err != nil || pv.Compare(version) != 0 {
			continue
		}
		if pairs[i].Attrs.Deleted {
			return fmt.Errorf("Corrupted schema history: multiple delete entries for version %s", version)
		}
		pairs[i].Attrs.Deleted = true
		return nil
	}
	return nil
}

func markRepeatableAsDeleted(description string, pairs []*appliedPair) {
	for i := len(pairs) - 1; i >= 0; i-- {
		row := pairs[i].Row
		if isSyntheticType(row.Type) || row.Description != description {
			continue
		}
		pairs[i].Attrs.Deleted = true
		return
	}
}

func pendingResolvedVersioned(applied []*appliedPair, resolved map[versionTypeKey]*Migration, context *MigrationInfoContext) []*Migration {
	pending := make([]*Migration, 0, len(resolved))
	for _, m := range resolved {
		pending = append(pending, m)
	}
	for _, pair := range applied {
		version, _ := ParseVersion(pair.Row.Version)
		key := versionTypeKey{version.Canonical(), strings.ToUpper(pair.Row.Type)}
		m := resolved[key]
		if m == nil || pair.Attrs.Deleted || strings.EqualFold(pair.Row.Type, TypeDelete) || pair.Attrs.Undone {
			continue
		}
		pending = removeResolved(pending, m)
	}
	for _, m := range pending {
		if m.Kind == KindBaseline && (context.PendingBaseline == nil || m.Version.Compare(*context.PendingBaseline) > 0) {
			v := m.Version
			context.PendingBaseline = &v
		}
	}
	return pending
}

func pendingResolvedRepeatable(applied []*appliedPair, resolved map[string]*Migration, context *MigrationInfoContext) []*Migration {
	pending := make([]*Migration, 0, len(resolved))
	for _, m := range resolved {
		pending = append(pending, m)
	}
	for _, pair := range applied {
		row := pair.Row
		m := resolved[row.Description]
		latestRank, ok := context.LatestRepeatableRuns[row.Description]
		if pair.Attrs.Deleted || strings.EqualFold(row.Type, TypeDelete) || m == nil || !ok || row.InstalledRank != latestRank || !checksumsEqual(row.Checksum, m.Checksum) {
			continue
		}
		pending = removeResolved(pending, m)
	}
	return pending
}

func removeResolved(ms []*Migration, target *Migration) []*Migration {
	for i, m := range ms {
		if m == target {
			return append(ms[:i], ms[i+1:]...)
		}
	}
	return ms
}

func checksumsEqual(applied sql.NullInt64, resolved int64) bool {
	return applied.Valid && applied.Int64 == resolved
}

func latestRepeatableRuns(applied []*appliedPair) map[string]int {
	out := map[string]int{}
	for _, pair := range applied {
		row := pair.Row
		if pair.Attrs.Deleted && strings.EqualFold(row.Type, TypeDelete) {
			continue
		}
		if current, ok := out[row.Description]; ok && row.InstalledRank <= current {
			continue
		}
		out[row.Description] = row.InstalledRank
	}
	return out
}

func updateContextFromAppliedVersioned(applied []*appliedPair, context *MigrationInfoContext) {
	for _, pair := range applied {
		row := pair.Row
		version, _ := ParseVersion(row.Version)
		if version.Compare(context.LastApplied) > 0 {
			if strings.EqualFold(row.Type, TypeDelete) || pair.Attrs.Deleted || !row.VersionValid || pair.Attrs.Undone {
				continue
			}
			context.LastApplied = version
			continue
		}
		pair.Attrs.OutOfOrder = true
	}
}

func validateTarget(target Version, infos []*MigrationInfo) error {
	for _, info := range infos {
		version := info.Version()
		if version != nil && target.Compare(*version) == 0 {
			return nil
		}
	}
	return fmt.Errorf("No migration with a target version %s could be found. Ensure target is specified correctly and the migration exists.", target)
}

func compareMigrationInfo(a, b *MigrationInfo) int {
	ar, br := a.InstalledRank(), b.InstalledRank()
	if ar != nil && br != nil {
		if *ar < *br {
			return -1
		}
		if *ar > *br {
			return 1
		}
		return 0
	}
	aState, bState := a.State(), b.State()
	if (aState == StateBelowBaseline || aState == StateBaselineIgnored) && bState.IsApplied() {
		return -1
	}
	if aState.IsApplied() && (bState == StateBelowBaseline || bState == StateBaselineIgnored) {
		return 1
	}
	if ar != nil {
		return -1
	}
	if br != nil {
		return 1
	}
	return compareMigrationVersion(a, b)
}

func compareMigrationVersion(a, b *MigrationInfo) int {
	av, bv := a.Version(), b.Version()
	if av != nil && bv != nil {
		if c := av.Compare(*bv); c != 0 {
			return c
		}
		at, bt := a.Type(), b.Type()
		if isUndoType(at) && isUndoType(bt) {
			return 0
		}
		if isUndoType(at) {
			return 1
		}
		if isUndoType(bt) {
			return -1
		}
		if isBaselineType(at) && isBaselineType(bt) {
			return 0
		}
		if isBaselineType(at) {
			return 1
		}
		if isBaselineType(bt) {
			return -1
		}
		return 0
	}
	if av != nil {
		return -1
	}
	if bv != nil {
		return 1
	}
	return strings.Compare(a.Description(), b.Description())
}

func isSyntheticType(typ string) bool {
	t := strings.ToUpper(typ)
	return t == TypeSchema || t == TypeBaseline || t == TypeDelete
}

func isBaselineType(typ string) bool {
	t := strings.ToUpper(typ)
	return t == TypeBaseline || t == TypeSQLBaseline || t == TypeJDBCBaseline
}

func isUndoType(typ string) bool { return strings.EqualFold(typ, TypeSQLUndo) }

func isSQLType(typ string) bool { return strings.EqualFold(typ, TypeSQLMigration) }

// ValidationError 保存官方校验失败的结构化错误码。
type ValidationError struct {
	Code    string
	message string
}

func (e *ValidationError) Error() string { return e.message }

func validationError(code, format string, args ...interface{}) error {
	return &ValidationError{Code: code, message: fmt.Sprintf(format, args...)}
}

// ErrorCode 返回官方 ValidateOutput 使用的结构化错误码。
func ErrorCode(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return "VALIDATE_ERROR"
}

// Validate 返回 MigrationInfoImpl.validate() 对应的首个/全部错误。
func (i *MigrationInfo) Validate() error {
	state := i.State()
	if state == StateUndone || state == StateAboveTarget || state == StateDeleted {
		return nil
	}
	versioned := i.Version() != nil
	for _, p := range i.Context.IgnorePatterns {
		if p.MatchesMigration(versioned, state) {
			return nil
		}
	}
	if state == StatePending && !i.Context.IsPendingIgnored() {
		if versioned {
			return validationError("RESOLVED_VERSIONED_MIGRATION_NOT_APPLIED", "Detected resolved migration not applied to database: %s.\nTo fix this error, either run migrate, or set -ignoreMigrationPatterns='*:pending'.", i.Version())

		}
		return validationError("RESOLVED_REPEATABLE_MIGRATION_NOT_APPLIED", "Detected resolved repeatable migration not applied to database: %s.\nTo fix this error, either run migrate, or set -ignoreMigrationPatterns='*:pending'.", i.Description())

	}
	if state.IsFailed() && (!i.Context.IsFutureIgnored() || state != StateFutureFailed) {
		if !versioned {
			return validationError("FAILED_REPEATABLE_MIGRATION", "Detected failed repeatable migration: %s.\nPlease remove any half-completed changes then run repair to fix the schema history.", i.Description())
		}
		return validationError("FAILED_VERSIONED_MIGRATION", "Detected failed migration to version %s (%s).\nPlease remove any half-completed changes then run repair to fix the schema history.", i.Version(), i.Description())
	}
	if i.Resolved == nil && !(isSyntheticType(i.Applied.Type) || isUndoType(i.Applied.Type) || state == StateSuperseded || i.Context.IsMissingIgnored() && (state == StateMissingSuccess || state == StateMissingFailed) || i.Context.IsFutureIgnored() && (state == StateFutureSuccess || state == StateFutureFailed)) {
		if i.Version() != nil {
			return validationError("APPLIED_VERSIONED_MIGRATION_NOT_RESOLVED", "Detected applied migration not resolved locally: %s.\nIf you removed this migration intentionally, run repair to mark the migration as deleted.", i.Version())
		}
		return validationError("APPLIED_REPEATABLE_MIGRATION_NOT_RESOLVED", "Detected applied migration not resolved locally: %s.\nIf you removed this migration intentionally, run repair to mark the migration as deleted.", i.Description())
	}
	if !i.Context.IsIgnoredIgnored() && state == StateIgnored && i.Resolved != nil && !isBaselineType(i.Resolved.Type()) && !isUndoType(i.Resolved.Type()) {
		if versioned {
			return validationError("RESOLVED_VERSIONED_MIGRATION_NOT_APPLIED", "Detected resolved migration not applied to database: %s.\nTo ignore this migration, set -ignoreMigrationPatterns='*:ignored'. To allow executing this migration, set -outOfOrder=true.", i.Version())
		}
		return validationError("RESOLVED_REPEATABLE_MIGRATION_NOT_APPLIED", "Detected resolved repeatable migration not applied to database: %s.\nTo ignore this migration, set -ignoreMigrationPatterns='*:ignored'.", i.Description())
	}
	if !i.Context.IsPendingIgnored() && state == StatePending {
		if versioned {
			return validationError("RESOLVED_VERSIONED_MIGRATION_NOT_APPLIED", "Detected resolved migration not applied to database: %s.\nTo fix this error, either run migrate, or set -ignoreMigrationPatterns='*:pending'.", i.Version())
		}
		return validationError("RESOLVED_REPEATABLE_MIGRATION_NOT_APPLIED", "Detected resolved repeatable migration not applied to database: %s.\nTo fix this error, either run migrate, or set -ignoreMigrationPatterns='*:pending'.", i.Description())
	}
	if !i.Context.IsPendingIgnored() && state == StateOutdated {
		return validationError("OUTDATED_REPEATABLE_MIGRATION", "Detected outdated resolved repeatable migration that should be re-applied to database: %s.\nRun migrate to execute this migration.", i.Description())
	}
	if i.Applied == nil {
		return nil
	}
	if i.Resolved != nil && i.Applied != nil && !strings.EqualFold(i.Type(), TypeDelete) && !isUndoType(i.Type()) {
		identifier := i.Applied.Script
		if i.Applied.VersionValid {
			identifier = "version " + i.Applied.Version
		}
		if !i.Applied.VersionValid || i.Context.AppliedBaseline == nil || i.Version() != nil && i.Version().Compare(*i.Context.AppliedBaseline) > 0 {
			if !typesEqual(i.Resolved.Type(), i.Applied.Type) {
				return validationError("TYPE_MISMATCH", "%s", mismatchMessage("type", identifier, i.Applied.Type, i.Resolved.Type()))
			}
			if (i.Resolved.Kind != KindRepeatable || i.Context.IsPendingIgnored() && state != StateOutdated && state != StateSuperseded) && !i.isChecksumMatching() {
				return validationError("CHECKSUM_MISMATCH", "%s", mismatchMessage("checksum", identifier, checksumText(i.Applied.Checksum), strconv.FormatInt(i.Resolved.Checksum, 10)))
			}
			if descriptionMismatch(i.Resolved.Description, i.Applied.Description) {
				return validationError("DESCRIPTION_MISMATCH", "%s", mismatchMessage("description", identifier, i.Applied.Description, abbreviateDescription(i.Resolved.Description)))
			}
		}
	}
	return nil
}

func typesEqual(a, b string) bool { return strings.EqualFold(a, b) }

func descriptionMismatch(resolved, applied string) bool {
	if applied == "<< no description >>" {
		return resolved != ""
	}
	return abbreviateDescription(resolved) != applied
}

func checksumText(v sql.NullInt64) string {
	if !v.Valid {
		return "null"
	}
	return strconv.FormatInt(v.Int64, 10)
}

func mismatchMessage(kind, identifier string, applied, resolved interface{}) string {
	return fmt.Sprintf("Migration %s mismatch for migration %s\n-> Applied to database : %v\n-> Resolved locally    : %v\nEither revert the changes to the migration, or run repair to update the schema history.", kind, identifier, applied, resolved)
}

func markAsUndone(version Version, pairs []*appliedPair) error {
	for i := len(pairs) - 1; i >= 0; i-- {
		row := pairs[i].Row
		if isSyntheticType(row.Type) {
			continue
		}
		pv, err := ParseVersion(row.Version)
		if err != nil || pv.Compare(version) != 0 {
			continue
		}
		if pairs[i].Attrs.Undone {
			return fmt.Errorf("Corrupted schema history: multiple undo entries for version %s", version)
		}
		pairs[i].Attrs.Undone = true
		return nil
	}
	return nil
}
