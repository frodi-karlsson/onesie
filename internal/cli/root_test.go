package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestNewRootCmd(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	const twoAnswers = `{"model":"jev-1.13.0","answers":` +
		`{"a":{"type":"noul","noul":0.92},"extra":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	const picked = `{"model":"jev-1.13.0","answers":{"answer":{"type":"choice",` +
		`"choice":"billing","confidence":0.91,` +
		`"probabilities":{"billing":0.91,"technical":0.09}}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	tests := []struct {
		name     string
		args     []string
		stdin    string
		files    map[string]string
		status   int
		response string
		wantCode int
		contains []string
		absent   []string
		sends    []string
	}{
		{
			name:     "should print help when given no arguments",
			args:     []string{},
			wantCode: cli.ExitOK,
			contains: []string{"Usage:", "jev [question]"},
		},
		{
			name:     "should print the limits for the version flag",
			args:     []string{"-V"},
			wantCode: cli.ExitOK,
			contains: []string{"jev 1.2.3", "max-choice-options 255", "max-retry-after 1m0s"},
		},
		{
			name:     "should print full provenance for the version subcommand",
			args:     []string{"version"},
			wantCode: cli.ExitOK,
			contains: []string{"version   1.2.3", "commit    abc1234", "min-score-levels 2"},
		},
		{
			name:     "should prefix a cobra parse error",
			args:     []string{"--nope"},
			wantCode: cli.ExitUsage,
			contains: []string{"jev: unknown flag: --nope"},
		},
		{
			name:     "should document how to ask a question that begins with a dash",
			args:     []string{"--help"},
			wantCode: cli.ExitOK,
			contains: []string{"begins with a dash", "jev -o json -- '-is this urgent'"},
		},
		{
			name:     "should answer a question that begins with a dash after --",
			args:     []string{"-o", "json", "--", "-is this urgent"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.92}`},
		},
		{
			name:     "should answer a positional question from stdin",
			args:     []string{"is this urgent", "-o", "json"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.92}`, `"model":"jev-1.13.0"`},
			absent:   []string{`"usage"`},
		},
		{
			name:     "should add usage when asked",
			args:     []string{"is this urgent", "-o", "json", "--usage"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"usage":{"input_tokens":10,"output_tokens":2}`},
		},
		{
			name:     "should print the bare value with -r",
			args:     []string{"is this urgent", "-r"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{"0.92"},
		},
		{
			name:     "should answer a pick question",
			args:     []string{"which team", "--pick", "billing,technical", "-r"},
			stdin:    "i was charged twice",
			response: picked,
			wantCode: cli.ExitOK,
			contains: []string{"billing"},
		},
		{
			name:     "should exit ok with -q above the threshold",
			args:     []string{"is this urgent", "-q", "--threshold", "0.9"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			absent:   []string{"0.92"},
		},
		{
			name:     "should exit rejected with -q below the threshold",
			args:     []string{"is this urgent", "-q", "--threshold", "0.95"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitRejected,
		},
		{
			name:     "should reject a state-less invocation",
			args:     []string{"is this urgent"},
			wantCode: cli.ExitUsage,
			contains: []string{"jev: no state given. Pipe one to stdin, or pass --state or --state-file"},
		},
		{
			name:     "should reject a flagged invocation that carries no question",
			args:     []string{"-o", "json"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: no question given. Pass a question, --ask, or -f"},
			absent:   []string{"Usage:", "Flags:"},
		},
		{
			name:     "should reject -r with two questions",
			args:     []string{"--ask", "a=one", "--ask", "b=two", "-r"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"-r needs a single question"},
		},
		{
			name:     "should reject -o raw with two questions",
			args:     []string{"--ask", "a=one", "--ask", "b=two", "-o", "raw"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: -o raw needs a single question. 'a', 'b' were asked"},
		},
		{
			name:     "should reject two --ask flags sharing an id",
			args:     []string{"--ask", "a=one", "--ask", "a=two"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: question id 'a' is given twice"},
		},
		{
			name:     "should reject a reserved question id",
			args:     []string{"--ask", "model=one"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"'model' is reserved"},
		},
		{
			name: "should warn about a partly described option set",
			args: []string{
				"which team", "--pick", "billing,technical", "--desc", "billing=payments", "-r",
			},
			stdin:    "i was charged twice",
			response: picked,
			wantCode: cli.ExitOK,
			contains: []string{"warning:", "but not technical"},
		},
		{
			name:     "should reject an unknown output mode",
			args:     []string{"is this urgent", "-o", "yaml"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"-o takes"},
		},
		{
			name: "should print the fallback word when the request fails",
			args: []string{
				"is this safe", "--pick", "safe,refuse",
				"--min-confidence", "0.7", "--fallback", "refuse", "-r",
			},
			stdin:    "rm -rf /",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{"refuse"},
		},
		{
			name:     "should print an empty line when a failed question has no fallback",
			args:     []string{"is this urgent", "-r"},
			stdin:    "body",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			absent:   []string{"refuse"},
		},
		{
			name:  "should exit unavailable when the answer has the wrong shape",
			args:  []string{"is this urgent", "-o", "json"},
			stdin: "body",
			response: `{"model":"jev-1.13.0","answers":{"answer":{"type":"choice",` +
				`"choice":"a","confidence":0.5,"probabilities":{"a":1}}}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"kind":"response"`, `"status":200`,
				"expects a noul answer, got choice",
			},
			absent: []string{`"kind":"transport"`},
		},
		{
			name: "should emit a response error for a 200 body it cannot use",
			args: []string{
				"--ask", "team=which team", "--pick", "billing,technical",
				"--min-confidence", "0.7", "--fallback", "human", "-o", "json",
			},
			stdin:    "body",
			response: "not json at all",
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"error"`, `"kind":"response"`, `"status":200`,
				`"decision":"human"`, `"fallback":"error"`,
			},
			absent: []string{`"kind":"transport"`, `"kind":"http"`},
		},
		{
			name: "should emit the error key in json on failure",
			args: []string{
				"--ask", "team=which team", "--pick", "billing,technical",
				"--min-confidence", "0.7", "--fallback", "human", "-o", "json",
			},
			stdin:    "body",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"error"`, `"kind":"http"`, `"status":500`,
				`"decision":"human"`, `"fallback":"error"`,
			},
		},
		{
			name:  "should answer questions from a file",
			args:  []string{"-f", "q.yaml", "-o", "json"},
			stdin: "the server is down",
			files: map[string]string{"q.yaml": "urgent: does this convey urgency\n"},
			response: `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":1,"output_tokens":1}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"urgent":{"value":0.92}`},
		},
		{
			name:  "should apply policy from a file",
			args:  []string{"-f", "q.yaml", "-o", "json"},
			stdin: "the server is down",
			files: map[string]string{
				"q.yaml": "urgent:\n  ask: does this convey urgency\n  threshold: 0.5\n",
			},
			response: `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"decision":true`},
		},
		{
			name:     "should reject an id defined in both the file and --ask",
			args:     []string{"-f", "q.yaml", "--ask", "urgent=different"},
			stdin:    "body",
			files:    map[string]string{"q.yaml": "urgent: does this convey urgency\n"},
			wantCode: cli.ExitUsage,
			contains: []string{"q.yaml", "Pass --replace to override"},
		},
		{
			name:     "should let --replace override a file question",
			args:     []string{"-f", "q.yaml", "--ask", "urgent=different", "--replace", "-r"},
			stdin:    "body",
			files:    map[string]string{"q.yaml": "urgent: does this convey urgency\n"},
			response: `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.5}}}`,
			wantCode: cli.ExitOK,
			contains: []string{"0.5"},
			sends:    []string{"different"},
		},
		{
			name:     "should report a missing file",
			args:     []string{"-f", "absent.yaml"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"absent.yaml"},
		},
		{
			name: "should take the state from a request body",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"from the body","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"a":{"value":0.3}`},
			sends:    []string{`"state":"from the body"`},
		},
		{
			name: "should send a body's state in the order it was written",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":{"ticket_id":12345678901234567890,"zebra":1,"alpha":2},` +
					`"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"state":{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`,
			},
		},
		{
			name:  "should let stdin override a body's state",
			args:  []string{"-f", "body.json", "-o", "json"},
			stdin: "from stdin",
			files: map[string]string{
				"body.json": `{"state":"from the body","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"a":{"value":0.3}`},
			sends:    []string{`"state":"from stdin"`},
		},
		{
			name: "should take the model from a request body",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","model":"jev-1.9.9","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"jev-1.9.9","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"model":"jev-1.9.9"`},
			sends:    []string{`"model":"jev-1.9.9"`},
		},
		{
			name: "should let -m override a request body's model",
			args: []string{"-f", "body.json", "-o", "json", "-m", "jev-1.2.0"},
			files: map[string]string{
				"body.json": `{"state":"x","model":"jev-1.9.9","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"jev-1.2.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"model":"jev-1.2.0"`},
		},
		{
			name: "should reject a null state in a request body",
			args: []string{"-f", "body.json"},
			files: map[string]string{
				"body.json": `{"state":null,"questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"state must be a string, object or array"},
		},
		{
			name: "should reject a policy flag on a multi question body",
			args: []string{"-f", "body.json", "--threshold", "0.5"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"a":{"type":"noul","instructions":"q"},` +
					`"b":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"one question", "body.json has 2"},
		},
		{
			name: "should allow policy on an ask added beside a one question body",
			args: []string{
				"-f", "body.json", "--ask", "extra=is this urgent",
				"--threshold", "0.8", "-o", "json",
			},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: twoAnswers,
			wantCode: cli.ExitOK,
			contains: []string{`"a":{"value":0.92`, `"extra":{"value":0.92`},
		},
		{
			name: "should orphan a top level flag when a file and one ask both ask",
			args: []string{
				"-f", "one.yaml", "--threshold", "0.8", "--ask", "other=is this urgent",
			},
			files:    map[string]string{"one.yaml": "urgent: is this urgent\n"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"--threshold given with no --ask to bind to and 2 questions asked"},
		},
		{
			name:     "should reject a question file holding a second document",
			args:     []string{"-f", "md.yaml"},
			files:    map[string]string{"md.yaml": "a: q1\n---\nb: q2\n"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"a question file is one document"},
		},
		{
			name:  "should load a question file holding a separator inside a block scalar",
			args:  []string{"-f", "block.yaml", "-o", "json"},
			files: map[string]string{"block.yaml": "a:\n  ask: |-\n    one\n    ---\n    two\n"},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"instructions":"one\n---\ntwo"`},
		},
		{
			name: "should merge a top level desc into the file's yes/no criteria",
			args: []string{"-f", "y.yaml", "--desc", "yes=CLI YES", "-o", "json"},
			files: map[string]string{
				"y.yaml": "urgent:\n  ask: q\n  yes_means: FILE YES\n  no_means: FILE NO\n",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"true":"CLI YES"`, `"false":"FILE NO"`},
		},
		{
			name: "should reject a shape flag aimed at a request body",
			args: []string{"-f", "body.json", "--pick", "x,y"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"--pick cannot reshape a request body's question"},
		},
		{
			name: "should keep a body's null score criteria null on the wire",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q","criteria":[null,null]}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"Calm","1":"Frustrated"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":[null,null]`},
			absent:   []string{`"criteria":["",""]`},
		},
		{
			name: "should keep flag score criteria in the order they were given",
			args: []string{
				"--ask", "mood=how is it", "--rate", "urgent,normal,cold", "-o", "json",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"mood":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b","2":"c"},` +
				`"probabilities":{"0":0.1,"1":0.1,"2":0.8}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":["urgent","normal","cold"]`},
		},
		{
			name: "should keep a question file's score criteria in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "mood:\n  ask: how is it\n  rate:\n    urgent: sky falling\n" +
					"    normal: a normal day\n    cold: nothing happening\n",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"mood":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b","2":"c"},` +
				`"probabilities":{"0":0.1,"1":0.1,"2":0.8}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":["sky falling","a normal day","nothing happening"]`,
			},
		},
		{
			name: "should keep a body's score criteria in the order they were written",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q",` +
					`"criteria":["zulu","mike","alpha"]}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b","2":"c"},` +
				`"probabilities":{"0":0.1,"1":0.1,"2":0.8}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":["zulu","mike","alpha"]`},
		},
		{
			name:     "should reject replace with no file to override",
			args:     []string{"--replace", "is this urgent"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: --replace applies to -f, which was not given"},
		},
		{
			name: "should reject a structured fallback in a question file",
			args: []string{"-f", "policy.yaml"},
			files: map[string]string{
				"policy.yaml": "team:\n  ask: q\n  pick: [a, b]\n" +
					"  min_confidence: 0.7\n  fallback:\n    x: 1\n",
			},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"'fallback' in question 'team' must be a string"},
			absent:   []string{`"decision"`},
		},
		{
			name: "should stay quiet about a replayed body's partly described criteria",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"choice","instructions":"q",` +
					`"criteria":{"x":"X","y":null}}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"choice",` +
				`"choice":"x","confidence":0.91,"probabilities":{"x":0.91,"y":0.09}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			absent:   []string{"warning:"},
		},
		{
			name: "should replay a body's mixed score criteria without a rubric complaint",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q","criteria":["x",null]}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"x","1":"y"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":["x",null]`},
		},
		{
			name: "should show the legend beside the index in the table",
			args: []string{"-f", "body.json", "-o", "table"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q","criteria":[null,null]}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"Calm","1":"Frustrated"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			contains: []string{"0 Calm", "1 Frustrated"},
		},
		{
			name: "should keep flag choice criteria in the order they were given",
			args: []string{
				"--ask", "team=who owns this", "--pick", "zebra,mike,alpha", "-o", "json",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"team":{"type":"choice",` +
				`"choice":"zebra","confidence":0.9,` +
				`"probabilities":{"zebra":0.9,"mike":0.05,"alpha":0.05}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":{"zebra":null,"mike":null,"alpha":null}`},
		},
		{
			name: "should keep a body's choice criteria in the order they were written",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"choice","instructions":"q",` +
					`"criteria":{"zebra":"z","mike":"m","alpha":"a"}}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"choice",` +
				`"choice":"zebra","confidence":0.9,` +
				`"probabilities":{"zebra":0.9,"mike":0.05,"alpha":0.05}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":{"zebra":"z","mike":"m","alpha":"a"}`},
		},
		{
			name: "should keep a structured body choice criterion in written order",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"choice","instructions":"q",` +
					`"criteria":{"zebra":{"what":"z","examples":["zz"]},"alpha":"a"}}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"choice",` +
				`"choice":"zebra","confidence":0.9,` +
				`"probabilities":{"zebra":0.9,"alpha":0.1}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":{"zebra":{"what":"z","examples":["zz"]},"alpha":"a"}`,
			},
		},
		{
			name: "should keep a structured body noul rubric in written order",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"noul","instructions":"q",` +
					`"criteria":{"true":{"what":"y","examples":["yy"]},"false":"n"}}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":{"true":{"what":"y","examples":["yy"]},"false":"n"}`,
			},
		},
		{
			name: "should keep a structured body score criterion in written order",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q",` +
					`"criteria":[{"what":"z","examples":["zz"]},"a"]}}}`,
			},
			response: `{"model":"jev-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":[{"what":"z","examples":["zz"]},"a"]`,
			},
		},
		{
			name: "should keep a question file's structured rubric in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "urgent:\n  ask: q\n  yes_means:\n    what: y\n" +
					"    examples: [yy]\n  no_means: n\n",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":{"true":{"what":"y","examples":["yy"]},"false":"n"}`,
			},
		},
		{
			name: "should keep a question file's structured level description in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "mood:\n  ask: how is it\n  rate:\n    low:\n      what: z\n" +
					"      examples: [zz]\n    high: h\n",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"mood":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":[{"what":"z","examples":["zz"]},"h"]`,
			},
		},
		{
			name: "should keep question ids in the order they were given",
			args: []string{
				"--ask", "zebra=one", "--ask", "mike=two", "--ask", "alpha=three", "-o", "json",
			},
			stdin: "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"zebra":{"type":"noul","noul":0.1},` +
				`"mike":{"type":"noul","noul":0.2},"alpha":{"type":"noul","noul":0.3}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"questions":{"zebra":`, `"mike":`, `"alpha":`},
		},
		{
			name: "should keep structured instructions in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "urgent:\n  ask:\n    zebra: z\n    mike: m\n    alpha: a\n",
			},
			stdin:    "the server is down",
			response: `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"instructions":{"zebra":"z","mike":"m","alpha":"a"}`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var sent []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Errorf("reading the request body: %v", readErr)
				}

				sent = body

				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}

				w.WriteHeader(status)

				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-01-01"},
				cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
					return jev.New(jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL))
				}),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				cli.WithReadFile(func(name string) ([]byte, error) {
					body, ok := tc.files[name]
					if !ok {
						return nil, fmt.Errorf("open %s: no such file or directory", name)
					}

					return []byte(body), nil
				}),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			code := cli.Execute(t.Context(), root)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\noutput:\n%s", code, tc.wantCode, out.String())
			}

			for _, want := range tc.contains {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out.String())
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("output should not contain %q\ngot:\n%s", unwanted, out.String())
				}
			}

			for _, want := range tc.sends {
				if !strings.Contains(string(sent), want) {
					t.Errorf("request missing %q\nsent:\n%s", want, sent)
				}
			}
		})
	}
}

func TestNewRootCmdFlagDrivenClient(t *testing.T) {
	t.Parallel()

	t.Run("should build a client from --api-key and --base-url", func(t *testing.T) {
		t.Parallel()

		var gotAuth string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")

			if _, err := w.Write([]byte(
				`{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`,
			)); err != nil {
				t.Errorf("writing stub response: %v", err)
			}
		}))
		defer srv.Close()

		var out bytes.Buffer

		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithStdin(strings.NewReader("body")),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
		)

		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{
			"is this urgent", "-r", "--api-key", "secret", "--base-url", srv.URL,
		})

		if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out.String())
		}

		if !strings.Contains(gotAuth, "secret") {
			t.Errorf("Authorization header = %q, want it to carry the flag's key", gotAuth)
		}
	})
}

func TestNewRootCmdEnvDrivenClient(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`

	t.Run("should build a client from the injected environment", func(t *testing.T) {
		t.Parallel()

		var gotAuth string

		srv := stubAnswering(t, answered, func(r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
		})
		defer srv.Close()

		env := map[string]string{
			jev.EnvAPIKey:  "from-env",
			jev.EnvBaseURL: srv.URL,
		}

		out, code := runWithEnv(t, env, []string{"is this urgent", "-r"})

		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out)
		}

		if !strings.Contains(gotAuth, "from-env") {
			t.Errorf("Authorization header = %q, want it to carry the environment key", gotAuth)
		}
	})

	t.Run("should let --base-url override the environment", func(t *testing.T) {
		t.Parallel()

		var wanted, unwanted int

		flagged := stubAnswering(t, answered, func(*http.Request) { wanted++ })
		defer flagged.Close()

		fromEnv := stubAnswering(t, answered, func(*http.Request) { unwanted++ })
		defer fromEnv.Close()

		env := map[string]string{
			jev.EnvAPIKey:  "from-env",
			jev.EnvBaseURL: fromEnv.URL,
		}

		out, code := runWithEnv(t, env,
			[]string{"is this urgent", "-r", "--base-url", flagged.URL})

		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out)
		}

		if wanted != 1 {
			t.Errorf("the flag's base url took %d requests, want 1", wanted)
		}

		if unwanted != 0 {
			t.Errorf("the environment's base url took %d requests, want 0", unwanted)
		}
	})
}

func runWithEnv(t *testing.T, env map[string]string, args []string) (string, int) {
	t.Helper()

	var out bytes.Buffer

	// No WithClientFactory, so the real factory runs and the injected lookup is the only thing
	// standing between it and the process environment.
	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithStdin(strings.NewReader("body")),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(name string) (string, bool) {
			value, ok := env[name]

			return value, ok
		}),
	)

	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	code := cli.Execute(t.Context(), root)

	return out.String(), code
}

func TestNewRootCmdQuestionOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		order []string
	}{
		{
			name: "should send question ids in argv order",
			args: []string{
				"--ask", "zebra=one", "--ask", "mike=two", "--ask", "alpha=three",
			},
			order: []string{`"zebra"`, `"mike"`, `"alpha"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var sent string

			srv := stubAnswering(t,
				`{"model":"jev-1.13.0","answers":{"zebra":{"type":"noul","noul":0.1},`+
					`"mike":{"type":"noul","noul":0.2},`+
					`"alpha":{"type":"noul","noul":0.3}}}`,
				func(r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("reading the request body: %v", err)

						return
					}

					sent = string(body)
				})
			defer srv.Close()

			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-01-01"},
				cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
					return jev.New(jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL))
				}),
				cli.WithStdin(strings.NewReader("the server is down")),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(append(tc.args, "-o", "json"))

			if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
				t.Fatalf("exit code = %d, want %d\noutput:\n%s", code, cli.ExitOK, out.String())
			}

			at := make([]int, 0, len(tc.order))

			for _, id := range tc.order {
				found := strings.Index(sent, id)
				if found < 0 {
					t.Fatalf("id %s absent from %s", id, sent)
				}

				at = append(at, found)
			}

			if !slices.IsSorted(at) {
				t.Errorf("ids out of order in %s, positions %v", sent, at)
			}
		})
	}
}

func stubAnswering(t *testing.T, body string, observe func(*http.Request)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observe(r)

		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("writing stub response: %v", err)
		}
	}))
}

func TestNewRootCmdConnectionPool(t *testing.T) {
	t.Parallel()

	t.Run("should reuse connections across a job count above two", func(t *testing.T) {
		t.Parallel()

		const (
			jobs    = 8
			records = 120
		)

		var opened atomic.Int64

		srv := httptest.NewUnstartedServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				// Slow enough that every worker really is in flight at once, which is what makes
				// the pool size rather than the request count decide the connection count.
				time.Sleep(2 * time.Millisecond)

				if _, err := w.Write([]byte(
					`{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`,
				)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))

		srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				opened.Add(1)
			}
		}

		srv.Start()

		defer srv.Close()

		var stdin strings.Builder
		for i := range records {
			fmt.Fprintf(&stdin, "line %d\n", i)
		}

		var out bytes.Buffer

		// No WithClientFactory, so the real composition root builds the transport from -j.
		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithStdin(strings.NewReader(stdin.String())),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(name string) (string, bool) {
				switch name {
				case jev.EnvAPIKey:
					return "k", true
				case jev.EnvBaseURL:
					return srv.URL, true
				default:
					return "", false
				}
			}),
		)

		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"x", "-i", "lines", "-j", strconv.Itoa(jobs), "-o", "values"})

		if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out.String())
		}

		// The default transport pools two idle connections per host, which had three quarters of
		// the records paying for a fresh handshake. A run that pools per job opens one connection
		// per worker and reuses it.
		if got := opened.Load(); got > jobs {
			t.Errorf("new connections = %d for %d records at -j %d, want at most %d",
				got, records, jobs, jobs)
		}
	})
}

func TestNewRootCmdInterrupt(t *testing.T) {
	t.Parallel()

	t.Run("should write nothing when a single record run is cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())

		var once sync.Once

		started := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			once.Do(func() { close(started) })

			// Bounded rather than waiting on the request context. A net/http server only notices
			// a client that hung up when it next reads the connection, so a handler blocked on
			// Done would hold Close open past the test's deadline.
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		go func() {
			<-started
			cancel()
		}()

		var out, errOut bytes.Buffer

		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
				return jev.New(jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL))
			}),
			cli.WithStdin(strings.NewReader("a ticket")),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
		)

		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"is this urgent", "-o", "values"})

		if code := cli.Execute(ctx, root); code != cli.ExitInterrupt {
			t.Errorf("exit code = %d, want %d", code, cli.ExitInterrupt)
		}

		// The caller ended the run themselves. A transport record written into the pipe they were
		// closing reports a network fault that never happened.
		if out.String() != "" {
			t.Errorf("stdout = %q, want nothing", out.String())
		}

		if errOut.String() != "" {
			t.Errorf("stderr = %q, want nothing", errOut.String())
		}
	})
}

func TestNewRootCmdRetryFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		attempts int
	}{
		{
			name:     "should make one attempt with retries disabled",
			args:     []string{"--retries", "0"},
			attempts: 1,
		},
		{
			name:     "should make three attempts by default",
			args:     []string{},
			attempts: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int64

			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					attempts.Add(1)
					// 503 is retryable per DefaultRetryStatus, and Retry-After is omitted so the
					// run uses jev's own backoff rather than a server requested wait.
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
			defer srv.Close()

			args := append([]string{
				"is this urgent", "--base-url", srv.URL, "--api-key", "test",
			}, tc.args...)

			var out bytes.Buffer

			// No WithClientFactory, since the flags are wired onto the client by the real factory
			// and a stub one would never see them.
			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithStdin(strings.NewReader("the server is down")),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)

			if code := cli.Execute(t.Context(), root); code == cli.ExitOK {
				t.Fatalf("exit code = %d, want a failure. output:\n%s", code, out.String())
			}

			if got := attempts.Load(); got != int64(tc.attempts) {
				t.Errorf("attempts = %d, want %d", got, tc.attempts)
			}
		})
	}
}

func TestNewRootCmdMaxRetryAfterFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		attempts int
	}{
		{
			name:     "should stop at the first attempt when Retry-After is above the cap",
			args:     []string{"--retries", "1", "--max-retry-after", "1"},
			attempts: 1,
		},
		{
			name:     "should retry when Retry-After is below the default cap",
			args:     []string{"--retries", "1"},
			attempts: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int64

			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					attempts.Add(1)
					// Two seconds, so the run is terminal under a cap of one and retryable under
					// the default of sixty.
					w.Header().Set("Retry-After", "2")
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
			defer srv.Close()

			args := append([]string{
				"is this urgent", "--base-url", srv.URL, "--api-key", "test",
			}, tc.args...)

			var out bytes.Buffer

			// No WithClientFactory, since the flag is wired onto the client by the real factory
			// and a stub one would never see it.
			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithStdin(strings.NewReader("the server is down")),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)

			if code := cli.Execute(t.Context(), root); code == cli.ExitOK {
				t.Fatalf("exit code = %d, want a failure. output:\n%s", code, out.String())
			}

			if got := attempts.Load(); got != int64(tc.attempts) {
				t.Errorf("attempts = %d, want %d", got, tc.attempts)
			}
		})
	}
}

func TestNewRootCmdTimeoutFlag(t *testing.T) {
	t.Parallel()

	t.Run("should abandon an attempt the server never answers", func(t *testing.T) {
		t.Parallel()

		blocked := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-blocked
		}))

		// Both through Cleanup rather than defer, and in this order, so the handler is released
		// before Close waits on it. A deferred Close would block on the handler it is waiting for.
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(blocked) })

		var out, errOut bytes.Buffer

		// No WithClientFactory, since the flag is wired onto the client by the real factory and a
		// stub one would never see it.
		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithStdin(strings.NewReader("the server is quiet")),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
		)

		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{
			"is this urgent", "--base-url", srv.URL, "--api-key", "test",
			"--timeout", "1", "--retries", "0",
		})

		if code := cli.Execute(t.Context(), root); code != cli.ExitTransport {
			t.Fatalf("exit code = %d, want %d. output:\n%s", code, cli.ExitTransport, out.String())
		}

		// The duration is part of the claim. Without the flag on the client the attempt runs to
		// the ten second default, which this handler outlasts.
		if !strings.Contains(errOut.String(), "timed out after 1s") {
			t.Errorf("stderr = %q, want it to name the one second timeout", errOut.String())
		}
	})
}

func TestNewRootCmdFlagValidation(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	tests := []struct {
		name     string
		args     []string
		code     int
		contains string
		requests int64
	}{
		{
			name:     "should reject a timeout of zero",
			args:     []string{"--timeout", "0"},
			code:     cli.ExitUsage,
			contains: "jev: --timeout takes a positive number of seconds, got 0",
		},
		{
			name:     "should reject a timeout above the ceiling",
			args:     []string{"--timeout", "86401"},
			code:     cli.ExitUsage,
			contains: "jev: --timeout takes at most 86400 seconds, got 86401",
		},
		{
			name:     "should reject a timeout that would overflow a duration",
			args:     []string{"--timeout", "18446744074"},
			code:     cli.ExitUsage,
			contains: "jev: --timeout takes at most 86400 seconds, got 18446744074",
		},
		{
			name:     "should reject a negative retry count",
			args:     []string{"--retries=-1"},
			code:     cli.ExitUsage,
			contains: "jev: --retries takes a retry count of zero or more, got -1",
		},
		{
			name:     "should reject a negative retry after cap",
			args:     []string{"--max-retry-after=-5"},
			code:     cli.ExitUsage,
			contains: "jev: --max-retry-after must be a positive number of seconds, got -5",
		},
		{
			name:     "should reject a retry after cap that would overflow a duration",
			args:     []string{"--max-retry-after", "18446744074"},
			code:     cli.ExitUsage,
			contains: "jev: --max-retry-after takes at most 86400 seconds, got 18446744074",
		},
		{
			name:     "should reject a job count of zero",
			args:     []string{"-j", "0"},
			code:     cli.ExitUsage,
			contains: "jev: -j takes a positive number of records in flight, got 0",
		},
		{
			name: "should reach the request with every flag in range",
			args: []string{
				"--timeout", "86400", "--retries", "0", "--max-retry-after", "86400", "-j", "1",
			},
			code:     cli.ExitOK,
			requests: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int64

			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")

					if _, err := io.WriteString(w, answered); err != nil {
						t.Errorf("writing the response: %v", err)
					}
				}))
			defer srv.Close()

			args := append([]string{
				"is this urgent", "--base-url", srv.URL, "--api-key", "test",
			}, tc.args...)

			var out bytes.Buffer

			// No WithClientFactory, since these flags reach the client through the real factory
			// and the point of the test is the plumbing a stub would replace.
			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithStdin(strings.NewReader("a ticket")),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)

			if code := cli.Execute(t.Context(), root); code != tc.code {
				t.Errorf("exit code = %d, want %d. output:\n%s", code, tc.code, out.String())
			}

			if tc.contains != "" && !strings.Contains(out.String(), tc.contains) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tc.contains)
			}

			// A rejected flag must be caught before anything is sent, which is what lets the
			// error cases run without a reachable server at all.
			if got := requests.Load(); got != tc.requests {
				t.Errorf("requests = %d, want %d", got, tc.requests)
			}
		})
	}
}
