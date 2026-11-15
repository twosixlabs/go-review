package review

import (
	"strings"
	"testing"
)

func TestQualityDiagnosticFailedThenPassedAttempts(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"Action":"run","Package":"example","Test":"TestF"}`,
		`{"Action":"fail","Package":"example","Test":"TestF"}`,
		`{"Action":"run","Package":"example","Test":"TestF"}`,
		`{"Action":"pass","Package":"example","Test":"TestF"}`,
	}, "\n"))
	attempts, err := unitTestAttempts(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Outcome != "fail" || attempts[1].Outcome != "pass" || attempts[1].Attempt != 2 {
		t.Fatalf("attempt history collapsed: %+v", attempts)
	}
	outcomes, err := unitTestOutcomes(data)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes["example::TestF"] != "fail" {
		t.Fatal("later pass hid an observed failure")
	}
}
