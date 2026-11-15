package testquality

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestQualityDiagnosticReviewedExampleAndFuzzSeeds(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example\n\ngo 1.24.0\n",
		"sample.go": "package example\nfunc F() int {return 1}\n",
		"sample_test.go": `package example
import ("testing";"fmt")
func ExampleF() {
fmt.Println(F())
// Output: 1
}
func FuzzF(f *testing.F) {f.Add(1);f.Fuzz(func(t *testing.T,n int) {if F()!=1 {t.Fatal("wrong")}})}
func BenchmarkF(b *testing.B) {for b.Loop() {F()}}
`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := review.Analyze(t.Context(), review.Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := review.CollectUnitEvidence(t.Context(), r, review.UnitEvidenceOptions{ArtifactDir: t.TempDir(), Selections: []review.UnitTestSelection{{Package: "example", Tests: []string{"ExampleF", "FuzzF"}}}}); err != nil {
		t.Fatal(err)
	}
	store := Store{Directory: t.TempDir()}
	a, err := store.Create(t.Context(), &review.RepositoryReview{Root: root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range a.Items {
		record, err := RunDiagnostic(t.Context(), a, DefaultDiagnosticPolicy(true), DiagnosticRequest{Kind: "repeat", Tests: []Identity{item.Identity}, Repetitions: 2, Reason: "Repeat reviewed normal entry, not a campaign", Evidence: []Citation{item.Declaration}}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, run := range record.Runs {
			for _, attempt := range run.Attempts {
				if attempt.Test == "example::"+item.Name {
					count++
				}
			}
			for _, command := range run.Commands {
				for _, arg := range command.Args {
					if arg == "-fuzz" || arg == "-bench" {
						t.Fatal("normal seed/example diagnostics broadened to a campaign")
					}
				}
			}
		}
		if count != 2 {
			t.Fatalf("reviewed %s repeated %d times", item.Name, count)
		}
	}
	if len(a.Items) != 2 {
		t.Fatalf("benchmark became an assessment/diagnostic target: %+v", a.Items)
	}
}
