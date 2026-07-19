package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSearchResults = 20
	maxSearchResults     = 50
)

var excludedSearchDirectories = map[string]struct{}{
	".git":         {},
	".gocache":     {},
	".gomodcache":  {},
	".venv":        {},
	"venv":         {},
	"node_modules": {},
}

// SearchInput describes a bounded literal search within one repository.
type SearchInput struct {
	Repo       string `json:"repo" jsonschema:"repository alias"`
	Query      string `json:"query" jsonschema:"literal text to find"`
	PathPrefix string `json:"pathPrefix,omitempty" jsonschema:"optional repository-relative directory"`
	Glob       string `json:"glob,omitempty" jsonschema:"optional filename glob"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"maximum matches from one to fifty"`
}

// SearchMatch describes a source line and adjacent context that matched a literal query.
type SearchMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// SearchOutput is the bounded result of a code.search call.
type SearchOutput struct {
	Repo      string        `json:"repo"`
	Query     string        `json:"query"`
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated"`
}

// Search returns literal text matches from regular files under an allowed repository root.
func (service *Service) Search(context context.Context, input SearchInput) (SearchOutput, error) {
	limit, err := validateSearchInput(input)
	if err != nil {
		return SearchOutput{}, err
	}
	repositoryRoot, err := service.resolveRepository(input.Repo)
	if err != nil {
		return SearchOutput{}, err
	}
	searchRoot, err := resolveSearchRoot(repositoryRoot, input.PathPrefix)
	if err != nil {
		return SearchOutput{}, err
	}
	output := SearchOutput{Repo: input.Repo, Query: input.Query, Matches: []SearchMatch{}}
	err = filepath.WalkDir(searchRoot, service.searchVisitor(context, repositoryRoot, input, limit, &output))
	if err != nil {
		return SearchOutput{}, err
	}
	return output, nil
}

func validateSearchInput(input SearchInput) (int, error) {
	if input.Query == "" {
		return 0, ErrInvalidPath
	}
	if input.MaxResults == 0 {
		return defaultSearchResults, nil
	}
	if input.MaxResults < 1 || input.MaxResults > maxSearchResults {
		return 0, ErrResultLimitExceeded
	}
	return input.MaxResults, nil
}

func resolveSearchRoot(repositoryRoot string, pathPrefix string) (string, error) {
	if pathPrefix == "" {
		return repositoryRoot, nil
	}
	if filepath.IsAbs(pathPrefix) || hasParentReference(filepath.Clean(pathPrefix)) {
		return "", ErrPathOutsideWorkspace
	}
	searchRoot := filepath.Join(repositoryRoot, filepath.Clean(pathPrefix))
	if err := ensureWithinRoot(repositoryRoot, searchRoot); err != nil {
		return "", err
	}
	info, err := os.Stat(searchRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrFileNotFound
		}
		return "", fmt.Errorf("stat pathPrefix: %w", err)
	}
	if !info.IsDir() {
		return "", ErrInvalidPath
	}
	return searchRoot, nil
}

func (service *Service) searchVisitor(
	context context.Context,
	repositoryRoot string,
	input SearchInput,
	limit int,
	output *SearchOutput,
) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk repository: %w", walkErr)
		}
		if err := context.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if isExcludedSearchDirectory(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if len(output.Matches) >= limit {
			output.Truncated = true
			return fs.SkipAll
		}
		return service.searchFile(repositoryRoot, path, input, limit, output)
	}
}

func isExcludedSearchDirectory(name string) bool {
	_, excluded := excludedSearchDirectories[name]
	return excluded
}

func (service *Service) searchFile(
	repositoryRoot string,
	path string,
	input SearchInput,
	limit int,
	output *SearchOutput,
) error {
	relativePath, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return fmt.Errorf("make result path relative: %w", err)
	}
	if input.Glob != "" {
		matched, err := filepath.Match(input.Glob, filepath.Base(relativePath))
		if err != nil {
			return fmt.Errorf("match search glob: %w", err)
		}
		if !matched {
			return nil
		}
	}
	contents, err := readBoundedFile(path)
	if err != nil {
		if err == ErrResultLimitExceeded {
			return nil
		}
		return err
	}
	appendMatches(relativePath, string(contents), input.Query, limit, output)
	return nil
}

func appendMatches(path string, contents string, query string, limit int, output *SearchOutput) {
	lines := strings.Split(contents, "\n")
	for index, line := range lines {
		if !strings.Contains(line, query) {
			continue
		}
		if len(output.Matches) >= limit {
			output.Truncated = true
			return
		}
		output.Matches = append(output.Matches, SearchMatch{
			Path:   filepath.ToSlash(path),
			Line:   index + 1,
			Text:   line,
			Before: adjacentLine(lines, index-1),
			After:  adjacentLine(lines, index+1),
		})
	}
}

func adjacentLine(lines []string, index int) string {
	if index < 0 || index >= len(lines) {
		return ""
	}
	return lines[index]
}
