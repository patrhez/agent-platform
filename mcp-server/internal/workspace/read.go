package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	maxReadBytes = 256 * 1024
	maxReadLines = 1000
)

// ReadInput identifies a bounded contiguous range of a repository file.
type ReadInput struct {
	Repo      string `json:"repo" jsonschema:"repository alias"`
	Path      string `json:"path" jsonschema:"repository-relative file path"`
	StartLine int    `json:"startLine" jsonschema:"first line to return, starting at one"`
	EndLine   int    `json:"endLine" jsonschema:"last line to return, inclusive"`
}

// ReadLine is one numbered line returned by file.read.
type ReadLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// ReadOutput is the safe, bounded result of a file.read call.
type ReadOutput struct {
	Repo      string     `json:"repo"`
	Path      string     `json:"path"`
	StartLine int        `json:"startLine"`
	EndLine   int        `json:"endLine"`
	Lines     []ReadLine `json:"lines"`
}

// Read returns requested lines from one regular file under an allowed repository root.
func (service *Service) Read(context context.Context, input ReadInput) (ReadOutput, error) {
	if err := validateReadInput(input); err != nil {
		return ReadOutput{}, err
	}
	if err := context.Err(); err != nil {
		return ReadOutput{}, fmt.Errorf("read context: %w", err)
	}
	repositoryRoot, err := service.resolveRepository(input.Repo)
	if err != nil {
		return ReadOutput{}, err
	}
	path, err := resolveFile(repositoryRoot, input.Path)
	if err != nil {
		return ReadOutput{}, err
	}
	contents, err := readBoundedFile(path)
	if err != nil {
		return ReadOutput{}, err
	}
	return selectLines(input, contents), nil
}

func validateReadInput(input ReadInput) error {
	if input.StartLine < 1 || input.EndLine < input.StartLine {
		return ErrInvalidPath
	}
	if input.EndLine-input.StartLine+1 > maxReadLines {
		return ErrResultLimitExceeded
	}
	return nil
}

func readBoundedFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrInvalidPath
	}
	if info.Size() > maxReadBytes {
		return nil, ErrResultLimitExceeded
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if len(contents) > maxReadBytes {
		return nil, ErrResultLimitExceeded
	}
	return contents, nil
}

func selectLines(input ReadInput, contents []byte) ReadOutput {
	allLines := strings.Split(string(contents), "\n")
	startIndex := input.StartLine - 1
	endIndex := min(input.EndLine, len(allLines))
	if startIndex >= endIndex {
		return ReadOutput{
			Repo:      input.Repo,
			Path:      input.Path,
			StartLine: input.StartLine,
			EndLine:   input.EndLine,
			Lines:     []ReadLine{},
		}
	}
	lines := make([]ReadLine, 0, endIndex-startIndex)
	for index := startIndex; index < endIndex; index++ {
		lines = append(lines, ReadLine{Number: index + 1, Text: allLines[index]})
	}
	return ReadOutput{
		Repo:      input.Repo,
		Path:      input.Path,
		StartLine: input.StartLine,
		EndLine:   input.EndLine,
		Lines:     lines,
	}
}
