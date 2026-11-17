package testutil_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A test that creates a database under a name it chose shares that database
// with every other run of itself: another worktree, another agent session or
// another package's process on the same local PostgreSQL. Tests get their
// databases from [testutil.NewDatabase] or [testutil.NewEmptyDatabase], which
// name them at random; this test keeps the rest of the code from creating one.
func TestOnlyTestutilCreatesDatabases(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	testutilDir := filepath.Dir(file)

	var offenders []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Worktrees under .claude and vendored or hidden trees are not this module's code.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if path == testutilDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.Contains(strings.ToUpper(lit.Value), "CREATE DATABASE") {
				offenders = append(offenders, fset.Position(lit.Pos()).String())
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders, "create a test database with testutil.NewDatabase or testutil.NewEmptyDatabase, never by a name of your own")
}
