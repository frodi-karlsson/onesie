package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func checkModes(t *testing.T) {
	t.Parallel()

	image, err := filepath.Abs(filepath.Join("..", "..", "examples", "images", "square.png"))
	if err != nil {
		t.Fatal(err)
	}

	// The flag beside a question it applies to, and whatever else it needs to run.
	cases := map[settingrow.Name][]string{
		"ask":             {"--ask", "u=is it urgent"},
		"for":             {"-f", "QUESTIONS", "--for", "u", "--threshold", "0.7"},
		"pick":            {"--ask", "t=which team", "--pick", "billing,platform"},
		"rate":            {"--ask", "r=how rude", "--rate", "calm,curt,rude"},
		"desc":            {"--ask", "t=which team", "--pick", "billing,platform", "--desc", "billing=money"},
		"sep":             {"--ask", "t=which team", "--sep", ";", "--pick", "billing;platform"},
		"box":             {"--ask", "b=where is the mug", "--box", "--image", "photo=" + image, "-m", "clef-flash"},
		"threshold":       {"is it urgent", "--threshold", "0.7"},
		"min_confidence":  {"--ask", "t=which team", "--pick", "billing,platform", "--min-confidence", "0.5", "--fallback", "billing"},
		"fallback":        {"--ask", "t=which team", "--pick", "billing,platform", "--min-confidence", "0.5", "--fallback", "billing"},
		"assert":          {"is it urgent", "--assert", "answer.value > 0.5"},
		"abstain_if":      {"is it urgent", "--assert", "answer.value > 0.5", "--abstain-if", "answer.value < 0.6"},
		"output":          {"is it urgent", "-o", "values"},
		"quiet":           {"is it urgent", "-q"},
		"out":             {"is it urgent", "--out", "OUT"},
		"resume":          {"is it urgent", "--out", "OUT", "--resume"},
		"prune":           {"is it urgent", "--out", "OUT", "--resume", "--id", ".", "--prune"},
		"merge":           {"is it urgent", "--merge"},
		"merge_key":       {"is it urgent", "--merge-key", "answers"},
		"usage":           {"is it urgent", "--usage"},
		"stats":           {"is it urgent", "--stats"},
		"delimiter":       {"is it urgent", "-o", "csv", "--delimiter", ";"},
		"state":           {"is it urgent", "--state", "the site is down"},
		"image":           {"is there a mug", "--image", "photo=" + image, "-m", "clef-flash"},
		"image_field":     {"is there a mug", "--image-field", "photo", "-m", "clef-flash"},
		"map":             {"is it urgent", "--map", "."},
		"id":              {"is it urgent", "--id", "."},
		"skip_blank":      {"is it urgent", "--skip-blank"},
		"unordered":       {"is it urgent", "--unordered"},
		"stop_on":         {"is it urgent", "--stop-on", "error"},
		"keep":            {"is it urgent", "--keep", "pass"},
		"no_dedup":        {"is it urgent", "--no-dedup"},
		"file":            {"-f", "QUESTIONS"},
		"replace":         {"-f", "QUESTIONS", "--ask", "u=is it on fire", "--replace"},
		"provider":        {"is it urgent", "--provider", "typesafe"},
		"profile":         {"is it urgent", "--profile", "p"},
		"model":           {"is it urgent", "-m", "jev-latest"},
		"endpoint":        {"is it urgent"},
		"allow_http":      {"is it urgent", "--allow-http"},
		"long_context":    {"is it urgent", "--long-context"},
		"mock":            {"is it urgent", "--mock", "MOCK"},
		"cache":           {"is it urgent", "--cache"},
		"jobs":            {"is it urgent", "-j", "2"},
		"timeout":         {"is it urgent", "--timeout", "5"},
		"retries":         {"is it urgent", "--retries", "1"},
		"max_retry_after": {"is it urgent", "--max-retry-after", "5"},
	}

	for _, row := range settingrow.Table() {
		_, registered := row.FlagOn(settingrow.CommandRoot)
		if !registered || !row.Modes.Has(settingrow.ModeAsk) && !row.Modes.Has(settingrow.ModeStream) {
			continue
		}

		args, listed := cases[row.Name]
		if !listed && row.Name != "input" {
			t.Errorf("no case runs %s, so nothing pins its Modes on one record and a stream", row.Name)

			continue
		}

		if !listed {
			// -i chooses between the two, so it is the one root flag both runs give.
			continue
		}

		for _, stream := range []bool{false, true} {
			mode, where := settingrow.ModeAsk, "one record"
			if stream {
				mode, where = settingrow.ModeStream, "a stream"
			}

			t.Run("should run "+string(row.Name)+" on "+where+" only if its row lists the mode", func(t *testing.T) {
				t.Parallel()

				code, errOut := runInMode(t, args, stream, image)
				if refused := code == ExitUsage; refused == row.Modes.Has(mode) {
					t.Errorf("exit %d on %s, stderr %q, while Modes has it %t", code, where, errOut, row.Modes.Has(mode))
				}
			})
		}
	}
}

func runInMode(t *testing.T, args []string, stream bool, image string) (int, string) {
	t.Helper()

	stub, dir := newAnswerStub(t), t.TempDir()
	env := cacheEnv(t)

	questions := filepath.Join(dir, "questions.yaml")
	mock := filepath.Join(dir, "answers.mock")
	writeTo(t, questions, "u:\n  ask: is it urgent\n")
	writeTo(t, mock, `{"answer":0.9}`+"\n")

	run := make([]string, 0, len(args)+4)
	for _, arg := range args {
		switch arg {
		case "QUESTIONS":
			arg = questions
		case "MOCK":
			arg = mock
		case "OUT":
			arg = filepath.Join(dir, "answers.jsonl")
		}

		run = append(run, arg)
	}

	if slices.Contains(args, "--profile") {
		writeConfig(t, env["ONESIE_CONFIG_DIR"], "profiles.json",
			`{"profiles":{"p":{"provider":"typesafe","api_key":"k","base_url":"`+stub.url+`"}}}`, 0)
	}

	// A profile, a mock and an explicit base URL each say where the run goes, so the stub is the base
	// URL only beside neither of the other two.
	if !slices.Contains(run, "--profile") && !slices.Contains(run, "--mock") {
		run = append(run, "--base-url", stub.url)
	}

	stdin := ""
	switch {
	case stream:
		run = append(run, "-i", "lines")
		stdin = "the site is down\n"
		if slices.Contains(run, "--image-field") {
			stdin = image + "\n"
		}
	case !slices.Contains(run, "--state"):
		run = append(run, "--state", "the site is down")
	}

	_, errOut, code := runCached(t, env, run, stdin)

	return code, strings.TrimSpace(errOut)
}

func writeTo(t *testing.T, path, text string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
