package mock

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jev"
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
		timeout   time.Duration
		lookups   []lookup
		wantErr   string
	}{
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
				{position: 1, sentinel: jev.ErrAuthentication, status: 401},
				{position: 2, sentinel: jev.ErrServer, status: 503},
				{position: 3, sentinel: jev.ErrTimeout},
				{position: 4, sentinel: jev.ErrConnection},
				{position: 5, sentinel: jev.ErrRateLimit, status: 429},
				{position: 6, sentinel: jev.ErrConnection},
				{position: 7, sentinel: jev.ErrResponse},
				{position: 8, missing: true},
			},
		},
		{
			name:    "should read an error in the object shape for every record",
			file:    `{"error":503}`,
			lookups: []lookup{{position: 4, sentinel: jev.ErrServer, status: 503}},
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
			name:    "should time out after the run's own timeout",
			file:    `{"error":"timeout"}`,
			timeout: 7 * time.Second,
			lookups: []lookup{{position: 1, sentinel: jev.ErrTimeout, message: "onesie: request timed out after 7s"}},
		},
		{
			name:    "should name the variable the file came from",
			file:    `{"u":1.2,"t":"billing","r":"curt"}`,
			spelled: "ONESIE_MOCK",
			wantErr: "onesie: ONESIE_MOCK: question 'u' answered 1.2, which lies outside [0,1]",
		},
		{
			name: "should refuse one object over several lines that carries an id",
			file: "{\n  \"id\": \"T-1\",\n  " + full + "\n}\n",
			wantErr: "onesie: --mock: the file is one object over several lines that carries an id. " +
				"Write each answers line on a line of its own, or drop the id to answer every record",
		},
		{
			name:    "should refuse an unknown question id",
			file:    "{" + full + "}\n" + `{` + full + `,"x":0.2}`,
			wantErr: "onesie: --mock line 2: 'x' is not a question. Questions: u, t, r",
		},
		{
			name:    "should refuse an entry that leaves a question out",
			file:    `{"u":0.9,"t":"billing"}`,
			wantErr: "onesie: --mock: question 'r' has no answer, and an entry answers every question",
		},
		{
			name:    "should refuse a probability above 1",
			file:    `{"u":1.2,"t":"billing","r":"curt"}`,
			wantErr: "onesie: --mock: question 'u' answered 1.2, which lies outside [0,1]",
		},
		{
			name:    "should refuse a yes/no answer that is not a number",
			file:    `{"u":"yes","t":"billing","r":"curt"}`,
			wantErr: `onesie: --mock: question 'u' answered "yes", which is not a probability`,
		},
		{
			name:    "should refuse a number that names no option",
			file:    `{"u":0.9,"t":7,"r":"curt"}`,
			wantErr: "onesie: --mock: question 't' picked '7', which is not an option. Options: billing, platform",
		},
		{
			name:    "should refuse a name in another case",
			file:    "{" + full + "}\n" + `{"u":0.9,"t":"Billing","r":"curt"}`,
			wantErr: "onesie: --mock line 2: question 't' picked 'Billing', which is not an option. Options: billing, platform",
		},
		{
			name:    "should refuse a level that is not one",
			file:    `{"u":0.9,"t":"billing","r":"loud"}`,
			wantErr: "onesie: --mock: question 'r' rated 'loud', which is not a level. Levels: calm, curt, rude",
		},
		{
			name:    "should refuse a confidence of 2",
			file:    `{"u":0.9,"t":{"value":"billing","confidence":2},"r":"curt"}`,
			wantErr: "onesie: --mock: question 't' has confidence 2, which lies outside [0,1]",
		},
		{
			name:    "should refuse an unknown answer key",
			file:    `{"u":0.9,"t":{"value":"billing","weight":2},"r":"curt"}`,
			wantErr: "onesie: --mock: question 't' carries the key 'weight', which a mock answer does not take",
		},
		{
			name:    "should refuse a confidence on a yes/no answer",
			file:    `{"u":{"value":0.9,"confidence":0.5},"t":"billing","r":"curt"}`,
			wantErr: "onesie: --mock: question 'u' carries the key 'confidence', which a mock answer does not take",
		},
		{
			name:    "should refuse an error status that is not an error",
			file:    `{"error":200}`,
			wantErr: "onesie: --mock: error 200 is not one onesie can replay. Use a status from 400 to 599, timeout or connection",
		},
		{
			name:    "should refuse an error word it does not know",
			file:    `{"error":"slow"}`,
			wantErr: `onesie: --mock: error "slow" is not one onesie can replay. Use a status from 400 to 599, timeout or connection`,
		},
		{
			name:    "should refuse a line with no id under byID",
			file:    `{"id":"T-1",` + full + "}\n" + "{" + full + "}\n",
			byID:    true,
			wantErr: "onesie: --mock line 2 has no id, and --id matches the file by id",
		},
		{
			name:    "should refuse a repeated id",
			file:    `{"id":"T-1",` + full + "}\n" + `{"id":"T-2",` + full + "}\n" + `{"id":"T-1",` + full + "}\n",
			byID:    true,
			wantErr: "onesie: --mock line 3: id 'T-1' is also the id of line 1",
		},
		{
			name:    "should refuse a line that is not an object",
			file:    "{" + full + "}\n[1]\n",
			wantErr: "onesie: --mock line 2 is not a JSON object",
		},
		{
			name:    "should refuse a blank line",
			file:    "{" + full + "}\n\n{" + full + "}\n",
			wantErr: "onesie: --mock line 2 is blank",
		},
		{
			name:    "should refuse an empty file",
			file:    "",
			wantErr: "onesie: --mock: the file is empty",
		},
		{
			name:    "should refuse a file of blank lines as empty",
			file:    "\n \n",
			wantErr: "onesie: --mock: the file is empty",
		},
		{
			name:    "should refuse a line longer than the cap",
			file:    `{"u":0.9,"pad":"` + strings.Repeat("a", limits.MaxLineBytes) + `"}`,
			wantErr: "onesie: --mock line 1 is longer than 8388608 bytes, the max-line-bytes cap -V lists",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asked := tc.questions
			if asked == nil {
				asked = questions
			}

			answers, err := Load(strings.NewReader(tc.file), asked, Options{ByID: tc.byID, Spelled: tc.spelled, Timeout: tc.timeout})
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

	result, err := entry.Result()
	if look.sentinel != nil {
		if !errors.Is(err, look.sentinel) {
			t.Errorf("position %d error %v, want %v", look.position, err, look.sentinel)
		}

		var api *jev.APIError
		if look.status != 0 && (!errors.As(err, &api) || api.Status != look.status) {
			t.Errorf("position %d error %v, want status %d", look.position, err, look.status)
		}

		if look.message != "" && err.Error() != look.message {
			t.Errorf("position %d error %q, want %q", look.position, err.Error(), look.message)
		}

		return
	}

	if err != nil {
		t.Fatalf("position %d Result: %v", look.position, err)
	}

	for _, question := range questions {
		want, asked := look.want[question.ID]
		if !asked {
			continue
		}

		normalized, normErr := answer.Normalize(question, result.Answers[question.ID])
		if normErr != nil {
			t.Fatalf("question %s: %v", question.ID, normErr)
		}

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
	status     int
	message    string
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
			want:     "onesie: --mock has no line 3, so input line 5 has no answer. Add one",
		},
		{
			name:     "should name a mock line that answers nothing and the input line",
			file:     "{" + full + "}\n" + unread + "\n",
			position: 2,
			line:     2,
			want: "onesie: --mock line 2 replays a line onesie could not read, so input line 2 " +
				"has no answer. Give it answers",
		},
		{
			name: "should name a missing id and the input line",
			file: `{"id":"a",` + full + "}\n",
			byID: true,
			id:   "b",
			line: 4,
			want: "onesie: --mock has no line with id 'b', so input line 4 has no answer. Add one",
		},
		{
			name: "should name the mock line of an id that answers nothing",
			file: `{"id":"a",` + full + "}\n" + `{"id":"b","error":{"kind":"input","status":null,"message":"x"}}` + "\n",
			byID: true,
			id:   "b",
			line: 4,
			want: "onesie: --mock line 2 replays a line onesie could not read, so input line 4 " +
				"has no answer. Give it answers",
		},
		{
			name: "should name an object that answers nothing",
			file: unread,
			line: 1,
			want: "onesie: --mock replays a line onesie could not read, so input line 1 has no answer. " +
				"Give it answers",
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
