package gitlab

import (
	"os"
	"testing"
)

func TestParseUpgradeNotes(t *testing.T) {
	md, err := os.ReadFile("testdata/gitlab_19_changes.md")
	if err != nil {
		t.Fatal(err)
	}
	notes := ParseUpgradeNotes(string(md), 19, mustVersion(t, "19.4.2"), mustVersion(t, "19.5.1"))
	if len(notes) != 3 {
		t.Fatalf("got %d notes, want the three 19.5.0 notes: %+v", len(notes), notes)
	}
	kas := notes[0]
	if kas.Title != "Agent for Kubernetes address defaults to native gRPC" {
		t.Errorf("first note = %q", kas.Title)
	}
	if kas.Affects != "Linux package, Helm chart" || kas.AffectedVersions != "19.5.0 and later" {
		t.Errorf("meta = %q / %q", kas.Affects, kas.AffectedVersions)
	}
	if kas.URL != "https://docs.gitlab.com/update/versions/gitlab_19_changes/#agent-for-kubernetes-address-defaults-to-native-grpc" {
		t.Errorf("url = %s", kas.URL)
	}

	facts := Facts{Geo: false}
	want := map[string]string{
		"Agent for Kubernetes address defaults to native gRPC":      "check",
		"Geo upload checksumming by upload type enabled by default": "no: Geo is not configured",
	}
	for i := range notes {
		notes[i].Classify(facts)
		if w, ok := want[notes[i].Title]; ok && notes[i].Applies != w {
			t.Errorf("%s: applies = %q, want %q", notes[i].Title, notes[i].Applies, w)
		}
	}
}

func TestClassifyHelmOnly(t *testing.T) {
	n := UpgradeNote{Title: "NGINX Ingress replaced", Affects: "Helm chart"}
	n.Classify(Facts{})
	if n.Applies[:3] != "no:" {
		t.Errorf("applies = %q, want no:", n.Applies)
	}
}

func TestCountLegacyNotFinished(t *testing.T) {
	out := `
id      | table_name          | job_class_name                 | status     | progress
--------|---------------------|--------------------------------|------------|---------
main_1  | failed_jobs_archive | BackfillFailedJobs             | finalized  | 100.00%
main_2  | issues              | BackfillMissingWorkItemStatus  | active     | 40.00%
main_3  | todos               | MarkDoneTodos                  | paused     | 0.00%
main_4  | notes               | FixNotes                       | finished   | 100.00%
`
	if n := CountLegacyNotFinished(out); n != 2 {
		t.Errorf("not finished = %d, want 2", n)
	}
	if n := CountLegacyNotFinished("Migration A: finished\nMigration B: pending\n"); n != 1 {
		t.Errorf("old format not finished = %d, want 1", n)
	}
}

func TestParseBackgroundMigrations(t *testing.T) {
	js := `const backgroundMigrations = {"19.5":[{"has_large_table":true},{"has_large_table":false}],"19.4":[]};`
	got, err := ParseBackgroundMigrations([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if got["19.5"] != (MigrationCount{Total: 2, Large: 1}) || got["19.4"].Total != 0 {
		t.Errorf("got %+v", got)
	}
}
