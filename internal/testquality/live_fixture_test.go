package testquality

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

const qualityLiveContract = `package qualityfixture
import "errors"
var ErrRange=errors.New("range")
// Contract: 1 and 2 are accepted; values below 1 or above 2 are rejected.
func Classify(n int)(string,error) {if n<1||n>2 {return "",ErrRange};return "accepted",nil}
`

func qualityLiveAssessment(t *testing.T, bodies string) *Assessment {
	t.Helper()
	tests := "package qualityfixture; import \"testing\"\n" + bodies
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, "contract_test.go", tests, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	report := &review.Report{Root: t.TempDir(), Module: "qualityfixture", Files: []review.File{{ID: "contract", Path: "contract.go", Package: "qualityfixture", Source: qualityLiveContract, SHA256: Digest([]byte(qualityLiveContract))}}}
	source := review.TestSource{File: review.File{ID: "tests", Path: "contract_test.go", Package: "qualityfixture", Source: tests, SHA256: Digest([]byte(tests))}}
	for _, declaration := range tree.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		source.Declarations = append(source.Declarations, review.TestDeclaration{ID: fn.Name.Name, Name: fn.Name.Name, Kind: "test", Location: review.Location{FileID: "tests", Line: fset.Position(fn.Pos()).Line, EndLine: fset.Position(fn.End()).Line}})
	}
	report.TestSources = []review.TestSource{source}
	store := Store{Directory: t.TempDir()}
	a, err := store.Create(t.Context(), &review.RepositoryReview{Root: report.Root, Modules: []review.ReviewedModule{{Report: report}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}
