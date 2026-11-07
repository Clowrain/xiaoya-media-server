package xiaoya

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrEmptyMonitorPath = errors.New("monitor path cannot be empty or root '/'")
	ErrInvalidPathSeg   = errors.New("monitor path contains invalid relative segment")
	ErrPathNotFound     = errors.New("monitor path not found in xiaoya sqlite")
	ErrAmbiguousPath    = errors.New("ambiguous duplicate directory name in xiaoya sqlite")
	ErrNotDirectory     = errors.New("target node is not a directory")
)

// RawNode represents a single row in Xiaoya's `nodes` table.
type RawNode struct {
	ID       int64
	Name     string
	ParentID int64
	IsDir    bool
	Content  string
}

// Node represents a resolved node in a monitored subtree with normalized full and relative paths.
type Node struct {
	RawNode
	FullPath string
	RelPath  string
}

// NormalizeMonitorPath normalizes a monitor root path while preserving exact Unicode code points
// (for example, fullwidth '（115）' vs ASCII '(115)').
func NormalizeMonitorPath(raw string) (string, []string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "/" {
		return "", nil, ErrEmptyMonitorPath
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	rawParts := strings.Split(trimmed, "/")
	segments := make([]string, 0, len(rawParts))
	for _, seg := range rawParts {
		if seg == "" {
			continue
		}
		if seg == "." || seg == ".." {
			return "", nil, fmt.Errorf("%w: %q", ErrInvalidPathSeg, seg)
		}
		segments = append(segments, seg)
	}
	if len(segments) == 0 {
		return "", nil, ErrEmptyMonitorPath
	}
	return "/" + strings.Join(segments, "/"), segments, nil
}

// Reader provides strictly read-only queries against Xiaoya's strm_internal.db.
type Reader struct {
	db             *sql.DB
	ownsDB         bool
	parentChunkMax int
	defaultPageMax int
}

// OpenReader opens a read-only SQLite connection and returns a Reader.
func OpenReader(sqlitePath string) (*Reader, error) {
	db, err := OpenReadOnly(sqlitePath)
	if err != nil {
		return nil, err
	}
	return &Reader{
		db:             db,
		ownsDB:         true,
		parentChunkMax: 500,
		defaultPageMax: 1000,
	}, nil
}

// NewReaderFromDB wraps an existing read-only *sql.DB handle without taking ownership of Close().
func NewReaderFromDB(db *sql.DB) *Reader {
	return &Reader{
		db:             db,
		ownsDB:         false,
		parentChunkMax: 500,
		defaultPageMax: 1000,
	}
}

func (r *Reader) Close() error {
	if r != nil && r.ownsDB && r.db != nil {
		return r.db.Close()
	}
	return nil
}

// VerifyReadOnly confirms query_only=1 and the presence of the `nodes` table.
func (r *Reader) VerifyReadOnly(ctx context.Context) error {
	var queryOnly int
	if err := r.db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		return fmt.Errorf("check PRAGMA query_only: %w", err)
	}
	if queryOnly != 1 {
		return fmt.Errorf("sqlite connection is not query_only (got %d)", queryOnly)
	}
	var tableName string
	err := r.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='nodes'").Scan(&tableName)
	if err != nil {
		return fmt.Errorf("verify nodes table: %w", err)
	}
	return nil
}

// MaxNodeID returns the current maximum `id` in `nodes` (0 if empty).
func (r *Reader) MaxNodeID(ctx context.Context) (int64, error) {
	var maxID sql.NullInt64
	if err := r.db.QueryRowContext(ctx, "SELECT MAX(id) FROM nodes").Scan(&maxID); err != nil {
		return 0, err
	}
	if !maxID.Valid {
		return 0, nil
	}
	return maxID.Int64, nil
}

// ResolvePath resolves an exact directory path segment-by-segment starting from parent_id = 0.
// Returns ErrPathNotFound if any segment is missing, ErrAmbiguousPath if duplicate sibling names exist,
// and ErrNotDirectory if any resolved segment is not a directory.
func (r *Reader) ResolvePath(ctx context.Context, rootPath string) (Node, error) {
	normPath, segments, err := NormalizeMonitorPath(rootPath)
	if err != nil {
		return Node{}, err
	}

	var parentID int64 = 0
	var current RawNode
	builtSegments := make([]string, 0, len(segments))

	for _, seg := range segments {
		rows, qErr := r.db.QueryContext(
			ctx,
			"SELECT id, name, parent_id, is_dir, content FROM nodes WHERE parent_id = ? AND name = ? ORDER BY id ASC",
			parentID, seg,
		)
		if qErr != nil {
			return Node{}, fmt.Errorf("query segment %q under parent_id=%d: %w", seg, parentID, qErr)
		}
		var matches []RawNode
		for rows.Next() {
			var n RawNode
			var isDirInt int
			if sErr := rows.Scan(&n.ID, &n.Name, &n.ParentID, &isDirInt, &n.Content); sErr != nil {
				rows.Close()
				return Node{}, sErr
			}
			n.IsDir = isDirInt != 0
			matches = append(matches, n)
		}
		if rErr := rows.Err(); rErr != nil {
			rows.Close()
			return Node{}, rErr
		}
		rows.Close()

		if len(matches) == 0 {
			return Node{}, fmt.Errorf("%w: segment %q in %q (parent_id=%d)", ErrPathNotFound, seg, normPath, parentID)
		}
		if len(matches) > 1 {
			return Node{}, fmt.Errorf("%w: %d sibling nodes named %q under parent_id=%d in %q", ErrAmbiguousPath, len(matches), seg, parentID, normPath)
		}
		current = matches[0]
		if !current.IsDir {
			return Node{}, fmt.Errorf("%w: segment %q (id=%d) in %q", ErrNotDirectory, seg, current.ID, normPath)
		}
		parentID = current.ID
		builtSegments = append(builtSegments, seg)
	}

	return Node{
		RawNode:  current,
		FullPath: "/" + strings.Join(builtSegments, "/"),
		RelPath:  "",
	}, nil
}

type parentDirRef struct {
	id       int64
	fullPath string
	relPath  string
}

// WalkStats summarizes a completed subtree traversal.
type WalkStats struct {
	RootNodeID int64
	MaxNodeID  int64
	TotalNodes int
	DirNodes   int
	FileNodes  int
}

// WalkSubtreeBatched traverses all descendants under `root` in bounded BFS batches without loading
// the entire SQLite table into memory. `onBatch` receives slices of up to `pageSize` descendant nodes.
func (r *Reader) WalkSubtreeBatched(ctx context.Context, root Node, pageSize int, onBatch func(batch []Node) error) (WalkStats, error) {
	if !root.IsDir {
		return WalkStats{}, fmt.Errorf("%w: root id=%d (%s)", ErrNotDirectory, root.ID, root.FullPath)
	}
	if pageSize <= 0 || pageSize > 2000 {
		pageSize = r.defaultPageMax
		if pageSize <= 0 {
			pageSize = 1000
		}
	}
	chunkMax := r.parentChunkMax
	if chunkMax <= 0 {
		chunkMax = 500
	}

	stats := WalkStats{
		RootNodeID: root.ID,
		MaxNodeID:  root.ID,
	}

	visitedDirs := map[int64]bool{root.ID: true}
	frontier := []parentDirRef{{
		id:       root.ID,
		fullPath: strings.TrimRight(root.FullPath, "/"),
		relPath:  "",
	}}

	for len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var nextFrontier []parentDirRef

		for start := 0; start < len(frontier); start += chunkMax {
			end := start + chunkMax
			if end > len(frontier) {
				end = len(frontier)
			}
			chunk := frontier[start:end]
			parentLookup := make(map[int64]parentDirRef, len(chunk))
			placeholders := make([]string, len(chunk))
			baseArgs := make([]any, len(chunk))
			for i, p := range chunk {
				parentLookup[p.id] = p
				placeholders[i] = "?"
				baseArgs[i] = p.id
			}

			query := "SELECT id, name, parent_id, is_dir, content FROM nodes WHERE parent_id IN (" +
				strings.Join(placeholders, ",") + ") AND id > ? ORDER BY id ASC LIMIT ?"

			var cursorID int64 = 0
			for {
				if err := ctx.Err(); err != nil {
					return stats, err
				}
				args := make([]any, 0, len(baseArgs)+2)
				args = append(args, baseArgs...)
				args = append(args, cursorID, pageSize)

				rows, qErr := r.db.QueryContext(ctx, query, args...)
				if qErr != nil {
					return stats, fmt.Errorf("walk subtree chunk cursor=%d: %w", cursorID, qErr)
				}

				batch := make([]Node, 0, pageSize)
				for rows.Next() {
					var raw RawNode
					var isDirInt int
					if sErr := rows.Scan(&raw.ID, &raw.Name, &raw.ParentID, &isDirInt, &raw.Content); sErr != nil {
						rows.Close()
						return stats, sErr
					}
					raw.IsDir = isDirInt != 0
					cursorID = raw.ID
					if raw.ID > stats.MaxNodeID {
						stats.MaxNodeID = raw.ID
					}

					pRef := parentLookup[raw.ParentID]
					fullPath := pRef.fullPath + "/" + raw.Name
					relPath := raw.Name
					if pRef.relPath != "" {
						relPath = pRef.relPath + "/" + raw.Name
					}

					node := Node{
						RawNode:  raw,
						FullPath: fullPath,
						RelPath:  relPath,
					}
					batch = append(batch, node)
					stats.TotalNodes++
					if raw.IsDir {
						stats.DirNodes++
						if !visitedDirs[raw.ID] {
							visitedDirs[raw.ID] = true
							nextFrontier = append(nextFrontier, parentDirRef{
								id:       raw.ID,
								fullPath: fullPath,
								relPath:  relPath,
							})
						}
					} else {
						stats.FileNodes++
					}
				}
				if rErr := rows.Err(); rErr != nil {
					rows.Close()
					return stats, rErr
				}
				rows.Close()

				if len(batch) > 0 && onBatch != nil {
					if err := onBatch(batch); err != nil {
						return stats, err
					}
				}
				if len(batch) < pageSize {
					break
				}
			}
		}

		frontier = nextFrontier
	}

	return stats, nil
}

// ReadNodesAfterID reads newly inserted rows with `id > afterID` using the primary-key index.
func (r *Reader) ReadNodesAfterID(ctx context.Context, afterID int64, pageSize int, onBatch func(batch []RawNode) error) (int64, error) {
	if pageSize <= 0 || pageSize > 2000 {
		pageSize = 1000
	}
	cursor := afterID
	for {
		if err := ctx.Err(); err != nil {
			return cursor, err
		}
		rows, err := r.db.QueryContext(
			ctx,
			"SELECT id, name, parent_id, is_dir, content FROM nodes WHERE id > ? ORDER BY id ASC LIMIT ?",
			cursor, pageSize,
		)
		if err != nil {
			return cursor, err
		}
		batch := make([]RawNode, 0, pageSize)
		for rows.Next() {
			var n RawNode
			var isDirInt int
			if sErr := rows.Scan(&n.ID, &n.Name, &n.ParentID, &isDirInt, &n.Content); sErr != nil {
				rows.Close()
				return cursor, sErr
			}
			n.IsDir = isDirInt != 0
			cursor = n.ID
			batch = append(batch, n)
		}
		if rErr := rows.Err(); rErr != nil {
			rows.Close()
			return cursor, rErr
		}
		rows.Close()
		if len(batch) > 0 && onBatch != nil {
			if err := onBatch(batch); err != nil {
				return cursor, err
			}
		}
		if len(batch) < pageSize {
			break
		}
	}
	return cursor, nil
}
