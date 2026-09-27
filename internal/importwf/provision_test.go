package importwf

import "testing"

func TestSafeWorkflowName(t *testing.T) {
	n, err := SafeWorkflowName("../foo/bar.json")
	if err != nil || n != "bar.json" {
		t.Fatalf("%q %v", n, err)
	}
	n, err = SafeWorkflowName("My Graph")
	if err != nil || n != "My Graph.json" {
		t.Fatalf("%q %v", n, err)
	}
	if _, err := SafeWorkflowName(""); err == nil {
		t.Fatal("empty")
	}
}
