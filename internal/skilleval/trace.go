package skilleval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// A trace line can carry a whole skill body, so the scanner's default 64 KiB line cap is too small.
const maxTraceLine = 16 << 20

func traceScripts(trace []byte) ([]string, error) {
	var scripts []string

	scanner := bufio.NewScanner(bytes.NewReader(trace))
	scanner.Buffer(make([]byte, 0, 64<<10), maxTraceLine)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			return nil, fmt.Errorf("parsing a trace line: %w", err)
		}

		// Only an assistant entry has a fixed shape. Others carry a message as a plain string.
		if kind.Type != "assistant" {
			continue
		}

		var entry assistantEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("parsing an assistant trace line: %w", err)
		}

		for _, block := range entry.Message.Content {
			switch {
			case block.Type == "text":
				scripts = append(scripts, fencedBlocks(block.Text)...)
			case block.Type == "tool_use" && block.Name == "Bash" && block.Input.Command != "":
				scripts = append(scripts, block.Input.Command)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the trace: %w", err)
	}

	return scripts, nil
}

type assistantEntry struct {
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input struct {
		Command string `json:"command"`
	} `json:"input"`
}

func fencedBlocks(text string) []string {
	// Prose is left out on purpose. A command named in a sentence is usually a fragment, and a
	// dry run of a fragment says nothing about what the agent would have run.
	var blocks []string

	var current []string

	inside := false

	for line := range strings.Lines(text) {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inside {
				blocks = append(blocks, strings.Join(current, ""))
				current = nil
			}

			inside = !inside

			continue
		}

		if inside {
			current = append(current, line)
		}
	}

	return blocks
}
