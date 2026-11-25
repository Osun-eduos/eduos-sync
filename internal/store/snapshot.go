package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Osun-eduos/eduos-sync/internal/names"
	"github.com/jackc/pgx/v5"
)

// Snapshot is everything a teacher needs to work offline: their classes and rolls, recent registers, and the
// current term's score sheets for the subjects they teach.
type Snapshot struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	User        SnapUser    `json:"user"`
	School      *SnapSchool `json:"school"`
	Session     *SnapPeriod `json:"session"`
	Term        *SnapTerm   `json:"term"`
	Classes     []SnapClass `json:"classes"`
	Subjects    []SnapSubj  `json:"subjects"`
}

type SnapUser struct {
	ID       string `json:"id"`
	FullName string `json:"fullName"`
	Role     string `json:"role"`
}

type SnapSchool struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SnapPeriod struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type SnapTerm struct {
	SnapPeriod
	Number     int             `json:"number"`
	Components json.RawMessage `json:"components"`
	SSSGrading string          `json:"sssGrading"`
}

type SnapStudent struct {
	ID         string `json:"id"`
	LearnerID  string `json:"learnerId"`
	FullName   string `json:"fullName"`
	Sex        string `json:"sex"`
	AdmittedOn string `json:"admittedOn"`
}

type SnapMark struct {
	StudentID string  `json:"studentId"`
	Status    string  `json:"status"`
	Note      *string `json:"note"`
}

type SnapRegister struct {
	Date  string     `json:"date"`
	Marks []SnapMark `json:"marks"`
}

type SnapClass struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Level     string         `json:"level"`
	Students  []SnapStudent  `json:"students"`
	Registers []SnapRegister `json:"registers"`
}

type SnapScore struct {
	StudentID string          `json:"studentId"`
	Marks     json.RawMessage `json:"marks"`
	Total     *float64        `json:"total"`
}

type SnapSheet struct {
	ID         string      `json:"id"`
	Status     string      `json:"status"`
	ReturnNote *string     `json:"returnNote"`
	Scores     []SnapScore `json:"scores"`
}

type SnapSubj struct {
	ClassSubjectID string        `json:"classSubjectId"`
	Code           string        `json:"code"`
	Name           string        `json:"name"`
	ClassID        string        `json:"classId"`
	ClassName      string        `json:"className"`
	Level          string        `json:"level"`
	Released       bool          `json:"released"`
	Students       []SnapStudent `json:"students"`
	Sheet          *SnapSheet    `json:"sheet"`
}

var ErrInactive = errors.New("account is not active")

const day = "2006-01-02"

func fullName(surname, first string, middle *string) string {
	parts := []string{surname, first}
	if middle != nil && *middle != "" {
		parts = append(parts, *middle)
	}
	return strings.Join(parts, " ")
}

// Snapshot builds the offline working set for a teacher. subjectName maps subject codes to names.
func (s *Store) Snapshot(ctx context.Context, userID string, registerDays int, today time.Time, subjectName func(string) string) (*Snapshot, error) {
	snap := &Snapshot{GeneratedAt: time.Now().UTC(), Classes: []SnapClass{}, Subjects: []SnapSubj{}}
	var schoolID, schoolName *string
	var active bool
	err := s.db.QueryRow(ctx, `SELECT u.id, u."fullName", u.role::text, u."isActive", u."schoolId", sc.name FROM "User" u LEFT JOIN "School" sc ON sc.id = u."schoolId" WHERE u.id = $1`, userID).
		Scan(&snap.User.ID, &snap.User.FullName, &snap.User.Role, &active, &schoolID, &schoolName)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return nil, ErrInactive
	}
	if err != nil {
		return nil, err
	}
	if schoolID != nil {
		snap.School = &SnapSchool{ID: *schoolID, Name: *schoolName}
	}

	var sess SnapPeriod
	var start, end time.Time
	err = s.db.QueryRow(ctx, `SELECT id, name, "startDate", "endDate" FROM "AcademicSession" WHERE "isCurrent"`).Scan(&sess.ID, &sess.Name, &start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return snap, nil
	}
	if err != nil {
		return nil, err
	}
	sess.StartDate, sess.EndDate = start.Format(day), end.Format(day)
	snap.Session = &sess

	var term SnapTerm
	var termEnd time.Time
	var termSession string
	err = s.db.QueryRow(ctx, `SELECT id, number, "startDate", "endDate", components, "sssGrading"::text, "sessionId" FROM "Term" WHERE "isCurrent"`).
		Scan(&term.ID, &term.Number, &start, &termEnd, &term.Components, &term.SSSGrading, &termSession)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && termSession == sess.ID {
		term.Name = names.Term(term.Number) + ", " + sess.Name
		term.StartDate, term.EndDate = start.Format(day), termEnd.Format(day)
		snap.Term = &term
	}

	// Classes this teacher is class teacher of.
	rows, err := s.db.Query(ctx, `SELECT id, level::text, arm FROM "ClassArm" WHERE "classTeacherId" = $1 AND "sessionId" = $2`, userID, sess.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c SnapClass
		var arm string
		if err := rows.Scan(&c.ID, &c.Level, &arm); err != nil {
			return nil, err
		}
		c.Name, c.Students, c.Registers = names.Class(c.Level, arm), []SnapStudent{}, []SnapRegister{}
		snap.Classes = append(snap.Classes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Subjects this teacher teaches.
	rows, err = s.db.Query(ctx, `SELECT cs.id, cs."subjectCode", ca.id, ca.level::text, ca.arm,
			EXISTS (SELECT 1 FROM "ClassResult" r WHERE r."classArmId" = ca.id AND r."termId" = $3)
		FROM "ClassSubject" cs JOIN "ClassArm" ca ON ca.id = cs."classArmId"
		WHERE cs."teacherId" = $1 AND ca."sessionId" = $2`, userID, sess.ID, termIDOrNil(snap.Term))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sj SnapSubj
		var arm string
		if err := rows.Scan(&sj.ClassSubjectID, &sj.Code, &sj.ClassID, &sj.Level, &arm, &sj.Released); err != nil {
			return nil, err
		}
		sj.Name, sj.ClassName, sj.Students = subjectName(sj.Code), names.Class(sj.Level, arm), []SnapStudent{}
		snap.Subjects = append(snap.Subjects, sj)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Rolls for every class involved.
	classIDs := map[string]bool{}
	for _, c := range snap.Classes {
		classIDs[c.ID] = true
	}
	for _, sj := range snap.Subjects {
		classIDs[sj.ClassID] = true
	}
	if len(classIDs) == 0 {
		return snap, nil
	}
	ids := keys(classIDs)
	roll := map[string][]SnapStudent{}
	rows, err = s.db.Query(ctx, `SELECT p."classArmId", st.id, st."learnerId", st.surname, st."firstName", st."middleName", st.sex::text, e."admittedOn"
		FROM "ClassPlacement" p
		JOIN "Student" st ON st.id = p."studentId"
		JOIN "ClassArm" ca ON ca.id = p."classArmId"
		JOIN "Enrolment" e ON e."studentId" = st.id AND e."schoolId" = ca."schoolId" AND e."leftOn" IS NULL
		WHERE p."classArmId" = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var classID, surname, first string
		var middle *string
		var st SnapStudent
		var admitted time.Time
		if err := rows.Scan(&classID, &st.ID, &st.LearnerID, &surname, &first, &middle, &st.Sex, &admitted); err != nil {
			return nil, err
		}
		st.FullName, st.AdmittedOn = fullName(surname, first, middle), admitted.Format(day)
		roll[classID] = append(roll[classID], st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, list := range roll {
		sort.Slice(list, func(i, j int) bool { return list[i].FullName < list[j].FullName })
	}
	for i := range snap.Classes {
		if r := roll[snap.Classes[i].ID]; r != nil {
			snap.Classes[i].Students = r
		}
	}
	for i := range snap.Subjects {
		if r := roll[snap.Subjects[i].ClassID]; r != nil {
			snap.Subjects[i].Students = r
		}
	}

	// Recent registers for the teacher's own classes.
	if len(snap.Classes) > 0 {
		classOnly := make([]string, 0, len(snap.Classes))
		byClass := map[string]int{}
		for i, c := range snap.Classes {
			classOnly = append(classOnly, c.ID)
			byClass[c.ID] = i
		}
		since := today.AddDate(0, 0, -registerDays)
		rows, err = s.db.Query(ctx, `SELECT r."classArmId", r.date, m."studentId", m.status::text, m.note
			FROM "AttendanceRegister" r JOIN "AttendanceMark" m ON m."registerId" = r.id
			WHERE r."classArmId" = ANY($1) AND r.date >= $2 ORDER BY r.date`, classOnly, since)
		if err != nil {
			return nil, err
		}
		regs := map[string]map[string]*SnapRegister{}
		for rows.Next() {
			var classID string
			var date time.Time
			var m SnapMark
			if err := rows.Scan(&classID, &date, &m.StudentID, &m.Status, &m.Note); err != nil {
				return nil, err
			}
			d := date.Format(day)
			if regs[classID] == nil {
				regs[classID] = map[string]*SnapRegister{}
			}
			if regs[classID][d] == nil {
				regs[classID][d] = &SnapRegister{Date: d, Marks: []SnapMark{}}
				snap.Classes[byClass[classID]].Registers = append(snap.Classes[byClass[classID]].Registers, SnapRegister{Date: d})
			}
			regs[classID][d].Marks = append(regs[classID][d].Marks, m)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for i := range snap.Classes {
			for j, r := range snap.Classes[i].Registers {
				snap.Classes[i].Registers[j] = *regs[snap.Classes[i].ID][r.Date]
			}
		}
	}

	// Current term's score sheets for the teacher's subjects.
	if snap.Term != nil && len(snap.Subjects) > 0 {
		csIDs := make([]string, 0, len(snap.Subjects))
		bySubj := map[string]int{}
		for i, sj := range snap.Subjects {
			csIDs = append(csIDs, sj.ClassSubjectID)
			bySubj[sj.ClassSubjectID] = i
		}
		rows, err = s.db.Query(ctx, `SELECT sh.id, sh."classSubjectId", sh.status::text, sh."returnNote", sc."studentId", sc.marks, sc.total
			FROM "ScoreSheet" sh LEFT JOIN "Score" sc ON sc."sheetId" = sh.id
			WHERE sh."classSubjectId" = ANY($1) AND sh."termId" = $2`, csIDs, snap.Term.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sheetID, csID, status string
			var note, studentID *string
			var marks []byte
			var total *float64
			if err := rows.Scan(&sheetID, &csID, &status, &note, &studentID, &marks, &total); err != nil {
				return nil, err
			}
			sj := &snap.Subjects[bySubj[csID]]
			if sj.Sheet == nil {
				sj.Sheet = &SnapSheet{ID: sheetID, Status: status, ReturnNote: note, Scores: []SnapScore{}}
			}
			if studentID != nil {
				sj.Sheet.Scores = append(sj.Sheet.Scores, SnapScore{StudentID: *studentID, Marks: marks, Total: total})
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	sort.Slice(snap.Classes, func(i, j int) bool { return snap.Classes[i].Name < snap.Classes[j].Name })
	sort.Slice(snap.Subjects, func(i, j int) bool {
		a, b := snap.Subjects[i], snap.Subjects[j]
		return a.ClassName < b.ClassName || (a.ClassName == b.ClassName && a.Name < b.Name)
	})
	return snap, nil
}

func termIDOrNil(t *SnapTerm) any {
	if t == nil {
		return nil
	}
	return t.ID
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
