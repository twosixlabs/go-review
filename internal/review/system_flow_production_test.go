package review

import (
	"strings"
	"testing"
)

func TestSystemFlowTestOnlyImportedSupportIsNotAnExecutableComponent(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{
		"source.go":  "package main\nimport \"example.test/source/api\"\nfunc main(){api.Run()}\n",
		"api/api.go": "package api\nfunc Run(){}\n",
		"api/api_test.go": `package api
import ("testing";"example.test/source/fakes")
func TestRun(t *testing.T){fakes.Run();Run()}
`,
		"fakes/fake.go":    "package fakes\nimport \"net/http\"\nfunc Run(){http.HandleFunc(\"/fake\",func(http.ResponseWriter,*http.Request){})}\n",
		"unused/unused.go": "package unused\nfunc Available(){}\n",
	}, nil)
	unwired := false
	for _, c := range r.SystemFlow.Components {
		if strings.Contains(c.Package, "/fakes") {
			t.Fatal("test-only imports became production architecture")
		}
		unwired = unwired || strings.Contains(c.Package, "/unused")
	}
	if !unwired {
		t.Fatal("unobserved production declarations were discarded as test support")
	}
	for _, e := range r.SystemFlow.Exchanges {
		if e.Kind == "ingress" {
			t.Fatal("test-only HTTP registrations became production ingress")
		}
	}
}
