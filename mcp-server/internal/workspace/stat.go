package workspace

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
)

const maxStatFileBytes = 16 * 1024 * 1024

// StatInput identifies one repository path to inspect.
type StatInput struct {
	Repo string `json:"repo" jsonschema:"repository alias"`
	Path string `json:"path" jsonschema:"repository-relative file or directory path"`
}

// StatOutput is the safe metadata returned by file.stat.
type StatOutput struct {
	Repo       string `json:"repo"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	TotalLines int    `json:"totalLines,omitempty"`
}

// Stat returns bounded metadata for one file or directory under an allowed repository root.
func (service *Service) Stat(ctx context.Context, input StatInput) (StatOutput, error) {
	if input.Path == "" {
		return StatOutput{}, ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return StatOutput{}, fmt.Errorf("stat context: %w", err)
	}
	repositoryRoot, err := service.resolveRepository(input.Repo)
	if err != nil {
		return StatOutput{}, err
	}
	path, err := resolvePath(repositoryRoot, input.Path)
	if err != nil {
		return StatOutput{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StatOutput{}, ErrFileNotFound
		}
		return StatOutput{}, fmt.Errorf("stat path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return StatOutput{}, ErrInvalidPath
	}
	output := StatOutput{Repo: input.Repo, Path: input.Path}
	if info.IsDir() {
		output.Type = "dir"
		return output, nil
	}
	if !info.Mode().IsRegular() {
		return StatOutput{}, ErrInvalidPath
	}
	output.Type = "file"
	output.SizeBytes = info.Size()
	if info.Size() > maxStatFileBytes {
		return output, nil
	}
	totalLines, err := countLines(path)
	if err != nil {
		return StatOutput{}, err
	}
	output.TotalLines = totalLines
	return output, nil
}

func resolvePath(repositoryRoot string, relativePath string) (string, error) {
	return resolveFile(repositoryRoot, relativePath)
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)
	lines := 0
	for {
		text, readErr := reader.ReadString('\n')
		if text == "" && readErr == io.EOF {
			break
		}
		lines++
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, fmt.Errorf("count lines: %w", readErr)
		}
	}
	return lines, nil
}
