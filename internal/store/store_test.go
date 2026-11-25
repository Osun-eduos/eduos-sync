package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Osun-eduos/eduos-sync/internal/config"
)

// These tests run against a database migrated by eduos-api (prisma migrate deploy). They are skipped unless
// SYNC_TEST_DATABASE_URL is set, and they erase that database's data.
func open(t *testing.T) *Store {
	t.Helper()
	raw := os.Getenv("SYNC_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("SYNC_TEST_DATABASE_URL not set")
	}
	url, err := config.PgxURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

const (
	school  = "00000000-0000-4000-8000-000000000001"
	teacher = "00000000-0000-4000-8000-000000000002"
	other   = "00000000-0000-4000-8000-000000000003"
	session = "00000000-0000-4000-8000-000000000004"
	term    = "00000000-0000-4000-8000-000000000005"
	jss2a   = "00000000-0000-4000-8000-000000000006"
	sss1a   = "00000000-0000-4000-8000-000000000007"
	ada     = "00000000-0000-4000-8000-000000000008"
	bola    = "00000000-0000-4000-8000-000000000009"
	newKid  = "00000000-0000-4000-8000-000000000010"
	mth     = "00000000-0000-4000-8000-000000000011"
	bio     = "00000000-0000-4000-8000-000000000012"
	sheet   = "00000000-0000-4000-8000-000000000013"
	reg     = "00000000-0000-4000-8000-000000000014"
	oldReg  = "00000000-0000-4000-8000-000000000015"
)

func seed(t *testing.T, s *Store, today time.Time) {
	t.Helper()
	ctx := context.Background()
	d := func(offset int) time.Time { return today.AddDate(0, 0, offset) }
	stmts := []struct {
		sql  string
		args []any
	}{
		{`TRUNCATE "Holiday", "SyncEvent", "Score", "ScoreSheet", "ClassSubject", "ClassResult", "ReportRemark", "Term", "AttendanceMark", "AttendanceRegister", "AuditLog", "RefreshToken", "Transfer", "StudentGuardian", "ClassPlacement", "Enrolment", "Student", "ClassArm", "AcademicSession", "Counter", "User", "School" CASCADE`, nil},
		{`INSERT INTO "School" (id, code, name, lga, level, ownership, "updatedAt") VALUES ($1, 'OS/ILE/COM/0001', 'Ilesa Grammar School', 'Ilesa East', 'JUNIOR_AND_SENIOR', 'PUBLIC', now())`, []any{school}},
		{`INSERT INTO "User" (id, "fullName", email, role, "schoolId", "passwordHash", "mustChangePassword", "updatedAt") VALUES ($1, 'Grace Akinola', 'g@x.test', 'TEACHER', $2, 'x', false, now()), ($3, 'Segun Ojo', 's@x.test', 'TEACHER', $2, 'x', false, now())`, []any{teacher, school, other}},
		{`INSERT INTO "AcademicSession" (id, name, "startDate", "endDate", "isCurrent") VALUES ($1, '2030/2031', $2, $3, true)`, []any{session, d(-60), d(200)}},
		{`INSERT INTO "Term" (id, "sessionId", number, "startDate", "endDate", "isCurrent", components) VALUES ($1, $2, 1, $3, $4, true, '[{"key":"CA1","label":"CA 1","max":20},{"key":"CA2","label":"CA 2","max":20},{"key":"EXAM","label":"Exam","max":60}]')`, []any{term, session, d(-50), d(30)}},
		{`INSERT INTO "ClassArm" (id, "schoolId", "sessionId", level, arm, "classTeacherId") VALUES ($1, $3, $4, 'JSS_2', 'A', $5), ($2, $3, $4, 'SSS_1', 'A', $6)`, []any{jss2a, sss1a, school, session, teacher, other}},
		{`INSERT INTO "Student" (id, "learnerId", surname, "firstName", "middleName", sex, "dateOfBirth", "currentSchoolId", "updatedAt") VALUES
			($1, 'OSN-30-000001', 'Bello', 'Ada', NULL, 'F', '2013-01-01', $4, now()),
			($2, 'OSN-30-000002', 'Adio', 'Bola', 'Kemi', 'F', '2013-01-01', $4, now()),
			($3, 'OSN-30-000003', 'Zaki', 'New', NULL, 'M', '2013-01-01', $4, now())`, []any{ada, bola, newKid, school}},
		{`INSERT INTO "Enrolment" (id, "studentId", "schoolId", "admissionNumber", "admittedOn") VALUES
			(gen_random_uuid(), $1, $4, '2030/0001', $5), (gen_random_uuid(), $2, $4, '2030/0002', $5), (gen_random_uuid(), $3, $4, '2030/0003', $6)`, []any{ada, bola, newKid, school, d(-40), d(0)}},
		{`INSERT INTO "ClassPlacement" (id, "studentId", "classArmId", "sessionId") VALUES (gen_random_uuid(), $1, $4, $5), (gen_random_uuid(), $2, $4, $5), (gen_random_uuid(), $3, $4, $5)`, []any{ada, bola, newKid, jss2a, session}},
		{`INSERT INTO "ClassSubject" (id, "classArmId", "subjectCode", "teacherId") VALUES ($1, $3, 'MTH', $5), ($2, $4, 'BIO', $5)`, []any{mth, bio, jss2a, sss1a, teacher}},
		{`INSERT INTO "ScoreSheet" (id, "classSubjectId", "termId", status, "returnNote", "updatedAt") VALUES ($1, $2, $3, 'RETURNED', 'Check Ada', now())`, []any{sheet, mth, term}},
		{`INSERT INTO "Score" (id, "sheetId", "studentId", marks, total, "updatedAt") VALUES (gen_random_uuid(), $1, $2, '{"CA1":18,"CA2":17,"EXAM":50}', 85, now()), (gen_random_uuid(), $1, $3, '{"CA1":10,"CA2":null,"EXAM":null}', NULL, now())`, []any{sheet, ada, bola}},
		{`INSERT INTO "AttendanceRegister" (id, "classArmId", date, "takenById", "updatedAt") VALUES ($1, $3, $4, $5, now()), ($2, $3, $6, $5, now())`, []any{reg, oldReg, jss2a, d(-1), teacher, d(-30)}},
		{`INSERT INTO "Holiday" (id, name, "startDate", "endDate") VALUES (gen_random_uuid(), 'Mid-term break', $1, $2), (gen_random_uuid(), 'Old holiday', $3, $3)`, []any{d(3), d(5), d(-400)}},
		{`INSERT INTO "AttendanceMark" (id, "registerId", "studentId", status, note) VALUES (gen_random_uuid(), $1, $3, 'PRESENT', NULL), (gen_random_uuid(), $1, $4, 'ABSENT', 'Sick'), (gen_random_uuid(), $2, $3, 'PRESENT', NULL)`, []any{reg, oldReg, ada, bola}},
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, st.sql)
		}
	}
}

func TestSnapshotAgainstDatabase(t *testing.T) {
	s := open(t)
	today := time.Date(2030, 11, 20, 0, 0, 0, 0, time.UTC)
	seed(t, s, today)
	names := map[string]string{"MTH": "Mathematics", "BIO": "Biology"}
	snap, err := s.Snapshot(context.Background(), teacher, 14, today, func(c string) string { return names[c] })
	if err != nil {
		t.Fatal(err)
	}
	if snap.User.FullName != "Grace Akinola" || snap.School.Name != "Ilesa Grammar School" || snap.Session.Name != "2030/2031" {
		t.Errorf("header: %+v %+v %+v", snap.User, snap.School, snap.Session)
	}
	if snap.Term == nil || snap.Term.Name != "First term, 2030/2031" || snap.Term.SSSGrading != "WAEC" || !json.Valid(snap.Term.Components) {
		t.Fatalf("term: %+v", snap.Term)
	}
	if len(snap.Classes) != 1 || snap.Classes[0].Name != "JSS 2A" || len(snap.Classes[0].Students) != 3 {
		t.Fatalf("classes: %+v", snap.Classes)
	}
	c := snap.Classes[0]
	if c.Students[0].FullName != "Adio Bola Kemi" || c.Students[2].AdmittedOn != today.Format(day) {
		t.Errorf("students: %+v", c.Students)
	}
	if len(c.Registers) != 1 || c.Registers[0].Date != today.AddDate(0, 0, -1).Format(day) || len(c.Registers[0].Marks) != 2 {
		t.Errorf("registers (only the last 14 days): %+v", c.Registers)
	}
	if len(snap.Subjects) != 2 || snap.Subjects[0].Name != "Mathematics" || snap.Subjects[1].ClassName != "SSS 1A" {
		t.Fatalf("subjects: %+v", snap.Subjects)
	}
	m := snap.Subjects[0]
	if m.Sheet == nil || m.Sheet.Status != "RETURNED" || *m.Sheet.ReturnNote != "Check Ada" || len(m.Sheet.Scores) != 2 || m.Released {
		t.Errorf("maths sheet: %+v", m.Sheet)
	}
	if len(snap.Holidays) != 1 || snap.Holidays[0].Name != "Mid-term break" || snap.Holidays[0].EndDate != today.AddDate(0, 0, 5).Format(day) {
		t.Errorf("holidays (only this session's): %+v", snap.Holidays)
	}
	if b := snap.Subjects[1]; b.Sheet != nil || len(b.Students) != 0 {
		t.Errorf("biology: %+v", b)
	}

	if _, err := s.Snapshot(context.Background(), "00000000-0000-4000-8000-0000000000ff", 14, today, func(c string) string { return c }); err != ErrInactive {
		t.Errorf("unknown user: %v", err)
	}
	if id, err := s.SheetID(context.Background(), mth); err != nil || id != sheet {
		t.Errorf("SheetID: %s %v", id, err)
	}
	if _, err := s.SheetID(context.Background(), bio); err != ErrNotFound {
		t.Errorf("SheetID for no sheet: %v", err)
	}
}

func TestEventsAgainstDatabase(t *testing.T) {
	s := open(t)
	seed(t, s, time.Date(2030, 11, 20, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	id := "11111111-2222-4333-8444-555555555555"
	unlock, err := s.Lock(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e := Event{ID: id, UserID: teacher, DeviceID: "phone-1", Type: "attendance.register", OccurredAt: time.Now(), Status: "REJECTED", HTTPStatus: 403, Result: json.RawMessage(`{"message":"no"}`)}
	if err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := s.SaveEvent(ctx, e); err != nil {
		t.Fatalf("saving twice should be a no-op: %v", err)
	}
	got, err := s.FindEvent(ctx, id)
	if err != nil || got.Status != "REJECTED" || got.HTTPStatus != 403 || got.UserID != teacher {
		t.Errorf("FindEvent: %+v %v", got, err)
	}
	if _, err := s.FindEvent(ctx, "99999999-2222-4333-8444-555555555555"); err != ErrNotFound {
		t.Errorf("missing event: %v", err)
	}
}
