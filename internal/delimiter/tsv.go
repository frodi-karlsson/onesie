package delimiter

import "strings"

// TSVFields splits a physical TSV record, removing CRLF or LF framing when present.
// Without a final LF, every byte belongs to the payload.
func TSVFields(line string) []string {
	if payload, framed := strings.CutSuffix(line, "\n"); framed {
		line = strings.TrimSuffix(payload, "\r")
	}
	return strings.Split(line, "\t")
}
