package migrate_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/migrate"
)

// The root package's migrate_test.go drives the migration end to end through
// real stacks. These tests own the package's own behaviour: which directories
// count, the exact plan and its wording, the order, and the failure modes.

func mkdir(t *testing.T, elem ...string) string {
	t.Helper()
	p := filepath.Join(elem...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNeededIsFalseWithoutADirectory(t *testing.T) {
	if migrate.Needed("") {
		t.Error("Needed(\"\") = true; an empty data dir is not a directory to migrate")
	}
	if migrate.Needed(filepath.Join(t.TempDir(), "absent")) {
		t.Error("Needed on a path that does not exist = true")
	}
	if migrate.Needed(t.TempDir()) {
		t.Error("Needed on an empty directory = true")
	}
}

func TestNeededWantsAServiceDirectory(t *testing.T) {
	// One known service directly under the data dir is enough.
	dir := t.TempDir()
	mkdir(t, dir, "kms")
	if !migrate.Needed(dir) {
		t.Error("Needed = false with <dir>/kms present")
	}

	// A regular file with a service's name is not a store.
	dir = t.TempDir()
	write(t, filepath.Join(dir, "sqs"), "not a directory")
	if migrate.Needed(dir) {
		t.Error("Needed = true for a file named sqs")
	}

	// A service already in the new layout is not the old layout.
	dir = t.TempDir()
	mkdir(t, dir, awsident.Region, "sqs")
	mkdir(t, dir, dozeaws.GlobalDir, "iam")
	if migrate.Needed(dir) {
		t.Error("Needed = true for a data dir already in the per-region layout")
	}
}

func TestNeededChecksEveryImplementedService(t *testing.T) {
	// Each service name, alone, must be recognised: a service missing from the
	// scan would be left behind by the migration and silently start empty.
	for _, svc := range dozeaws.Implemented {
		dir := t.TempDir()
		mkdir(t, dir, svc)
		if !migrate.Needed(dir) {
			t.Errorf("Needed = false with only <dir>/%s present", svc)
		}
	}
}

func TestPlanNamesEachMoveInServiceOrder(t *testing.T) {
	dir := t.TempDir()
	// Created in an order unlike dozeaws.Implemented, to show the plan follows
	// Implemented and not the directory listing (which is alphabetical).
	for _, svc := range []string{"sqs", "iam", "s3", "stepfunctions"} {
		mkdir(t, dir, svc)
	}
	mkdir(t, dir, "notes") // not a service: left out

	got := migrate.Plan(dir, "eu-west-1")
	want := migrate.Steps{Moves: []migrate.Move{
		{Service: "s3", From: filepath.Join(dir, "s3"), To: filepath.Join(dir, "eu-west-1", "s3")},
		{Service: "sqs", From: filepath.Join(dir, "sqs"), To: filepath.Join(dir, "eu-west-1", "sqs")},
		{Service: "iam", From: filepath.Join(dir, "iam"), To: filepath.Join(dir, "_global", "iam")},
		{Service: "stepfunctions", From: filepath.Join(dir, "stepfunctions"), To: filepath.Join(dir, "eu-west-1", "stepfunctions")},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Plan =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPlanSendsRegionlessServicesToGlobal(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "iam")
	mkdir(t, dir, "sts")
	got := migrate.Plan(dir, "ap-south-1")
	if len(got.Moves) != 2 {
		t.Fatalf("Plan = %+v, want two moves", got.Moves)
	}
	for _, m := range got.Moves {
		if want := filepath.Join(dir, "_global", m.Service); m.To != want {
			t.Errorf("%s moves to %s, want %s", m.Service, m.To, want)
		}
	}
}

func TestPlanWithoutARegionUsesTheDefault(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "sns")
	got := migrate.Plan(dir, "")
	want := filepath.Join(dir, awsident.Region, "sns")
	if len(got.Moves) != 1 || got.Moves[0].To != want {
		t.Errorf("Plan(dir, \"\") = %+v, want one move to %s", got.Moves, want)
	}
}

func TestPlanIsEmptyWhenThereIsNothingToMove(t *testing.T) {
	got := migrate.Plan(t.TempDir(), "us-east-1")
	if got.Moves != nil {
		t.Errorf("Plan on an empty dir = %+v, want no moves", got.Moves)
	}
	if got := migrate.Plan("", "us-east-1"); got.Moves != nil {
		t.Errorf("Plan(\"\") = %+v, want no moves", got.Moves)
	}
}

func TestDescribeIsEmptyForAnEmptyPlan(t *testing.T) {
	if got := (migrate.Steps{}).Describe(); got != nil {
		t.Errorf("Describe of nothing = %q, want nil", got)
	}
}

func TestDescribeCountsAndListsEveryMove(t *testing.T) {
	p := migrate.Steps{Moves: []migrate.Move{
		{Service: "sqs", From: "/d/sqs", To: "/d/us-east-1/sqs"},
		{Service: "iam", From: "/d/iam", To: "/d/_global/iam"},
		{Service: "kms", From: "/d/kms", To: "/d/us-east-1/kms"},
	}}
	want := []string{
		"moving 3 service directories into the per-region layout:",
		"  /d/sqs  ->  /d/us-east-1/sqs",
		"  /d/iam  ->  /d/_global/iam",
		"  /d/kms  ->  /d/us-east-1/kms",
	}
	if got := p.Describe(); !reflect.DeepEqual(got, want) {
		t.Errorf("Describe =\n%q\nwant\n%q", got, want)
	}

	one := migrate.Steps{Moves: p.Moves[:1]}
	if got := one.Describe(); len(got) != 2 || got[0] != "moving 1 service directories into the per-region layout:" {
		t.Errorf("Describe of one move = %q", got)
	}
}

func TestApplyRenamesWithoutTouchingContents(t *testing.T) {
	dir := t.TempDir()
	// A nested store with a file, a subdirectory and an empty file: all of it
	// must arrive byte for byte, because the migration never reads a file.
	write(t, filepath.Join(mkdir(t, dir, "sqs", "queues", "q1"), "msg.json"), `{"id":"1"}`)
	write(t, filepath.Join(dir, "sqs", "empty"), "")
	write(t, filepath.Join(mkdir(t, dir, "iam"), "users.json"), `["deploy"]`)

	steps, err := migrate.Apply(dir, "eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps.Moves) != 2 || steps.Moves[0].Service != "sqs" || steps.Moves[1].Service != "iam" {
		t.Fatalf("Apply returned %+v, want sqs then iam", steps.Moves)
	}

	for path, body := range map[string]string{
		filepath.Join(dir, "eu-west-1", "sqs", "queues", "q1", "msg.json"): `{"id":"1"}`,
		filepath.Join(dir, "eu-west-1", "sqs", "empty"):                    "",
		filepath.Join(dir, "_global", "iam", "users.json"):                 `["deploy"]`,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if string(got) != body {
			t.Errorf("%s = %q, want %q", path, got, body)
		}
	}
	for _, gone := range []string{"sqs", "iam"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still at the old path (err %v)", gone, err)
		}
	}
	if migrate.Needed(dir) {
		t.Error("Needed = true after Apply")
	}
}

func TestApplyTwiceMovesNothingTheSecondTime(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "dynamodb")
	if _, err := migrate.Apply(dir, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	steps, err := migrate.Apply(dir, "us-east-1")
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if steps.Moves != nil {
		t.Errorf("second Apply moved %+v", steps.Moves)
	}
	if _, err := os.Stat(filepath.Join(dir, "us-east-1", "dynamodb")); err != nil {
		t.Errorf("store gone after the second Apply: %v", err)
	}
}

func TestApplyOnNothingIsNotAnError(t *testing.T) {
	for _, dir := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "absent")} {
		steps, err := migrate.Apply(dir, "us-east-1")
		if err != nil || steps.Moves != nil {
			t.Errorf("Apply(%q) = %+v, %v; want an empty plan and no error", dir, steps, err)
		}
	}
}

func TestApplyRefusalNamesBothSidesAndStops(t *testing.T) {
	dir := t.TempDir()
	// s3 comes before sqs, so it is moved before the conflict is met; sqs
	// conflicts; kms comes after and must not be touched.
	write(t, filepath.Join(mkdir(t, dir, "s3"), "a"), "s3 data")
	write(t, filepath.Join(mkdir(t, dir, "sqs"), "a"), "old sqs")
	write(t, filepath.Join(mkdir(t, dir, "us-east-1", "sqs"), "a"), "new sqs")
	mkdir(t, dir, "kms")

	steps, err := migrate.Apply(dir, "us-east-1")
	if err == nil {
		t.Fatal("Apply over an existing destination = nil error")
	}
	dest := filepath.Join(dir, "us-east-1", "sqs")
	want := "dozeaws: cannot migrate sqs: " + dest + " already exists; move or remove one of them and start again"
	if err.Error() != want {
		t.Errorf("error = %q\nwant    %q", err, want)
	}
	if len(steps.Moves) != 3 {
		t.Errorf("the returned plan has %d moves, want the whole plan (3)", len(steps.Moves))
	}

	// Stops at the first problem: earlier moves stand, later ones are not tried.
	if b, err := os.ReadFile(filepath.Join(dir, "us-east-1", "s3", "a")); err != nil || string(b) != "s3 data" {
		t.Errorf("s3, moved before the conflict: %q, %v", b, err)
	}
	// Neither side of the conflict is changed.
	if b, err := os.ReadFile(filepath.Join(dir, "sqs", "a")); err != nil || string(b) != "old sqs" {
		t.Errorf("old sqs store changed: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "a")); err != nil || string(b) != "new sqs" {
		t.Errorf("new sqs store changed: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kms")); err != nil {
		t.Errorf("kms, after the conflict, was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "us-east-1", "kms")); !os.IsNotExist(err) {
		t.Errorf("kms appeared in the region (err %v)", err)
	}
}

func TestApplyRefusesAnExistingFileAtTheDestination(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "ssm")
	mkdir(t, dir, "us-east-1")
	write(t, filepath.Join(dir, "us-east-1", "ssm"), "a file in the way")
	_, err := migrate.Apply(dir, "us-east-1")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want a refusal naming the existing path", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "ssm")); serr != nil {
		t.Errorf("source moved despite the refusal: %v", serr)
	}
}

func TestApplyReportsADestinationDirectoryItCannotMake(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "sqs")
	// <dir>/us-east-1 is a file, so <dir>/us-east-1/sqs cannot be created.
	write(t, filepath.Join(dir, "us-east-1"), "in the way")

	steps, err := migrate.Apply(dir, "us-east-1")
	if err == nil {
		t.Fatal("Apply = nil error when the region directory cannot be made")
	}
	if !strings.HasPrefix(err.Error(), "dozeaws: migrate sqs: ") {
		t.Errorf("error = %q, want the prefix %q", err, "dozeaws: migrate sqs: ")
	}
	if len(steps.Moves) != 1 {
		t.Errorf("the returned plan = %+v, want the one move", steps.Moves)
	}
	if _, serr := os.Stat(filepath.Join(dir, "sqs")); serr != nil {
		t.Errorf("source gone though nothing could move: %v", serr)
	}
}

func TestApplyReportsARenameItCannotDo(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs directory permissions to bite")
	}
	dir := t.TempDir()
	mkdir(t, dir, "sqs")
	region := mkdir(t, dir, "us-east-1")
	// The region directory exists (MkdirAll succeeds) but cannot be written to,
	// so the rename into it fails.
	if err := os.Chmod(region, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(region, 0o755) })

	_, err := migrate.Apply(dir, "us-east-1")
	if err == nil {
		t.Fatal("Apply = nil error when the rename is refused")
	}
	if !strings.HasPrefix(err.Error(), "dozeaws: migrate sqs: ") {
		t.Errorf("error = %q, want the prefix %q", err, "dozeaws: migrate sqs: ")
	}
	if _, serr := os.Stat(filepath.Join(dir, "sqs")); serr != nil {
		t.Errorf("source gone though the rename failed: %v", serr)
	}
}
