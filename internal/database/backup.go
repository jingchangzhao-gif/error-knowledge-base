// Package database: data lifecycle for EKB-026.
//
// Three operations live here:
//
//   - BackupDatabase dumps the current database with pg_dump (custom format)
//     and records a manifest next to the dump: the SHA-256, the size, the exact
//     row counts of every business table, and the migration ledger position.
//     A dump counts as complete only after pg_restore --list proves the archive
//     contains every business table; a file that merely exists is not a backup.
//   - RestoreDatabase restores a manifest and verifies it into a *new* database
//     (an empty-db restore). Before restoring it checks the dump checksum, and
//     it refuses a target that already exists: restoring on top of data could
//     hide a lost table behind rows that happened to be there already. After
//     restoring it compares the row counts with the manifest and checks that the
//     key constraints came back.
//   - PreviewDelete / DeleteScoped and ApplyRetention wrap the SQL functions
//     from 0006_retention.sql. Deletion never touches audit_log and writes one
//     audit row itself; retention erases samples first, then whole events, and
//     finally expired audit rows (behind the database guard).
//
// Shortcut: pg_dump / pg_restore are external binaries, so they must be on PATH
// or in %ProgramFiles%\PostgreSQL\16\bin (or in ERRKB_PG_BIN). A pure-SQL backup
// would have to reimplement the server's archive format; upgrade if the tool
// must run without PostgreSQL client binaries.
//
// Shortcut: the connection parameters are rebuilt as a keyword DSN from the
// parsed config with sslmode disable/require; a deployment that uses custom TLS
// roots must run the tools itself. Local PostgreSQL (the plan's default) uses
// sslmode=disable, which this utility reproduces exactly.
package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Default retention policy, matching the policy defaults in the implementation
// plan. The effective values come from the connector configuration
// (spec/config.cue retention) and are passed to ApplyRetention explicitly.
const (
	DefaultSampleRetentionDays     = 7
	DefaultNormalizedRetentionDays = 90
	DefaultAuditRetentionDays      = 365
)

// BusinessTables is the section 4 entity set, in dependency order.
var BusinessTables = []string{
	"connectors",
	"sessions",
	"events",
	"issues",
	"prevention_artifacts",
	"prevention_artifact_versions",
	"issue_preventions",
	"prevention_verifications",
	"occurrences",
	"solutions",
	"solution_versions",
	"outcomes",
	"retrievals",
	"notices",
	"audit_log",
}

// RetentionPolicy holds the three independent retention classes.
type RetentionPolicy struct {
	SampleDays     int
	NormalizedDays int
	AuditDays      int
}

// DefaultRetentionPolicy returns the documented policy defaults.
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		SampleDays:     DefaultSampleRetentionDays,
		NormalizedDays: DefaultNormalizedRetentionDays,
		AuditDays:      DefaultAuditRetentionDays,
	}
}

// RetentionReport records what one retention pass removed.
type RetentionReport struct {
	SamplesCleared   int64
	EventsRemoved    int64
	AuditRowsRemoved int64
}

// ApplyRetention runs the three retention classes once. It is explicit rather
// than scheduled: the daemon (EKB-028) owns the clock, and tests would be
// non-deterministic if this package started a timer.
func ApplyRetention(ctx context.Context, conn *pgx.Conn, policy RetentionPolicy, now time.Time) (RetentionReport, error) {
	if policy.SampleDays < 0 || policy.NormalizedDays < 0 || policy.AuditDays < 0 {
		return RetentionReport{}, fmt.Errorf("retention: days must be zero or positive")
	}
	rows, err := conn.Query(ctx,
		`select kind, removed from errkb_purge_retention($1, $2, $3, $4)`,
		policy.SampleDays, policy.NormalizedDays, policy.AuditDays, now)
	if err != nil {
		return RetentionReport{}, fmt.Errorf("retention: %w", err)
	}
	defer rows.Close()

	var report RetentionReport
	for rows.Next() {
		var kind string
		var removed int64
		if err := rows.Scan(&kind, &removed); err != nil {
			return RetentionReport{}, fmt.Errorf("retention: %w", err)
		}
		switch kind {
		case "sample":
			report.SamplesCleared = removed
		case "normalized":
			report.EventsRemoved = removed
		case "audit":
			report.AuditRowsRemoved = removed
		}
	}
	if err := rows.Err(); err != nil {
		return RetentionReport{}, fmt.Errorf("retention: %w", err)
	}
	return report, nil
}

// DeleteScope selects the data a deletion may remove. At least one filter is
// required; Before is exclusive on the observation time (an event exactly at
// the cutoff is kept).
type DeleteScope struct {
	ProjectKey  string
	ConnectorID string
	Before      *time.Time
	Actor       string
}

// DeleteCounts is the preview and the result of a scoped deletion, table by
// table. Audit rows are never deleted by a scope.
type DeleteCounts struct {
	Issues                     int64
	Solutions                  int64
	SolutionVersions           int64
	Outcomes                   int64
	Retrievals                 int64
	Notices                    int64
	Occurrences                int64
	Events                     int64
	PreventionArtifacts        int64
	PreventionArtifactVersions int64
	IssuePreventions           int64
	PreventionVerifications    int64
}

func (scope DeleteScope) args() (any, any, any, error) {
	if scope.ProjectKey == "" && scope.ConnectorID == "" && scope.Before == nil {
		return nil, nil, nil, fmt.Errorf("deletion: at least one filter (project, connector or before) is required")
	}
	var project any
	if scope.ProjectKey != "" {
		project = scope.ProjectKey
	}
	var connector any
	if scope.ConnectorID != "" {
		connector = scope.ConnectorID
	}
	var before any
	if scope.Before != nil {
		before = *scope.Before
	}
	return project, connector, before, nil
}

// PreviewDelete reports exactly the rows DeleteScoped would remove.
func PreviewDelete(ctx context.Context, conn *pgx.Conn, scope DeleteScope) (DeleteCounts, error) {
	project, connector, before, err := scope.args()
	if err != nil {
		return DeleteCounts{}, err
	}
	rows, err := conn.Query(ctx,
		`select kind, rows from errkb_delete_preview($1::text, $2::uuid, $3::timestamptz)`,
		project, connector, before)
	if err != nil {
		return DeleteCounts{}, translateDeleteError(err)
	}
	defer rows.Close()
	return scanDeleteCounts(rows)
}

// DeleteScoped removes the previewed rows in one transaction and records a
// delete.scope row in audit_log (scope, counts, actor).
func DeleteScoped(ctx context.Context, conn *pgx.Conn, scope DeleteScope) (DeleteCounts, error) {
	project, connector, before, err := scope.args()
	if err != nil {
		return DeleteCounts{}, err
	}
	actor := scope.Actor
	if actor == "" {
		actor = "owner"
	}
	rows, err := conn.Query(ctx,
		`select kind, removed from errkb_delete_scope($1::text, $2::uuid, $3::timestamptz, $4::text)`,
		project, connector, before, actor)
	if err != nil {
		return DeleteCounts{}, translateDeleteError(err)
	}
	defer rows.Close()
	return scanDeleteCounts(rows)
}

func scanDeleteCounts(rows pgx.Rows) (DeleteCounts, error) {
	var counts DeleteCounts
	for rows.Next() {
		var kind string
		var value int64
		if err := rows.Scan(&kind, &value); err != nil {
			return DeleteCounts{}, fmt.Errorf("deletion: %w", err)
		}
		switch kind {
		case "prevention_artifacts":
			counts.PreventionArtifacts = value
		case "prevention_artifact_versions":
			counts.PreventionArtifactVersions = value
		case "issue_preventions":
			counts.IssuePreventions = value
		case "prevention_verifications":
			counts.PreventionVerifications = value
		case "issues":
			counts.Issues = value
		case "solutions":
			counts.Solutions = value
		case "solution_versions":
			counts.SolutionVersions = value
		case "outcomes":
			counts.Outcomes = value
		case "retrievals":
			counts.Retrievals = value
		case "notices":
			counts.Notices = value
		case "occurrences":
			counts.Occurrences = value
		case "events":
			counts.Events = value
		}
	}
	if err := rows.Err(); err != nil {
		return DeleteCounts{}, fmt.Errorf("deletion: %w", err)
	}
	return counts, nil
}

func translateDeleteError(err error) error {
	return fmt.Errorf("deletion: %w", err)
}

// BackupManifest is the integrity record stored next to a dump. Restore takes
// this file, not the dump alone, so a truncated or swapped file is visible.
type BackupManifest struct {
	Version       int              `json:"version"`
	Database      string           `json:"database"`
	Schema        string           `json:"schema"`
	CreatedAt     time.Time        `json:"created_at"`
	Dump          string           `json:"dump"`
	SHA256        string           `json:"sha256"`
	SizeBytes     int64            `json:"size_bytes"`
	Tables        map[string]int64 `json:"tables"`
	LedgerRows    int64            `json:"ledger_rows"`
	LedgerVersion string           `json:"ledger_version"`
}

// RestoreReport is what RestoreDatabase measured in the restored database.
type RestoreReport struct {
	Database      string
	Tables        map[string]int64
	LedgerRows    int64
	LedgerVersion string
}

// BackupDatabase writes a custom-format dump plus its manifest into dir and
// verifies the archive table of contents before returning.
func BackupDatabase(ctx context.Context, conn *pgx.Conn, dir string) (BackupManifest, error) {
	if dir == "" {
		return BackupManifest{}, fmt.Errorf("backup: a destination directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}

	schema, err := currentSchema(ctx, conn)
	if err != nil {
		return BackupManifest{}, err
	}
	cfg := conn.Config()
	name := fmt.Sprintf("errkb-%s.dump", time.Now().UTC().Format("20060102T150405.000000000Z"))
	dumpPath := filepath.Join(dir, name)

	pgDump, err := resolvePostgresTool("pg_dump")
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}
	dsn := keywordDSN(conn, cfg.Database)
	cmd := exec.CommandContext(ctx, pgDump, "-Fc", "--no-owner", "--no-privileges", "--file", dumpPath, dsn)
	cmd.Env = postgresEnv(cfg.Password)
	if output, err := cmd.CombinedOutput(); err != nil {
		return BackupManifest{}, fmt.Errorf("backup: pg_dump: %w: %s", err, strings.TrimSpace(string(output)))
	}

	size, digest, err := fileDigest(dumpPath)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}

	manifest := BackupManifest{
		Version:   1,
		Database:  cfg.Database,
		Schema:    schema,
		CreatedAt: time.Now().UTC(),
		Dump:      name,
		SHA256:    digest,
		SizeBytes: size,
		Tables:    map[string]int64{},
	}
	for _, table := range BusinessTables {
		count, err := tableRows(ctx, conn, schema, table)
		if err != nil {
			return BackupManifest{}, fmt.Errorf("backup: %w", err)
		}
		manifest.Tables[table] = count
	}
	manifest.LedgerRows, manifest.LedgerVersion, err = ledgerPosition(ctx, conn, schema)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}

	pgRestore, err := resolvePostgresTool("pg_restore")
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}
	listing, err := exec.CommandContext(ctx, pgRestore, "--list", dumpPath).Output()
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: pg_restore --list: %w", err)
	}
	for _, table := range BusinessTables {
		entry := fmt.Sprintf(" TABLE %s %s ", schema, table)
		if !strings.Contains(string(listing), entry) {
			return BackupManifest{}, fmt.Errorf("backup: the archive is incomplete: no table entry for %s.%s", schema, table)
		}
	}
	if !strings.Contains(string(listing), " TABLE "+schema+" "+LedgerTable+" ") {
		return BackupManifest{}, fmt.Errorf("backup: the archive is incomplete: no migration ledger")
	}

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}
	manifestPath := dumpPath + ".json"
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o600); err != nil {
		return BackupManifest{}, fmt.Errorf("backup: %w", err)
	}
	return manifest, nil
}

// ReadManifest loads a manifest from disk.
func ReadManifest(path string) (BackupManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("restore: %w", err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return BackupManifest{}, fmt.Errorf("restore: %w", err)
	}
	if manifest.Dump == "" || manifest.SHA256 == "" {
		return BackupManifest{}, fmt.Errorf("restore: the manifest is incomplete")
	}
	return manifest, nil
}

// RestoreDatabase restores a backup into a new database and verifies the result.
// manifestPath is the ".dump.json" file written by BackupDatabase; target must
// not exist. The caller owns cleanup (see DropDatabase).
func RestoreDatabase(ctx context.Context, admin *pgx.Conn, manifestPath, target string) (RestoreReport, error) {
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return RestoreReport{}, err
	}
	if target == "" {
		return RestoreReport{}, fmt.Errorf("restore: a target database name is required")
	}

	dumpPath := filepath.Join(filepath.Dir(manifestPath), manifest.Dump)
	size, digest, err := fileDigest(dumpPath)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("restore: %w", err)
	}
	if digest != manifest.SHA256 || size != manifest.SizeBytes {
		return RestoreReport{}, fmt.Errorf("restore: the dump does not match its manifest (checksum or size changed)")
	}

	var exists int
	if err := admin.QueryRow(ctx, `select count(*) from pg_database where datname = $1`, target).Scan(&exists); err != nil {
		return RestoreReport{}, fmt.Errorf("restore: %w", err)
	}
	if exists != 0 {
		return RestoreReport{}, fmt.Errorf("restore: database %q already exists; an empty database is required", target)
	}
	if _, err := admin.Exec(ctx, `create database `+pgx.Identifier{target}.Sanitize()); err != nil {
		return RestoreReport{}, fmt.Errorf("restore: create database: %w", err)
	}

	pgRestore, err := resolvePostgresTool("pg_restore")
	if err != nil {
		return RestoreReport{}, fmt.Errorf("restore: %w", err)
	}
	dsn := keywordDSN(admin, target)
	cmd := exec.CommandContext(ctx, pgRestore, "--no-owner", "--no-privileges", "--exit-on-error", "--dbname", dsn, dumpPath)
	cmd.Env = postgresEnv(admin.Config().Password)
	if output, err := cmd.CombinedOutput(); err != nil {
		return RestoreReport{}, fmt.Errorf("restore: pg_restore: %w: %s", err, strings.TrimSpace(string(output)))
	}

	restored, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("restore: connect to %q: %w", target, err)
	}
	defer func() { _ = restored.Close(context.WithoutCancel(ctx)) }()

	report := RestoreReport{Database: target, Tables: map[string]int64{}}
	for _, table := range BusinessTables {
		count, err := tableRows(ctx, restored, manifest.Schema, table)
		if err != nil {
			return RestoreReport{}, fmt.Errorf("restore: verify %s: %w", table, err)
		}
		if count != manifest.Tables[table] {
			return RestoreReport{}, fmt.Errorf("restore: %s has %d rows, the manifest recorded %d", table, count, manifest.Tables[table])
		}
		report.Tables[table] = count
	}
	report.LedgerRows, report.LedgerVersion, err = ledgerPosition(ctx, restored, manifest.Schema)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("restore: verify ledger: %w", err)
	}
	if report.LedgerRows != manifest.LedgerRows || report.LedgerVersion != manifest.LedgerVersion {
		return RestoreReport{}, fmt.Errorf("restore: the migration ledger does not match the manifest")
	}

	checks, err := constraintChecks(ctx, restored, manifest.Schema)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("restore: verify constraints: %w", err)
	}
	if checks["events fingerprint check"] == 0 {
		return RestoreReport{}, fmt.Errorf("restore: the events fingerprint CHECK did not come back")
	}
	if checks["issues fingerprint unique"] == 0 {
		return RestoreReport{}, fmt.Errorf("restore: the issues fingerprint unique index did not come back")
	}
	return report, nil
}

// DropDatabase terminates connections to name and drops it. It exists so tests
// and operators can clean up a verification database.
func DropDatabase(ctx context.Context, admin *pgx.Conn, name string) error {
	if name == "" {
		return nil
	}
	if _, err := admin.Exec(ctx,
		`select pg_terminate_backend(pid) from pg_stat_activity where datname = $1 and pid <> pg_backend_pid()`,
		name); err != nil {
		return fmt.Errorf("drop database %q: %w", name, err)
	}
	if _, err := admin.Exec(ctx, `drop database if exists `+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("drop database %q: %w", name, err)
	}
	return nil
}

func constraintChecks(ctx context.Context, conn *pgx.Conn, schema string) (map[string]int, error) {
	var fingerprintCheck int
	if err := conn.QueryRow(ctx, `
		select count(*) from pg_constraint c
		join pg_class t on t.oid = c.conrelid
		join pg_namespace n on n.oid = t.relnamespace
		where n.nspname = $1 and t.relname = 'events' and c.contype = 'c'`, schema).Scan(&fingerprintCheck); err != nil {
		return nil, err
	}
	var uniqueIndex int
	if err := conn.QueryRow(ctx, `
		select count(*) from pg_indexes
		where schemaname = $1 and tablename = 'issues' and indexname = 'issues_fingerprint_project_key_key'`, schema).Scan(&uniqueIndex); err != nil {
		return nil, err
	}
	return map[string]int{
		"events fingerprint check":  fingerprintCheck,
		"issues fingerprint unique": uniqueIndex,
	}, nil
}

func currentSchema(ctx context.Context, conn *pgx.Conn) (string, error) {
	var schema string
	if err := conn.QueryRow(ctx, `select current_schema()`).Scan(&schema); err != nil {
		return "", fmt.Errorf("current schema: %w", err)
	}
	if schema == "" {
		return "", fmt.Errorf("current schema: no schema is on the search path")
	}
	return schema, nil
}

func tableRows(ctx context.Context, conn *pgx.Conn, schema, table string) (int64, error) {
	var count int64
	query := `select count(*) from ` + pgx.Identifier{schema, table}.Sanitize()
	if err := conn.QueryRow(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s.%s: %w", schema, table, err)
	}
	return count, nil
}

func ledgerPosition(ctx context.Context, conn *pgx.Conn, schema string) (int64, string, error) {
	var rows int64
	var version string
	query := `select count(*), coalesce(max(version), '') from ` + pgx.Identifier{schema, LedgerTable}.Sanitize()
	if err := conn.QueryRow(ctx, query).Scan(&rows, &version); err != nil {
		return 0, "", fmt.Errorf("migration ledger: %w", err)
	}
	return rows, version, nil
}

func fileDigest(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

func resolvePostgresTool(name string) (string, error) {
	binary := name
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if dir := os.Getenv("ERRKB_PG_BIN"); dir != "" {
		if path := filepath.Join(dir, binary); fileExists(path) {
			return path, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		if root := os.Getenv("ProgramFiles"); root != "" {
			if path := filepath.Join(root, "PostgreSQL", "16", "bin", binary); fileExists(path) {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("cannot find %s: put the PostgreSQL client binaries on PATH or set ERRKB_PG_BIN", name)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func postgresEnv(password string) []string {
	env := os.Environ()
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	return env
}

// keywordDSN rebuilds a libpq keyword connection string with a different target
// database. The password is quoted; sslmode is disable when the source
// connection is not using TLS (the local default) and require otherwise.
func keywordDSN(conn *pgx.Conn, database string) string {
	cfg := conn.Config()
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	parts := []string{
		"host=" + quoteConnValue(host),
		fmt.Sprintf("port=%d", cfg.Port),
		"user=" + quoteConnValue(cfg.User),
		"dbname=" + quoteConnValue(database),
		"sslmode=" + sslMode(cfg),
	}
	if cfg.Password != "" {
		parts = append(parts, "password="+quoteConnValue(cfg.Password))
	}
	return strings.Join(parts, " ")
}

func sslMode(cfg *pgx.ConnConfig) string {
	if cfg.TLSConfig == nil {
		return "disable"
	}
	return "require"
}

func quoteConnValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'"
}
