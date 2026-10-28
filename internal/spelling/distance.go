// Package spelling measures how far one word is from another, so a message can name the word a typo
// meant.
package spelling

// Distance counts the edits that turn a into b, byte by byte. An edit inserts, deletes or replaces
// one byte, or swaps two neighbouring ones, so questoins is one edit from questions.
func Distance(a, b string) int {
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}

	for j := range rows[0] {
		rows[0][j] = j
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)

			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				rows[i][j] = min(rows[i][j], rows[i-2][j-2]+1)
			}
		}
	}

	return rows[len(a)][len(b)]
}
