package calibrate

import (
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Report is a whole calibration run, the counts across every record and a score per question in
// plan order. Usage is nil unless the run was asked for token counts.
type Report struct {
	Models                                               []string
	Records, Labelled, Unlabelled, Asked, Stored, Failed int
	Usage                                                *Usage
	Questions                                            []QuestionReport
}

// Usage is the token count summed over the records that carry one, failed ones included. Records
// says how many that is, since a stored line written without --usage carries none.
type Usage struct {
	jev.Usage

	Records int `json:"records"`
}

// QuestionReport is one question's score. Only the score for its Shape is set.
type QuestionReport struct {
	ID    string
	Shape plan.Shape
	YesNo *YesNoScore
	Pick  *PickScore
	Rate  *RateScore
}

func shapeName(shape plan.Shape) string {
	if shape == plan.Noul {
		return "yes/no"
	}

	return shape.String()
}
