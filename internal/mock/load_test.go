package mock

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	questions := threeQuestions()
	full := `"u":0.9,"t":"billing","r":"curt"`

	tests := []struct {
		name      string
		file      string
		questions []plan.Question
		byID      bool
		spelled   string
		asked     []string
		lookups   []lookup
		wantErr   string
	}{
		{
			name:  "should answer only the asked questions from an entry that answers every question",
			file:  "{" + full + "}\n",
			asked: []string{"u"},
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.9}, absent: []string{"t", "r"}},
			},
		},
		{
			name:  "should accept an entry that answers only the asked questions",
			file:  `{"u":0.4}` + "\n",
			asked: []string{"u"},
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.4}, absent: []string{"t", "r"}},
			},
		},
		{
			name:  "should refuse an entry missing an asked question",
			file:  `{"t":"billing","r":"curt"}` + "\n",
			asked: []string{"u"},
			wantErr: "--mock: the entry has no answer for question 'u'. " +
				"Every entry answers every question the run asks",
		},
		{
			name:    "should still refuse a key that names no question",
			file:    `{"u":0.4,"mood":"calm"}` + "\n",
			asked:   []string{"u"},
			wantErr: "--mock: 'mood' is not a question. Questions: u, t, r",
		},
		{
			name: "should answer every position from one object",
			file: "{" + full + "}\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.9, "t": "billing", "r": "curt"}},
				{position: 7, want: map[string]any{"u": 0.9, "t": "billing", "r": "curt"}},
				{position: 3, id: json.Number("12"), want: map[string]any{"u": 0.9}},
			},
		},
		{
			name: "should read one object written over several lines",
			file: "{\n  \"u\": 0.9,\n  \"t\": \"billing\",\n  \"r\": \"curt\"\n}\n",
			lookups: []lookup{
				{position: 2, want: map[string]any{"u": 0.9, "t": "billing", "r": "curt"}},
			},
		},
		{
			name:      "should answer the positional question under its id",
			file:      `{"answer":0.9}`,
			questions: []plan.Question{{ID: plan.PositionalID, Shape: plan.Noul}},
			lookups:   []lookup{{position: 1, want: map[string]any{plan.PositionalID: 0.9}}},
		},
		{
			name: "should match lines by position without an id",
			file: `{"u":0.1,"t":"billing","r":"calm"}` + "\n" + `{"id":"x","u":0.2,"t":"platform","r":"rude"}` + "\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.1, "t": "billing", "r": "calm"}},
				{position: 2, id: "nothing", want: map[string]any{"u": 0.2, "t": "platform", "r": "rude"}},
				{position: 3, missing: true},
			},
		},
		{
			name: "should match lines by id under byID",
			file: `{"id":"T-2","u":0.2,` + `"t":"platform","r":"rude"}` + "\n" + `{"id":7,"u":0.7,"t":"billing","r":"calm"}` + "\n",
			byID: true,
			lookups: []lookup{
				{position: 1, id: "T-2", want: map[string]any{"u": 0.2}},
				{position: 1, id: json.Number("7"), want: map[string]any{"u": 0.7}},
				{position: 2, id: "7", want: map[string]any{"u": 0.7}},
				{position: 2, id: "T-3", missing: true},
				{position: 2, missing: true},
			},
		},
		{
			name: "should read one line that carries an id as the lines shape",
			file: `{"id":"T-1",` + full + "}\n",
			byID: true,
			lookups: []lookup{
				{position: 1, id: "T-1", want: map[string]any{"u": 0.9}},
				{position: 1, id: "T-2", missing: true},
			},
		},
		{
			name: "should read one line that carries a model as the lines shape",
			file: `{"model":"mock",` + full + "}\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.9}},
				{position: 2, missing: true},
			},
		},
		{
			name: "should read one line that carries usage, assert or abstain as the lines shape",
			file: `{"usage":{"input_tokens":1},"assert":false,"abstain":true,` + full + "}\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.9}},
				{position: 2, missing: true},
			},
		},
		{
			name: "should read one line that carries a line number as the lines shape",
			file: `{"line":1,` + full + "}\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.9}},
				{position: 2, missing: true},
			},
		},
		{
			name: "should read one line that carries a written error as the lines shape",
			file: `{"error":{"kind":"http","status":503,"message":"503 busy"}}` + "\n",
			lookups: []lookup{
				{position: 1, sentinel: onesie.ErrServer, status: 503},
				{position: 2, missing: true},
			},
		},
		{
			name:      "should read a key that is a question id as that question, not as a line key",
			file:      `{"line":0.9}` + "\n",
			questions: []plan.Question{{ID: "line", Shape: plan.Noul}},
			lookups: []lookup{
				{position: 1, want: map[string]any{"line": 0.9}},
				{position: 3, want: map[string]any{"line": 0.9}},
			},
		},
		{
			name: "should load a gated answers line as onesie writes it",
			file: `{"id":"T-1","assert":false,"model":"jev-1.13.0","usage":{"input_tokens":120,"output_tokens":9},` +
				`"u":{"value":0.9,"decision":true},` +
				`"t":{"value":"billing","confidence":0.8,"p":{"billing":0.8,"platform":0.2},"fallback":"low_confidence","decision":"platform"},` +
				`"r":{"value":"curt","score":1.2,"norm":0.6,"confidence":0.7,"p":{"calm":0.1,"curt":0.6,"rude":0.3},"legend":{"0":"calm"}}}` + "\n" +
				`{"id":"T-2","abstain":true,"model":"jev-1.13.0","u":0.4,"t":"platform","r":"calm"}` + "\n",
			byID: true,
			lookups: []lookup{
				{position: 1, id: "T-1", want: map[string]any{"u": 0.9, "t": "billing", "r": "curt"}},
				{position: 2, id: "T-2", want: map[string]any{"u": 0.4, "t": "platform", "r": "calm"}},
			},
		},
		{
			name: "should take a yes/no answer as a number and as an object",
			file: `{"u":{"value":0.25},"t":"billing","r":"curt"}` + "\n" + `{"u":1,"t":"billing","r":"curt"}` + "\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.25}},
				{position: 2, want: map[string]any{"u": 1.0}},
			},
		},
		{
			name: "should take a pick answer as a name and as an object",
			file: `{"u":0.9,"t":{"value":"platform","confidence":0.4},"r":{"value":"rude"}}`,
			lookups: []lookup{
				{position: 1, want: map[string]any{"t": "platform", "r": "rude"}, confidence: map[string]float64{"t": 0.4, "r": 1}},
			},
		},
		{
			name: "should ignore the line number an -i lines run writes",
			file: `{"line":1,"u":0.9,"t":"platform","r":"rude"}` + "\n" + `{"line":3,"u":0.2,"t":"platform","r":"rude"}` + "\n",
			lookups: []lookup{
				{position: 1, want: map[string]any{"u": 0.9}},
				{position: 2, want: map[string]any{"u": 0.2}},
			},
		},
		{
			name:      "should take a rate answer as a number whose text is a level",
			file:      `{"n":4}`,
			questions: []plan.Question{rateQuestion("n", "1", "2", "3", "4", "5")},
			lookups:   []lookup{{position: 1, want: map[string]any{"n": "4"}}},
		},
		{
			name:      "should take a level index for a rate question from a request body",
			file:      `{"b":"2"}` + "\n" + `{"b":0}` + "\n",
			questions: []plan.Question{{ID: "b", Shape: plan.Rate, Origin: plan.OriginBody, Levels: []plan.Level{{}, {}, {}}}},
			lookups: []lookup{
				{position: 1, want: map[string]any{"b": "2"}},
				{position: 2, want: map[string]any{"b": "0"}},
			},
		},
		{
			name: "should read error entries of every kind",
			file: `{"error":401}` + "\n" + `{"error":503}` + "\n" + `{"error":"timeout"}` + "\n" +
				`{"error":"connection"}` + "\n" +
				`{"error":{"kind":"http","status":429,"message":"429 slow down"}}` + "\n" +
				`{"error":{"kind":"transport","status":null,"message":"connection error: reset"}}` + "\n" +
				`{"error":{"kind":"response","status":200,"message":"question 'u' has no answer"}}` + "\n" +
				`{"error":{"kind":"input","status":null,"message":"line 8: not json"}}` + "\n",
			lookups: []lookup{
				{position: 1, sentinel: onesie.ErrAuthentication, status: 401, message: "onesie: 401 status code, no body"},
				{position: 2, sentinel: onesie.ErrServer, status: 503},
				{position: 3, sentinel: onesie.ErrTimeout},
				{
					position: 4, sentinel: onesie.ErrConnection,
					message: "onesie: could not reach mock.invalid: mock connection error",
				},
				{position: 5, sentinel: onesie.ErrRateLimit, status: 429},
				{position: 6, sentinel: onesie.ErrConnection},
				{position: 7, unusable: true},
				{position: 8, missing: true},
			},
		},
		{
			name: "should fail an http entry with a body as the provider reads that body",
			file: `{"error":{"kind":"http","status":400,"body":{"detail":"Unknown model: m"}}}`,
			lookups: []lookup{{
				position: 1, sentinel: onesie.ErrUnknownModel, status: 400, message: "onesie: 400 Unknown model: m",
			}},
		},
		{
			name: "should fail an http entry with a null body as one with no body",
			file: `{"error":{"kind":"http","status":401,"body":null}}`,
			lookups: []lookup{{
				position: 1, sentinel: onesie.ErrAuthentication, status: 401, message: "onesie: 401 status code, no body",
			}},
		},
		{
			name: "should fail an http entry with a string body as a live body of that text",
			file: `{"error":{"kind":"http","status":503,"body":"<html>nope</html>"}}`,
			lookups: []lookup{{
				position: 1, sentinel: onesie.ErrServer, status: 503,
				message: "onesie: 503 Service Unavailable, with a response body that is not JSON",
			}},
		},
		{
			name:    "should read an error in the object shape for every record",
			file:    `{"error":503}`,
			lookups: []lookup{{position: 4, sentinel: onesie.ErrServer, status: 503}},
		},
		{
			name: "should allow blank lines at the end of a lines file",
			file: `{"u":0.1,"t":"billing","r":"calm"}` + "\n" + `{"u":0.2,"t":"billing","r":"calm"}` + "\n\n \n",
			lookups: []lookup{
				{position: 2, want: map[string]any{"u": 0.2}},
				{position: 3, missing: true},
			},
		},
		{
			name: "should time out as a transport that reports a timeout does",
			file: `{"error":"timeout"}`,
			lookups: []lookup{{
				position: 1, sentinel: onesie.ErrTimeout, message: "onesie: request to mock.invalid:443 timed out",
			}},
		},
		{
			name:    "should name the variable the file came from",
			file:    `{"u":1.2,"t":"billing","r":"curt"}`,
			spelled: "ONESIE_MOCK",
			wantErr: "ONESIE_MOCK: question 'u' answered 1.2, which lies outside [0,1]",
		},
		{
			name: "should refuse one object over several lines that carries an id",
			file: "{\n  \"id\": \"T-1\",\n  " + full + "\n}\n",
			wantErr: "--mock: the file is one object over several lines that carries 'id', " +
				"which -o json writes on an answers line. Write each answers line on a line of its own, " +
				"or drop 'id' to answer every record",
		},
		{
			name: "should refuse one object over several lines that carries a model",
			file: "{\n  \"model\": \"mock\",\n  " + full + "\n}\n",
			wantErr: "--mock: the file is one object over several lines that carries 'model', " +
				"which -o json writes on an answers line. Write each answers line on a line of its own, " +
				"or drop 'model' to answer every record",
		},
		{
			name:    "should refuse an unknown question id",
			file:    "{" + full + "}\n" + `{` + full + `,"x":0.2}`,
			wantErr: "--mock line 2: 'x' is not a question. Questions: u, t, r",
		},
		{
			name: "should refuse an entry that leaves a question out",
			file: `{"u":0.9,"t":"billing"}`,
			wantErr: "--mock: the entry has no answer for question 'r'. " +
				"Every entry answers every question the run asks",
		},
		{
			name:    "should refuse a probability above 1",
			file:    `{"u":1.2,"t":"billing","r":"curt"}`,
			wantErr: "--mock: question 'u' answered 1.2, which lies outside [0,1]",
		},
		{
			name:    "should refuse a yes/no answer that is not a number",
			file:    `{"u":"yes","t":"billing","r":"curt"}`,
			wantErr: `--mock: question 'u' answered "yes", which is not a probability`,
		},
		{
			name:    "should refuse a written p that does not add up to 1, naming the line",
			file:    "{" + full + "}\n" + `{"u":0.9,"t":{"value":"billing","p":{"billing":0.5,"platform":0.2}},"r":"curt"}`,
			wantErr: "--mock line 2: question 't' has p that adds up to 0.7, not 1",
		},
		{
			name:    "should refuse a number that names no option",
			file:    `{"u":0.9,"t":7,"r":"curt"}`,
			wantErr: "--mock: question 't' picked '7', which is not an option. Options: billing, platform",
		},
		{
			name:    "should refuse a name in another case",
			file:    "{" + full + "}\n" + `{"u":0.9,"t":"Billing","r":"curt"}`,
			wantErr: "--mock line 2: question 't' picked 'Billing', which is not an option. Options: billing, platform",
		},
		{
			name:    "should refuse a level that is not one",
			file:    `{"u":0.9,"t":"billing","r":"loud"}`,
			wantErr: "--mock: question 'r' rated 'loud', which is not a level. Levels: calm, curt, rude",
		},
		{
			name:    "should refuse a confidence of 2",
			file:    `{"u":0.9,"t":{"value":"billing","confidence":2},"r":"curt"}`,
			wantErr: "--mock: question 't' has confidence 2, which lies outside [0,1]",
		},
		{
			name:    "should refuse an unknown answer key",
			file:    `{"u":0.9,"t":{"value":"billing","weight":2},"r":"curt"}`,
			wantErr: "--mock: question 't' carries the key 'weight', which a mock answer does not take",
		},
		{
			name:    "should name the line of an answer Result.Check refuses, with no second prefix",
			file:    "{" + full + "}\n" + `{"u":1.5,"t":"billing","r":"curt"}`,
			wantErr: "--mock line 2: question 'u' answered 1.5, which lies outside [0,1]",
		},
		{
			name:    "should name a rate's p outside [0,1] when the score it gives lies outside the levels",
			file:    `{"u":0.9,"t":"billing","r":{"value":"calm","p":{"calm":1.5,"curt":-0.25,"rude":-0.25}}}`,
			wantErr: "--mock: question 'r' has p for '0' of 1.5, which lies outside [0,1]",
		},
		{
			name:    "should name a rate's p by its level index",
			file:    `{"u":0.9,"t":"billing","r":{"value":"curt","p":{"calm":0,"curt":1.5,"rude":0}}}`,
			wantErr: "--mock: question 'r' has p for '1' of 1.5, which lies outside [0,1]",
		},
		{
			name:    "should refuse a p that is not a number",
			file:    `{"u":0.9,"t":{"value":"billing","p":{"billing":"most","platform":0}},"r":"curt"}`,
			wantErr: `--mock: question 't' has p for 'billing' of "most", which is not a number`,
		},
		{
			name:    "should refuse a confidence that is not a number",
			file:    `{"u":0.9,"t":{"value":"billing","confidence":"high"},"r":"curt"}`,
			wantErr: `--mock: question 't' has confidence "high", which is not a number`,
		},
		{
			name:      "should refuse a box edge outside [0,1]",
			file:      `{"b":{"left":0.1,"top":0.1,"right":1.5,"bottom":0.2}}`,
			questions: []plan.Question{{ID: "b", Shape: plan.Box}},
			wantErr:   "--mock: box question 'b' has an edge or a confidence outside [0,1]",
		},
		{
			name:      "should refuse a box whose edges are out of order",
			file:      `{"b":{"left":0.5,"top":0.1,"right":0.2,"bottom":0.2}}`,
			questions: []plan.Question{{ID: "b", Shape: plan.Box}},
			wantErr: "--mock: box question 'b' has a left edge not left of its right edge, " +
				"or a top edge not above its bottom edge",
		},
		{
			name:    "should refuse a confidence on a yes/no answer",
			file:    `{"u":{"value":0.9,"confidence":0.5},"t":"billing","r":"curt"}`,
			wantErr: "--mock: question 'u' carries the key 'confidence', which a mock answer does not take",
		},
		{
			name:    "should refuse an error status that is not an error",
			file:    `{"error":200}`,
			wantErr: "--mock: error 200 is not one onesie can replay. Use a status from 400 to 599, timeout or connection",
		},
		{
			name:    "should refuse an error word it does not know",
			file:    `{"error":"slow"}`,
			wantErr: `--mock: error "slow" is not one onesie can replay. Use a status from 400 to 599, timeout or connection`,
		},
		{
			name:    "should refuse a line with no id under byID",
			file:    `{"id":"T-1",` + full + "}\n" + "{" + full + "}\n",
			byID:    true,
			wantErr: "--mock line 2 has no id, and --id matches the file by id",
		},
		{
			name:    "should refuse a repeated id",
			file:    `{"id":"T-1",` + full + "}\n" + `{"id":"T-2",` + full + "}\n" + `{"id":"T-1",` + full + "}\n",
			byID:    true,
			wantErr: "--mock line 3: id 'T-1' is also the id of line 1",
		},
		{
			name:    "should refuse a line that is not an object",
			file:    "{" + full + "}\n[1]\n",
			wantErr: "--mock line 2 is not a JSON object",
		},
		{
			name:    "should refuse a blank line",
			file:    "{" + full + "}\n\n{" + full + "}\n",
			wantErr: "--mock line 2 is blank",
		},
		{
			name:    "should refuse an empty file",
			file:    "",
			wantErr: "--mock: the file is empty",
		},
		{
			name:    "should refuse a file of blank lines as empty",
			file:    "\n \n",
			wantErr: "--mock: the file is empty",
		},
		{
			name:    "should refuse a line longer than the cap",
			file:    `{"u":0.9,"pad":"` + strings.Repeat("a", limits.MaxLineBytes) + `"}`,
			wantErr: "--mock line 1 is longer than 8388608 bytes, the max-line-bytes cap -V lists",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asked := tc.questions
			if asked == nil {
				asked = questions
			}

			answers, err := Load(strings.NewReader(tc.file), asked, Options{
				ByID: tc.byID, Spelled: tc.spelled, Asked: tc.asked,
			})
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error\n got: %v\nwant: %s", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			for _, look := range tc.lookups {
				checkLookup(t, answers, asked, look)
			}
		})
	}
}

func checkLookup(t *testing.T, answers *Answers, questions []plan.Question, look lookup) {
	t.Helper()

	entry, found := answers.Lookup(look.position, look.id)
	if look.missing {
		if found {
			t.Errorf("position %d id %v found an entry, want none", look.position, look.id)
		}

		return
	}

	if !found {
		t.Fatalf("position %d id %v found no entry", look.position, look.id)
	}

	if look.unusable {
		_, err := sendThrough(t, onesie.TypeSafe(), entry)

		var unusable *onesie.ResponseError
		if !errors.As(err, &unusable) || unusable.Status != http.StatusOK || err.Error() != "onesie: 200 response "+
			"could not be used: the response is not a System One result, since it has no answers" {
			t.Errorf("position %d error %v, want a 200 ResponseError", look.position, err)
		}

		return
	}

	if look.sentinel != nil {
		_, err := sendThrough(t, onesie.TypeSafe(), entry)

		if !errors.Is(err, look.sentinel) {
			t.Errorf("position %d error %v, want %v", look.position, err, look.sentinel)
		}

		var api *onesie.APIError
		if look.status != 0 && (!errors.As(err, &api) || api.Status != look.status) {
			t.Errorf("position %d error %v, want status %d", look.position, err, look.status)
		}

		if look.message != "" && err.Error() != look.message {
			t.Errorf("position %d error %q, want %q", look.position, err.Error(), look.message)
		}

		return
	}

	result := resultOf(entry)

	for _, id := range look.absent {
		if _, held := result.Answers[id]; held {
			t.Errorf("position %d answers question %s, which the run does not ask", look.position, id)
		}
	}

	for _, question := range questions {
		want, asked := look.want[question.ID]
		if !asked {
			continue
		}

		normalized := answer.Normalize(question, result.Answers[question.ID])

		if normalized.Value != want {
			t.Errorf("position %d question %s value %v, want %v", look.position, question.ID, normalized.Value, want)
		}

		if c, given := look.confidence[question.ID]; given && *normalized.Confidence != c {
			t.Errorf("question %s confidence %v, want %v", question.ID, *normalized.Confidence, c)
		}
	}
}

type lookup struct {
	position   int
	id         any
	want       map[string]any
	confidence map[string]float64
	missing    bool
	sentinel   error
	unusable   bool
	status     int
	message    string
	absent     []string
}

func threeQuestions() []plan.Question {
	return []plan.Question{
		{ID: "u", Shape: plan.Noul},
		{ID: "t", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "platform"}}},
		rateQuestion("r", "calm", "curt", "rude"),
	}
}

func rateQuestion(id string, labels ...string) plan.Question {
	question := plan.Question{ID: id, Shape: plan.Rate, Labelled: true}
	for _, label := range labels {
		question.Levels = append(question.Levels, plan.Level{Label: label})
	}

	return question
}

func TestAnswersMissing(t *testing.T) {
	t.Parallel()

	full := `"u":0.9,"t":"billing","r":"curt"`
	unread := `{"error":{"kind":"input","status":null,"message":"x"}}`

	tests := []struct {
		name     string
		file     string
		byID     bool
		position int
		id       any
		line     int
		want     string
	}{
		{
			name:     "should name the missing mock line and the input line",
			file:     "{" + full + "}\n{" + full + "}\n",
			position: 3,
			line:     5,
			want:     "--mock has no line 3, so input line 5 has no answer. Add one",
		},
		{
			name:     "should name a mock line that answers nothing and the input line",
			file:     "{" + full + "}\n" + unread + "\n",
			position: 2,
			line:     2,
			want: "--mock line 2 replays a line onesie could not read, so input line 2 " +
				"has no answer. Give it answers",
		},
		{
			name:     "should name the key that made a one line file a replay",
			file:     `{"abstain":true,` + full + "}\n",
			position: 2,
			line:     2,
			want: "--mock has no line 2, so input line 2 has no answer. The file was read as -o json " +
				"lines, since its one line carries abstain. Add a line per record, or drop abstain to answer " +
				"every record",
		},
		{
			name: "should name the key that made a one line file a replay under byID",
			file: `{"id":"a",` + full + "}\n",
			byID: true,
			id:   "b",
			line: 3,
			want: "--mock has no line with id 'b', so input line 3 has no answer. The file was read as " +
				"-o json lines, since its one line carries id. Add a line per record, or drop id to answer " +
				"every record",
		},
		{
			name: "should name a missing id and the input line",
			file: `{"id":"a",` + full + "}\n" + `{"id":"c",` + full + "}\n",
			byID: true,
			id:   "b",
			line: 4,
			want: "--mock has no line with id 'b', so input line 4 has no answer. Add one",
		},
		{
			name: "should name the mock line of an id that answers nothing",
			file: `{"id":"a",` + full + "}\n" + `{"id":"b","error":{"kind":"input","status":null,"message":"x"}}` + "\n",
			byID: true,
			id:   "b",
			line: 4,
			want: "--mock line 2 replays a line onesie could not read, so input line 4 " +
				"has no answer. Give it answers",
		},
		{
			name:     "should name a lone written error line that answers nothing",
			file:     unread,
			position: 1,
			line:     1,
			want: "--mock line 1 replays a line onesie could not read, so input line 1 " +
				"has no answer. Give it answers",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers, err := Load(strings.NewReader(tc.file), threeQuestions(), Options{ByID: tc.byID})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if got := answers.Missing(tc.position, tc.id, tc.line); got != tc.want {
				t.Errorf("message\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
