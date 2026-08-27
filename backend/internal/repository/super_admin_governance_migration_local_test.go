//go:build governancemigration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/internal/service"
	migrationassets "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const governanceMigrationName = "173_phase1_super_admin_governance.sql"

func TestGovernanceMigrationAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("GOVERNANCE_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("GOVERNANCE_TEST_DATABASE_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	preGovernance, governanceOnly := splitGovernanceMigrationFS(t)
	if err := applyMigrationsFS(ctx, db, preGovernance); err != nil {
		t.Fatalf("apply pre-Task-1 migrations: %v", err)
	}
	var originalHolder int64
	if err := db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,balance,concurrency,status,created_at,updated_at) VALUES($1,'synthetic','admin',0,1,'active',NOW(),NOW()) RETURNING id`, governanceEmail("existing-admin")).Scan(&originalHolder); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationsFS(ctx, db, governanceOnly); err != nil {
		t.Fatalf("apply governance migration: %v", err)
	}

	assertGovernanceSchema(t, ctx, db)
	assertOneActiveSeat(t, ctx, db)
	var migratedHolder, migratedVersion int64
	if err := db.QueryRowContext(ctx, `SELECT user_id,version FROM super_admin_seat WHERE singleton_id=1`).Scan(&migratedHolder, &migratedVersion); err != nil {
		t.Fatal(err)
	}
	if migratedHolder != originalHolder || migratedVersion != 1 {
		t.Fatalf("migrated seat=(%d,%d), want=(%d,1)", migratedHolder, migratedVersion, originalHolder)
	}

	testConcurrentSeatTransfer(t, ctx, db, migratedHolder, migratedVersion)
	testLifecycleAndCredentialInvalidation(t, ctx, db)
	testEmergencyRecoveryAndReplay(t, ctx, db)
	testTransferLifecycleRace(t, ctx, db)
	testAdminIdentityGovernance(t, ctx, db)
	testGovernanceFailureRollsBack(t, ctx, db)
	testGovernanceEventsAreAppendOnly(t, ctx, db)
	assertOneActiveSeat(t, ctx, db)
}

func splitGovernanceMigrationFS(t *testing.T) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	names, err := fs.Glob(migrationassets.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	governance := fstest.MapFS{}
	for _, name := range names {
		data, readErr := fs.ReadFile(migrationassets.FS, name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		entry := &fstest.MapFile{Data: data, Mode: 0o444}
		if name == governanceMigrationName {
			governance[name] = entry
		} else if name < governanceMigrationName {
			before[name] = entry
		}
		// Later milestones are intentionally excluded: this fixture freezes the
		// database immediately before and after governance so its governance
		// regression remains runnable as new migrations are added.
	}
	if _, ok := governance[governanceMigrationName]; !ok {
		t.Fatalf("missing %s", governanceMigrationName)
	}
	return before, governance
}

func assertGovernanceSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT session_version FROM users LIMIT 0`,
		`SELECT singleton_id,user_id,version,updated_at FROM super_admin_seat LIMIT 0`,
		`SELECT operation_id,actor_kind,recovery_nonce_fingerprint FROM governance_events LIMIT 0`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("schema query %q: %v", query, err)
		}
	}
	var indexCount, triggerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname='users_one_active_super_admin_idx'`).Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("single-seat index=%d err=%v", indexCount, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_trigger WHERE tgname IN ('users_super_admin_seat_guard','super_admin_seat_guard','governance_events_append_only') AND NOT tgisinternal`).Scan(&triggerCount); err != nil || triggerCount != 3 {
		t.Fatalf("governance triggers=%d err=%v", triggerCount, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(email,password_hash,role,balance,concurrency,status,created_at,updated_at) VALUES($1,'synthetic','root',0,1,'active',NOW(),NOW())`, governanceEmail("invalid-role")); err == nil {
		t.Fatal("undefined role unexpectedly passed database constraint")
	}
}

func testConcurrentSeatTransfer(t *testing.T, ctx context.Context, db *sql.DB, holder, version int64) {
	t.Helper()
	targets := []int64{createGovernanceUser(t, ctx, db, service.RoleAdmin), createGovernanceUser(t, ctx, db, service.RoleAdmin)}
	repo := NewGovernanceRepository(db)
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.TransferSuperAdmin(ctx, service.SuperAdminTransferInput{
				OperationID: uuid.New(), ActorUserID: holder, TargetUserID: targets[i],
				ExpectedSeatVersion: version, Reason: "synthetic concurrent transfer",
			})
		}(i)
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for _, transferErr := range errs {
		if transferErr == nil {
			successes++
		} else if errors.Is(transferErr, service.ErrSuperAdminConflict) || errors.Is(transferErr, service.ErrSuperAdminForbidden) {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent successes=%d conflicts=%d errors=%v", successes, conflicts, errs)
	}
	assertOneActiveSeat(t, ctx, db)
}

func testLifecycleAndCredentialInvalidation(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewGovernanceRepository(db)
	holder, _ := currentGovernanceSeat(t, ctx, db)
	ordinaryAdmin := createGovernanceUser(t, ctx, db, service.RoleAdmin)
	managedAdmin := createGovernanceUser(t, ctx, db, service.RoleAdmin)
	employee := createGovernanceUser(t, ctx, db, service.RoleUser)

	rejectedOperation := uuid.New()
	err := repo.ChangeUserLifecycle(ctx, service.UserLifecycleInput{
		OperationID: rejectedOperation, ActorUserID: ordinaryAdmin, TargetUserID: managedAdmin,
		Reason: "synthetic ordinary admin rejection", Action: "deactivate_user", NamedActor: true,
	})
	if !errors.Is(err, service.ErrUserLifecycleForbidden) {
		t.Fatalf("ordinary admin managed admin: %v", err)
	}
	assertGovernanceResult(t, ctx, db, rejectedOperation, "rejected")

	seatOperation := uuid.New()
	err = repo.ChangeUserLifecycle(ctx, service.UserLifecycleInput{
		OperationID: seatOperation, ActorUserID: holder, TargetUserID: holder,
		Reason: "synthetic seat holder rejection", Action: "deactivate_user", NamedActor: true,
	})
	if !errors.Is(err, service.ErrUserLifecycleForbidden) {
		t.Fatalf("seat holder lifecycle err=%v", err)
	}
	assertGovernanceResult(t, ctx, db, seatOperation, "rejected")

	keyStatuses := []string{
		service.StatusAPIKeyActive,
		"inactive",
		service.StatusAPIKeyQuotaExhausted,
		service.StatusAPIKeyExpired,
	}
	keyIDs := make([]int64, 0, len(keyStatuses))
	for _, status := range keyStatuses {
		key := "sk-governance-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		var keyID int64
		if err := db.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,status,created_at,updated_at) VALUES($1,$2,'synthetic',$3,NOW(),NOW()) RETURNING id`, managedAdmin, key, status).Scan(&keyID); err != nil {
			t.Fatal(err)
		}
		keyIDs = append(keyIDs, keyID)
	}
	if err := repo.ChangeUserLifecycle(ctx, service.UserLifecycleInput{OperationID: uuid.New(), ActorUserID: holder, TargetUserID: managedAdmin, Reason: "synthetic deactivate admin", Action: "deactivate_user", NamedActor: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ChangeUserLifecycle(ctx, service.UserLifecycleInput{OperationID: uuid.New(), ActorUserID: holder, TargetUserID: managedAdmin, Reason: "synthetic reactivate admin", Action: "reactivate_user", NamedActor: true}); err != nil {
		t.Fatal(err)
	}
	var userStatus string
	var sessionVersion int64
	if err := db.QueryRowContext(ctx, `SELECT status,session_version FROM users WHERE id=$1`, managedAdmin).Scan(&userStatus, &sessionVersion); err != nil {
		t.Fatal(err)
	}
	var nonDisabledKeys int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE id=ANY($1) AND status<>'disabled'`, pq.Array(keyIDs)).Scan(&nonDisabledKeys); err != nil {
		t.Fatal(err)
	}
	if userStatus != service.StatusActive || sessionVersion != 1 || nonDisabledKeys != 0 {
		t.Fatalf("reactivation restored credential: user=%s session=%d non_disabled_keys=%d", userStatus, sessionVersion, nonDisabledKeys)
	}

	if err := repo.ChangeUserLifecycle(ctx, service.UserLifecycleInput{OperationID: uuid.New(), ActorUserID: ordinaryAdmin, TargetUserID: employee, Reason: "synthetic employee lifecycle", Action: "deactivate_user", NamedActor: true}); err != nil {
		t.Fatalf("ordinary admin employee lifecycle: %v", err)
	}
}

func testEmergencyRecoveryAndReplay(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewGovernanceRepository(db)
	beforeHolder, beforeVersion := currentGovernanceSeat(t, ctx, db)
	target := createGovernanceUser(t, ctx, db, service.RoleAdmin)
	nonce := fmt.Sprintf("%064x", 1)
	result, err := repo.EmergencyRecoverSuperAdmin(ctx, service.EmergencyRecoveryInput{
		OperationID: uuid.New(), TargetUserID: target, DeploymentOperatorID: "synthetic-operator",
		Reason: "synthetic recovery", NonceFingerprint: nonce,
	})
	if err != nil || result.PreviousUserID != beforeHolder || result.SeatVersion != beforeVersion+1 {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
	_, err = repo.EmergencyRecoverSuperAdmin(ctx, service.EmergencyRecoveryInput{
		OperationID: uuid.New(), TargetUserID: beforeHolder, DeploymentOperatorID: "synthetic-operator",
		Reason: "synthetic replay", NonceFingerprint: nonce,
	})
	if !errors.Is(err, service.ErrSuperAdminRecoveryInvalid) {
		t.Fatalf("replay err=%v", err)
	}
	var replayEvents int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM governance_events WHERE safe_error_summary='recovery_nonce_replay' AND result='rejected'`).Scan(&replayEvents); err != nil || replayEvents != 1 {
		t.Fatalf("replay events=%d err=%v", replayEvents, err)
	}
	assertOneActiveSeat(t, ctx, db)
}

func testTransferLifecycleRace(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewGovernanceRepository(db)
	holder, version := currentGovernanceSeat(t, ctx, db)
	target := createGovernanceUser(t, ctx, db, service.RoleAdmin)
	operations := []uuid.UUID{uuid.New(), uuid.New()}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = repo.TransferSuperAdmin(ctx, service.SuperAdminTransferInput{OperationID: operations[0], ActorUserID: holder, TargetUserID: target, ExpectedSeatVersion: version, Reason: "synthetic transfer lifecycle race"})
	}()
	go func() {
		defer wg.Done()
		errs[1] = repo.ChangeUserLifecycle(ctx, service.UserLifecycleInput{OperationID: operations[1], ActorUserID: holder, TargetUserID: target, Reason: "synthetic transfer lifecycle race", Action: "deactivate_user", NamedActor: true})
	}()
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("transfer/lifecycle successes=%d errors=%v", successes, errs)
	}
	var events int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM governance_events WHERE operation_id = ANY($1)`, pq.Array(operations)).Scan(&events); err != nil || events != 2 {
		t.Fatalf("race events=%d err=%v", events, err)
	}
	assertOneActiveSeat(t, ctx, db)
}

func testAdminIdentityGovernance(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewGovernanceRepository(db)
	holder, _ := currentGovernanceSeat(t, ctx, db)
	ordinaryAdmin := createGovernanceUser(t, ctx, db, service.RoleAdmin)
	target := createGovernanceUser(t, ctx, db, service.RoleUser)
	denied := uuid.New()
	err := repo.ChangeUserRole(ctx, service.AdminRoleChangeInput{OperationID: denied, ActorUserID: ordinaryAdmin, TargetUserID: target, TargetRole: service.RoleAdmin, Reason: "synthetic denied promotion"})
	if !errors.Is(err, service.ErrAdminIdentityForbidden) {
		t.Fatalf("ordinary admin promotion err=%v", err)
	}
	assertGovernanceResult(t, ctx, db, denied, "rejected")
	if err := repo.ChangeUserRole(ctx, service.AdminRoleChangeInput{OperationID: uuid.New(), ActorUserID: holder, TargetUserID: target, TargetRole: service.RoleAdmin, Reason: "synthetic promotion"}); err != nil {
		t.Fatal(err)
	}
	var role string
	var sessionVersion int64
	if err := db.QueryRowContext(ctx, `SELECT role,session_version FROM users WHERE id=$1`, target).Scan(&role, &sessionVersion); err != nil {
		t.Fatal(err)
	}
	if role != service.RoleAdmin || sessionVersion != 1 {
		t.Fatalf("role=%s session=%d", role, sessionVersion)
	}
}

func testGovernanceFailureRollsBack(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
CREATE FUNCTION governance_reject_governance_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.reason = 'synthetic force rollback' THEN RAISE EXCEPTION 'synthetic governance failure'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER governance_reject_governance_event BEFORE INSERT ON governance_events
FOR EACH ROW EXECUTE FUNCTION governance_reject_governance_event()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS governance_reject_governance_event ON governance_events; DROP FUNCTION IF EXISTS governance_reject_governance_event()`)
	}()
	repo := NewGovernanceRepository(db)
	holder, version := currentGovernanceSeat(t, ctx, db)
	target := createGovernanceUser(t, ctx, db, service.RoleAdmin)
	_, err := repo.TransferSuperAdmin(ctx, service.SuperAdminTransferInput{OperationID: uuid.New(), ActorUserID: holder, TargetUserID: target, ExpectedSeatVersion: version, Reason: "synthetic force rollback"})
	if err == nil {
		t.Fatal("governance insert failure unexpectedly committed")
	}
	afterHolder, afterVersion := currentGovernanceSeat(t, ctx, db)
	var role string
	if err := db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=$1`, target).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if afterHolder != holder || afterVersion != version || role != service.RoleAdmin {
		t.Fatalf("rollback failed: before=(%d,%d) after=(%d,%d) target=%s", holder, version, afterHolder, afterVersion, role)
	}
}

func testGovernanceEventsAreAppendOnly(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT id FROM governance_events ORDER BY occurred_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE governance_events SET reason='mutated' WHERE id=$1`, id); err == nil {
		t.Fatal("governance event update unexpectedly succeeded")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM governance_events WHERE id=$1`, id); err == nil {
		t.Fatal("governance event delete unexpectedly succeeded")
	}
}

func createGovernanceUser(t *testing.T, ctx context.Context, db *sql.DB, role string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,balance,concurrency,status,session_version,created_at,updated_at) VALUES($1,'synthetic',$2,0,1,'active',0,NOW(),NOW()) RETURNING id`, governanceEmail(role), role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func governanceEmail(prefix string) string {
	return prefix + "-" + uuid.NewString() + "@example.invalid"
}

func currentGovernanceSeat(t *testing.T, ctx context.Context, db *sql.DB) (int64, int64) {
	t.Helper()
	var holder, version int64
	if err := db.QueryRowContext(ctx, `SELECT user_id,version FROM super_admin_seat WHERE singleton_id=1`).Scan(&holder, &version); err != nil {
		t.Fatal(err)
	}
	return holder, version
}

func assertGovernanceResult(t *testing.T, ctx context.Context, db *sql.DB, operationID uuid.UUID, want string) {
	t.Helper()
	var result string
	if err := db.QueryRowContext(ctx, `SELECT result::text FROM governance_events WHERE operation_id=$1`, operationID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != want {
		t.Fatalf("operation=%s result=%s want=%s", operationID, result, want)
	}
}

func assertOneActiveSeat(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var activeSeats, activeSuperAdmins int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM super_admin_seat s JOIN users u ON u.id=s.user_id WHERE s.singleton_id=1 AND u.role='super_admin' AND u.status='active' AND u.deleted_at IS NULL`).Scan(&activeSeats); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='super_admin' AND status='active' AND deleted_at IS NULL`).Scan(&activeSuperAdmins); err != nil {
		t.Fatal(err)
	}
	if activeSeats != 1 || activeSuperAdmins != 1 {
		t.Fatalf("active seats=%d active super admins=%d", activeSeats, activeSuperAdmins)
	}
}
