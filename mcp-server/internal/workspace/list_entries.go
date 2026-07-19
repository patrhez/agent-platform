package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const (
	defaultListEntries  = 200
	maxListEntries      = 500
	defaultListDepth    = 1
	defaultFindDepth    = 8
	maxListDepth        = 8
	maxListGlobLength   = 128
)

// ListEntriesInput describes a bounded directory listing or filename search.
type ListEntriesInput struct {
	Repo       string `json:"repo" jsonschema:"repository alias"`
	Path       string `json:"path,omitempty" jsonschema:"optional repository-relative directory, defaults to the repository root"`
	Glob       string `json:"glob,omitempty" jsonschema:"optional filename glob; when set, matching files are searched recursively"`
	MaxDepth   int    `json:"maxDepth,omitempty" jsonschema:"directory depth from one to eight; defaults to one, or eight when glob is set"`
	MaxEntries int    `json:"maxEntries,omitempty" jsonschema:"maximum entries from one to five hundred"`
}

// ListEntry is one visible file or directory below the listing root.
type ListEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// ListEntriesOutput is the bounded result of a file.list call.
type ListEntriesOutput struct {
	Repo      string      `json:"repo"`
	Path      string      `json:"path,omitempty"`
	Entries   []ListEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

// ListEntries returns bounded directory entries under an allowed repository root.
func (service *Service) ListEntries(ctx context.Context, input ListEntriesInput) (ListEntriesOutput, error) {
	limit, depth, err := validateListEntriesInput(input)
	if err != nil {
		return ListEntriesOutput{}, err
	}
	repositoryRoot, err := service.resolveRepository(input.Repo)
	if err != nil {
		return ListEntriesOutput{}, err
	}
	listRoot, err := resolveSearchRoot(repositoryRoot, input.Path)
	if err != nil {
		return ListEntriesOutput{}, err
	}
	output := ListEntriesOutput{Repo: input.Repo, Path: input.Path, Entries: []ListEntry{}}
	err = filepath.WalkDir(listRoot, listVisitor(ctx, listRoot, repositoryRoot, input, limit, depth, &output))
	if err != nil {
		return ListEntriesOutput{}, err
	}
	return output, nil
}

func validateListEntriesInput(input ListEntriesInput) (limit int, depth int, err error) {
	limit = input.MaxEntries
	if limit == 0 {
		limit = defaultListEntries
	}
	if limit < 1 || limit > maxListEntries {
		return 0, 0, ErrResultLimitExceeded
	}
	depth = input.MaxDepth
	if depth == 0 {
		depth = defaultListDepth
		if input.Glob != "" {
			depth = defaultFindDepth
		}
	}
	if depth < 1 || depth > maxListDepth {
		return 0, 0, ErrResultLimitExceeded
	}
	if len(input.Glob) > maxListGlobLength {
		return 0, 0, ErrInvalidPath
	}
	if input.Glob != "" {
		if _, err := filepath.Match(input.Glob, "probe"); err != nil {
			return 0, 0, ErrInvalidPath
		}
	}
	return limit, depth, nil
}

func listVisitor(
	ctx context.Context,
	listRoot string,
	repositoryRoot string,
	input ListEntriesInput,
	limit int,
	depth int,
	output *ListEntriesOutput,
) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk repository: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == listRoot {
			return nil
		}
		if entry.IsDir() && isExcludedSearchDirectory(entry.Name()) {
			return fs.SkipDir
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if len(output.Entries) >= limit {
			output.Truncated = true
			return fs.SkipAll
		}
		if err := appendListEntry(repositoryRoot, path, entry, input.Glob, output); err != nil {
			return err
		}
		relativeToListRoot, err := filepath.Rel(listRoot, path)
		if err != nil {
			return fmt.Errorf("make list path relative: %w", err)
		}
		entryDepth := strings.Count(relativeToListRoot, string(filepath.Separator)) + 1
		if entry.IsDir() && entryDepth >= depth {
			return fs.SkipDir
		}
		return nil
	}
}

func appendListEntry(
	repositoryRoot string,
	path string,
	entry fs.DirEntry,
	glob string,
	output *ListEntriesOutput,
) error {
	relativePath, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return fmt.Errorf("make entry path relative: %w", err)
	}
	if glob != "" {
		if entry.IsDir() {
			return nil
		}
		matched, err := filepath.Match(glob, entry.Name())
		if err != nil || !matched {
			return err
		}
	}
	listEntry := ListEntry{Path: filepath.ToSlash(relativePath), Type: "file"}
	if entry.IsDir() {
		listEntry.Type = "dir"
	} else {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat entry %s: %w", relativePath, err)
		}
		listEntry.SizeBytes = info.Size()
	}
	output.Entries = append(output.Entries, listEntry)
	return nil
}
