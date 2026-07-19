package workspace

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultSearchResults  = 20
	maxSearchResults      = 50
	maxSearchPatternBytes = 256
	maxSearchFileBytes    = 1024 * 1024
	binaryProbeBytes      = 8 * 1024
)

var excludedSearchDirectories = map[string]struct{}{
	".git":         {},
	".gocache":     {},
	".gomodcache":  {},
	".venv":        {},
	"venv":         {},
	"node_modules": {},
}

// SearchInput describes a bounded literal or regular-expression search within one repository.
type SearchInput struct {
	Repo            string `json:"repo" jsonschema:"repository alias"`
	Query           string `json:"query" jsonschema:"literal text to find, or an RE2 regular expression when regex is true"`
	PathPrefix      string `json:"pathPrefix,omitempty" jsonschema:"optional repository-relative directory"`
	Glob            string `json:"glob,omitempty" jsonschema:"optional filename glob"`
	MaxResults      int    `json:"maxResults,omitempty" jsonschema:"maximum matches from one to fifty"`
	Regex           bool   `json:"regex,omitempty" jsonschema:"treat query as an RE2 regular expression"`
	CaseInsensitive bool   `json:"caseInsensitive,omitempty" jsonschema:"match without case sensitivity"`
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

// Search returns bounded text matches from regular files under an allowed repository root.
func (service *Service) Search(context context.Context, input SearchInput) (SearchOutput, error) {
	limit, err := validateSearchInput(input)
	if err != nil {
		return SearchOutput{}, err
	}
	matcher, err := newLineMatcher(input)
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
	err = filepath.WalkDir(searchRoot, service.searchVisitor(context, repositoryRoot, input, matcher, limit, &output))
	if err != nil {
		return SearchOutput{}, err
	}
	return output, nil
}

func validateSearchInput(input SearchInput) (int, error) {
	if input.Query == "" || len(input.Query) > maxSearchPatternBytes {
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

// newLineMatcher compiles the query into a per-line predicate once per search.
func newLineMatcher(input SearchInput) (func(string) bool, error) {
	if input.Regex {
		pattern := input.Query
		if input.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, ErrInvalidPath
		}
		return compiled.MatchString, nil
	}
	if input.CaseInsensitive {
		query := strings.ToLower(input.Query)
		return func(line string) bool {
			return strings.Contains(strings.ToLower(line), query)
		}, nil
	}
	return func(line string) bool {
		return strings.Contains(line, input.Query)
	}, nil
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
	matcher func(string) bool,
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
		return service.searchFile(repositoryRoot, path, input, matcher, limit, output)
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
	matcher func(string) bool,
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
	return streamSearchFile(path, filepath.ToSlash(relativePath), matcher, limit, output)
}

// streamSearchFile scans a file line-by-line without loading it into memory.
// Binary files and oversized files are skipped so one bad artifact cannot stall a search.
func streamSearchFile(
	path string,
	relativePath string,
	matcher func(string) bool,
	limit int,
	output *SearchOutput,
) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	probe := make([]byte, binaryProbeBytes)
	read, err := io.ReadFull(file, probe)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("probe file: %w", err)
	}
	if read > 0 && bytes.IndexByte(probe[:read], 0) >= 0 {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind file: %w", err)
	}

	reader := bufio.NewReader(file)
	lineNumber := 0
	before := ""
	current := ""
	hasCurrent := false
	for {
		text, readErr := reader.ReadString('\n')
		if text == "" && readErr == io.EOF {
			break
		}
		line := strings.TrimSuffix(text, "\n")
		if hasCurrent {
			if matcher(current) {
				if len(output.Matches) >= limit {
					output.Truncated = true
					return nil
				}
				output.Matches = append(output.Matches, SearchMatch{
					Path:   relativePath,
					Line:   lineNumber,
					Text:   current,
					Before: before,
					After:  line,
				})
			}
			before = current
		}
		lineNumber++
		current = line
		hasCurrent = true
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read file: %w", readErr)
		}
	}
	if hasCurrent && matcher(current) {
		if len(output.Matches) >= limit {
			output.Truncated = true
			return nil
		}
		output.Matches = append(output.Matches, SearchMatch{
			Path:   relativePath,
			Line:   lineNumber,
			Text:   current,
			Before: before,
		})
	}
	return nil
}
