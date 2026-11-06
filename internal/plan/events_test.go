package plan_test

import (
	"reflect"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestEventsFor(t *testing.T) {
	tests := []struct {
		name      string
		questions []plan.Question
		want      []argv.Event
	}{
		{name: "should put lone question defaults before its selection", questions: []plan.Question{{ID: "q"}}, want: []argv.Event{{Name: "threshold", Value: "0.5"}, {Name: "fallback", Value: "yes"}}},
		{name: "should keep top level flags unbound beside several questions", questions: []plan.Question{{ID: "q"}, {ID: "other"}}, want: []argv.Event{{Name: "fallback", Value: "yes"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			built := &plan.Plan{Questions: tc.questions, Orphans: []argv.Event{{Name: "threshold", Value: "0.5"}}, Selections: []plan.Selection{{ID: "other", Events: []argv.Event{{Name: "threshold", Value: "0.8"}}}, {ID: "q", Events: []argv.Event{{Name: "fallback", Value: "yes"}}}}}
			if got := built.EventsFor("q"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EventsFor = %v, want %v", got, tc.want)
			}
		})
	}
}
