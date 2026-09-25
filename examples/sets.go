// Package examples describes the starter question sets, so the tests and the tool that regenerates
// their answers read each set the same way.
package examples

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Model is the pinned model every committed answers file was made with, and that the offline check
// reads them as.
const Model = "jev-1.13.0"

// Sets reads every starter set under dir, from questions/NAME.yaml and data/NAME.jsonl, in name
// order.
func Sets(dir string) ([]Set, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "questions", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("listing question files: %w", err)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no question files in %s", filepath.Join(dir, "questions"))
	}

	sets := make([]Set, 0, len(paths))

	for _, path := range paths {
		set, setErr := readSet(dir, strings.TrimSuffix(filepath.Base(path), ".yaml"))
		if setErr != nil {
			return nil, setErr
		}

		sets = append(sets, set)
	}

	return sets, nil
}

// Set is one starter set. IDs are its question ids in order, and TextField is the record field
// that holds the text a person would read.
type Set struct {
	Name      string
	IDs       []string
	TextField string
}

// Questions is the path of the set's question file under dir.
func (s Set) Questions(dir string) string {
	return filepath.Join(dir, "questions", s.Name+".yaml")
}

// Data is the path of the set's labelled records under dir.
func (s Set) Data(dir string) string {
	return filepath.Join(dir, "data", s.Name+".jsonl")
}

// Answers is the path of the set's committed answers file under dir.
func (s Set) Answers(dir string) string {
	return filepath.Join(dir, "data", s.Name+".answers.jsonl")
}

// CalibrateArgs are the calibrate arguments that read the set's records, without --out.
func (s Set) CalibrateArgs(dir string) []string {
	args := []string{
		"calibrate", "-f", s.Questions(dir), "-i", "jsonl", "--map", "." + s.TextField, "--id", ".id",
		"-m", Model, "--provider", "typesafe",
	}
	for _, id := range s.IDs {
		args = append(args, "--label", id+"=."+id)
	}

	return args
}

func readSet(dir, name string) (Set, error) {
	set := Set{Name: name}

	ids, err := questionIDs(set.Questions(dir))
	if err != nil {
		return Set{}, err
	}

	set.IDs = ids

	field, err := textField(set.Data(dir), ids)
	if err != nil {
		return Set{}, err
	}

	set.TextField = field

	return set, nil
}

func questionIDs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file map[string]any
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var ids []string

	for key, value := range file {
		if question, ok := value.(map[string]any); ok && question["ask"] != nil {
			ids = append(ids, key)
		}
	}

	slices.Sort(ids)

	return ids, nil
}

func textField(path string, ids []string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	first, _, _ := bytes.Cut(data, []byte("\n"))

	var record map[string]any
	if err := json.Unmarshal(first, &record); err != nil {
		return "", fmt.Errorf("%s line 1: %w", path, err)
	}

	var fields []string

	for key := range record {
		if key != "id" && !slices.Contains(ids, key) {
			fields = append(fields, key)
		}
	}

	if len(fields) != 1 {
		return "", errors.New(path + " should hold one text field beside id and the labels, got " +
			strings.Join(fields, ", "))
	}

	return fields[0], nil
}
