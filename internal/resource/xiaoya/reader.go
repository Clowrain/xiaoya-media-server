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
type queryReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Reader struct {
	db             *sql.DB
	ownsDB         bool
	tx             *sql.Tx
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

func (r *Reader) query() queryReader {
	if r.tx != nil {
		return r.tx
	}
	return r.db
}

// WithSnapshot keeps path resolution, traversal and the high-water mark in one SQLite read transaction.
func (r *Reader) WithSnapshot(ctx context.Context, fn func(*Reader) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	view := *r
	view.tx = tx
	view.ownsDB = false
	if err = fn(&view); err != nil {
		return err
	}
	return tx.Commit()
}

// VerifyReadOnly confirms query_only=1 and the presence of the `nodes` table.
func (r *Reader) VerifyReadOnly(ctx context.Context) error {
	var queryOnly int
	if err := r.query().QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		return fmt.Errorf("check PRAGMA query_only: %w", err)
	}
	if queryOnly != 1 {
		return fmt.Errorf("sqlite connection is not query_only (got %d)", queryOnly)
	}
	var tableName string
	err := r.query().QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='nodes'").Scan(&tableName)
	if err != nil {
		return fmt.Errorf("verify nodes table: %w", err)
	}
	return nil
}

// MaxNodeID returns the current maximum `id` in `nodes` (0 if empty).
func (r *Reader) MaxNodeID(ctx context.Context) (int64, error) {
	var maxID sql.NullInt64
	if err := r.query().QueryRowContext(ctx, "SELECT MAX(id) FROM nodes").Scan(&maxID); err != nil {
		return 0, err
	}
	if !maxID.Valid {
		return 0, nil
	}
	return maxID.Int64, nil
}

// DataVersion returns SQLite's connection-level change counter.  It changes
// when another connection commits and lets the scheduler skip an unchanged
// source before starting a subtree walk.
func (r *Reader) DataVersion(ctx context.Context) (int64, error) {
	var version int64
	if err := r.query().QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// ResolvePath resolves an exact directory path segment-by-segment starting from parent_id = 0.
// Returns ErrPathNotFound if any segment is missing, ErrAmbiguousPath if duplicate sibling names exist,
// and ErrNotDirectory if any resolved segment is not a directory.
func (r *Reader) ResolvePath(ctx context.Context, rootPath string) (Node, error) {
	return r.resolvePath(ctx, rootPath, true, false)
}

// ResolveAnyPath resolves either a file or directory. It is used only by an
// explicit administrator import; monitor roots continue to require a directory.
// Administrator imports allow fuzzy fallback (e.g. matching title when release year is present).
func (r *Reader) ResolveAnyPath(ctx context.Context, rootPath string) (Node, error) {
	return r.resolvePath(ctx, rootPath, false, true)
}

func (r *Reader) resolvePath(ctx context.Context, rootPath string, requireDir, allowFuzzy bool) (Node, error) {
	normPath, segments, err := NormalizeMonitorPath(rootPath)
	if err != nil {
		return Node{}, err
	}

	var parentID int64 = 0
	var current RawNode
	builtSegments := make([]string, 0, len(segments))

	for _, seg := range segments {
		rows, qErr := r.query().QueryContext(
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

		if len(matches) == 0 && allowFuzzy {
			// Fallback: check if siblings match after trimming whitespace or matching title without release year.
			sRows, sErr := r.query().QueryContext(ctx, "SELECT id, name, parent_id, is_dir, content FROM nodes WHERE parent_id = ? ORDER BY id ASC", parentID)
			if sErr == nil {
				var siblings []RawNode
				for sRows.Next() {
					var sn RawNode
					var isDirI int
					if err := sRows.Scan(&sn.ID, &sn.Name, &sn.ParentID, &isDirI, &sn.Content); err == nil {
						sn.IsDir = isDirI != 0
						siblings = append(siblings, sn)
					}
				}
				sRows.Close()
				fallbackMatches := matchFuzzySegment(siblings, seg)
				if len(fallbackMatches) == 1 {
					matches = fallbackMatches
				}
			}
		}

		if len(matches) == 0 {
			if allowFuzzy && len(segments) > 0 {
				targetSeg := segments[len(segments)-1]
				if bottomUpNode, bErr := r.resolveByBottomUp(ctx, targetSeg, segments, requireDir); bErr == nil {
					return bottomUpNode, nil
				}
			}
			return Node{}, fmt.Errorf("%w: 该路径尚未进入小雅 SQLite，暂不能入库（等待小雅数据库更新后重试）: segment %q in %q (parent_id=%d)", ErrPathNotFound, seg, normPath, parentID)
		}
		if len(matches) > 1 {
			return Node{}, fmt.Errorf("%w: %d sibling nodes named %q under parent_id=%d in %q", ErrAmbiguousPath, len(matches), seg, parentID, normPath)
		}
		current = matches[0]
		if !current.IsDir && (requireDir || len(builtSegments) < len(segments)-1) {
			return Node{}, fmt.Errorf("%w: segment %q (id=%d) in %q", ErrNotDirectory, seg, current.ID, normPath)
		}
		parentID = current.ID
		builtSegments = append(builtSegments, current.Name)
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

				rows, qErr := r.query().QueryContext(ctx, query, args...)
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
	maxID, err := r.MaxNodeID(ctx)
	if err != nil {
		return afterID, err
	}
	return r.readNodesBetween(ctx, afterID, maxID, pageSize, onBatch)
}

func (r *Reader) readNodesBetween(ctx context.Context, afterID, maxID int64, pageSize int, onBatch func([]RawNode) error) (int64, error) {
	if pageSize <= 0 || pageSize > 2000 {
		pageSize = 1000
	}
	cursor := afterID
	for {
		if err := ctx.Err(); err != nil {
			return cursor, err
		}
		rows, err := r.query().QueryContext(
			ctx,
			"SELECT id, name, parent_id, is_dir, content FROM nodes WHERE id > ? AND id <= ? ORDER BY id ASC LIMIT ?",
			cursor, maxID, pageSize,
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

// WalkNewUnderRoot resolves new rows against their current ancestors, not yesterday's saved paths.
func (r *Reader) WalkNewUnderRoot(ctx context.Context, root Node, after, high int64, onBatch func([]Node) error) error {
	dirs := map[int64]string{root.ID: root.FullPath, 0: ""}
	var resolve func(int64, map[int64]bool) (string, error)
	resolve = func(id int64, trail map[int64]bool) (string, error) {
		if p, ok := dirs[id]; ok {
			return p, nil
		}
		if trail[id] {
			return "", fmt.Errorf("cycle in SQLite parents at node %d", id)
		}
		trail[id] = true
		var name string
		var parent int64
		var isDir int
		err := r.query().QueryRowContext(ctx, "SELECT name,parent_id,is_dir FROM nodes WHERE id=?", id).Scan(&name, &parent, &isDir)
		if err == sql.ErrNoRows || (err == nil && isDir == 0) {
			dirs[id] = ""
			return "", nil
		}
		if err != nil {
			return "", err
		}
		p, err := resolve(parent, trail)
		if err != nil {
			return "", err
		}
		if p != "" {
			p += "/" + name
		}
		dirs[id] = p
		return p, nil
	}
	_, err := r.readNodesBetween(ctx, after, high, 1000, func(raw []RawNode) error {
		nodes := make([]Node, 0, len(raw))
		for _, n := range raw {
			if n.ID == root.ID {
				continue
			}
			p, err := resolve(n.ParentID, map[int64]bool{})
			if err != nil {
				return err
			}
			if p == "" {
				continue
			}
			full := p + "/" + n.Name
			if n.IsDir {
				dirs[n.ID] = full
			}
			nodes = append(nodes, Node{RawNode: n, FullPath: full, RelPath: strings.TrimPrefix(full, root.FullPath+"/")})
		}
		return onBatch(nodes)
	})
	return err
}

func matchFuzzySegment(siblings []RawNode, target string) []RawNode {
	tClean := strings.TrimSpace(target)
	var exactTrim []RawNode
	var titleOnly []RawNode
	for _, n := range siblings {
		nClean := strings.TrimSpace(n.Name)
		if nClean == tClean || strings.EqualFold(nClean, tClean) {
			exactTrim = append(exactTrim, n)
			continue
		}
		if syN, syT := stripYear(nClean), stripYear(tClean); syN != "" && syN == syT {
			titleOnly = append(titleOnly, n)
		}
	}
	if len(exactTrim) > 0 {
		return exactTrim
	}
	return titleOnly
}

func stripYear(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"(", "（", "[", "【"} {
		if idx := strings.Index(s, p); idx > 0 {
			s = s[:idx]
		}
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// resolveByBottomUp searches SQLite globally for the target name and walks up parent_id
// to find a candidate whose ancestor chain matches the expected path segments (e.g. 电视剧, 国产剧).
func (r *Reader) resolveByBottomUp(ctx context.Context, targetSeg string, segments []string, requireDir bool) (Node, error) {
	cleanTarget := strings.TrimSpace(targetSeg)
	if cleanTarget == "" {
		return Node{}, ErrPathNotFound
	}

	q := "SELECT id, name, parent_id, is_dir, content FROM nodes WHERE (name = ? OR name LIKE ? OR name LIKE ?) ORDER BY id ASC LIMIT 50"
	rows, err := r.query().QueryContext(ctx, q, cleanTarget, cleanTarget+" %", cleanTarget+"（%")
	if err != nil {
		return Node{}, err
	}
	defer rows.Close()

	var candidates []RawNode
	for rows.Next() {
		var n RawNode
		var isDirInt int
		if err := rows.Scan(&n.ID, &n.Name, &n.ParentID, &isDirInt, &n.Content); err != nil {
			return Node{}, err
		}
		n.IsDir = isDirInt != 0
		if requireDir && !n.IsDir {
			continue
		}
		candidates = append(candidates, n)
	}

	if len(candidates) == 0 {
		return Node{}, ErrPathNotFound
	}

	var validMatches []Node
	for _, c := range candidates {
		fullPath, pathErr := r.buildFullPathUpward(ctx, c)
		if pathErr != nil {
			continue
		}
		if matchesAncestors(fullPath, segments) {
			validMatches = append(validMatches, Node{
				RawNode:  c,
				FullPath: fullPath,
			})
		}
	}

	if len(validMatches) == 1 {
		return validMatches[0], nil
	}
	if len(validMatches) > 1 {
		return Node{}, fmt.Errorf("%w: 找到 %d 个同名路径，无法自动确定唯一目标", ErrAmbiguousPath, len(validMatches))
	}
	return Node{}, ErrPathNotFound
}

func (r *Reader) buildFullPathUpward(ctx context.Context, n RawNode) (string, error) {
	currentID := n.ID
	currentName := n.Name
	parts := []string{currentName}
	parentID := n.ParentID
	visited := map[int64]bool{currentID: true}

	for parentID > 0 {
		if visited[parentID] {
			break
		}
		visited[parentID] = true

		var pName string
		var nextParent int64
		err := r.query().QueryRowContext(ctx, "SELECT name, parent_id FROM nodes WHERE id = ?", parentID).Scan(&pName, &nextParent)
		if err != nil {
			return "", err
		}
		parts = append([]string{pName}, parts...)
		parentID = nextParent
	}
	return "/" + strings.Join(parts, "/"), nil
}

func matchesAncestors(fullPath string, expectedSegments []string) bool {
	if len(expectedSegments) <= 1 {
		return true
	}
	matchedCategory := false
	hasCategory := false
	for _, seg := range expectedSegments[:len(expectedSegments)-1] {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "每日更新" {
			continue
		}
		base := stripYear(seg)
		if isCategoryKeyword(base) {
			hasCategory = true
			if strings.Contains(fullPath, base) {
				matchedCategory = true
			}
		}
	}
	if hasCategory {
		return matchedCategory
	}
	for _, seg := range expectedSegments[:len(expectedSegments)-1] {
		base := stripYear(seg)
		if len(base) > 0 && strings.Contains(fullPath, base) {
			return true
		}
	}
	return false
}

func isCategoryKeyword(s string) bool {
	for _, cat := range []string{"电视剧", "国产剧", "美剧", "韩剧", "日剧", "泰剧", "电影", "综艺", "动漫", "短剧", "纪录片"} {
		if strings.Contains(s, cat) {
			return true
		}
	}
	return false
}

// SQLiteBrowseResult wraps pagination and node items for directory browsing directly from Xiaoya SQLite.
type SQLiteBrowseResult struct {
	Current Node   `json:"current"`
	Items   []Node `json:"items"`
	Total   int    `json:"total"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
}

// ListChildren lists direct child nodes of a directory given path or parentID,
// supporting instant local pagination directly from Xiaoya's SQLite nodes table.
func (r *Reader) ListChildren(ctx context.Context, parentID int64, path string, page, perPage int) (SQLiteBrowseResult, error) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 200 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	var currNode Node
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" || cleanPath == "/" {
		currNode = Node{
			RawNode:  RawNode{ID: 0, Name: "/", ParentID: 0, IsDir: true},
			FullPath: "/",
		}
	} else if parentID > 0 {
		var n RawNode
		var isDirInt int
		err := r.query().QueryRowContext(ctx, "SELECT id, name, parent_id, is_dir, content FROM nodes WHERE id = ?", parentID).Scan(&n.ID, &n.Name, &n.ParentID, &isDirInt, &n.Content)
		if err != nil {
			return SQLiteBrowseResult{}, err
		}
		n.IsDir = isDirInt != 0
		currNode = Node{RawNode: n, FullPath: cleanPath}
	} else {
		resolved, err := r.ResolveAnyPath(ctx, cleanPath)
		if err != nil {
			return SQLiteBrowseResult{}, err
		}
		currNode = resolved
	}

	targetParentID := currNode.ID

	var total int
	err := r.query().QueryRowContext(ctx, "SELECT COUNT(*) FROM nodes WHERE parent_id = ?", targetParentID).Scan(&total)
	if err != nil {
		return SQLiteBrowseResult{}, err
	}

	q := "SELECT id, name, parent_id, is_dir, content FROM nodes WHERE parent_id = ? ORDER BY is_dir DESC, name ASC LIMIT ? OFFSET ?"
	rows, err := r.query().QueryContext(ctx, q, targetParentID, perPage, offset)
	if err != nil {
		return SQLiteBrowseResult{}, err
	}
	defer rows.Close()

	var items []Node
	for rows.Next() {
		var n RawNode
		var isDirInt int
		if err := rows.Scan(&n.ID, &n.Name, &n.ParentID, &isDirInt, &n.Content); err != nil {
			return SQLiteBrowseResult{}, err
		}
		n.IsDir = isDirInt != 0
		itemPath := n.Name
		if currNode.FullPath == "/" {
			itemPath = "/" + n.Name
		} else {
			itemPath = strings.TrimRight(currNode.FullPath, "/") + "/" + n.Name
		}
		items = append(items, Node{
			RawNode:  n,
			FullPath: itemPath,
			RelPath:  strings.TrimPrefix(itemPath, "/"),
		})
	}

	return SQLiteBrowseResult{
		Current: currNode,
		Items:   items,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	}, rows.Err()
}


