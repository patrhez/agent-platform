package workspace

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	maxReadFileBytes = 16 * 1024 * 1024
	maxReadLines     = 1000
	maxLineBytes     = 4096
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
	Repo       string     `json:"repo"`
	Path       string     `json:"path"`
	StartLine  int        `json:"startLine"`
	EndLine    int        `json:"endLine"`
	TotalLines int        `json:"totalLines"`
	Lines      []ReadLine `json:"lines"`
}

// Read streams the requested lines from one regular file under an allowed repository root.
func (service *Service) Read(ctx context.Context, input ReadInput) (ReadOutput, error) {
	if err := validateReadInput(input); err != nil {
		return ReadOutput{}, err
	}
	if err := ctx.Err(); err != nil {
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
	lines, totalLines, err := streamLines(path, input.StartLine, input.EndLine)
	if err != nil {
		return ReadOutput{}, err
	}
	return ReadOutput{
		Repo:       input.Repo,
		Path:       input.Path,
		StartLine:  input.StartLine,
		EndLine:    input.EndLine,
		TotalLines: totalLines,
		Lines:      lines,
	}, nil
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

// streamLines scans the file once, collecting the requested range and counting all lines,
// so large files never need to be held in memory.
func streamLines(path string, startLine int, endLine int) ([]ReadLine, int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, ErrInvalidPath
	}
	if info.Size() > maxReadFileBytes {
		return nil, 0, ErrResultLimitExceeded
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	lines := make([]ReadLine, 0, endLine-startLine+1)
	reader := bufio.NewReader(file)
	lineNumber := 0
	for {
		text, readErr := reader.ReadString('\n')
		if text == "" && readErr == io.EOF {
			break
		}
		lineNumber++
		if lineNumber >= startLine && lineNumber <= endLine {
			lines = append(lines, ReadLine{Number: lineNumber, Text: truncateLine(strings.TrimSuffix(text, "\n"))})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("read file: %w", readErr)
		}
	}
	return lines, lineNumber, nil
}

func truncateLine(text string) string {
	if len(text) <= maxLineBytes {
		return text
	}
	cut := maxLineBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}
